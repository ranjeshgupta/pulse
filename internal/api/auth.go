package api

// Sign-in and workspace settings: signup, password login, sessions, team, API key, SSO settings.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/delivery"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

const (
	loginAttempts = 10
	loginWindow   = 15 * time.Minute
)

// dummyHash keeps login timing identical whether or not the email exists (no user enumeration).
var dummyHash, _ = auth.HashPassword("pulse-timing-equalizer")

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a workspace name into a URL-safe slug.
func Slugify(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "workspace"
	}
	return s
}

// withUniqueSlug calls create with the slug of name, then with random suffixes while it conflicts.
func withUniqueSlug(name string, create func(slug string) error) error {
	slug := Slugify(name)
	for attempt := 0; ; attempt++ {
		err := create(slug)
		if !errors.Is(err, pg.ErrConflict) || attempt == 5 {
			return err
		}
		var b [3]byte
		_, _ = rand.Read(b[:])
		slug = Slugify(name) + "-" + hex.EncodeToString(b[:])
	}
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u model.User) error {
	token := auth.NewToken("ps_")
	expires := s.now().Add(sessionTTL)
	if err := s.Store.CreateSession(r.Context(), auth.HashToken(token), u.ID, u.TenantID, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !s.AllowSignup {
		writeErr(w, http.StatusForbidden, "signup is disabled")
		return
	}
	var req struct {
		Workspace string `json:"workspace"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		Password  string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Workspace) == "" {
		writeErr(w, http.StatusBadRequest, "workspace name is required")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeErr(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	key, siteKey := auth.NewToken("pk_"), auth.NewToken("sk_")
	owner := model.User{Email: req.Email, Name: req.Name, PasswordHash: hash}
	var slug string
	err = withUniqueSlug(req.Workspace, func(sl string) error {
		slug = sl
		var err error
		owner.TenantID, owner.ID, err = s.Store.CreateWorkspace(r.Context(), req.Workspace, sl, HashKey(key), siteKey, owner)
		return err
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.startSession(w, r, owner); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"workspace": model.Tenant{ID: owner.TenantID, Name: req.Workspace, Slug: slug, SiteKey: siteKey},
		"api_key":   key, // shown once
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workspace string `json:"workspace"`
		Email     string `json:"email"`
		Password  string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !s.allowLogin(r.Context(), req.Workspace, req.Email) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	const invalid = "invalid workspace, email or password"
	u, err := s.lookupLogin(r.Context(), req.Workspace, req.Email)
	if err != nil && !errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return
	}
	hash := u.PasswordHash
	if hash == "" {
		hash = dummyHash
	}
	if !auth.CheckPassword(hash, req.Password) || u.PasswordHash == "" {
		s.auditSignIn(r, u.TenantID, u.ID, u.Email, "sign-in", false) // only for a known user
		writeErr(w, http.StatusUnauthorized, invalid)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, err)
		return
	}
	s.loginSucceeded(r.Context(), req.Workspace, req.Email)
	s.auditSignIn(r, u.TenantID, u.ID, u.Email, "sign-in", true)
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) lookupLogin(ctx context.Context, slug, email string) (model.User, error) {
	t, err := s.Store.TenantBySlug(ctx, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		return model.User{}, err
	}
	return s.Store.UserByEmail(ctx, t.ID, email)
}

// loginKey is the Redis counter of sign-in attempts for (workspace, email).
func loginKey(slug, email string) string {
	return "login:" + strings.ToLower(slug) + ":" + strings.ToLower(email)
}

// loginSucceeded clears the attempt counter: only failed attempts in a row should lock someone out.
func (s *Server) loginSucceeded(ctx context.Context, slug, email string) {
	if s.RDB != nil {
		s.RDB.Del(ctx, loginKey(slug, email))
	}
}

// allowLogin counts attempts per (workspace, email), before the password is checked so parallel
// guesses can't slip past the limit; a successful sign-in clears the count. Without Redis it allows
// everything.
func (s *Server) allowLogin(ctx context.Context, slug, email string) bool {
	if s.RDB == nil {
		return true
	}
	key := loginKey(slug, email)
	n, err := s.RDB.Incr(ctx, key).Result()
	if err != nil {
		return true // fail open on a Redis outage rather than locking everyone out
	}
	if n == 1 {
		s.RDB.Expire(ctx, key, loginWindow)
	}
	return n <= loginAttempts
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Store.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, p model.Principal) {
	t, err := s.Store.Tenant(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"workspace": t, "role": p.Role, "permissions": p.Perms}
	if p.UserID > 0 {
		u, err := s.Store.User(r.Context(), p.TenantID, p.UserID)
		if err != nil {
			s.fail(w, err)
			return
		}
		out["user"] = u
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- team ----

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, p model.Principal) {
	users, err := s.Store.ListUsers(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// createUser adds a teammate. Without a password the user can only sign in through SSO.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeErr(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	if err := s.grantable(r.Context(), p, req.Role); err != nil {
		s.failGrant(w, err)
		return
	}
	u := model.User{TenantID: p.TenantID, Email: req.Email, Name: req.Name, Role: req.Role}
	if req.Password != "" {
		var err error
		if u.PasswordHash, err = auth.HashPassword(req.Password); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	id, err := s.Store.CreateUser(r.Context(), u)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == p.UserID {
		writeErr(w, http.StatusBadRequest, "you cannot remove yourself")
		return
	}
	if err := s.canManageUser(r.Context(), p, id); err != nil {
		s.failGrant(w, err)
		return
	}
	if err := s.Store.DeleteUser(r.Context(), p.TenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateAPIKey(w http.ResponseWriter, r *http.Request, p model.Principal) {
	key := auth.NewToken("pk_")
	if err := s.Store.RotateAPIKey(r.Context(), p.TenantID, HashKey(key)); err != nil {
		s.fail(w, err)
		return
	}
	s.forgetKeys() // the old key stops working immediately on this instance
	writeJSON(w, http.StatusOK, map[string]any{"api_key": key})
}

// ---- SSO settings ----

func (s *Server) getSSO(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, _, err := s.Store.GetSSO(r.Context(), p.TenantID)
	if errors.Is(err, pg.ErrNotFound) {
		writeJSON(w, http.StatusOK, model.SSOConfig{AutoCreateUsers: true, DefaultRole: auth.RoleEditor})
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) putSSO(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var c model.SSOConfig
	if !decode(w, r, &c) {
		return
	}
	if c.DefaultRole == "" {
		c.DefaultRole = auth.RoleEditor
	}
	if err := s.grantable(r.Context(), p, c.DefaultRole); err != nil {
		s.failGrant(w, err)
		return
	}
	for _, u := range []string{c.Issuer, c.DiscoveryURL} {
		if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			writeErr(w, http.StatusBadRequest, "issuer and discovery_url must be http(s) URLs")
			return
		}
	}
	if c.Issuer == "" || c.ClientID == "" {
		writeErr(w, http.StatusBadRequest, "issuer and client_id are required")
		return
	}
	var sealed []byte
	if c.ClientSecret != "" {
		if s.Box == nil {
			writeErr(w, http.StatusServiceUnavailable, "server has no secret key configured")
			return
		}
		sealed = s.Box.Seal([]byte(c.ClientSecret))
	} else if _, old, err := s.Store.GetSSO(r.Context(), p.TenantID); err != nil || len(old) == 0 {
		writeErr(w, http.StatusBadRequest, "client_secret is required")
		return
	}
	if err := s.Store.PutSSO(r.Context(), p.TenantID, c, sealed); err != nil {
		s.fail(w, err)
		return
	}
	s.sso.forget(p.TenantID)
	c.ClientSecret, c.HasClientSecret = "", true
	writeJSON(w, http.StatusOK, c)
}

// deleteSSO removes single sign-on: the settings and the stored client secret. People then sign in
// with a password (those who only ever used SSO reset theirs or are invited again).
func (s *Server) deleteSSO(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if err := s.Store.DeleteSSO(r.Context(), p.TenantID); err != nil && !errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return
	}
	s.sso.forget(p.TenantID)
	writeJSON(w, http.StatusOK, model.SSOConfig{AutoCreateUsers: true, DefaultRole: auth.RoleEditor})
}

// ---- delivery rules ----

type DeliveryStore interface {
	DeliveryRules(ctx context.Context, tenant int64) (delivery.Rules, error)
	PutDeliveryRules(ctx context.Context, tenant int64, r delivery.Rules) error
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request, p model.Principal) {
	rules, err := s.Store.(DeliveryStore).DeliveryRules(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) putDelivery(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var rules delivery.Rules
	if !decode(w, r, &rules) {
		return
	}
	if rules.Caps == nil {
		rules.Caps = map[string]delivery.Cap{}
	}
	if rules.Keywords.OptOut == nil && rules.Keywords.OptIn == nil {
		rules.Keywords = delivery.DefaultKeywords // a client that doesn't know about keywords
	}
	for _, list := range [][]string{rules.Keywords.OptOut, rules.Keywords.OptIn} {
		for i := range list {
			list[i] = strings.ToUpper(strings.TrimSpace(list[i]))
		}
	}
	if err := rules.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.(DeliveryStore).PutDeliveryRules(r.Context(), p.TenantID, rules); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}
