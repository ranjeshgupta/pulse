package api

// Two-step sign-in with an authenticator app (Google Authenticator, Microsoft Authenticator, Authy,
// 1Password…), for password sign-ins; with SSO the identity provider handles extra steps.
//
// Sign-in: when the password is right and the person has two-step sign-in on (or the workspace
// requires it and they haven't set it up), POST /v1/auth/login answers {"two_factor": "code"} or
// {"two_factor": "setup"} and, instead of a session, sets a short-lived cookie sealed with the
// server key that names the user. The sign-in page then sends a code (or a recovery code) to
// /v1/auth/two-factor/verify, or sets the app up through /v1/auth/two-factor/setup and /enable,
// and only then is the session created. Codes are limited per person per minute and count as
// sign-in attempts; a code's 30-second step is remembered, so the same code never works twice.
//
// People manage their own setup under /v1/account/two-factor; admins set the workspace policy
// (/v1/settings/two-factor) and can reset a teammate's setup (DELETE /v1/users/{id}/two-factor).

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

const (
	twoFactorCookie = "pulse_2fa"
	twoFactorPath   = "/v1/auth/two-factor"
	twoFactorTTL    = 10 * time.Minute // time to find the phone, or to scan the QR code when setting up
	codesPerMinute  = 5                // code attempts per person per minute
	totpIssuer      = "Pulse"

	pendingCode  = "code"  // the person has the app: enter a code
	pendingSetup = "setup" // the workspace requires it: set the app up first
)

type TwoFactorStore interface {
	TwoFactor(ctx context.Context, tenant, user int64) (model.TwoFactor, error)
	// StartTwoFactor and EnableTwoFactor change a confirmed secret only with replace (the caller
	// checked a current code or the password); without it they fail with pg.ErrConflict when two-step
	// sign-in is on.
	StartTwoFactor(ctx context.Context, tenant, user int64, sealed []byte, replace bool) error
	EnableTwoFactor(ctx context.Context, tenant, user int64, pending []byte, step int64, recovery []string, replace bool) error
	UseTwoFactorStep(ctx context.Context, tenant, user, step int64) (bool, error)
	UseRecoveryCode(ctx context.Context, tenant, user int64, hash string) (bool, error)
	SetRecoveryCodes(ctx context.Context, tenant, user int64, hashes []string) error
	DeleteTwoFactor(ctx context.Context, tenant, user int64) error
	TwoFactorPolicy(ctx context.Context, tenant int64) (auth.TwoFactorPolicy, error)
	PutTwoFactorPolicy(ctx context.Context, tenant int64, p auth.TwoFactorPolicy) error
}

var (
	errNoBox       = errUser("two-step sign-in needs the server's secret key (PULSE_SECRET_KEY); ask your administrator")
	errBadCode     = errors.New("that code didn't work. Check the time on your phone, or use one of your recovery codes")
	errSSOOnly     = errUser("you sign in with single sign-on, so your identity provider handles extra sign-in steps")
	errNotAPerson  = errUser("two-step sign-in is for people who sign in, not for the API key")
	errNotSetUp    = errUser("set up the authenticator app first")
	errTooManyCode = errors.New("too many codes tried, wait a minute and try again")
	errAlreadyOn   = errUser("two-step sign-in is already on")
)

func (s *Server) twoFactorRoutes(mux *http.ServeMux) {
	// sign-in (no session yet: the sealed cookie from POST /v1/auth/login says who)
	mux.HandleFunc("POST /v1/auth/two-factor/verify", s.signInVerify)
	mux.HandleFunc("POST /v1/auth/two-factor/setup", s.signInSetup)
	mux.HandleFunc("POST /v1/auth/two-factor/enable", s.signInEnable)
	// your own account
	mux.HandleFunc("GET /v1/account/two-factor", s.authed(s.getMyTwoFactor))
	mux.HandleFunc("POST /v1/account/two-factor/setup", s.authed(s.setupMyTwoFactor))
	mux.HandleFunc("POST /v1/account/two-factor/enable", s.authed(s.enableMyTwoFactor))
	mux.HandleFunc("POST /v1/account/two-factor/disable", s.authed(s.disableMyTwoFactor))
	mux.HandleFunc("POST /v1/account/two-factor/recovery-codes", s.authed(s.newRecoveryCodes))
	// the workspace policy, and resetting a teammate's setup
	mux.HandleFunc("GET /v1/settings/two-factor", s.authed(s.getTwoFactorPolicy))
	mux.HandleFunc("PUT /v1/settings/two-factor", s.fullAdmin(s.putTwoFactorPolicy))
	mux.HandleFunc("DELETE /v1/users/{id}/two-factor", s.admin(s.resetTwoFactor))
}

