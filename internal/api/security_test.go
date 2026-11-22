package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/store/pg"
	"pulse/internal/webhooks"
)

// A cross-site page can post a text/plain form to the sign-in routes (login CSRF): every POST to
// /v1/auth/* must be JSON or carry the CSRF header.
func TestAuthPostsRefuseCrossSiteForms(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	for _, path := range []string{"/v1/auth/login", "/v1/auth/signup", "/v1/auth/logout", "/v1/auth/two-factor/verify"} {
		for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{"workspace":"a","email":"a@b.co","password":"x"}`))
			if ct != "" {
				r.Header.Set("Content-Type", ct)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "application/json") {
				t.Errorf("%s as %q: %d %s", path, ct, w.Code, w.Body)
			}
		}
	}
	r := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(`{"workspace":"a","email":"a@b.co","password":"x"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("JSON login: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(`{"workspace":"a","email":"a@b.co","password":"x"}`))
	r.Header.Set(csrfHeader, "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("login with the CSRF header: %d %s", w.Code, w.Body)
	}
	// Public forms are not under /v1/auth and keep working as plain form posts.
	r = httptest.NewRequest("POST", "/f/fkey", strings.NewReader("email=a@b.co"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "application/json") {
		t.Errorf("public form refused by the sign-in guard: %d %s", w.Code, w.Body)
	}
}

func redisServer(t *testing.T, s *Server) {
	t.Helper()
	s.RDB = redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
}

func TestSignupIsRateLimited(t *testing.T) {
	s, _, _ := newServer()
	redisServer(t, s)
	s.SignupRate = 2
	// The limits count per clock minute: a fixed clock keeps a slow run from crossing into the next one.
	fixed := time.Date(2026, 11, 21, 12, 0, 10, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	h := s.Handler()
	signup := func(i int, ip string) int {
		r := httptest.NewRequest("POST", "/v1/auth/signup", strings.NewReader(`{"workspace":"W`+string(rune('a'+i))+`","email":"o`+string(rune('a'+i))+`@acme.io","password":"long enough pw"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c1, c2, c3 := signup(0, "203.0.113.1"), signup(1, "203.0.113.1"), signup(2, "203.0.113.1"); c1 != 201 || c2 != 201 || c3 != 429 {
		t.Errorf("per IP: %d %d %d, want 201 201 429", c1, c2, c3)
	}
	if c := signup(3, "203.0.113.2"); c != 201 {
		t.Errorf("another IP: %d", c)
	}
	// All IPs together: ten times the per-IP rate a minute.
	n429 := 0
	for i := 0; i < 30; i++ {
		if signup(4+i, "198.51.100."+string(rune('0'+i%10))+string(rune('0'+i/10))) == 429 {
			n429++
		}
	}
	if n429 == 0 {
		t.Error("no global signup limit")
	}
	// Signup is off unless the operator turns it on.
	off := New(Deps{Store: s.Store, PublicURL: "http://localhost:7080", Log: s.Log})
	if w := browser(t, off.Handler(), "POST", "/v1/auth/signup", `{"workspace":"A","email":"a@b.co","password":"long enough pw"}`, nil, false); w.Code != 403 {
		t.Errorf("signup without AllowSignup: %d", w.Code)
	}
}

// sessionStore adds password changes and "sign out everywhere" to the in-memory store.
type sessionMem struct{ *memStore }

func (m sessionMem) DeleteUserSessions(_ context.Context, tenant, user int64, keep string) error {
	for h, p := range m.sessions {
		if p.TenantID == tenant && p.UserID == user && h != keep {
			delete(m.sessions, h)
		}
	}
	return nil
}

func (m sessionMem) SetPassword(_ context.Context, tenant, user int64, hash string) error {
	for i, u := range m.users {
		if u.TenantID == tenant && u.ID == user {
			m.users[i].PasswordHash = hash
			return nil
		}
	}
	return pg.ErrNotFound
}

// Behind https the session cookie is __Host- prefixed (Secure, Path=/, no Domain), HSTS is sent,
// and a plain-named cookie (which a sibling subdomain could plant) is never read over https.
func TestSecureSessionCookieAndHSTS(t *testing.T) {
	s, store, _ := newServer()
	var logs bytes.Buffer
	s = New(Deps{Store: store, Members: s.Members, Pub: s.Pub, AllowSignup: true, PublicURL: "https://app.acme.test",
		Log: slog.New(slog.NewTextHandler(&logs, nil))})
	if !strings.Contains(logs.String(), "PULSE_PAGES_URL is not set") {
		t.Errorf("no warning about the missing pages origin: %s", logs.String())
	}
	h := s.Handler()
	w := browser(t, h, "POST", "/v1/auth/signup", `{"workspace":"Acme","email":"a@acme.io","password":"long enough pw"}`, nil, false)
	if w.Header().Get("Strict-Transport-Security") != "max-age=31536000; includeSubDomains" {
		t.Errorf("HSTS = %q", w.Header().Get("Strict-Transport-Security"))
	}
	var c *http.Cookie
	for _, k := range w.Result().Cookies() {
		if k.Name == secureSessionCookie {
			c = k
		}
	}
	if c == nil || !c.Secure || c.Path != "/" || c.Domain != "" || !c.HttpOnly {
		t.Fatalf("session cookie = %+v", c)
	}
	if w := browser(t, h, "GET", "/v1/auth/me", "", c, false); w.Code != 200 {
		t.Errorf("new cookie: %d", w.Code)
	}
	planted := &http.Cookie{Name: sessionCookie, Value: c.Value}
	if w := browser(t, h, "GET", "/v1/auth/me", "", planted, false); w.Code != http.StatusUnauthorized {
		t.Errorf("plain-named cookie over https: %d, want 401", w.Code)
	}
	w = browser(t, h, "POST", "/v1/auth/logout", "", c, true)
	cleared := map[string]bool{}
	for _, k := range w.Result().Cookies() {
		if k.MaxAge < 0 {
			cleared[k.Name] = true
		}
	}
	if !cleared[secureSessionCookie] || !cleared[sessionCookie] {
		t.Errorf("logout cleared %v", cleared)
	}
	// Over http there is no HSTS, no warning and the plain name.
	logs.Reset()
	plain := New(Deps{Store: store, PublicURL: "http://localhost:7080", Log: slog.New(slog.NewTextHandler(&logs, nil))})
	if logs.Len() != 0 || plain.sessionCookieName() != sessionCookie {
		t.Errorf("http: %q %s", logs.String(), plain.sessionCookieName())
	}
	if w := browser(t, plain.Handler(), "GET", "/healthz", "", nil, false); w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS over http")
	}
}

func TestCloudflareClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 172.70.1.1")
	r.Header.Set("CF-Connecting-IP", "198.51.100.9")
	for _, tc := range []struct {
		proxy, cf bool
		want      string
	}{{false, false, "10.0.0.2"}, {false, true, "10.0.0.2"}, {true, false, "172.70.1.1"}, {true, true, "198.51.100.9"}} {
		s := &Server{Deps: Deps{TrustProxy: tc.proxy, TrustCloudflare: tc.cf}}
		if got := s.ip(r); got != tc.want {
			t.Errorf("proxy=%v cf=%v: %s, want %s", tc.proxy, tc.cf, got, tc.want)
		}
	}
	r.Header.Set("CF-Connecting-IP", "not an ip")
	if got := (&Server{Deps: Deps{TrustProxy: true, TrustCloudflare: true}}).ip(r); got != "172.70.1.1" {
		t.Errorf("bad header: %s", got)
	}
}

// Changing the password (and signing out everywhere, and losing two-step sign-in) ends sessions.
func TestPasswordChangeSignsOutOtherSessions(t *testing.T) {
	s, mem, _ := newServer()
	redisServer(t, s)
	s.Store = sessionMem{mem}
	h := s.Handler()
	a := sessionFrom(t, browser(t, h, "POST", "/v1/auth/signup", `{"workspace":"Acme","email":"a@acme.io","password":"long enough pw"}`, nil, false))
	b := sessionFrom(t, browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"a@acme.io","password":"long enough pw"}`, nil, false))
	if w := browser(t, h, "POST", "/v1/account/password", `{"current_password":"wrong one!!","new_password":"another long pw"}`, a, true); w.Code != 403 {
		t.Errorf("wrong current password: %d %s", w.Code, w.Body)
	}
	if w := browser(t, h, "POST", "/v1/account/password", `{"current_password":"long enough pw","new_password":"short"}`, a, true); w.Code != 400 {
		t.Errorf("short password: %d %s", w.Code, w.Body)
	}
	if w := browser(t, h, "POST", "/v1/account/password", `{"current_password":"long enough pw","new_password":"another long pw"}`, a, true); w.Code != 200 {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	if w := browser(t, h, "GET", "/v1/auth/me", "", a, false); w.Code != 200 {
		t.Errorf("this session ended: %d", w.Code)
	}
	if w := browser(t, h, "GET", "/v1/auth/me", "", b, false); w.Code != 401 {
		t.Errorf("other session still works: %d", w.Code)
	}
	if w := browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"a@acme.io","password":"long enough pw"}`, nil, false); w.Code != 401 {
		t.Errorf("old password still works: %d", w.Code)
	}
	c := sessionFrom(t, browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"a@acme.io","password":"another long pw"}`, nil, false))
	if w := browser(t, h, "POST", "/v1/account/sessions/revoke", "", c, true); w.Code != 204 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	for _, k := range []*http.Cookie{a, c} {
		if w := browser(t, h, "GET", "/v1/auth/me", "", k, false); w.Code != 401 {
			t.Errorf("signed out everywhere, but a session works: %d", w.Code)
		}
	}
	if w := do(t, h, "POST", "/v1/account/password", "key-1", `{"current_password":"x","new_password":"another long pw"}`); w.Code != 400 {
		t.Errorf("API key password change: %d", w.Code)
	}
}

func TestAuditRedactsAnySecretLookingKey(t *testing.T) {
	out := string(redactJSON([]byte(`{"name":"x","mailgun_signing_key":"mg1","config":{"accountSid":"AC1","apiKey":"k1","X-Auth-Header":"h1",
		"webhookSigningSecret":"s1","oauth":{"refresh_token":"r1"},"credentials":"c1","keywords":["STOP"],"url":"u"},"pw":"p1"}`)))
	for _, leak := range []string{"mg1", "AC1", "k1", "h1", "s1", "r1", "c1", "p1"} {
		if strings.Contains(out, `"`+leak+`"`) {
			t.Errorf("secret %s leaked: %s", leak, out)
		}
	}
	for _, keep := range []string{`"url":"u"`, `"keywords":["STOP"]`, `"name":"x"`} {
		if !strings.Contains(out, keep) {
			t.Errorf("non-secret %s dropped: %s", keep, out)
		}
	}
	for _, p := range []string{"/v1/settings/bounces/signing", "/v1/channels/providers/4", "/v1/integrations/2", "/v1/settings/sso"} {
		if !secretRoute(p) {
			t.Errorf("%s bodies are recorded", p)
		}
	}
	if secretRoute("/v1/segments") {
		t.Error("ordinary routes must still be recorded")
	}
}

func TestHeavyRoutesAreRateLimited(t *testing.T) {
	s, _, _ := newServer()
	redisServer(t, s)
	ran := 0
	h := s.heavyLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { ran++ }))
	call := func(method, path, key string) int {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	limit := heavyRoutes["GET /v1/contacts/export"]
	for i := 0; i < limit; i++ {
		if c := call("GET", "/v1/contacts/export", "key-1"); c != 200 {
			t.Fatalf("export %d: %d", i, c)
		}
	}
	if c := call("GET", "/v1/contacts/export", "key-1"); c != http.StatusTooManyRequests {
		t.Errorf("over the limit: %d", c)
	}
	if c := call("GET", "/v1/contacts/export", "key-2"); c != 200 {
		t.Errorf("another workspace: %d", c)
	}
	if c := call("GET", "/v1/contacts", "key-1"); c != 200 {
		t.Errorf("ordinary route: %d", c)
	}
	for _, route := range []string{"POST /v1/reports/run", "POST /v1/funnels/run", "POST /v1/cohorts/run", "POST /v1/analytics/paths",
		"POST /v1/segments/preview", "POST /v1/revenue/report", "POST /v1/revenue/customer-value/refresh"} {
		if heavyRoutes[route] == 0 {
			t.Errorf("%s has no limit", route)
		}
	}
}

// permStore gives sessions chosen permissions.
type permStore struct {
	*memStore
	perms auth.Perms
}

func (p permStore) SessionPrincipal(context.Context, string) (model.Principal, error) {
	return model.Principal{TenantID: 1, UserID: 9, Role: "role:1", Perms: p.perms}, nil
}

// Previewing a template for a real contact shows that contact's data: content access alone isn't enough.
func TestTemplatePreviewForAContactNeedsContactsView(t *testing.T) {
	s, mem, _ := newServer()
	s.Store = permStore{mem, auth.Perms{auth.AreaContent: auth.Edit}}
	h := s.Handler()
	cookie := &http.Cookie{Name: sessionCookie, Value: "ps_x"}
	body := `{"template":{"channel":"sms","name":"t","content":{"text":"Hi {{contact.first_name}}"}},"external_id":"u1"}`
	if w := browser(t, h, "POST", "/v1/templates/preview", body, cookie, true); w.Code != 403 || !strings.Contains(w.Body.String(), "contacts") {
		t.Errorf("content-only role previewed a contact: %d %s", w.Code, w.Body)
	}
	if w := browser(t, h, "POST", "/v1/templates/preview", strings.Replace(body, `,"external_id":"u1"`, "", 1), cookie, true); w.Code != 200 {
		t.Errorf("sample preview: %d %s", w.Code, w.Body)
	}
}

// webhookMem keeps outbound webhooks in memory.
type webhookMem struct {
	*memStore
	hooks map[int64]model.Webhook
}

func (m *webhookMem) ListWebhooks(context.Context, int64) ([]model.Webhook, error) {
	var out []model.Webhook
	for _, h := range m.hooks {
		out = append(out, h)
	}
	return out, nil
}
func (m *webhookMem) GetWebhook(_ context.Context, _, id int64) (model.Webhook, error) {
	if h, ok := m.hooks[id]; ok {
		return h, nil
	}
	return model.Webhook{}, pg.ErrNotFound
}
func (m *webhookMem) SaveWebhook(_ context.Context, h model.Webhook) (int64, error) {
	if h.ID == 0 {
		h.ID = int64(len(m.hooks) + 1)
	}
	if h.Sealed == nil {
		h.Sealed = m.hooks[h.ID].Sealed
	}
	m.hooks[h.ID] = h
	return h.ID, nil
}
func (m *webhookMem) DeleteWebhook(context.Context, int64, int64) error { return nil }
func (m *webhookMem) WebhookDeliveries(context.Context, int64, int64, int) ([]model.WebhookDelivery, error) {
	return nil, nil
}
func (m *webhookMem) RecordDelivery(context.Context, webhooks.Delivery) error { return nil }

func TestWebhookSecretShownOnlyOnCreateAndRotate(t *testing.T) {
	s, mem, _ := newServer()
	box, _ := auth.NewBox(strings.Repeat("ab", 32))
	s.Box = box
	s.Store = &webhookMem{memStore: mem, hooks: map[int64]model.Webhook{}}
	h := s.Handler()
	w := do(t, h, "POST", "/v1/settings/webhooks", "key-1", `{"name":"CRM","url":"https://crm.acme.test/hook","events":["*"],"active":true}`)
	var created struct {
		ID     int64  `json:"id"`
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w.Code != 200 || !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/settings/webhooks", "/v1/settings/webhooks/1"} {
		w := do(t, h, "GET", path, "key-1", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), created.Secret) || !strings.Contains(w.Body.String(), created.Secret[len(created.Secret)-4:]) ||
			!strings.Contains(w.Body.String(), `"has_secret":true`) {
			t.Errorf("GET %s: %d %s", path, w.Code, w.Body)
		}
	}
	w = do(t, h, "PUT", "/v1/settings/webhooks/1", "key-1", `{"name":"CRM","url":"https://crm.acme.test/hook","events":["*"],"active":true}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Errorf("plain save: %d %s", w.Code, w.Body)
	}
	w = do(t, h, "PUT", "/v1/settings/webhooks/1", "key-1", `{"name":"CRM","url":"https://crm.acme.test/hook","events":["*"],"active":true,"rotate_secret":true}`)
	var rotated struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rotated)
	if w.Code != 200 || !strings.HasPrefix(rotated.Secret, "whsec_") || rotated.Secret == created.Secret {
		t.Errorf("rotate: %d %s", w.Code, w.Body)
	}
}

// ssoMem keeps one workspace's SSO settings.
type ssoMem struct {
	*memStore
	cfg    model.SSOConfig
	secret []byte
	puts   int
}

func (m *ssoMem) GetSSO(context.Context, int64) (model.SSOConfig, []byte, error) {
	if m.cfg.Issuer == "" {
		return model.SSOConfig{}, nil, pg.ErrNotFound
	}
	return m.cfg, m.secret, nil
}
func (m *ssoMem) PutSSO(_ context.Context, _ int64, c model.SSOConfig, sealed []byte) error {
	m.cfg, m.puts = c, m.puts+1
	if sealed != nil {
		m.secret = sealed
	}
	return nil
}

// The stored client secret was given to one identity provider: pointing SSO at another needs it again.
func TestSSOSecretMustBeReenteredForANewIssuer(t *testing.T) {
	s, mem, _ := newServer()
	box, _ := auth.NewBox(strings.Repeat("cd", 32))
	s.Box = box
	st := &ssoMem{memStore: mem}
	s.Store = st
	h := s.Handler()
	base := `{"enabled":true,"issuer":"https://idp.acme.test/realms/a","client_id":"pulse"`
	if w := do(t, h, "PUT", "/v1/settings/sso", "key-1", base+`,"client_secret":"s3cret"}`); w.Code != 200 {
		t.Fatalf("first save: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "PUT", "/v1/settings/sso", "key-1", base+`,"auto_create_users":false}`); w.Code != 200 {
		t.Errorf("same issuer, kept secret: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{
		`{"enabled":true,"issuer":"https://evil.test/realms/a","client_id":"pulse"}`,
		base + `,"discovery_url":"https://evil.test/.well-known/openid-configuration"}`,
		`{"enabled":true,"issuer":"https://idp.acme.test/realms/a","client_id":"other"}`,
	} {
		puts := st.puts
		if w := do(t, h, "PUT", "/v1/settings/sso", "key-1", body); w.Code != 400 || !strings.Contains(w.Body.String(), "client secret again") || st.puts != puts {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := do(t, h, "PUT", "/v1/settings/sso", "key-1", `{"enabled":true,"issuer":"https://new.acme.test","client_id":"pulse","client_secret":"n3w"}`); w.Code != 200 {
		t.Errorf("new issuer with its secret: %d %s", w.Code, w.Body)
	}
}

func TestSSOEmailVerified(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		claim *bool
		flag  bool
		want  bool
	}{{nil, false, false}, {nil, true, true}, {&yes, false, true}, {&no, true, false}, {&yes, true, true}} {
		if got := emailVerified(tc.claim, tc.flag); got != tc.want {
			t.Errorf("claim %v flag %v: %v", tc.claim, tc.flag, got)
		}
	}
}

func TestOptinPageCSP(t *testing.T) {
	w := httptest.NewRecorder()
	renderOptin(w, 200, "✅", "Done", "Thanks")
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "script-src") ||
		w.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("headers: %v", w.Header())
	}
}

func TestTestSendChecksTheFallbackTemplate(t *testing.T) {
	s, cs, pub := contentServer(t)
	s.Store = uiContent{memStore: cs.memStore, cs: cs}
	cs.templates[5] = model.Template{ID: 5, TenantID: 1, Name: "Offer", Channel: "whatsapp", Content: json.RawMessage(`{"text":"hi"}`)}
	cs.templates[6] = model.Template{ID: 6, TenantID: 1, Name: "Mail", Channel: "email"}
	cs.templates[7] = model.Template{ID: 7, TenantID: 1, Name: "SMS", Channel: "sms", Content: json.RawMessage(`{"text":"hi"}`)}
	h := s.Handler()
	for fb, want := range map[string]int{"99": 400, "6": 400, "7": 202} {
		w := do(t, h, "POST", "/v1/channels/test", "key-1", `{"template_id":5,"fallback_template_id":`+fb+`,"external_id":"u1"}`)
		if w.Code != want {
			t.Errorf("fallback %s: %d %s", fb, w.Code, w.Body)
		}
	}
	if len(pub.recs) != 1 {
		t.Errorf("published %d", len(pub.recs))
	}
}

func TestIngestUsesTheSharedDecoder(t *testing.T) {
	s, _, pub := newServer()
	h := s.Handler()
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"x","props":{"a":"b\u0000"}}`); w.Code != 400 || !strings.Contains(w.Body.String(), "NUL") {
		t.Errorf("NUL: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":`); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid JSON") {
		t.Errorf("broken JSON: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"x","pad":"`+strings.Repeat("x", maxBody)+`"}`); w.Code != 413 {
		t.Errorf("too large: %d", w.Code)
	}
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"events":[{"external_id":"u1","name":"a"},{"external_id":"u2","name":"b"}]}`); w.Code != 202 || len(pub.recs) != 2 {
		t.Errorf("batch: %d %s", w.Code, w.Body)
	}
}

// The products route allows more than the general body limit.
func TestProductsBodyLimit(t *testing.T) {
	var limit int64
	h := bodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			limit = mb.Limit
		}
	}))
	big := strings.Repeat("x", maxProductsBody+1)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/v1/products", strings.NewReader(big)))
	if limit != maxProductsBody {
		t.Errorf("products limit %d, want %d", limit, maxProductsBody)
	}
	limit = 0
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/v1/products", strings.NewReader(strings.Repeat("x", maxBody+1))))
	if limit != 0 {
		t.Errorf("a body over 4 MB but under 8 MB was refused (%d)", limit)
	}
}

func TestOnsiteContentIsSanitised(t *testing.T) {
	f := model.FocusItem{Content: model.FocusContent{BodyHTML: `<p onmouseover="x()">Hi</p><script>alert(1)</script>`, CTAURL: "javascript:alert(1)", ImageURL: "https://a.test/i.png"}}
	sanitizeFocus(&f)
	if strings.Contains(f.Content.BodyHTML, "script") || strings.Contains(f.Content.BodyHTML, "onmouseover") || f.Content.CTAURL != "#" ||
		f.Content.ImageURL != "https://a.test/i.png" || !strings.Contains(f.Content.BodyHTML, "<p>Hi</p>") {
		t.Errorf("focus = %+v", f.Content)
	}
	d := model.DWCSlot{DefaultHTML: `<img src=x onerror=alert(1)>`, Variants: []model.DWCVariant{{HTML: `<a href="javascript:x">l</a>`}}}
	sanitizeDWC(&d)
	if strings.Contains(d.DefaultHTML, "onerror") || strings.Contains(d.Variants[0].HTML, "javascript:") {
		t.Errorf("slot = %+v", d)
	}
}

// approvalMem: one pending journey approval, a journey that was archived meanwhile.
type approvalMem struct {
	*memStore
	UIStore
	ApprovalStore
	journey  model.Journey
	approval model.Approval
	latest   error
	decided  []string
	status   string
}

func (m *approvalMem) GetJourney(context.Context, int64, int64) (model.Journey, error) {
	return m.journey, nil
}
func (m *approvalMem) GetApproval(context.Context, int64, int64) (model.Approval, error) {
	return m.approval, nil
}
func (m *approvalMem) LatestApproval(context.Context, int64, string, int64) (model.Approval, error) {
	return model.Approval{}, m.latest
}
func (m *approvalMem) DecideApproval(_ context.Context, _, _ int64, status string, _ int64, _, _ string) (bool, error) {
	m.decided = append(m.decided, status)
	return true, nil
}
func (m *approvalMem) SetJourneyStatus(_ context.Context, _, _ int64, status string) error {
	m.status = status
	return nil
}

func TestApprovingAnArchivedJourneyDoesNotActivateIt(t *testing.T) {
	s, mem, _ := newServer()
	now := time.Now()
	st := &approvalMem{memStore: mem, journey: model.Journey{ID: 4, TenantID: 1, Name: "Welcome",
		Graph: json.RawMessage(`{"trigger":{"type":"event","event":"x"},"entry":"e","nodes":{"e":{"type":"exit"}}}`), ArchivedAt: &now}}
	s.Store = st
	ver, _, err := s.campaignVersion(context.Background(), 1, "journey", 4)
	if err != nil {
		t.Fatal(err)
	}
	unarchived := st.journey
	unarchived.ArchivedAt = nil
	st.journey = unarchived
	before, _, _ := s.campaignVersion(context.Background(), 1, "journey", 4)
	if before == ver {
		t.Error("archiving doesn't change the campaign version")
	}
	st.journey.ArchivedAt = &now
	st.approval = model.Approval{ID: 8, TenantID: 1, Kind: "journey", CampaignID: 4, Status: "pending", RequestedBy: 5, Version: ver}
	w := do(t, s.Handler(), "POST", "/v1/approvals/8/approve", "key-1", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "archived") || st.status == "active" {
		t.Errorf("approve archived: %d %s (status %q, decided %v)", w.Code, w.Body, st.status, st.decided)
	}
	// latest: only "not found" means no approval; other errors are errors.
	st.latest = pg.ErrNotFound
	if w := do(t, s.Handler(), "GET", "/v1/approvals/latest?kind=journey&id=4", "key-1", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"approval":null`) {
		t.Errorf("none: %d %s", w.Code, w.Body)
	}
	st.latest = errors.New("database down")
	if w := do(t, s.Handler(), "GET", "/v1/approvals/latest?kind=journey&id=4", "key-1", ""); w.Code != 500 {
		t.Errorf("store error: %d %s", w.Code, w.Body)
	}
}

