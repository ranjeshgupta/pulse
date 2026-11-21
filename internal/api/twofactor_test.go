package api

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// tfMem adds two-step sign-in and an audit log to memStore.
type tfMem struct {
	*memStore
	mu     sync.Mutex
	tf     map[int64]*model.TwoFactor
	codes  map[int64][]string
	policy auth.TwoFactorPolicy
	audit  []model.AuditEntry
}

func (m *tfMem) TwoFactor(_ context.Context, _, user int64) (model.TwoFactor, error) {
	t, ok := m.tf[user]
	if !ok {
		return model.TwoFactor{}, pg.ErrNotFound
	}
	out := *t
	out.RecoveryLeft = len(m.codes[user])
	return out, nil
}
func (m *tfMem) StartTwoFactor(_ context.Context, _, user int64, sealed []byte, replace bool) error {
	if m.tf[user] == nil {
		m.tf[user] = &model.TwoFactor{}
	}
	if m.tf[user].Enabled() && !replace {
		return pg.ErrConflict
	}
	m.tf[user].Pending = sealed
	return nil
}
func (m *tfMem) EnableTwoFactor(_ context.Context, _, user int64, pending []byte, step int64, recovery []string, replace bool) error {
	t := m.tf[user]
	if t != nil && t.Enabled() && !replace {
		return pg.ErrConflict
	}
	if t == nil || string(t.Pending) != string(pending) {
		return pg.ErrNotFound
	}
	now := time.Now()
	t.Secret, t.Pending, t.LastStep, t.EnabledAt = pending, nil, step, &now
	m.codes[user] = recovery
	return nil
}
func (m *tfMem) UseTwoFactorStep(_ context.Context, _, user, step int64) (bool, error) {
	t := m.tf[user]
	if t == nil || !t.Enabled() || t.LastStep >= step {
		return false, nil
	}
	t.LastStep = step
	return true, nil
}
func (m *tfMem) UseRecoveryCode(_ context.Context, _, user int64, hash string) (bool, error) {
	i := slices.Index(m.codes[user], hash)
	if i < 0 {
		return false, nil
	}
	m.codes[user] = slices.Delete(m.codes[user], i, i+1)
	return true, nil
}
func (m *tfMem) SetRecoveryCodes(_ context.Context, _, user int64, hashes []string) error {
	if t := m.tf[user]; t == nil || !t.Enabled() {
		return pg.ErrNotFound
	}
	m.codes[user] = hashes
	return nil
}
func (m *tfMem) DeleteTwoFactor(_ context.Context, _, user int64) error {
	delete(m.tf, user)
	delete(m.codes, user)
	return nil
}
func (m *tfMem) TwoFactorPolicy(context.Context, int64) (auth.TwoFactorPolicy, error) {
	p := m.policy
	return p, p.Normalize()
}
func (m *tfMem) PutTwoFactorPolicy(_ context.Context, _ int64, p auth.TwoFactorPolicy) error {
	m.policy = p
	return nil
}
func (m *tfMem) InsertAudit(_ context.Context, _ int64, e model.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, e)
	return nil
}
func (m *tfMem) ListAudit(context.Context, int64, model.AuditFilter) ([]model.AuditEntry, error) {
	return nil, nil
}

// audited waits briefly for an entry: the audit log is written in the background.
func (m *tfMem) audited(action string) bool {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		m.mu.Lock()
		for _, e := range m.audit {
			if e.Action == action {
				m.mu.Unlock()
				return true
			}
		}
		m.mu.Unlock()
	}
	return false
}

// tfEnv is a server with a secret key, Redis and a clock the test moves.
type tfEnv struct {
	t     *testing.T
	s     *Server
	h     http.Handler
	st    *tfMem
	mr    *miniredis.Miniredis
	now   time.Time
	admin *http.Cookie
}