func (s *Server) twoFactor() (TwoFactorStore, bool) {
	ts, ok := s.Store.(TwoFactorStore)
	return ts, ok
}

// twoFactorNext says what a password sign-in still needs: "" (nothing), pendingCode or pendingSetup,
// and the user's current setup (for the pending cookie's fingerprint).
func (s *Server) twoFactorNext(ctx context.Context, u model.User) (string, model.TwoFactor, error) {
	ts, ok := s.twoFactor()
	if !ok {
		return "", model.TwoFactor{}, nil
	}
	tf, err := ts.TwoFactor(ctx, u.TenantID, u.ID)
	if err != nil && !errors.Is(err, pg.ErrNotFound) {
		return "", tf, err
	}
	if tf.Enabled() {
		if s.Box == nil {
			return "", tf, errNoBox // fail closed: a code can't be checked without the key
		}
		return pendingCode, tf, nil
	}
	if s.Box == nil {
		return "", tf, nil // the policy can't be saved without the key either
	}
	pol, err := ts.TwoFactorPolicy(ctx, u.TenantID)
	if err != nil {
		return "", tf, err
	}
	if pol.Requires(u.ID, u.Role) {
		return pendingSetup, tf, nil
	}
	return "", tf, nil
}

// pendingPrint fingerprints what a half-finished sign-in was started against: the password, the
// confirmed secret, the last code step and the recovery codes left. Any successful use (a code, a
// recovery code, finishing a setup) or change (a reset, a new phone, a new password) changes it, so
// a pending cookie works for one sign-in only, and never after the state it saw has changed.
func pendingPrint(u model.User, tf model.TwoFactor) string {
	var at int64
	if tf.EnabledAt != nil {
		at = tf.EnabledAt.UnixNano()
	}
	h := auth.HashToken(strings.Join([]string{u.PasswordHash, hex.EncodeToString(tf.Secret), strconv.FormatInt(at, 10),
		strconv.FormatInt(tf.LastStep, 10), strconv.Itoa(tf.RecoveryLeft)}, "|"))
	return h[:32]
}

