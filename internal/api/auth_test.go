package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/model"
)

// browser sends requests the way the web app does: with the session cookie and, unless told
// otherwise, the CSRF header.
func browser(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.Header.Set(csrfHeader, "1")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func sessionFrom(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
				t.Errorf("session cookie must be HttpOnly and SameSite=Lax: %+v", c)
			}
			return c
		}
	}
	t.Fatalf("no session cookie in %d %s", w.Code, w.Body)
	return nil
}

func TestSignupLoginLogout(t *testing.T) {
	s, store, _ := newServer()
	h := s.Handler()

	w := browser(t, h, "POST", "/v1/auth/signup", `{"workspace":"Value Research","name":"Ranj","email":"r@vr.in","password":"long enough pw"}`, nil, false)
	if w.Code != http.StatusCreated {
		t.Fatalf("signup: %d %s", w.Code, w.Body)
	}
	var out struct {
		Workspace model.Tenant `json:"workspace"`
		APIKey    string       `json:"api_key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Workspace.Slug != "value-research" || !strings.HasPrefix(out.APIKey, "pk_") || !strings.HasPrefix(out.Workspace.SiteKey, "sk_") {
		t.Errorf("signup response %+v", out)
	}
	cookie := sessionFrom(t, w)

	if w := browser(t, h, "GET", "/v1/auth/me", "", cookie, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"email":"r@vr.in"`) {
		t.Errorf("me: %d %s", w.Code, w.Body)
	}

	// Same workspace name again gets a different slug.
	w = browser(t, h, "POST", "/v1/auth/signup", `{"workspace":"Value Research","email":"x@vr.in","password":"long enough pw"}`, nil, false)
	var second struct{ Workspace model.Tenant }
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if w.Code != 201 || second.Workspace.Slug == "value-research" || !strings.HasPrefix(second.Workspace.Slug, "value-research-") {
		t.Errorf("second signup: %d slug %q", w.Code, second.Workspace.Slug)
	}

	if w := browser(t, h, "POST", "/v1/auth/login", `{"workspace":"value-research","email":"r@vr.in","password":"wrong password"}`, nil, false); w.Code != 401 {
		t.Errorf("wrong password: %d", w.Code)
	}
	if w := browser(t, h, "POST", "/v1/auth/login", `{"workspace":"value-research","email":"nobody@vr.in","password":"long enough pw"}`, nil, false); w.Code != 401 || !strings.Contains(w.Body.String(), "invalid workspace, email or password") {
		t.Errorf("unknown user must look like a wrong password: %d %s", w.Code, w.Body)
	}
	w = browser(t, h, "POST", "/v1/auth/login", `{"workspace":"value-research","email":"r@vr.in","password":"long enough pw"}`, nil, false)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookie = sessionFrom(t, w)

	if w := browser(t, h, "POST", "/v1/auth/logout", "", cookie, true); w.Code != http.StatusNoContent {
		t.Errorf("logout: %d", w.Code)
	}
	if len(store.sessions) != 2 { // both signup sessions remain, the login session is gone
		t.Errorf("%d sessions after logout, want 2", len(store.sessions))
	}
	if w := browser(t, h, "GET", "/v1/auth/me", "", cookie, false); w.Code != 401 {
		t.Errorf("me after logout: %d", w.Code)
	}
}

func TestSessionRequiresCSRFHeader(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	w := browser(t, h, "POST", "/v1/auth/signup", `{"workspace":"Acme","email":"a@acme.io","password":"long enough pw"}`, nil, false)
	cookie := sessionFrom(t, w)

	body := `{"name":"Pune","rule":{"attr":"city","op":"eq","value":"Pune"}}`
	if w := browser(t, h, "POST", "/v1/segments", body, cookie, false); w.Code != http.StatusForbidden {
		t.Errorf("cookie POST without CSRF header: %d, want 403", w.Code)
	}
	if w := browser(t, h, "GET", "/v1/segments", "", cookie, false); w.Code != 200 {
		t.Errorf("GET needs no CSRF header: %d", w.Code)
	}
}

