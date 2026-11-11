package httpauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func send(t *testing.T, a *Auth, target string, body []byte) *http.Request {
	t.Helper()
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		got = r.Clone(context.Background())
		got.Body = io.NopCloser(strings.NewReader(string(b)))
	}))
	defer srv.Close()
	resp, err := a.Do(context.Background(), srv.Client(), func() (*http.Request, []byte, error) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+target, nil)
		req.Header.Set("Content-Type", "application/json")
		return req, body, err
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return got
}

func TestStaticTypes(t *testing.T) {
	a, err := New(Config{Type: Basic}, map[string]string{SecretUsername: "u", SecretPassword: "p:w"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if u, p, ok := send(t, a, "/", nil).BasicAuth(); !ok || u != "u" || p != "p:w" {
		t.Errorf("basic: %v %v %v", u, p, ok)
	}
	a, _ = New(Config{Type: Bearer}, map[string]string{SecretToken: "tok"}, Options{})
	if h := send(t, a, "/", nil).Header.Get("Authorization"); h != "Bearer tok" {
		t.Errorf("bearer: %q", h)
	}
	a, _ = New(Config{Type: APIKey, KeyName: "X-Key"}, map[string]string{SecretAPIKey: "k1"}, Options{})
	if h := send(t, a, "/", nil).Header.Get("X-Key"); h != "k1" {
		t.Errorf("api key header: %q", h)
	}
	a, _ = New(Config{Type: APIKey, KeyIn: "query", KeyName: "key"}, map[string]string{SecretAPIKey: "k 2"}, Options{})
	if q := send(t, a, "/x?a=1", nil).URL.Query(); q.Get("key") != "k 2" || q.Get("a") != "1" {
		t.Errorf("api key query: %v", q)
	}
	if _, err := New(Config{Type: Bearer}, map[string]string{}, Options{}); err == nil {
		t.Error("bearer without a token accepted")
	}
	if _, err := New(Config{Type: "magic"}, nil, Options{}); err == nil {
		t.Error("unknown type accepted")
	}
}

// tokenServer answers logins with a new token each time, and the API with 401 for tokens it has
// revoked.
type tokenServer struct {
	logins  atomic.Int32
	revoked sync.Map
	srv     *httptest.Server
	answer  func(w http.ResponseWriter, n int32)
	last    atomic.Value // last login body
}

func newTokenServer(t *testing.T) *tokenServer {
	ts := &tokenServer{}
	ts.answer = func(w http.ResponseWriter, n int32) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"auth":{"token":"tok-%d"},"ttl":120}}`, n)
	}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			b, _ := io.ReadAll(r.Body)
			ts.last.Store(string(b))
			n := ts.logins.Add(1)
			time.Sleep(20 * time.Millisecond)
			ts.answer(w, n)
		case "/api":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if _, bad := ts.revoked.Load(tok); bad || tok == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, tok)
		}
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func loginConfig(url string) Config {
	return Config{Type: Login, Login: &LoginRequest{URL: url + "/login",
		Body:      `{"user":"{{secrets.user}}","pass":"{{secrets.pass}}"}`,
		TokenFrom: "json", TokenPath: "data.auth.token", ExpiryFrom: "json", ExpiryPath: "$.data.ttl"}}
}

func call(t *testing.T, a *Auth, ts *tokenServer) (int, string) {
	t.Helper()
	resp, err := a.Do(context.Background(), ts.srv.Client(), func() (*http.Request, []byte, error) {
		req, err := http.NewRequest(http.MethodGet, ts.srv.URL+"/api", nil)
		return req, nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestLoginFlowJSONAndRefreshOn401(t *testing.T) {
	ts := newTokenServer(t)
	secrets := map[string]string{"user": `a"b`, "pass": "p\\w"}
	cache := &MemoryCache{}
	a, err := New(loginConfig(ts.srv.URL), secrets, Options{HTTP: ts.srv.Client(), Cache: cache, Scope: "1:7", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := call(t, a, ts); code != 200 || body != "tok-1" {
		t.Fatalf("first call %d %s", code, body)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(ts.last.Load().(string)), &sent); err != nil || sent["user"] != `a"b` || sent["pass"] != `p\w` {
		t.Errorf("login body not JSON-escaped: %v %v", ts.last.Load(), err)
	}
	// Reused, from this process and from the shared cache (a second instance).
	call(t, a, ts)
	b, _ := New(loginConfig(ts.srv.URL), secrets, Options{HTTP: ts.srv.Client(), Cache: cache, Scope: "1:7", AllowHTTP: true})
	if _, body := call(t, b, ts); body != "tok-1" || ts.logins.Load() != 1 {
		t.Errorf("token not reused: %s after %d logins", body, ts.logins.Load())
	}
	// Revoked: 401, a new login, one retry.
	ts.revoked.Store("tok-1", true)
	if code, body := call(t, a, ts); code != 200 || body != "tok-2" || ts.logins.Load() != 2 {
		t.Errorf("after 401: %d %s, %d logins", code, body, ts.logins.Load())
	}
	// The other instance follows the shared cache once its own copy is refused.
	if _, body := call(t, b, ts); body != "tok-2" || ts.logins.Load() != 2 {
		t.Errorf("second instance: %s, %d logins", body, ts.logins.Load())
	}
	// A token that keeps failing gives up after one retry.
	ts.revoked.Store("tok-2", true)
	ts.revoked.Store("tok-3", true)
	if code, _ := call(t, a, ts); code != http.StatusUnauthorized {
		t.Errorf("gave %d, want the 401 after one retry", code)
	}
}

func TestTenantIsolationAndFingerprint(t *testing.T) {
	ts := newTokenServer(t)
	cache := &MemoryCache{}
	s := map[string]string{"user": "u", "pass": "p"}
	a, _ := New(loginConfig(ts.srv.URL), s, Options{HTTP: ts.srv.Client(), Cache: cache, Scope: "1:7", AllowHTTP: true})
	b, _ := New(loginConfig(ts.srv.URL), s, Options{HTTP: ts.srv.Client(), Cache: cache, Scope: "2:7", AllowHTTP: true})
	c, _ := New(loginConfig(ts.srv.URL), map[string]string{"user": "u", "pass": "new"}, Options{HTTP: ts.srv.Client(), Cache: cache, Scope: "1:7", AllowHTTP: true})
	_, x := call(t, a, ts)
	_, y := call(t, b, ts)
	_, z := call(t, c, ts)
	if x == y || x == z || ts.logins.Load() != 3 {
		t.Errorf("tokens shared across workspaces or passwords: %s %s %s", x, y, z)
	}
	for _, k := range cache.Keys() {
		if !strings.HasPrefix(k, "httpauth:1:7:") && !strings.HasPrefix(k, "httpauth:2:7:") {
			t.Errorf("key %s", k)
		}
	}
}

func TestStampedeLock(t *testing.T) {
	ts := newTokenServer(t)
	cache := &MemoryCache{}
	s := map[string]string{"user": "u", "pass": "p"}
	var wg sync.WaitGroup
	tokens := make([]string, 20)
	for i := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A separate Auth per goroutine: like separate instances sharing Redis.
			a, _ := New(loginConfig(ts.srv.URL), s, Options{HTTP: ts.srv.Client(), Cache: cache, Scope: "1:9", AllowHTTP: true})
			tok, err := a.Token(context.Background())
			if err != nil {
				t.Error(err)
			}
			tokens[i] = tok.Value
		}()
	}
	wg.Wait()
	if n := ts.logins.Load(); n != 1 {
		t.Errorf("%d logins for 20 concurrent callers, want 1", n)
	}
	for _, tok := range tokens {
		if tok != "tok-1" {
			t.Errorf("token %q", tok)
		}
	}
}