// setPending sets the cookie that carries a half-finished sign-in:
// tenant|user|purpose|expiry|fingerprint.
func (s *Server) setPending(w http.ResponseWriter, u model.User, purpose string, tf model.TwoFactor) {
	exp := s.now().Add(twoFactorTTL).Unix()
	v := strings.Join([]string{strconv.FormatInt(u.TenantID, 10), strconv.FormatInt(u.ID, 10), purpose, strconv.FormatInt(exp, 10),
		pendingPrint(u, tf)}, "|")
	http.SetCookie(w, &http.Cookie{
		Name: twoFactorCookie, Value: s.Box.SealString(v), Path: twoFactorPath, MaxAge: int(twoFactorTTL.Seconds()),
		HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearPending(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: twoFactorCookie, Value: "", Path: twoFactorPath, MaxAge: -1, HttpOnly: true,
		Secure: s.secureCookies(), SameSite: http.SameSiteStrictMode})
}

// pending reads the half-finished sign-in for purpose and loads its user; it answers the request
// itself when there is none, it expired, or the user's two-step state changed since it was made.
func (s *Server) pending(w http.ResponseWriter, r *http.Request, purpose string) (model.User, bool) {
	if r.Header.Get(csrfHeader) == "" {
		writeErr(w, http.StatusForbidden, errCSRF.Error())
		return model.User{}, false
	}
	const expired = "your sign-in timed out. Sign in again with your password"
	c, err := r.Cookie(twoFactorCookie)
	if err != nil || s.Box == nil {
		writeErr(w, http.StatusUnauthorized, expired)
		return model.User{}, false
	}
	v, err := s.Box.OpenString(c.Value)
	parts := strings.Split(v, "|")
	if err != nil || len(parts) != 5 || parts[2] != purpose {
		writeErr(w, http.StatusUnauthorized, expired)
		return model.User{}, false
	}
	tenant, _ := strconv.ParseInt(parts[0], 10, 64)
	user, _ := strconv.ParseInt(parts[1], 10, 64)
	exp, _ := strconv.ParseInt(parts[3], 10, 64)
	if s.now().Unix() > exp {
		writeErr(w, http.StatusUnauthorized, expired)
		return model.User{}, false
	}
	u, err := s.Store.User(r.Context(), tenant, user)
	if errors.Is(err, pg.ErrNotFound) {
		writeErr(w, http.StatusUnauthorized, expired)
		return model.User{}, false
	} else if err != nil {
		s.fail(w, err)
		return model.User{}, false
	}
	ts, _ := s.twoFactor()
	tf, err := ts.TwoFactor(r.Context(), u.TenantID, u.ID)
	if err != nil && !errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return model.User{}, false
	}
	// Setting up at sign-in is only for people who don't have it on: never a way to replace a secret.
	if (purpose == pendingSetup && tf.Enabled()) || subtle.ConstantTimeCompare([]byte(parts[4]), []byte(pendingPrint(u, tf))) != 1 {
		writeErr(w, http.StatusUnauthorized, expired)
		return model.User{}, false
	}
	return u, true
}

// allowCode limits code attempts: codesPerMinute per person, and each counts as a sign-in attempt
// (10 per 15 minutes, cleared when someone signs in), so a stolen password doesn't buy unlimited
// guesses at a 6-digit code.
func (s *Server) allowCode(ctx context.Context, u model.User, signIn bool) bool {
	if !s.allowTenant(ctx, "rl2fa", u.ID, codesPerMinute) {
		return false
	}
	if !signIn {
		return true
	}
	t, err := s.Store.Tenant(ctx, u.TenantID)
	return err != nil || s.allowLogin(ctx, t.Slug, u.Email)
}

// checkCode accepts an app code (once: its step is recorded) or an unused recovery code (then
// removed). It returns how the person proved it: "app" or "recovery code".
func (s *Server) checkCode(ctx context.Context, u model.User, code string) (string, error) {
	ts, _ := s.twoFactor()
	tf, err := ts.TwoFactor(ctx, u.TenantID, u.ID)
	if errors.Is(err, pg.ErrNotFound) || (err == nil && !tf.Enabled()) {
		return "", errNotSetUp
	} else if err != nil {
		return "", err
	}
	if auth.LooksLikeTOTP(code) {
		if s.Box == nil {
			return "", errNoBox
		}
		secret, err := s.Box.Open(tf.Secret)
		if err != nil {
			return "", err
		}
		step, ok := auth.TOTPMatch(secret, code, s.now(), tf.LastStep)
		if !ok {
			return "", errBadCode
		}
		if ok, err := ts.UseTwoFactorStep(ctx, u.TenantID, u.ID, step); err != nil {
			return "", err
		} else if !ok {
			return "", errBadCode // the same code, sent twice at once
		}
		return "app", nil
	}
	if strings.TrimSpace(code) == "" {
		return "", errBadCode
	}
	ok, err := ts.UseRecoveryCode(ctx, u.TenantID, u.ID, auth.HashRecoveryCode(code))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errBadCode
	}
	return "recovery code", nil
}

// failCode answers checkCode and setup errors.
func (s *Server) failCode(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBadCode):
		writeErr(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, errTooManyCode):
		writeErr(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, errNoBox):
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	case errors.As(err, new(errUser)):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		s.fail(w, err)
	}
}