// templateDeleteMem: template 3 is sent by a running broadcast.
type templateDeleteMem struct {
	*memStore
	UIStore
	BroadcastStore
	deleted []int64
}

func (m *templateDeleteMem) ListBroadcasts(context.Context, int64) ([]model.Broadcast, error) {
	return []model.Broadcast{{ID: 1, Name: "Diwali sale", Status: "running", TemplateID: 3}}, nil
}
func (m *templateDeleteMem) ListJourneys(context.Context, int64) ([]model.Journey, error) {
	return nil, nil
}
func (m *templateDeleteMem) DeleteTemplate(_ context.Context, _, id int64) error {
	m.deleted = append(m.deleted, id)
	return nil
}

func TestTemplateSentByALiveCampaignCantBeDeleted(t *testing.T) {
	s, mem, _ := newServer()
	st := &templateDeleteMem{memStore: mem}
	s.Store = st
	h := s.Handler()
	if w := do(t, h, "DELETE", "/v1/templates/3", "key-1", ""); w.Code != 409 || !strings.Contains(w.Body.String(), "Diwali sale") {
		t.Errorf("live template: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "DELETE", "/v1/templates/4", "key-1", ""); w.Code != 204 {
		t.Errorf("unused template: %d %s", w.Code, w.Body)
	}
	if len(st.deleted) != 1 || st.deleted[0] != 4 {
		t.Errorf("deleted %v", st.deleted)
	}
}

// templatesOnly knows template 1 (another workspace's template 2 isn't found for tenant 1).
type templatesOnly struct{ *memStore }

func (templatesOnly) Template(_ context.Context, tenant, id int64) (model.Template, error) {
	if tenant == 1 && id == 1 {
		return model.Template{ID: 1, TenantID: 1, Channel: "whatsapp"}, nil
	}
	return model.Template{}, pg.ErrNotFound
}

func TestSendMessageRulesCheckTheirTemplates(t *testing.T) {
	s := New(Deps{Store: templatesOnly{&memStore{}}})
	for _, tc := range []struct {
		rule string
		ok   bool
	}{
		{`{"channel":"whatsapp","template_id":1}`, true},
		{`{"channel":"whatsapp","template_id":2}`, false},
		{`{"channel":"whatsapp","template_id":1,"fallback_template_id":2}`, false},
	} {
		g, err := journeyGraphFor(`{"type":"send_message","channels":[` + tc.rule + `],"next":"e"}`)
		if err != nil {
			t.Fatal(err)
		}
		err = s.checkJourneyRefs(context.Background(), 1, 0, g)
		if tc.ok != (err == nil) || !tc.ok && !errors.Is(err, errBadJourney) {
			t.Errorf("%s: %v", tc.rule, err)
		}
	}
}

// importMem lists a workspace's imports.
type importMem struct {
	*memStore
	ImportStore
	list    []model.Import
	created int
}

func (m *importMem) ListImports(context.Context, int64) ([]model.Import, error) { return m.list, nil }
func (m *importMem) CreateImport(context.Context, int64, int64, string, int64) (int64, error) {
	m.created++
	return 1, nil
}
func (m *importMem) AppendImportChunk(context.Context, int64, int, []byte) error { return nil }
func (m *importMem) QueueImport(context.Context, int64, string) error            { return nil }

func TestBackgroundImportQuota(t *testing.T) {
	s, mem, _ := newServer()
	now := time.Now()
	st := &importMem{memStore: mem}
	s.Store = st
	h := s.Handler()
	start := func() int {
		r := httptest.NewRequest("POST", "/v1/imports?filename=a.csv", strings.NewReader("id,email\n1,a@b.co\n"))
		r.Header.Set("Authorization", "Bearer key-1")
		r.Header.Set("Content-Type", "text/csv")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := start(); c != 202 {
		t.Fatalf("first import: %d", c)
	}
	st.list = []model.Import{{Status: "running", CreatedAt: now}, {Status: "queued", CreatedAt: now}}
	if c := start(); c != 429 {
		t.Errorf("third concurrent import: %d", c)
	}
	st.list = []model.Import{{Status: "uploading", CreatedAt: now.Add(-3 * time.Hour)}, {Status: "running", CreatedAt: now}}
	if c := start(); c != 202 {
		t.Errorf("a long-dead upload still counts: %d", c)
	}
	st.list = []model.Import{{Status: "done", CreatedAt: now.Add(-time.Hour), Bytes: maxImportBytesPerDay}}
	if c := start(); c != 429 {
		t.Errorf("over the daily bytes: %d", c)
	}
	st.list = []model.Import{{Status: "done", CreatedAt: now.Add(-25 * time.Hour), Bytes: maxImportBytesPerDay}}
	if c := start(); c != 202 {
		t.Errorf("yesterday's bytes counted: %d", c)
	}
}

func journeyGraphFor(step string) (journey.Graph, error) {
	return journey.ParseGraph([]byte(`{"trigger":{"type":"event","event":"x"},"entry":"s","nodes":{"s":` + step + `,"e":{"type":"exit"}}}`))
}

// linkMem: a signed-in password user, and SSO linking.
type linkMem struct {
	*memStore
	linked string
}

func (m *linkMem) SessionPrincipal(_ context.Context, hash string) (model.Principal, error) {
	if hash != auth.HashToken("ps_me") {
		return model.Principal{}, pg.ErrNotFound
	}
	return model.Principal{TenantID: 1, UserID: 7, Actor: "Me@Acme.io"}, nil
}
func (m *linkMem) LinkSSO(_ context.Context, _, user int64, subject string) (model.User, error) {
	m.linked = subject
	return model.User{ID: user, TenantID: 1, Email: "me@acme.io"}, nil
}

// An SSO identity whose email has a password account links only for a browser signed in to it.
func TestSSOLinksPasswordAccountsOnlyWhenSignedIn(t *testing.T) {
	st := &linkMem{memStore: &memStore{}}
	s := New(Deps{Store: st, PublicURL: "http://localhost:7080"})
	req := func(cookie string) *http.Request {
		r := httptest.NewRequest("GET", "/v1/auth/sso/callback", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		}
		return r
	}
	if _, err := s.ssoLinkUser(req(""), 1, "me@acme.io", "sub-1"); !errors.Is(err, errSSOLink) || st.linked != "" {
		t.Errorf("no session: %v", err)
	}
	if _, err := s.ssoLinkUser(req("ps_me"), 1, "other@acme.io", "sub-1"); !errors.Is(err, errSSOLink) || st.linked != "" {
		t.Errorf("signed in as someone else: %v", err)
	}
	if _, err := s.ssoLinkUser(req("ps_me"), 2, "me@acme.io", "sub-1"); !errors.Is(err, errSSOLink) || st.linked != "" {
		t.Errorf("another workspace: %v", err)
	}
	if u, err := s.ssoLinkUser(req("ps_me"), 1, "me@acme.io", "sub-1"); err != nil || u.ID != 7 || st.linked != "sub-1" {
		t.Errorf("own account: %+v %v", u, err)
	}
}

// Your own password and "sign out everywhere" are yours whatever your role: an editor or viewer
// must not need settings access to change their own password. A shared login (a public demo) can
// do neither, so one visitor can't lock the others out.
func TestEveryRoleManagesTheirOwnPasswordAndSessions(t *testing.T) {
	for _, role := range []string{auth.RoleEditor, auth.RoleViewer} {
		s, mem, _ := newServer()
		redisServer(t, s)
		s.Store = sessionMem{mem}
		s.SharedLogins = []string{"demo@acme.io"}
		h := s.Handler()
		browser(t, h, "POST", "/v1/auth/signup", `{"workspace":"Acme","email":"a@acme.io","password":"long enough pw"}`, nil, false)
		for i, email := range []string{"p@acme.io", "demo@acme.io"} {
			hash, _ := auth.HashPassword("their own password")
			mem.users = append(mem.users, model.User{ID: int64(300 + i), TenantID: mem.users[0].TenantID, Email: email, Role: role, PasswordHash: hash})
		}
		login := func(email, pw string) *http.Cookie {
			return sessionFrom(t, browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"`+email+`","password":"`+pw+`"}`, nil, false))
		}
		person := login("p@acme.io", "their own password")
		if w := browser(t, h, "POST", "/v1/account/password", `{"current_password":"their own password","new_password":"a brand new password"}`, person, true); w.Code != 200 {
			t.Fatalf("%s changing own password: %d %s", role, w.Code, w.Body)
		}
		if w := browser(t, h, "POST", "/v1/account/sessions/revoke", "", login("p@acme.io", "a brand new password"), true); w.Code != 204 {
			t.Fatalf("%s signing out everywhere: %d %s", role, w.Code, w.Body)
		}
		demo := login("demo@acme.io", "their own password")
		for _, path := range []string{"/v1/account/password", "/v1/account/sessions/revoke"} {
			if w := browser(t, h, "POST", path, `{"current_password":"their own password","new_password":"mine now, all mine"}`, demo, true); w.Code != 403 || !strings.Contains(w.Body.String(), "shared login") {
				t.Errorf("%s shared login %s: want 403, got %d %s", role, path, w.Code, w.Body)
			}
		}
	}
}