func TestExpiryAndRefreshBefore(t *testing.T) {
	ts := newTokenServer(t)
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	cache := &MemoryCache{Now: clock}
	a, _ := New(loginConfig(ts.srv.URL), map[string]string{"user": "u", "pass": "p"}, Options{HTTP: ts.srv.Client(), Cache: cache, Scope: "1:1", AllowHTTP: true, Now: clock})
	t1, _ := a.Token(context.Background())
	if !t1.Expires.Equal(now.Add(120 * time.Second)) {
		t.Errorf("expiry %v", t1.Expires)
	}
	now = now.Add(100 * time.Second) // 20 s left: still fresh (margin is 12 s)
	if t2, _ := a.Token(context.Background()); t2.Value != t1.Value {
		t.Error("refreshed too early")
	}
	now = now.Add(10 * time.Second) // 10 s left: inside the margin
	if t3, _ := a.Token(context.Background()); t3.Value == t1.Value {
		t.Error("not refreshed before expiry")
	}

	for _, tc := range []struct {
		v    any
		want time.Time
	}{
		{3600.0, now.Add(time.Hour)},
		{"600", now.Add(10 * time.Minute)},
		{float64(now.Add(time.Hour).Unix()), now.Add(time.Hour)},
		{float64(now.Add(time.Hour).UnixMilli()), now.Add(time.Hour)},
		{now.Add(2 * time.Hour).Format(time.RFC3339), now.Add(2 * time.Hour)},
	} {
		if got, ok := expiry(tc.v, now); !ok || !got.Equal(tc.want) {
			t.Errorf("expiry(%v) = %v %v, want %v", tc.v, got, ok, tc.want)
		}
	}
	if _, ok := expiry("soon", now); ok {
		t.Error("unreadable expiry accepted")
	}
}