// beginSetup makes a new secret for u, keeps it as pending and returns what the app needs.
// With replace false it refuses when two-step sign-in is already on.
func (s *Server) beginSetup(ctx context.Context, u model.User, replace bool) (map[string]any, error) {
	if s.Box == nil {
		return nil, errNoBox
	}
	if u.PasswordHash == "" {
		return nil, errSSOOnly
	}
	t, err := s.Store.Tenant(ctx, u.TenantID)
	if err != nil {
		return nil, err
	}
	secret := auth.NewTOTPSecret()
	ts, _ := s.twoFactor()
	if err := ts.StartTwoFactor(ctx, u.TenantID, u.ID, s.Box.Seal(secret), replace); errors.Is(err, pg.ErrConflict) {
		return nil, errAlreadyOn
	} else if err != nil {
		return nil, err
	}
	account := u.Email
	if t.Name != "" {
		account += " (" + t.Name + ")"
	}
	return map[string]any{"secret": auth.TOTPSecretText(secret), "uri": auth.TOTPURI(totpIssuer, account, secret),
		"issuer": totpIssuer, "account": account}, nil
}

// confirmSetup checks a code from the app against the pending secret and turns two-step sign-in
// on with new recovery codes, which it returns (shown once).
// With replace false it refuses when two-step sign-in is already on.
func (s *Server) confirmSetup(ctx context.Context, u model.User, code string, replace bool) ([]string, error) {
	if s.Box == nil {
		return nil, errNoBox
	}
	ts, _ := s.twoFactor()
	tf, err := ts.TwoFactor(ctx, u.TenantID, u.ID)
	if errors.Is(err, pg.ErrNotFound) || (err == nil && len(tf.Pending) == 0) {
		return nil, errUser("start again: scan a new QR code")
	} else if err != nil {
		return nil, err
	}
	secret, err := s.Box.Open(tf.Pending)
	if err != nil {
		return nil, err
	}
	step, ok := auth.TOTPMatch(secret, code, s.now(), 0)
	if !ok {
		return nil, errBadCode
	}
	codes, hashes := newRecovery()
	if !replace && tf.Enabled() {
		return nil, errAlreadyOn
	}
	if err := ts.EnableTwoFactor(ctx, u.TenantID, u.ID, tf.Pending, step, hashes, replace); errors.Is(err, pg.ErrConflict) {
		return nil, errAlreadyOn
	} else if errors.Is(err, pg.ErrNotFound) {
		return nil, errUser("start again: scan a new QR code")
	} else if err != nil {
		return nil, err
	}
	return codes, nil
}

func newRecovery() (codes, hashes []string) {
	codes = auth.NewRecoveryCodes(auth.RecoveryCodes)
	for _, c := range codes {
		hashes = append(hashes, auth.HashRecoveryCode(c))
	}
	return codes, hashes
}

// finishSignIn creates the session once the second step is done.
func (s *Server) finishSignIn(w http.ResponseWriter, r *http.Request, u model.User, how string) bool {
	s.clearPending(w)
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, err)
		return false
	}
	if t, err := s.Store.Tenant(r.Context(), u.TenantID); err == nil {
		s.loginSucceeded(r.Context(), t.Slug, u.Email)
	}
	s.auditSignIn(r, u.TenantID, u.ID, u.Email, how, true)
	return true
}

type codeRequest struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

// ---- sign-in ----

