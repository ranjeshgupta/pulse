package api

// Single sign-on with any OIDC provider (Keycloak, Azure AD, Google Workspace, Okta), configured per
// workspace. Authorization code flow with PKCE and a nonce. The flow state lives in a short-lived
// cookie sealed with the server key, so nothing is stored server side between redirect and callback.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/safehttp"
	"pulse/internal/store/pg"
)

const (
	ssoCookie   = "pulse_sso"
	ssoFlowTTL  = 10 * time.Minute
	providerTTL = 10 * time.Minute
)

type ssoClient struct {
	cfg      model.SSOConfig
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	http     *http.Client // refuses private addresses unless the operator allows them
	exp      time.Time
}

type ssoProviders struct {
	mu      sync.Mutex
	clients map[int64]*ssoClient
}

func newSSOProviders() *ssoProviders { return &ssoProviders{clients: map[int64]*ssoClient{}} }

func (p *ssoProviders) forget(tenant int64) {
	p.mu.Lock()
	delete(p.clients, tenant)
	p.mu.Unlock()
}

// client builds (and caches) the OIDC client for a workspace from its stored settings.
func (s *Server) ssoClient(ctx context.Context, tenant int64) (*ssoClient, error) {
	s.sso.mu.Lock()
	c := s.sso.clients[tenant]
	s.sso.mu.Unlock()
	if c != nil && s.now().Before(c.exp) {
		return c, nil
	}
	cfg, sealed, err := s.Store.GetSSO(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, pg.ErrNotFound
	}
	if s.Box == nil {
		return nil, errors.New("server has no secret key configured")
	}
	secret, err := s.Box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("open client secret: %w", err)
	}
	discovery := cfg.Issuer
	dctx := context.WithoutCancel(ctx)
	if cfg.DiscoveryURL != "" {
		// The server reaches the IdP on an internal address, while tokens carry the public issuer.
		discovery = cfg.DiscoveryURL
		dctx = oidc.InsecureIssuerURLContext(dctx, cfg.Issuer)
	}
	// Workspace admins type these URLs, so calls to them can't reach the server's own network
	// unless the operator allows it (an IdP on the internal network).
	client := safehttp.Client(10*time.Second, s.AllowPrivateSSO)
	dctx = oidc.ClientContext(dctx, client)
	provider, err := oidc.NewProvider(dctx, discovery)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	c = &ssoClient{
		cfg: cfg,
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: string(secret),
			Endpoint:     provider.Endpoint(),
			RedirectURL:  strings.TrimRight(s.PublicURL, "/") + "/v1/auth/sso/callback",
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		exp:      s.now().Add(providerTTL),
		http:     client,
	}
	s.sso.mu.Lock()
	s.sso.clients[tenant] = c
	s.sso.mu.Unlock()
	return c, nil
}

// ssoInfo tells the login page whether a workspace offers SSO.
func (s *Server) ssoInfo(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.TenantBySlug(r.Context(), r.URL.Query().Get("workspace"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
		return
	}
	cfg, _, err := s.Store.GetSSO(r.Context(), t.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": err == nil && cfg.Enabled})
}

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.TenantBySlug(r.Context(), r.URL.Query().Get("workspace"))
	if err != nil {
		loginRedirect(w, r, "unknown_workspace")
		return
	}
	c, err := s.ssoClient(r.Context(), t.ID)
	if err != nil {
		s.Log.Error("sso start", "tenant", t.ID, "err", err)
		loginRedirect(w, r, "sso_unavailable")
		return
	}
	state, nonce, verifier := auth.NewToken(""), auth.NewToken(""), oauth2.GenerateVerifier()
	exp := s.now().Add(ssoFlowTTL).Unix()
	flow := strings.Join([]string{strconv.FormatInt(t.ID, 10), state, nonce, verifier, strconv.FormatInt(exp, 10)}, "|")
	http.SetCookie(w, &http.Cookie{
		Name: ssoCookie, Value: s.Box.SealString(flow), Path: "/v1/auth/sso", MaxAge: int(ssoFlowTTL.Seconds()),
		HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, c.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cookie, err := r.Cookie(ssoCookie)
	if err != nil || s.Box == nil {
		loginRedirect(w, r, "sso_expired")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Path: "/v1/auth/sso", MaxAge: -1})
	flow, err := s.Box.OpenString(cookie.Value)
	parts := strings.Split(flow, "|")
	if err != nil || len(parts) != 5 {
		loginRedirect(w, r, "sso_expired")
		return
	}
	tenant, _ := strconv.ParseInt(parts[0], 10, 64)
	state, nonce, verifier := parts[1], parts[2], parts[3]
	exp, _ := strconv.ParseInt(parts[4], 10, 64)
	if s.now().Unix() > exp || q.Get("state") != state {
		loginRedirect(w, r, "sso_expired")
		return
	}
	if e := q.Get("error"); e != "" {
		loginRedirect(w, r, "sso_denied")
		return
	}
	c, err := s.ssoClient(r.Context(), tenant)
	if err != nil {
		loginRedirect(w, r, "sso_unavailable")
		return
	}
	tok, err := c.oauth.Exchange(context.WithValue(r.Context(), oauth2.HTTPClient, c.http), q.Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		s.Log.Error("sso code exchange", "tenant", tenant, "err", err)
		loginRedirect(w, r, "sso_failed")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := c.verifier.Verify(r.Context(), raw)
	if err != nil || idt.Nonce != nonce {
		s.Log.Error("sso id token", "tenant", tenant, "err", err)
		loginRedirect(w, r, "sso_failed")
		return
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil || claims.Email == "" || (claims.EmailVerified != nil && !*claims.EmailVerified) {
		loginRedirect(w, r, "sso_no_verified_email")
		return
	}
	u, err := s.Store.SSOUser(r.Context(), tenant, idt.Subject, claims.Email, claims.Name, c.cfg.AutoCreateUsers, c.cfg.DefaultRole)
	if errors.Is(err, pg.ErrNotFound) {
		loginRedirect(w, r, "sso_not_provisioned")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func loginRedirect(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(code), http.StatusFound)
}