func TestLoginExtraction(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name string
		l    LoginRequest
		body string
		hdr  http.Header
		want string
		exp  time.Duration
	}{
		{"json array path", LoginRequest{TokenFrom: "json", TokenPath: "items[1].t", ExpiryFrom: "fixed", ExpirySeconds: 300},
			`{"items":[{"t":"a"},{"t":"b"}]}`, nil, "b", 5 * time.Minute},
		{"json dotted index", LoginRequest{TokenFrom: "json", TokenPath: "items.0.t"}, `{"items":[{"t":"a"}]}`, nil, "a", DefaultLifetime},
		{"header", LoginRequest{TokenFrom: "header", TokenPath: "X-Session"}, `ok`, http.Header{"X-Session": {"Bearer s3"}}, "s3", DefaultLifetime},
		{"regex group", LoginRequest{TokenFrom: "regex", TokenPath: `<token>([^<]+)</token>`}, `<r><token>xml1</token></r>`, nil, "xml1", DefaultLifetime},
		{"regex whole", LoginRequest{TokenFrom: "regex", TokenPath: `tk_[a-z0-9]+`}, `session=tk_ab12;`, nil, "tk_ab12", DefaultLifetime},
		{"json expiry", LoginRequest{TokenFrom: "json", TokenPath: "token", ExpiryFrom: "json", ExpiryPath: "expires_in"}, `{"token":"x","expires_in":90}`, nil, "x", 90 * time.Second},
	} {
		l := tc.l
		l.URL = "https://login.example.com/x"
		a := &Auth{cfg: Config{Type: Login, Login: &l}, opt: Options{Now: func() time.Time { return now }}}
		hdr := tc.hdr
		if hdr == nil {
			hdr = http.Header{}
		}
		tok, err := a.extract([]byte(tc.body), hdr)
		if err != nil || tok.Value != tc.want || !tok.Expires.Equal(now.Add(tc.exp)) {
			t.Errorf("%s: %+v %v", tc.name, tok, err)
		}
	}
	a := &Auth{cfg: Config{Type: Login, Login: &LoginRequest{TokenFrom: "json", TokenPath: "nope"}}, opt: Options{Now: time.Now}}
	if _, err := a.extract([]byte(`{"x":1}`), http.Header{}); err == nil {
		t.Error("missing token accepted")
	}
}

func TestLoginRejectedAndFormBody(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		if r.PostForm.Get("password") != "right&pw" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("X-Token", "ok-token")
	}))
	defer srv.Close()
	cfg := Config{Type: Login, Login: &LoginRequest{URL: srv.URL + "/login", ContentType: "application/x-www-form-urlencoded",
		Body: "username={{secrets.u}}&password={{secrets.p}}", TokenFrom: "header", TokenPath: "X-Token"},
		Inject: &Inject{In: "query", Name: "access_token"}}
	a, err := New(cfg, map[string]string{"u": "me", "p": "wrong"}, Options{HTTP: srv.Client(), AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Test(context.Background()); err == nil || !strings.Contains(err.Error(), "credentials rejected") {
		t.Errorf("403 login: %v", err)
	}
	a, _ = New(cfg, map[string]string{"u": "me", "p": "right&pw"}, Options{HTTP: srv.Client(), AllowHTTP: true})
	res, err := a.Test(context.Background())
	if err != nil || res.Token != "••••" || res.Static {
		t.Errorf("test: %+v %v", res, err)
	}
	if got.Get("username") != "me" {
		t.Errorf("form %v", got)
	}
	if _, err := New(cfg, map[string]string{"u": "me"}, Options{}); err == nil {
		t.Error("missing {{secrets.p}} accepted")
	}
}

func TestOAuth2ClientCredentials(t *testing.T) {
	var form url.Values
	var basicUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		basicUser, _, _ = r.BasicAuth()
		fmt.Fprint(w, `{"access_token":"at-1","expires_in":3599,"token_type":"Bearer"}`)
	}))
	defer srv.Close()
	cfg := Config{Type: OAuth2, TokenURL: srv.URL, ClientID: "cid", Scope: "send", Audience: "https://api", Inject: &Inject{In: "body", Name: "token"}}
	a, err := New(cfg, map[string]string{SecretClientSecret: "cs"}, Options{HTTP: srv.Client(), AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	r := send(t, a, "/", []byte(`{"to":"+91"}`))
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), `"token":"at-1"`) || !strings.Contains(string(b), `"to":"+91"`) {
		t.Errorf("body injection: %s", b)
	}
	if form.Get("grant_type") != "client_credentials" || form.Get("client_secret") != "cs" || form.Get("scope") != "send" || form.Get("audience") != "https://api" {
		t.Errorf("token form %v", form)
	}
	cfg.ClientAuth, cfg.Grant, cfg.Inject = "basic", "refresh_token", nil
	a, _ = New(cfg, map[string]string{SecretClientSecret: "cs", SecretRefreshToken: "rt"}, Options{HTTP: srv.Client(), AllowHTTP: true})
	if h := send(t, a, "/", nil).Header.Get("Authorization"); h != "Bearer at-1" {
		t.Errorf("header %q", h)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt" || form.Get("client_secret") != "" || basicUser != "cid" {
		t.Errorf("refresh grant %v basic=%q", form, basicUser)
	}
	// https only, unless local development allows http.
	if _, err := New(cfg, map[string]string{SecretClientSecret: "cs", SecretRefreshToken: "rt"}, Options{}); err == nil {
		t.Error("http token URL accepted in production")
	}
}

