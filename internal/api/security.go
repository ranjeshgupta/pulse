package api

// Hardening that spans routes: sign-in form CSRF, the session cookie's name, signup limits,
// rate limits on expensive queries, HSTS, password change and signing out everywhere.

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// secureSessionCookie is the session cookie's name behind https: the __Host- prefix makes the
// browser insist on Secure, Path=/ and no Domain, so a sibling subdomain can't plant or shadow it.
const secureSessionCookie = "__Host-pulse_session"

// sessionCookieName is the name new sessions get.
func (s *Server) sessionCookieName() string {
	if s.secureCookies() {
		return secureSessionCookie
	}
	return sessionCookie
}

// sessionToken reads the session cookie under its one current name. Over https that's only the
// __Host- name: a plain-named cookie could be planted by a sibling subdomain, so it is never read
// there (the plain name is for local http, where __Host- cookies can't be set).
func (s *Server) sessionToken(r *http.Request) string {
	if c, err := r.Cookie(s.sessionCookieName()); err == nil {
		return c.Value
	}
	return ""
}

// clearSessionCookies removes the session cookie under both names.
func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	if s.secureCookies() {
		http.SetCookie(w, &http.Cookie{Name: secureSessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
			Secure: true, SameSite: http.SameSiteLaxMode})
	}
}

// authFormGuard refuses sign-in requests a cross-site page could send: an HTML form can post
// text/plain or form data to /v1/auth/login (signing the victim in to the attacker's account), but
// can't send JSON or a custom header. Every POST to /v1/auth/* must be JSON or carry X-Pulse-CSRF.
// Public forms (/f/, /s/, one-click unsubscribe) are elsewhere and unaffected.
func authFormGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/auth/") && r.Header.Get(csrfHeader) == "" {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mt != "application/json" {
				writeErr(w, http.StatusForbidden, "send JSON (Content-Type: application/json) or the "+csrfHeader+" header")
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// defaultSignupRate is signups per client IP per minute when Deps.SignupRate is 0.
const defaultSignupRate = 5

// allowSignup limits signups per client IP and in total (ten times the per-IP rate), per minute.
func (s *Server) allowSignup(r *http.Request) bool {
	n := s.SignupRate
	if n <= 0 {
		n = defaultSignupRate
	}
	return s.allowRate(r.Context(), "signup", 0, r, 1, n) && s.allowTenant(r.Context(), "signup-all", 0, 10*n)
}

// heavyRoutes are the requests that run large ClickHouse queries or exports, with how many each
// workspace may make a minute. The limit is applied around the routes (heavyLimit), so it covers
// handlers in any file.
var heavyRoutes = map[string]int{
	"POST /v1/reports/run":                    30,
	"POST /v1/funnels/run":                    30,
	"POST /v1/cohorts/run":                    30,
	"POST /v1/analytics/paths":                20,
	"POST /v1/segments/preview":               60,
	"POST /v1/revenue/report":                 30,
	"POST /v1/revenue/customer-value/refresh": 2,
	"GET /v1/contacts/export":                 5,
}

// heavyLimit answers 429 when a workspace makes too many expensive requests in a minute. Callers
// that aren't signed in pass through (the route itself refuses them).
func (s *Server) heavyLimit(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.Method + " " + r.URL.Path
		if limit, ok := heavyRoutes[route]; ok && s.RDB != nil {
			if p, err := s.authenticate(r); err == nil && !s.allowTenant(r.Context(), "heavy:"+route, p.TenantID, limit) {
				w.Header().Set("Retry-After", "60")
				writeErr(w, http.StatusTooManyRequests, "this workspace ran "+strconv.Itoa(limit)+
					" of these in the last minute, the most allowed; wait a minute and try again")
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// queryLimits puts the ClickHouse limits on every request's context, so whatever query a handler
// runs (in any file) is bounded. Exports stream every row and keep only the memory limit.
func (s *Server) queryLimits(h http.Handler) http.Handler {
	l := s.CHLimits
	if l == (ch.Limits{}) {
		l = ch.DefaultLimits
	}
	export := l
	export.MaxExecutionTime = 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		use := l
		if r.URL.Path == "/v1/contacts/export" { // the contact export streams everything; only it has no time limit
			use = export
		}
		h.ServeHTTP(w, r.WithContext(ch.WithLimits(r.Context(), use)))
	})
}

// hsts adds Strict-Transport-Security when the app is served over https.
func (s *Server) hsts(h http.Handler) http.Handler {
	if !s.secureCookies() {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.ServeHTTP(w, r)
	})
}

// SessionStore signs users out and changes passwords (pg.Store).
type SessionStore interface {
	DeleteUserSessions(ctx context.Context, tenant, user int64, keepHash string) error
	SetPassword(ctx context.Context, tenant, user int64, hash string) error
}

func (s *Server) sessionStore() (SessionStore, bool) {
	ss, ok := s.Store.(SessionStore)
	return ss, ok
}

func (s *Server) accountSecurityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/account/password", s.authed(s.changePassword))
	mux.HandleFunc("POST /v1/account/sessions/revoke", s.authed(s.revokeSessions))
}