func (s *Server) signInVerify(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pending(w, r, pendingCode)
	if !ok {
		return
	}
	var req codeRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.allowCode(r.Context(), u, true) {
		s.failCode(w, errTooManyCode)
		return
	}
	how, err := s.checkCode(r.Context(), u, req.Code)
	if err != nil {
		if errors.Is(err, errBadCode) {
			s.auditSignIn(r, u.TenantID, u.ID, u.Email, "sign-in (two-step)", false)
		}
		s.failCode(w, err)
		return
	}
	if !s.finishSignIn(w, r, u, "sign-in (two-step, "+how+")") {
		return
	}
	out := map[string]any{"user": u}
	if how == "recovery code" {
		if ts, _ := s.twoFactor(); ts != nil {
			if tf, err := ts.TwoFactor(r.Context(), u.TenantID, u.ID); err == nil {
				out["recovery_codes_left"] = tf.RecoveryLeft
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) signInSetup(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pending(w, r, pendingSetup)
	if !ok {
		return
	}
	out, err := s.beginSetup(r.Context(), u, false)
	if err != nil {
		s.failCode(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) signInEnable(w http.ResponseWriter, r *http.Request) {
	u, ok := s.pending(w, r, pendingSetup)
	if !ok {
		return
	}
	var req codeRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.allowCode(r.Context(), u, true) {
		s.failCode(w, errTooManyCode)
		return
	}
	codes, err := s.confirmSetup(r.Context(), u, req.Code, false)
	if err != nil {
		s.failCode(w, err)
		return
	}
	u.TwoFactor = true
	if !s.finishSignIn(w, r, u, "sign-in (two-step set up)") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "recovery_codes": codes})
}

// ---- your account ----

// person loads the signed-in person (not the API key).
func (s *Server) person(w http.ResponseWriter, r *http.Request, p model.Principal) (model.User, bool) {
	if p.UserID == 0 {
		s.failCode(w, errNotAPerson)
		return model.User{}, false
	}
	if _, ok := s.twoFactor(); !ok {
		writeErr(w, http.StatusNotImplemented, "two-step sign-in isn't available on this server")
		return model.User{}, false
	}
	u, err := s.Store.User(r.Context(), p.TenantID, p.UserID)
	if err != nil {
		s.fail(w, err)
		return model.User{}, false
	}
	return u, true
}

func (s *Server) getMyTwoFactor(w http.ResponseWriter, r *http.Request, p model.Principal) {
	u, ok := s.person(w, r, p)
	if !ok {
		return
	}
	ts, _ := s.twoFactor()
	tf, err := ts.TwoFactor(r.Context(), u.TenantID, u.ID)
	if err != nil && !errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return
	}
	pol, err := ts.TwoFactorPolicy(r.Context(), u.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": tf.Enabled(), "enabled_at": tf.EnabledAt, "recovery_codes_left": tf.RecoveryLeft,
		"required": pol.Requires(u.ID, u.Role), "password": u.PasswordHash != "", "available": s.Box != nil,
	})
}

func (s *Server) setupMyTwoFactor(w http.ResponseWriter, r *http.Request, p model.Principal) {
	u, ok := s.person(w, r, p)
	if !ok {
		return
	}
	// Moving an existing setup to a new phone replaces the secret: that needs a current code or the
	// password, like turning it off, so an open session alone can't take over someone's sign-in.
	ts, _ := s.twoFactor()
	tf, err := ts.TwoFactor(r.Context(), u.TenantID, u.ID)
	if err != nil && !errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return
	}
	if tf.Enabled() && !s.reconfirm(w, r, u) {
		return
	}
	out, err := s.beginSetup(r.Context(), u, tf.Enabled())
	if err != nil {
		s.failCode(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// enableMyTwoFactor confirms the pending secret. While two-step sign-in is on, a pending secret can
// only exist through setupMyTwoFactor with proof (the store refuses one without replace), so
// confirming it replaces the old phone's secret.
func (s *Server) enableMyTwoFactor(w http.ResponseWriter, r *http.Request, p model.Principal) {
	u, ok := s.person(w, r, p)
	if !ok {
		return
	}
	var req codeRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.allowCode(r.Context(), u, false) {
		s.failCode(w, errTooManyCode)
		return
	}
	codes, err := s.confirmSetup(r.Context(), u, req.Code, true)
	if err != nil {
		s.failCode(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// reconfirm checks a current code or the password before a change to two-step sign-in, so a
// session left open on someone's desk can't be used to turn it off.
func (s *Server) reconfirm(w http.ResponseWriter, r *http.Request, u model.User) bool {
	var req codeRequest
	if !decode(w, r, &req) {
		return false
	}
	if !s.allowCode(r.Context(), u, false) {
		s.failCode(w, errTooManyCode)
		return false
	}
	if req.Password != "" {
		if u.PasswordHash == "" || !auth.CheckPassword(u.PasswordHash, req.Password) {
			writeErr(w, http.StatusUnauthorized, "that password isn't right")
			return false
		}
		return true
	}
	if _, err := s.checkCode(r.Context(), u, req.Code); err != nil {
		s.failCode(w, err)
		return false
	}
	return true
}

func (s *Server) disableMyTwoFactor(w http.ResponseWriter, r *http.Request, p model.Principal) {
	u, ok := s.person(w, r, p)
	if !ok {
		return
	}
	ts, _ := s.twoFactor()
	pol, err := ts.TwoFactorPolicy(r.Context(), u.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if pol.Requires(u.ID, u.Role) {
		writeErr(w, http.StatusForbidden, "your workspace requires two-step sign-in, so it can't be turned off. To use a new phone, set it up again")
		return
	}
	if !s.reconfirm(w, r, u) {
		return
	}
	if err := ts.DeleteTwoFactor(r.Context(), u.TenantID, u.ID); err != nil {
		s.fail(w, err)
		return
	}
	// Sessions signed in with the old second step end (all but this one).
	if err := s.revokeOtherSessions(r, u.TenantID, u.ID, true); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) newRecoveryCodes(w http.ResponseWriter, r *http.Request, p model.Principal) {
	u, ok := s.person(w, r, p)
	if !ok || !s.reconfirm(w, r, u) {
		return
	}
	codes, hashes := newRecovery()
	ts, _ := s.twoFactor()
	if err := ts.SetRecoveryCodes(r.Context(), u.TenantID, u.ID, hashes); errors.Is(err, pg.ErrNotFound) {
		s.failCode(w, errNotSetUp)
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// ---- workspace policy and resets ----

func (s *Server) getTwoFactorPolicy(w http.ResponseWriter, r *http.Request, p model.Principal) {
	ts, ok := s.twoFactor()
	if !ok {
		writeJSON(w, http.StatusOK, auth.TwoFactorPolicy{Mode: auth.TwoFactorOptional, Roles: []string{}, Users: []int64{}})
		return
	}
	pol, err := ts.TwoFactorPolicy(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pol)
}

func (s *Server) putTwoFactorPolicy(w http.ResponseWriter, r *http.Request, p model.Principal) {
	ts, ok := s.twoFactor()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "two-step sign-in isn't available on this server")
		return
	}
	var pol auth.TwoFactorPolicy
	if !decode(w, r, &pol) {
		return
	}
	if err := pol.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if pol.Mode != auth.TwoFactorOptional && s.Box == nil {
		s.failCode(w, errNoBox)
		return
	}
	if pol.Mode == auth.TwoFactorSome && len(pol.Roles) == 0 && len(pol.Users) == 0 {
		writeErr(w, http.StatusBadRequest, "choose the roles or people who must use two-step sign-in")
		return
	}
	for _, role := range pol.Roles {
		exists := auth.IsBuiltin(role)
		if rs, ok := s.Store.(RoleStore); ok && !exists {
			var err error
			if exists, err = rs.RoleExists(r.Context(), p.TenantID, role); err != nil {
				s.fail(w, err)
				return
			}
		}
		if !exists {
			writeErr(w, http.StatusBadRequest, "role "+role+" doesn't exist")
			return
		}
	}
	for _, id := range pol.Users {
		if _, err := s.Store.User(r.Context(), p.TenantID, id); errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "teammate "+strconv.FormatInt(id, 10)+" doesn't exist")
			return
		} else if err != nil {
			s.fail(w, err)
			return
		}
	}
	if err := ts.PutTwoFactorPolicy(r.Context(), p.TenantID, pol); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pol)
}

// resetTwoFactor turns a teammate's two-step sign-in off (a lost phone). If the workspace requires
// it, they set it up again at their next sign-in. The audit log records it like every change.
func (s *Server) resetTwoFactor(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ts, ok := s.twoFactor()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "two-step sign-in isn't available on this server")
		return
	}
	if id == p.UserID {
		writeErr(w, http.StatusBadRequest, "turn your own two-step sign-in off under Your account")
		return
	}
	if err := s.canManageUser(r.Context(), p, id); err != nil {
		s.failGrant(w, err)
		return
	}
	if err := ts.DeleteTwoFactor(r.Context(), p.TenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	// A reset usually means a lost phone (or a stolen one): every session of that person ends.
	if err := s.revokeOtherSessions(r, p.TenantID, id, false); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