func TestSecretKeysAndURLs(t *testing.T) {
	c := loginConfig("https://x.example.com")
	c.Login.Headers = []Header{{Name: "X-Tenant", Value: "{{secrets.tenant}}"}}
	if got := fmt.Sprint(c.SecretKeys()); got != "[pass tenant user]" {
		t.Errorf("keys %s", got)
	}
	if fmt.Sprint(c.URLs()) != "[https://x.example.com/login]" {
		t.Errorf("urls %v", c.URLs())
	}
	if Mask("abcdefghijklmnopqrstuvwxyz") != "abcd…wxyz" || Mask("short") != "••••" {
		t.Error("mask")
	}
}

func TestRedisCacheSealing(t *testing.T) {
	// The sealer round trip (Redis itself is covered by e2e).
	c := RedisCache{Box: rot{}}
	if v, _ := c.open(c.seal("tok")); v != "tok" {
		t.Errorf("open(seal) = %q", v)
	}
}

type rot struct{}

func (rot) Seal(p []byte) []byte { return append([]byte("s:"), p...) }
func (rot) Open(s []byte) ([]byte, error) {
	return []byte(strings.TrimPrefix(string(s), "s:")), nil
}

// A secret can't pick the host credentials go to: templates are refused in the scheme and host of
// the login and token URLs (at validation and again at request time), allowed in path and query.
func TestTemplatesNotInOrigin(t *testing.T) {
	s := map[string]string{"user": "u", "pass": "p", "tenant": "t1"}
	for _, u := range []string{
		"https://{{secrets.pass}}.evil.test/login",
		"https://login.example.com{{secrets.pass}}/x",
		"https://login.example.com:{{secrets.pass}}/x",
		"{{secrets.pass}}://login.example.com/x",
		"https://user:{{secrets.pass}}@login.example.com/x",
	} {
		c := loginConfig("x")
		c.Login.URL = u
		if _, err := New(c, s, Options{}); err == nil {
			t.Errorf("login URL %s accepted", u)
		}
		a := &Auth{cfg: c, opt: Options{Now: time.Now}}
		if _, err := a.LoginRequest(context.Background(), s); err == nil {
			t.Errorf("login request to %s built", u)
		}
	}
	for _, u := range []string{"https://login.example.com/{{secrets.tenant}}/login", "https://login.example.com/login?t={{secrets.tenant}}"} {
		c := loginConfig("x")
		c.Login.URL = u
		if _, err := New(c, s, Options{}); err != nil {
			t.Errorf("login URL %s refused: %v", u, err)
		}
	}
	o := Config{Type: OAuth2, TokenURL: "https://{{secrets.x}}.evil.test/token", ClientID: "c"}
	if _, err := New(o, map[string]string{SecretClientSecret: "s", "x": "y"}, Options{}); err == nil {
		t.Error("templated token URL host accepted")
	}
	a := &Auth{cfg: o, secrets: map[string]string{}, opt: Options{Now: time.Now, HTTP: http.DefaultClient}}
	if _, err := a.oauth2(context.Background()); err == nil {
		t.Error("token request to a templated host sent")
	}
}
