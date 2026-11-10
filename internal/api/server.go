// Package api serves the HTTP API used by the web app, servers (API key) and browsers (site key).
//
// Authentication, per route group:
//   - session cookie (web app users), with an X-Pulse-CSRF header required on state-changing requests
//   - "Authorization: Bearer <API key>" (servers; acts as an admin of its workspace)
//   - public site key (POST /v1/web/*, called from customer websites by the JS SDK)
//
// Every query is scoped to the caller's workspace (tenant).
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/geoip"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/store/pg"
	"pulse/internal/tracking"
)

const (
	maxBody      = 4 << 20
	maxBatch     = 1000
	authCacheTTL = time.Minute

	sessionCookie = "pulse_session"
	sessionTTL    = 14 * 24 * time.Hour
	csrfHeader    = "X-Pulse-CSRF"
)

type Store interface {
	// workspaces, users, sessions, SSO
	CreateTenant(ctx context.Context, name, slug, keyHash, siteKey string) (int64, error)
	CreateWorkspace(ctx context.Context, name, slug, keyHash, siteKey string, owner model.User) (int64, int64, error)
	TenantByKeyHash(ctx context.Context, keyHash string) (int64, error)
	Tenant(ctx context.Context, id int64) (model.Tenant, error)
	TenantBySlug(ctx context.Context, slug string) (model.Tenant, error)
	TenantBySiteKey(ctx context.Context, key string) (model.Tenant, error)
	RotateAPIKey(ctx context.Context, tenant int64, keyHash string) error
	UserByEmail(ctx context.Context, tenant int64, email string) (model.User, error)
	User(ctx context.Context, tenant, id int64) (model.User, error)
	ListUsers(ctx context.Context, tenant int64) ([]model.User, error)
	CreateUser(ctx context.Context, u model.User) (int64, error)
	DeleteUser(ctx context.Context, tenant, id int64) error
	SSOUser(ctx context.Context, tenant int64, subject, email, name string, autoCreate bool, role string) (model.User, error)
	CreateSession(ctx context.Context, tokenHash string, userID, tenantID int64, expires time.Time) error
	SessionPrincipal(ctx context.Context, tokenHash string) (model.Principal, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	GetSSO(ctx context.Context, tenant int64) (model.SSOConfig, []byte, error)
	PutSSO(ctx context.Context, tenant int64, c model.SSOConfig, sealedSecret []byte) error

	// marketing objects
	CreateSegment(ctx context.Context, tenant int64, name string, rule []byte, hasWindow bool) (int64, error)
	ListSegments(ctx context.Context, tenant int64) ([]model.Segment, error)
	GetSegment(ctx context.Context, tenant, id int64) (model.Segment, error)
	CreateTemplate(ctx context.Context, t model.Template) (int64, error)
	CreateJourney(ctx context.Context, tenant int64, name string, graph []byte, w schedule.Window) (int64, error)
	SetJourneyStatus(ctx context.Context, tenant, id int64, status string) error
	JourneyStats(ctx context.Context, tenant, id int64) (map[string]map[string]int, error)
	ContactByExternalID(ctx context.Context, tenant int64, externalID string) (model.Contact, error)
}

type Membership interface {
	IsMember(ctx context.Context, tenant, segment int64, contact uint32) (bool, error)
	Size(ctx context.Context, tenant, segment int64) (int64, error)
}

// Deps are the server's collaborators. Box, RDB and PublicURL are needed for web sign-in.
type Deps struct {
	Store       Store
	Members     Membership
	Analytics   Analytics // optional: segment previews, timelines, dashboard
	Pub         kafka.Publisher
	RDB         redis.Cmdable   // login rate limiting
	Box         *auth.Box       // seals SSO state cookies and stored secrets
	Tracking    *tracking.Codec // email tracking and unsubscribe links
	AdminToken  string          // platform operator token for POST /v1/tenants
	PublicURL   string          // e.g. https://app.example.com, used for SSO redirects and Secure cookies
	AllowSignup bool
	Log         *slog.Logger
	// AllowPrivateSSO lets SSO discovery and token calls reach private addresses (internal IdPs).
	AllowPrivateSSO bool
	// AllowPrivateWebhooks lets webhook tests reach private addresses (local development).
	AllowPrivateWebhooks bool
	// PagesURL is a separate origin for public, workspace-authored content: landing pages, asset
	// downloads and hosted forms (e.g. https://pages.example.com). Set it in production so that
	// content never shares an origin (or cookies) with the app. Empty serves them from PublicURL.
	PagesURL string
	// Geo locates web and app visitors by IP (nil: off).
	Geo *geoip.Locator
	// TrustProxy takes the client IP from the right-most X-Forwarded-For entry (your proxy's).
	TrustProxy bool
}

type Server struct {
	Deps
	now func() time.Time
	sso *ssoProviders
	ui  http.Handler

	// bg runs background work (fan-out of company and stage changes); tests run it inline.
	bg func(func())

	mu        sync.Mutex
	authCache map[string]cachedTenant
	dataCache map[int64]cachedTracking
}

type cachedTenant struct {
	id  int64
	exp time.Time
}

func New(d Deps) *Server {
	return &Server{Deps: d, now: time.Now, sso: newSSOProviders(), authCache: map[string]cachedTenant{},
		dataCache: map[int64]cachedTracking{}, bg: func(f func()) { go f() }}
}

// WithUI serves the web app for every path that is not an API route.
func (s *Server) WithUI(h http.Handler) *Server {
	s.ui = h
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/tenants", s.createTenant)

	// sign-in
	mux.HandleFunc("POST /v1/auth/signup", s.signup)
	mux.HandleFunc("POST /v1/auth/login", s.login)
	mux.HandleFunc("POST /v1/auth/logout", s.logout)
	mux.HandleFunc("GET /v1/auth/me", s.authed(s.me))
	mux.HandleFunc("GET /v1/auth/sso", s.ssoInfo)
	mux.HandleFunc("GET /v1/auth/sso/start", s.ssoStart)
	mux.HandleFunc("GET /v1/auth/sso/callback", s.ssoCallback)

	// workspace settings (admins)
	mux.HandleFunc("GET /v1/users", s.authed(s.listUsers))
	mux.HandleFunc("POST /v1/users", s.admin(s.createUser))
	mux.HandleFunc("DELETE /v1/users/{id}", s.admin(s.deleteUser))
	mux.HandleFunc("GET /v1/settings/sso", s.fullAdmin(s.getSSO))
	mux.HandleFunc("PUT /v1/settings/sso", s.fullAdmin(s.putSSO))
	mux.HandleFunc("POST /v1/settings/api-key", s.fullAdmin(s.rotateAPIKey))
	mux.HandleFunc("GET /v1/settings/delivery", s.authed(s.getDelivery))
	mux.HandleFunc("PUT /v1/settings/delivery", s.admin(s.putDelivery))

	// marketing objects
	mux.HandleFunc("POST /v1/events", s.tenant(s.ingest))
	mux.HandleFunc("POST /v1/segments", s.tenant(s.createSegment))
	mux.HandleFunc("GET /v1/segments", s.tenant(s.listSegments))
	mux.HandleFunc("GET /v1/segments/{id}", s.tenant(s.getSegment))
	mux.HandleFunc("POST /v1/templates", s.tenant(s.createTemplate))
	mux.HandleFunc("POST /v1/journeys", s.tenant(s.createJourney))
	mux.HandleFunc("POST /v1/journeys/{id}/{action}", s.tenant(s.journeyAction))
	mux.HandleFunc("GET /v1/journeys/{id}/stats", s.tenant(s.journeyStats))
	mux.HandleFunc("GET /v1/contacts/{external_id}", s.tenant(s.getContact))
	s.uiRoutes(mux)
	s.channelRoutes(mux)
	s.onsiteRoutes(mux)
	s.broadcastRoutes(mux)
	s.trackingRoutes(mux)
	s.importRoutes(mux)
	s.webRoutes(mux)
	s.audienceRoutes(mux)
	s.accountRoutes(mux)
	s.reportRoutes(mux)
	s.formRoutes(mux)
	s.roleRoutes(mux)
	s.webhookRoutes(mux)
	s.backgroundImportRoutes(mux)
	s.bounceRoutes(mux)
	s.webPushRoutes(mux)
	s.inboxRoutes(mux)
	s.inappRoutes(mux)
	s.dataSettingsRoutes(mux)
	s.auditRoutes(mux)
	s.preferenceRoutes(mux)
	s.assetRoutes(mux)
	s.landingRoutes(mux)

	if s.ui != nil {
		mux.Handle("/", s.ui)
	}
	return bodyLimit(securityHeaders(s.splitPages(mux)))
}

// publicBase is where public pages live: PagesURL, or PublicURL when there's no separate origin.
func (s *Server) publicBase() string {
	if s.PagesURL != "" {
		return strings.TrimRight(s.PagesURL, "/")
	}
	return strings.TrimRight(s.PublicURL, "/")
}

// publicPagePath reports whether a path is workspace-authored public content.
func publicPagePath(p string) bool {
	return strings.HasPrefix(p, "/p/") || strings.HasPrefix(p, "/a/") || strings.HasPrefix(p, "/f/")
}

// splitPages keeps public pages and the app apart when PagesURL is set: on the pages host only
// public pages and what they load (the SDK, its API) are served; on the app host, public pages
// redirect to the pages host.
func (s *Server) splitPages(h http.Handler) http.Handler {
	u, err := url.Parse(s.PagesURL)
	if s.PagesURL == "" || err != nil || u.Host == "" {
		return h
	}
	pagesHost := strings.ToLower(u.Host)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && s.TrustProxy {
			host = strings.ToLower(strings.TrimSpace(strings.Split(fh, ",")[0]))
		}
		p := r.URL.Path
		if host == pagesHost {
			if publicPagePath(p) || strings.HasPrefix(p, "/sdk/") || strings.HasPrefix(p, "/v1/web/") || p == "/healthz" {
				h.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		if publicPagePath(p) {
			http.Redirect(w, r, s.publicBase()+r.URL.RequestURI(), http.StatusPermanentRedirect)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// bodyLimit caps request bodies; CSV imports get a larger allowance than everything else.
func bodyLimit(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxBody)
		switch r.URL.Path {
		case "/v1/contacts/import":
			limit = maxImportBytes
		case "/v1/imports":
			limit = maxBackgroundSize
		case "/v1/assets":
			limit = maxAssetUpload
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		h.ServeHTTP(w, r)
	})
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		// The site-key API, hosted forms and landing pages are embedded on customer sites.
		if p := r.URL.Path; !strings.HasPrefix(p, "/v1/web/") && !strings.HasPrefix(p, "/f/") && !strings.HasPrefix(p, "/p/") {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		h.ServeHTTP(w, r)
	})
}

// ---- authentication ----

type principalHandler func(w http.ResponseWriter, r *http.Request, p model.Principal)
type tenantHandler func(w http.ResponseWriter, r *http.Request, tenant int64)

var errUnauthenticated = errors.New("unauthenticated")

// authenticate resolves the caller from an API key or a session cookie.
func (s *Server) authenticate(r *http.Request) (model.Principal, error) {
	if key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && key != "" {
		id, err := s.tenantByKey(r.Context(), key)
		if err != nil {
			return model.Principal{}, err
		}
		perms, _ := auth.BuiltinPerms(auth.RoleAdmin)
		return model.Principal{TenantID: id, Role: auth.RoleAdmin, Perms: perms, Actor: "API key"}, nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return model.Principal{}, errUnauthenticated
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) == "" {
		return model.Principal{}, errCSRF
	}
	p, err := s.Store.SessionPrincipal(r.Context(), auth.HashToken(c.Value))
	if errors.Is(err, pg.ErrNotFound) {
		return model.Principal{}, errUnauthenticated
	}
	return p, err
}

var errCSRF = errors.New("missing " + csrfHeader + " header")

func (s *Server) tenantByKey(ctx context.Context, key string) (int64, error) {
	hash := auth.HashToken(key)
	s.mu.Lock()
	c, hit := s.authCache[hash]
	s.mu.Unlock()
	if hit && s.now().Before(c.exp) {
		return c.id, nil
	}
	id, err := s.Store.TenantByKeyHash(ctx, hash)
	if errors.Is(err, pg.ErrNotFound) {
		return 0, errUnauthenticated
	} else if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.authCache[hash] = cachedTenant{id: id, exp: s.now().Add(authCacheTTL)}
	s.mu.Unlock()
	return id, nil
}

func (s *Server) forgetKeys() {
	s.mu.Lock()
	s.authCache = map[string]cachedTenant{}
	s.mu.Unlock()
}

func (s *Server) authed(h principalHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r)
		switch {
		case errors.Is(err, errUnauthenticated):
			writeErr(w, http.StatusUnauthorized, "sign in or send a valid API key")
		case errors.Is(err, errCSRF):
			writeErr(w, http.StatusForbidden, err.Error())
		case err != nil:
			s.fail(w, err)
		default:
			if n := required(r); n.area != "" && !p.Perms.Allows(n.area, n.level) {
				writeErr(w, http.StatusForbidden, "your role doesn't allow this ("+n.level.String()+" access to "+n.area+" is needed)")
				return
			}
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				h(w, r, p)
				return
			}
			s.audited(w, r, p, func(w http.ResponseWriter, r *http.Request) { h(w, r, p) })
		}
	}
}