func TestSignupValidationAndToggle(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	for body, want := range map[string]string{
		`{"workspace":"","email":"a@b.co","password":"long enough pw"}`:        "workspace",
		`{"workspace":"A","email":"not-an-email","password":"long enough pw"}`: "email",
		`{"workspace":"A","email":"a@b.co","password":"short"}`:                "at least",
	} {
		if w := browser(t, h, "POST", "/v1/auth/signup", body, nil, false); w.Code != 400 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	s.AllowSignup = false
	if w := browser(t, s.Handler(), "POST", "/v1/auth/signup", `{"workspace":"A","email":"a@b.co","password":"long enough pw"}`, nil, false); w.Code != 403 {
		t.Errorf("disabled signup: %d", w.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	s, _, _ := newServer()
	mr := miniredis.RunT(t)
	s.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	h := s.Handler()
	browser(t, h, "POST", "/v1/auth/signup", `{"workspace":"Acme","email":"a@acme.io","password":"long enough pw"}`, nil, false)
	// Successful sign-ins don't use up the limit.
	for i := 0; i < loginAttempts+2; i++ {
		if w := browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"a@acme.io","password":"long enough pw"}`, nil, false); w.Code != 200 {
			t.Fatalf("sign-in %d: %d", i+1, w.Code)
		}
	}
	for i := 0; i < loginAttempts; i++ {
		browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"a@acme.io","password":"wrong wrong"}`, nil, false)
	}
	// Even the right password is refused once the limit is hit.
	if w := browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"a@acme.io","password":"long enough pw"}`, nil, false); w.Code != http.StatusTooManyRequests {
		t.Errorf("after %d failures: %d, want 429", loginAttempts, w.Code)
	}
}

func TestEditorsCannotUseAdminRoutes(t *testing.T) {
	s, store, _ := newServer()
	h := s.Handler()
	hash, _ := auth.HashPassword("long enough pw")
	store.tenants["acme"] = model.Tenant{ID: 20, Slug: "acme"}
	store.users = append(store.users, model.User{ID: 7, TenantID: 20, Email: "ed@acme.io", Role: auth.RoleEditor, PasswordHash: hash})
	w := browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"ed@acme.io","password":"long enough pw"}`, nil, false)
	cookie := sessionFrom(t, w)
	if w := browser(t, h, "POST", "/v1/settings/api-key", "", cookie, true); w.Code != http.StatusForbidden {
		t.Errorf("editor rotating the API key: %d, want 403", w.Code)
	}
	if w := browser(t, h, "GET", "/v1/users", "", cookie, false); w.Code != 200 {
		t.Errorf("editor listing the team: %d", w.Code)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Value Research": "value-research", "  Ünïcode & Co!! ": "n-code-co", "***": "workspace"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// The sandbox receipt endpoint takes the same snake_case JSON the web app sends.
func TestSandboxReceiptContract(t *testing.T) {
	s, store, pub := newServer()
	hash, _ := auth.HashPassword("long enough pw")
	store.tenants["acme"] = model.Tenant{ID: 20, Slug: "acme"}
	store.users = append(store.users, model.User{ID: 7, TenantID: 20, Email: "a@acme.io", Role: auth.RoleAdmin, PasswordHash: hash})
	h := s.Handler()
	cookie := sessionFrom(t, browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"a@acme.io","password":"long enough pw"}`, nil, false))
	w := browser(t, h, "POST", "/v1/sandbox/receipts", `{"provider_message_id":"sandbox-1:wa","status":"failed","unreachable":true}`, cookie, true)
	if w.Code != http.StatusAccepted || len(pub.recs) != 1 {
		t.Fatalf("receipt: %d %s, %d records", w.Code, w.Body, len(pub.recs))
	}
	if !strings.Contains(string(pub.recs[0].Value), `"provider_message_id":"sandbox-1:wa"`) {
		t.Errorf("published %s", pub.recs[0].Value)
	}
}

func TestImportCSV(t *testing.T) {
	s, _, pub := newServer()
	h := s.Handler()
	body := "\ufeffExternal ID,Email,First Name,City,phone\n" +
		"u1,Asha@Example.com,Asha,Pune,+91 98100-00001\n" +
		",ravi@example.com,Ravi,,\n" + // no id: the email becomes the id; empty city is skipped
		",,Nobody,Delhi,\n"
	w := do(t, h, "POST", "/v1/contacts/import", "key-1", body)
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	var out struct {
		Rows, Imported, Skipped int
		Errors                  []rowError
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Rows != 3 || out.Imported != 2 || out.Skipped != 1 || len(out.Errors) != 1 || out.Errors[0].Row != 4 {
		t.Fatalf("result = %+v", out)
	}
	var e0, e1 event.Event
	_ = json.Unmarshal(pub.recs[0].Value, &e0)
	_ = json.Unmarshal(pub.recs[1].Value, &e1)
	if e0.ExternalID != "u1" || e0.Name != "identify" || e0.Attrs["first_name"] != "Asha" || e0.Attrs["phone"] != "+919810000001" || e0.TenantID != 1 {
		t.Errorf("row 1 event = %+v", e0)
	}
	if e1.ExternalID != "ravi@example.com" || e1.Attrs["city"] != nil {
		t.Errorf("row 2 event = %+v", e1)
	}
	if w := do(t, h, "POST", "/v1/contacts/import", "key-1", "id,Bad-Col!\n1,x\n"); w.Code != 400 || !strings.Contains(w.Body.String(), "Bad-Col!") {
		t.Errorf("bad header: %d %s", w.Code, w.Body)
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=HYPERLINK(1)": "'=HYPERLINK(1)", "-12.5": "-12.5", "@cmd": "'@cmd", "Pune": "Pune"} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