// revokeOtherSessions signs the user out of every session but the request's own (all of them when
// the request has none, e.g. an admin acting on someone else).
func (s *Server) revokeOtherSessions(r *http.Request, tenant, user int64, keepCurrent bool) error {
	ss, ok := s.sessionStore()
	if !ok || user == 0 {
		return nil
	}
	keep := ""
	if tok := s.sessionToken(r); keepCurrent && tok != "" {
		keep = auth.HashToken(tok)
	}
	return ss.DeleteUserSessions(r.Context(), tenant, user, keep)
}

// passwordChangesPerHour caps password change attempts per user (each checks the current password).
const passwordChangesPerHour = 10

// changePassword sets a new password after checking the current one, and signs the user out of
// every other session.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if s.refuseShared(w, r, p) {
		return
	}
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if p.UserID == 0 {
		writeErr(w, http.StatusBadRequest, "API keys have no password; sign in as a person")
		return
	}
	ss, ok := s.sessionStore()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "password changes aren't available")
		return
	}
	if !s.allowLimit(r.Context(), "pwchange:"+strconv.FormatInt(p.UserID, 10), passwordChangesPerHour) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts; try again in an hour")
		return
	}
	u, err := s.Store.User(r.Context(), p.TenantID, p.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if u.PasswordHash == "" {
		writeErr(w, http.StatusBadRequest, "your account signs in with single sign-on and has no password here")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.Current) {
		writeErr(w, http.StatusForbidden, "the current password is wrong")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ss.SetPassword(r.Context(), p.TenantID, p.UserID, hash); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.revokeOtherSessions(r, p.TenantID, p.UserID, true); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "other_sessions_signed_out": true})
}

// revokeSessions is "sign out everywhere": every session of the user ends, this one too.
func (s *Server) revokeSessions(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if s.refuseShared(w, r, p) { // it would sign out everyone else using the login
		return
	}
	if p.UserID == 0 {
		writeErr(w, http.StatusBadRequest, "API keys have no sessions")
		return
	}
	if err := s.revokeOtherSessions(r, p.TenantID, p.UserID, false); err != nil {
		s.fail(w, err)
		return
	}
	s.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// allowLimit counts toward a fixed hourly limit under key (Redis; without it everything passes).
func (s *Server) allowLimit(ctx context.Context, key string, limit int) bool {
	if s.RDB == nil {
		return true
	}
	k := "limit:" + key + ":" + strconv.FormatInt(s.now().Unix()/3600, 10)
	n, err := s.RDB.Incr(ctx, k).Result()
	if err != nil {
		return true
	}
	if n == 1 {
		s.RDB.Expire(ctx, k, 2*time.Hour)
	}
	return n <= int64(limit)
}

var errSSOLink = errors.New("sso link needed")

// ssoLinkUser links an SSO identity whose email belongs to a password account, when the browser
// is signed in as that very account (the person proved the password). Otherwise errSSOLink.
func (s *Server) ssoLinkUser(r *http.Request, tenant int64, email, subject string) (model.User, error) {
	ls, ok := s.Store.(interface {
		LinkSSO(ctx context.Context, tenant, user int64, subject string) (model.User, error)
	})
	tok := s.sessionToken(r)
	if !ok || tok == "" {
		return model.User{}, errSSOLink
	}
	p, err := s.Store.SessionPrincipal(r.Context(), auth.HashToken(tok))
	if err != nil || p.TenantID != tenant || !strings.EqualFold(p.Actor, email) {
		return model.User{}, errSSOLink
	}
	u, err := ls.LinkSSO(r.Context(), tenant, p.UserID, subject)
	if errors.Is(err, pg.ErrNotFound) {
		return model.User{}, errSSOLink // linked to another identity already
	}
	return u, err
}

// sharedLoginMsg is the refusal for a shared login's sign-in settings.
const sharedLoginMsg = "this is a shared login (such as a public demo): its password and two-step sign-in can't be changed"

// sharedLogin reports whether email in the given workspace is one of the server's shared logins
// (PULSE_SHARED_LOGINS, "workspace-slug:email" pairs). Several people sign in with it, so none of
// them may change how it signs in. Only that workspace's user matches, never the same email
// elsewhere.
func (s *Server) sharedLogin(ctx context.Context, tenant int64, email string) bool {
	if len(s.SharedLogins) == 0 || tenant == 0 {
		return false
	}
	t, err := s.Store.Tenant(ctx, tenant)
	if err != nil {
		return false
	}
	want := strings.ToLower(t.Slug) + ":" + strings.ToLower(strings.TrimSpace(email))
	for _, e := range s.SharedLogins {
		if e == want {
			return true
		}
	}
	return false
}

// refuseShared answers 403 and reports true when p is a person signed in with a shared login.
func (s *Server) refuseShared(w http.ResponseWriter, r *http.Request, p model.Principal) bool {
	if p.UserID > 0 && s.sharedLogin(r.Context(), p.TenantID, p.Actor) {
		writeErr(w, http.StatusForbidden, sharedLoginMsg)
		return true
	}
	return false
}