// fullAdmin is for settings that amount to all access: SSO (whoever controls the identity provider
// can sign in as anyone), the API key (it acts as an admin), identity-verification secrets and
// webhooks (they can copy every contact out). Only people with every admin permission may use them.
func (s *Server) fullAdmin(h principalHandler) http.HandlerFunc {
	all, _ := auth.BuiltinPerms(auth.RoleAdmin)
	return s.authed(func(w http.ResponseWriter, r *http.Request, p model.Principal) {
		if !p.Perms.Covers(all) {
			writeErr(w, http.StatusForbidden, "only admins can change this")
			return
		}
		h(w, r, p)
	})
}

// admin is for workspace settings: it needs settings edit access (admins, and custom roles given it).
func (s *Server) admin(h principalHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p model.Principal) {
		if !p.Perms.Allows(auth.AreaSettings, auth.Edit) {
			writeErr(w, http.StatusForbidden, "admins only")
			return
		}
		h(w, r, p)
	})
}

// tenant adapts handlers that only need the workspace id.
func (s *Server) tenant(h tenantHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p model.Principal) { h(w, r, p.TenantID) })
}

// HashKey is how API keys are stored: only the SHA-256 is kept.
func HashKey(key string) string { return auth.HashToken(key) }

// createTenant is for the platform operator (and tests): a workspace without users.
func (s *Server) createTenant(w http.ResponseWriter, r *http.Request) {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.AdminToken == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.AdminToken)) != 1 {
		writeErr(w, http.StatusUnauthorized, "admin token required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	key, siteKey := auth.NewToken("pk_"), auth.NewToken("sk_")
	var id int64
	err := withUniqueSlug(req.Name, func(slug string) error {
		var err error
		id, err = s.Store.CreateTenant(r.Context(), req.Name, slug, HashKey(key), siteKey)
		return err
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "api_key": key, "site_key": siteKey})
}

func (s *Server) secureCookies() bool {
	u, err := url.Parse(s.PublicURL)
	return err == nil && u.Scheme == "https"
}
