//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var keycloakURL = env("PULSE_KEYCLOAK", "http://localhost:8180")

// TestSSOWithKeycloak runs the real authorization-code flow against the dev Keycloak, the way a
// browser does: redirect to the IdP, submit its login form, follow the callback, land signed in.
func TestSSOWithKeycloak(t *testing.T) {
	if resp, err := http.Get(keycloakURL + "/realms/pulse/.well-known/openid-configuration"); err != nil || resp.StatusCode != 200 {
		t.Skipf("Keycloak not reachable at %s", keycloakURL)
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 20 * time.Second}

	// 1. An admin signs up and configures SSO.
	run := fmt.Sprint(time.Now().UnixNano())
	signup := jsonCall(t, browser, "POST", "/v1/auth/signup", map[string]any{
		"workspace": "SSO " + run, "email": "owner-" + run + "@example.com", "password": "owner password 1",
	}, 201)
	slug := signup["workspace"].(map[string]any)["slug"].(string)
	configure := func(autoCreate bool) {
		jsonCall(t, browser, "PUT", "/v1/settings/sso", map[string]any{
			"enabled":           true,
			"issuer":            keycloakURL + "/realms/pulse",
			"discovery_url":     env("PULSE_KEYCLOAK_INTERNAL", "http://keycloak:8180") + "/realms/pulse",
			"client_id":         "pulse-app",
			"client_secret":     "pulse-dev-client-secret",
			"auto_create_users": autoCreate,
			"default_role":      "editor",
		}, 200)
	}
	configure(false)
	if info := jsonCall(t, browser, "GET", "/v1/auth/sso?workspace="+slug, nil, 200); info["enabled"] != true {
		t.Fatalf("sso info = %v", info)
	}

	// 2. Without auto-provisioning, a valid IdP user who isn't on the team is refused.
	teammate, landed := ssoLogin(t, slug)
	if landed.Path != "/login" || landed.Query().Get("error") != "sso_not_provisioned" {
		t.Fatalf("unprovisioned user landed on %v, want /login?error=sso_not_provisioned", landed)
	}

	// 3. With auto-provisioning, the same login lands in the app.
	configure(true)
	teammate, landed = ssoLogin(t, slug)
	if landed.Path != "/" {
		t.Fatalf("after login, landed on %v, want the app root", landed)
	}

	// 4. The teammate is signed in as a just-provisioned SSO user with the default role.
	me := jsonCall(t, teammate, "GET", "/v1/auth/me", nil, 200)
	user, _ := me["user"].(map[string]any)
	if user["email"] != "sso.tester@example.com" || user["sso"] != true || me["role"] != "editor" {
		t.Errorf("me = %v", me)
	}
}

// ssoLogin plays a fresh browser: start SSO, submit Keycloak's login form with the seeded test user
// (deploy/keycloak/pulse-realm.json), follow redirects until back in the app.
func ssoLogin(t *testing.T, slug string) (*http.Client, *url.URL) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	var landed *url.URL
	c := &http.Client{Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		landed = req.URL
		if req.URL.Path == "/" || req.URL.Path == "/login" { // back in the app: stop, the UI isn't under test here
			return http.ErrUseLastResponse
		}
		return nil
	}}
	resp, err := c.Get(apiURL + "/v1/auth/sso/start?workspace=" + slug)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`<form[^>]+id="kc-form-login"[^>]+action="([^"]+)"`).FindSubmatch(page)
	if m == nil {
		t.Fatalf("no Keycloak login form at %v (status %d)", landed, resp.StatusCode)
	}
	resp, err = c.PostForm(html.UnescapeString(string(m[1])), url.Values{
		"username": {"sso.tester"}, "password": {"Pulse-SSO-test-1"}, "credentialId": {""},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if landed == nil {
		t.Fatal("no redirect back to Pulse")
	}
	return c, landed
}

func TestPasswordLoginAndCSRF(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	run := fmt.Sprint(time.Now().UnixNano())
	email := "login-" + run + "@example.com"
	signup := jsonCall(t, c, "POST", "/v1/auth/signup", map[string]any{"workspace": "Login " + run, "email": email, "password": "login password 1"}, 201)
	slug := signup["workspace"].(map[string]any)["slug"].(string)

	// Without the CSRF header, a cookie-authenticated write is refused.
	req, _ := http.NewRequest("POST", apiURL+"/v1/segments", strings.NewReader(`{"name":"x","rule":{"attr":"a","op":"exists"}}`))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF header: %d, want 403", resp.StatusCode)
	}

	jsonCall(t, c, "POST", "/v1/auth/logout", nil, 204)
	jsonCall(t, c, "GET", "/v1/auth/me", nil, 401)
	jsonCall(t, c, "POST", "/v1/auth/login", map[string]any{"workspace": slug, "email": email, "password": "nope nope nope"}, 401)
	jsonCall(t, c, "POST", "/v1/auth/login", map[string]any{"workspace": slug, "email": email, "password": "login password 1"}, 200)
	if me := jsonCall(t, c, "GET", "/v1/auth/me", nil, 200); me["role"] != "admin" {
		t.Errorf("owner role = %v", me["role"])
	}
}

// jsonCall sends a request like the web app (cookies from the client's jar, CSRF header).
func jsonCall(t *testing.T, c *http.Client, method, path string, body any, want int) map[string]any {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, apiURL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pulse-CSRF", "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if want > 0 && resp.StatusCode != want { // want < 0: any status (polling)
		t.Fatalf("%s %s: %d %s, want %d", method, path, resp.StatusCode, raw, want)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