func newTFEnv(t *testing.T) *tfEnv {
	_, base, _ := newServer()
	st := &tfMem{memStore: base, tf: map[int64]*model.TwoFactor{}, codes: map[int64][]string{}}
	box, err := auth.NewBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	s := New(Deps{Store: st, Box: box, RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()}), AllowSignup: true,
		PublicURL: "http://localhost:7080", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e := &tfEnv{t: t, s: s, st: st, mr: mr, now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	s.now = func() time.Time { return e.now }
	e.h = s.Handler()
	w := browser(t, e.h, "POST", "/v1/auth/signup", `{"workspace":"Acme","email":"a@acme.io","password":"long enough pw"}`, nil, false)
	e.admin = sessionFrom(t, w)
	// A second teammate, an editor with a password.
	hash, _ := auth.HashPassword("editor password")
	base.users = append(base.users, model.User{ID: 200, TenantID: base.users[0].TenantID, Email: "e@acme.io", Role: auth.RoleEditor, PasswordHash: hash})
	return e
}

func (e *tfEnv) req(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return browser(e.t, e.h, method, path, body, cookie, true)
}

// login signs in with a password and returns the response and the half-finished sign-in cookie.
func (e *tfEnv) login(email, password string) (*httptest.ResponseRecorder, *http.Cookie) {
	w := browser(e.t, e.h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"`+email+`","password":"`+password+`"}`, nil, false)
	for _, c := range w.Result().Cookies() {
		if c.Name == twoFactorCookie && c.Value != "" {
			if !c.HttpOnly || c.Path != twoFactorPath {
				e.t.Errorf("pending cookie must be HttpOnly on %s: %+v", twoFactorPath, c)
			}
			return w, c
		}
	}
	return w, nil
}

func hasSession(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return true
		}
	}
	return false
}

// setup runs the app setup with cookie on base (/v1/account or /v1/auth) and returns the secret.
func (e *tfEnv) setup(base string, cookie *http.Cookie) []byte {
	w := e.req("POST", base+"/two-factor/setup", "", cookie)
	var out struct{ Secret, URI string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		e.t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	if !strings.HasPrefix(out.URI, "otpauth://totp/Pulse:") {
		e.t.Errorf("uri %q", out.URI)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ReplaceAll(out.Secret, " ", ""))
	if err != nil {
		e.t.Fatal(err)
	}
	return secret
}

func (e *tfEnv) code(secret []byte) string { return auth.TOTPCode(secret, auth.TOTPStep(e.now)) }

func codeBody(c string) string { return `{"code":"` + c + `"}` }

func recoveryCodes(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var out struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.RecoveryCodes) != auth.RecoveryCodes {
		t.Fatalf("want %d recovery codes: %d %s", auth.RecoveryCodes, w.Code, w.Body)
	}
	return out.RecoveryCodes
}

func TestTwoFactorSetupAndSignIn(t *testing.T) {
	e := newTFEnv(t)

	// Without two-step sign-in, a password is enough.
	if w, pend := e.login("a@acme.io", "long enough pw"); w.Code != 200 || pend != nil || !hasSession(w) {
		t.Fatalf("plain sign-in: %d %s", w.Code, w.Body)
	}

	// Set it up from Your account: a wrong code doesn't turn it on, the right one does.
	secret := e.setup("/v1/account", e.admin)
	if w := e.req("POST", "/v1/account/two-factor/enable", codeBody("000000"), e.admin); w.Code != 401 {
		t.Errorf("wrong code on setup: %d %s", w.Code, w.Body)
	}
	codes := recoveryCodes(t, e.req("POST", "/v1/account/two-factor/enable", codeBody(e.code(secret)), e.admin))
	if w := e.req("GET", "/v1/account/two-factor", "", e.admin); !strings.Contains(w.Body.String(), `"enabled":true`) || !strings.Contains(w.Body.String(), `"recovery_codes_left":10`) {
		t.Errorf("status: %s", w.Body)
	}

	// Now a password alone gives no session, only the pending cookie.
	w, pend := e.login("a@acme.io", "long enough pw")
	if w.Code != 200 || pend == nil || hasSession(w) || !strings.Contains(w.Body.String(), `"two_factor":"code"`) {
		t.Fatalf("password step: %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), nil); w.Code != 401 {
		t.Errorf("no pending cookie: %d", w.Code)
	}
	if w := browser(t, e.h, "POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend, false); w.Code != 403 {
		t.Errorf("no CSRF header: %d", w.Code)
	}
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody("123456"), pend); w.Code != 401 || hasSession(w) {
		t.Errorf("wrong code: %d", w.Code)
	}
	// The code used to turn it on was recorded: it can't sign in.
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend); w.Code != 401 {
		t.Errorf("code from setup reused: %d", w.Code)
	}
	e.now = e.now.Add(30 * time.Second)
	used := e.code(secret)
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(used[:3]+" "+used[3:]), pend); w.Code != 200 || !hasSession(w) {
		t.Fatalf("right code: %d %s", w.Code, w.Body)
	}
	if !e.st.audited("sign-in (two-step, app)") {
		t.Error("sign-in not audited")
	}

	// The same code again (still within its 30 seconds) is refused. (Each part of this test starts
	// with fresh rate limits.)
	e.mr.FlushAll()
	_, pend = e.login("a@acme.io", "long enough pw")
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(used), pend); w.Code != 401 {
		t.Errorf("replayed code: %d", w.Code)
	}

	// A recovery code works once.
	w = e.req("POST", "/v1/auth/two-factor/verify", codeBody(strings.ToUpper(codes[3])), pend)
	if w.Code != 200 || !hasSession(w) || !strings.Contains(w.Body.String(), `"recovery_codes_left":9`) {
		t.Fatalf("recovery code: %d %s", w.Code, w.Body)
	}
	_, pend = e.login("a@acme.io", "long enough pw")
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(codes[3]), pend); w.Code != 401 {
		t.Errorf("recovery code used twice: %d", w.Code)
	}

	// The pending cookie expires.
	e.now = e.now.Add(twoFactorTTL + time.Minute)
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend); w.Code != 401 || !strings.Contains(w.Body.String(), "timed out") {
		t.Errorf("expired pending sign-in: %d %s", w.Code, w.Body)
	}

	// New recovery codes need a current code or the password; old codes stop working.
	e.mr.FlushAll()
	if w := e.req("POST", "/v1/account/two-factor/recovery-codes", `{"password":"wrong one!"}`, e.admin); w.Code != 401 {
		t.Errorf("new codes with a wrong password: %d", w.Code)
	}
	fresh := recoveryCodes(t, e.req("POST", "/v1/account/two-factor/recovery-codes", `{"password":"long enough pw"}`, e.admin))
	_, pend = e.login("a@acme.io", "long enough pw")
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(codes[0]), pend); w.Code != 401 {
		t.Errorf("old recovery code after replacing: %d", w.Code)
	}
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(fresh[0]), pend); w.Code != 200 {
		t.Errorf("new recovery code: %d %s", w.Code, w.Body)
	}

	// Turning it off needs a current code (or the password).
	e.mr.FlushAll()
	if w := e.req("POST", "/v1/account/two-factor/disable", codeBody("000000"), e.admin); w.Code != 401 {
		t.Errorf("disable with a wrong code: %d", w.Code)
	}
	e.now = e.now.Add(30 * time.Second)
	if w := e.req("POST", "/v1/account/two-factor/disable", codeBody(e.code(secret)), e.admin); w.Code != 204 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	if w, pend := e.login("a@acme.io", "long enough pw"); pend != nil || !hasSession(w) {
		t.Errorf("after turning it off a password is enough again: %d %s", w.Code, w.Body)
	}
	// Codes and passwords sent to /v1/account are never written to the audit log.
	e.st.mu.Lock()
	for _, a := range e.st.audit {
		if strings.HasPrefix(a.Action, "POST /v1/account/") && a.Detail != nil {
			t.Errorf("%s recorded its body: %s", a.Action, a.Detail)
		}
	}
	e.st.mu.Unlock()
}

func TestTwoFactorCodeRateLimit(t *testing.T) {
	e := newTFEnv(t)
	secret := e.setup("/v1/account", e.admin)
	recoveryCodes(t, e.req("POST", "/v1/account/two-factor/enable", codeBody(e.code(secret)), e.admin))
	e.now = e.now.Add(time.Minute)
	_, pend := e.login("a@acme.io", "long enough pw")
	for i := 0; i < codesPerMinute; i++ {
		if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody("000000"), pend); w.Code != 401 {
			t.Fatalf("attempt %d: %d", i+1, w.Code)
		}
	}
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend); w.Code != http.StatusTooManyRequests || hasSession(w) {
		t.Errorf("right code past the limit: %d", w.Code)
	}
	// The next minute it works, and signing in clears the sign-in attempt count.
	e.now = e.now.Add(time.Minute)
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend); w.Code != 200 {
		t.Errorf("next minute: %d %s", w.Code, w.Body)
	}
	if e.mr.Exists(loginKey("acme", "a@acme.io")) {
		t.Error("sign-in attempts not cleared")
	}
}

func TestTwoFactorPolicyEnrolmentAndAdminReset(t *testing.T) {
	e := newTFEnv(t)

	// Only admins with every permission change the policy; it checks roles and people exist.
	if w := e.req("PUT", "/v1/settings/two-factor", `{"mode":"some"}`, e.admin); w.Code != 400 {
		t.Errorf("some, nobody chosen: %d", w.Code)
	}
	if w := e.req("PUT", "/v1/settings/two-factor", `{"mode":"some","users":[999]}`, e.admin); w.Code != 400 {
		t.Errorf("unknown teammate: %d", w.Code)
	}
	if w := e.req("PUT", "/v1/settings/two-factor", `{"mode":"some","roles":["editor"]}`, e.admin); w.Code != 200 {
		t.Fatalf("policy: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/v1/settings/two-factor", "", e.admin); !strings.Contains(w.Body.String(), `"roles":["editor"]`) {
		t.Errorf("policy read back: %s", w.Body)
	}

	// The admin isn't covered; the editor must set the app up before getting a session.
	if _, pend := e.login("a@acme.io", "long enough pw"); pend != nil {
		t.Error("admin isn't required")
	}
	w, pend := e.login("e@acme.io", "editor password")
	if pend == nil || hasSession(w) || !strings.Contains(w.Body.String(), `"two_factor":"setup"`) {
		t.Fatalf("editor must set up: %d %s", w.Code, w.Body)
	}
	// A setup cookie doesn't let anyone skip to verify.
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody("000000"), pend); w.Code != 401 {
		t.Errorf("verify with a setup cookie: %d", w.Code)
	}
	secret := e.setup("/v1/auth", pend)
	if w := e.req("POST", "/v1/auth/two-factor/enable", codeBody("000000"), pend); w.Code != 401 || hasSession(w) {
		t.Errorf("wrong code at setup: %d", w.Code)
	}
	w = e.req("POST", "/v1/auth/two-factor/enable", codeBody(e.code(secret)), pend)
	recoveryCodes(t, w)
	if !hasSession(w) {
		t.Fatal("no session after setting up")
	}
	editor := sessionFrom(t, w)

	// Required: the editor can't turn it off.
	e.now = e.now.Add(30 * time.Second)
	if w := e.req("POST", "/v1/account/two-factor/disable", codeBody(e.code(secret)), editor); w.Code != 403 {
		t.Errorf("turning off a required setup: %d", w.Code)
	}
	// Next sign-in asks for a code.
	if w, _ := e.login("e@acme.io", "editor password"); !strings.Contains(w.Body.String(), `"two_factor":"code"`) {
		t.Errorf("editor's next sign-in: %s", w.Body)
	}

	// Lost phone: an admin resets it (not their own, and not an editor). It's in the audit log,
	// and the editor sets it up again at the next sign-in.
	if w := e.req("DELETE", "/v1/users/200/two-factor", "", editor); w.Code != 403 {
		t.Errorf("editor resetting: %d", w.Code)
	}
	if w := e.req("DELETE", "/v1/users/100/two-factor", "", e.admin); w.Code != 400 {
		t.Errorf("resetting your own: %d", w.Code)
	}
	if w := e.req("DELETE", "/v1/users/200/two-factor", "", e.admin); w.Code != 204 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if !e.st.audited("DELETE /v1/users/{id}/two-factor") {
		t.Error("reset not in the audit log")
	}
	if w, _ := e.login("e@acme.io", "editor password"); !strings.Contains(w.Body.String(), `"two_factor":"setup"`) {
		t.Errorf("after a reset: %s", w.Body)
	}

	// Back to optional: a password is enough for someone without the app.
	if w := e.req("PUT", "/v1/settings/two-factor", `{"mode":"optional","roles":["editor"]}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"roles":[]`) {
		t.Errorf("optional: %d %s", w.Code, w.Body)
	}
	if w, pend := e.login("e@acme.io", "editor password"); pend != nil || !hasSession(w) {
		t.Errorf("optional sign-in: %d %s", w.Code, w.Body)
	}
}

func TestTwoFactorNotForAPIKeyOrSSOOnly(t *testing.T) {
	e := newTFEnv(t)
	if w := do(t, e.h, "GET", "/v1/account/two-factor", "key-1", ""); w.Code != 400 {
		t.Errorf("API key: %d", w.Code)
	}
	e.st.users[1].PasswordHash = "" // the editor only signs in with SSO
	sso := e.st.users[1]
	tok := auth.NewToken("ps_")
	_ = e.st.CreateSession(context.Background(), auth.HashToken(tok), sso.ID, sso.TenantID, time.Time{})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}
	if w := e.req("POST", "/v1/account/two-factor/setup", "", cookie); w.Code != 400 || !strings.Contains(w.Body.String(), "single sign-on") {
		t.Errorf("SSO-only setup: %d %s", w.Code, w.Body)
	}
}

// Under a policy that requires two-step sign-in, an account with a password is sent to set it up,
// but an SSO-only account isn't (its identity provider handles extra steps; Pulse can't enrol it).
func TestTwoFactorPolicySkipsSSOOnlyAccounts(t *testing.T) {
	e := newTFEnv(t)
	e.st.policy = auth.TwoFactorPolicy{Mode: auth.TwoFactorEveryone}
	withPassword := e.st.users[1]
	if step, _, err := e.s.twoFactorNext(context.Background(), withPassword); err != nil || step != pendingSetup {
		t.Errorf("password account: %q %v", step, err)
	}
	ssoOnly := withPassword
	ssoOnly.PasswordHash = ""
	if step, _, err := e.s.twoFactorNext(context.Background(), ssoOnly); err != nil || step != "" {
		t.Errorf("SSO-only account: %q %v, want no step", step, err)
	}
}

func TestTwoFactorRoutePermissions(t *testing.T) {
	viewer, _ := auth.BuiltinPerms(auth.RoleViewer)
	editor, _ := auth.BuiltinPerms(auth.RoleEditor)
	for _, tc := range []struct {
		pattern string
		perms   auth.Perms
		want    bool
	}{
		{"GET /v1/account/two-factor", auth.Perms{}, true},
		{"POST /v1/account/two-factor/setup", auth.Perms{}, true},
		{"POST /v1/account/two-factor/disable", viewer, true},
		{"POST /v1/account/two-factor/recovery-codes", viewer, true},
		{"GET /v1/settings/two-factor", editor, true},
		{"GET /v1/settings/two-factor", viewer, false},
		{"PUT /v1/settings/two-factor", editor, false},
		{"DELETE /v1/users/{id}/two-factor", editor, false},
		{"DELETE /v1/users/{id}/two-factor", auth.Perms{auth.AreaSettings: auth.Edit}, true},
	} {
		method, path, _ := strings.Cut(tc.pattern, " ")
		r, _ := http.NewRequest(method, path, nil)
		r.Pattern = tc.pattern
		n := required(r)
		if got := tc.perms.Allows(n.area, n.level); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

// An open session alone can't move someone's two-step sign-in to another phone: starting a new
// secret while it's on needs a current code or the password.
func TestTwoFactorNewPhoneNeedsProof(t *testing.T) {
	e := newTFEnv(t)
	old := e.setup("/v1/account", e.admin)
	recoveryCodes(t, e.req("POST", "/v1/account/two-factor/enable", codeBody(e.code(old)), e.admin))
	for _, body := range []string{"{}", `{"password":"not the password"}`, codeBody("000000")} {
		if w := e.req("POST", "/v1/account/two-factor/setup", body, e.admin); w.Code != 401 {
			t.Errorf("new secret with %s: %d %s", body, w.Code, w.Body)
		}
	}
	// The store refuses too, whatever the handler does.
	if err := e.st.StartTwoFactor(context.Background(), 10, 100, []byte("x"), false); err == nil {
		t.Error("store started a new secret over an enabled one without replace")
	}
	e.mr.FlushAll()
	w := e.req("POST", "/v1/account/two-factor/setup", `{"password":"long enough pw"}`, e.admin)
	var out struct{ Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 {
		t.Fatalf("new phone with the password: %d %s", w.Code, w.Body)
	}
	fresh, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ReplaceAll(out.Secret, " ", ""))
	e.now = e.now.Add(30 * time.Second)
	recoveryCodes(t, e.req("POST", "/v1/account/two-factor/enable", codeBody(e.code(fresh)), e.admin))
	// The old phone's codes stop working; the new phone's work.
	e.now = e.now.Add(30 * time.Second)
	_, pend := e.login("a@acme.io", "long enough pw")
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(old)), pend); w.Code != 401 {
		t.Errorf("old phone: %d", w.Code)
	}
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(fresh)), pend); w.Code != 200 {
		t.Errorf("new phone: %d %s", w.Code, w.Body)
	}
}

// A pending sign-in cookie is bound to the two-step state it saw: once used, or once that state
// changed, it stops working; and setting up at sign-in never replaces an enabled secret.
func TestTwoFactorPendingCookieIsOneTime(t *testing.T) {
	e := newTFEnv(t)
	if w := e.req("PUT", "/v1/settings/two-factor", `{"mode":"everyone"}`, e.admin); w.Code != 200 {
		t.Fatalf("policy: %d", w.Code)
	}
	_, first := e.login("e@acme.io", "editor password")
	_, second := e.login("e@acme.io", "editor password")
	secret := e.setup("/v1/auth", first)
	if w := e.req("POST", "/v1/auth/two-factor/enable", codeBody(e.code(secret)), first); w.Code != 200 {
		t.Fatalf("enrol: %d %s", w.Code, w.Body)
	}
	// Two-step sign-in is on now: neither the used cookie nor the other one can start a new secret.
	for name, c := range map[string]*http.Cookie{"used": first, "stale": second} {
		if w := e.req("POST", "/v1/auth/two-factor/setup", "", c); w.Code != 401 {
			t.Errorf("%s setup cookie: %d %s", name, w.Code, w.Body)
		}
		e.now = e.now.Add(30 * time.Second)
		if w := e.req("POST", "/v1/auth/two-factor/enable", codeBody(e.code(secret)), c); w.Code != 401 || hasSession(w) {
			t.Errorf("%s enable cookie: %d %s", name, w.Code, w.Body)
		}
	}
	if err := e.st.EnableTwoFactor(context.Background(), 10, 200, nil, 0, nil, false); err == nil {
		t.Error("store enabled over an enabled secret without replace")
	}

	// A code cookie works for one sign-in: after it's used, even a fresh valid code is refused.
	e.mr.FlushAll()
	_, pend := e.login("e@acme.io", "editor password")
	e.now = e.now.Add(30 * time.Second)
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend); w.Code != 200 {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	e.now = e.now.Add(30 * time.Second)
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend); w.Code != 401 || hasSession(w) {
		t.Errorf("reused code cookie: %d %s", w.Code, w.Body)
	}
	// And an admin reset makes an outstanding code cookie useless.
	_, pend = e.login("e@acme.io", "editor password")
	if w := e.req("DELETE", "/v1/users/200/two-factor", "", e.admin); w.Code != 204 {
		t.Fatalf("reset: %d", w.Code)
	}
	e.now = e.now.Add(30 * time.Second)
	if w := e.req("POST", "/v1/auth/two-factor/verify", codeBody(e.code(secret)), pend); w.Code != 401 {
		t.Errorf("code cookie after a reset: %d", w.Code)
	}
}
