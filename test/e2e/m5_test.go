//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// providerMock is a provider API the Pulse container can call (runs in the hooks container, like
// receiver); handler gets every request.
func providerMock(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	host := env("PULSE_HOOK_HOST", "")
	if host == "" {
		t.Skip("set PULSE_HOOK_HOST: run inside the compose network (make e2e)")
	}
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return fmt.Sprintf("http://%s:%d", host, ln.Addr().(*net.TCPAddr).Port)
}

func eventsNamed(t *testing.T, c *http.Client, ext, name string) int {
	var out struct {
		Events []struct{ Name string } `json:"events"`
	}
	b, _ := json.Marshal(jsonCall(t, c, "GET", "/v1/contacts/"+url.PathEscape(ext)+"/events?limit=200&name="+name, nil, -1))
	_ = json.Unmarshal(b, &out)
	n := 0
	for _, e := range out.Events {
		if e.Name == name {
			n++
		}
	}
	return n
}

// TestM5GatewayLoginFlow: a custom-request SMS gateway behind a login flow. The token is fetched
// once and reused across sends (shared through Redis), and after the API answers 401 Pulse logs in
// again and retries once.
func TestM5GatewayLoginFlow(t *testing.T) {
	var logins atomic.Int32
	var mu sync.Mutex
	revoked := map[string]bool{}
	var sends []map[string]string
	base := providerMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["user"] != "acme" || in["password"] != "m5-test-password" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			n := logins.Add(1)
			fmt.Fprintf(w, `{"data":{"session":"tok-%d-abcdefghijklmnopqrstuvw"},"expires_in":600}`, n)
		case "/sms/send":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Session ")
			mu.Lock()
			bad := revoked[tok] || !strings.HasPrefix(tok, "tok-")
			mu.Unlock()
			if bad {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			in["token"], in["route"] = tok, r.URL.Query().Get("route")
			mu.Lock()
			sends = append(sends, in)
			n := len(sends)
			mu.Unlock()
			fmt.Fprintf(w, `{"ok":true,"msg":{"id":"P-%d"}}`, n)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c, _ := workspace(t, "M5 gateway")
	run := fmt.Sprint(time.Now().UnixNano())
	ext := "m5gw-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": "+919811100011", "first_name": "Meera"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })

	auth, _ := json.Marshal(map[string]any{"type": "login",
		"login": map[string]any{"url": base + "/auth/login", "body": `{"user":"{{secrets.user}}","password":"{{secrets.password}}"}`,
			"token_from": "json", "token_path": "data.session", "expiry_from": "json", "expiry_path": "expires_in"},
		"inject": map[string]any{"in": "header", "name": "Authorization", "template": "Session {{token}}"}})
	request, _ := json.Marshal(map[string]any{"method": "POST", "url": base + "/sms/send?route=promo",
		"body":    `{"to":"{{message.to}}","text":"{{message.text}}","name":"{{contact.first_name}}"}`,
		"success": map[string]any{"json_path": "ok", "json_value": "true"}, "id_path": "msg.id"})
	cfg := map[string]any{"mode": "custom", "auth": string(auth), "request": string(request)}
	secrets := map[string]any{"user": "acme", "password": "m5-test-password"}

	// Test connection logs in and shows the token masked; the preview masks secrets.
	res := jsonCall(t, c, "POST", "/v1/channels/providers/check-auth", map[string]any{"provider": "http_sms", "config": cfg, "secrets": secrets}, 200)
	tok, _ := res["result"].(map[string]any)["token"].(string)
	if res["ok"] != true || !strings.HasPrefix(tok, "tok-") || strings.Contains(tok, "abcdefgh") {
		t.Errorf("check-auth: %v", res)
	}
	pv := jsonCall(t, c, "POST", "/v1/channels/providers/preview", map[string]any{"provider": "http_sms", "config": cfg, "secrets": secrets}, 200)
	if b, _ := json.Marshal(pv); strings.Contains(string(b), "m5-test-password") || pv["url"] != base+"/sms/send?route=promo" {
		t.Errorf("preview: %s", b)
	}
	logins.Store(0)

	prov := jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "http_sms", "name": "Login gateway",
		"is_default": true, "config": cfg, "secrets": secrets}, 200)
	if prov["id"] == nil {
		t.Fatalf("provider: %v", prov)
	}
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "m5 sms", "channel": "sms", "content": map[string]any{"text": "Hi {{contact.first_name}}, sale today"}}, 201)["id"]
	send := func(want int) {
		t.Helper()
		jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
		waitFor(t, 20*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(sends) >= want
		})
	}
	send(1)
	send(2)
	if n := logins.Load(); n != 1 {
		t.Errorf("%d logins for two sends, want the token reused", n)
	}
	mu.Lock()
	first := sends[0]
	revoked[sends[1]["token"]] = true
	mu.Unlock()
	if first["text"] != "Hi Meera, sale today" || first["name"] != "Meera" || first["to"] != "+919811100011" || first["route"] != "promo" {
		t.Errorf("request body %v", first)
	}
	send(3)
	mu.Lock()
	third := sends[2]
	mu.Unlock()
	if n := logins.Load(); n != 2 || third["token"] == first["token"] {
		t.Errorf("after a 401: %d logins, token %s (was %s)", n, third["token"], first["token"])
	}
	waitFor(t, 15*time.Second, func() bool { return eventsNamed(t, c, ext, "sms_sent") >= 3 })
}

// TestM5GupshupWhatsApp: a Gupshup WhatsApp template message goes to the (mock) Gupshup API, and
// Gupshup's delivered and read events come back through the provider webhook, token checked.
func TestM5GupshupWhatsApp(t *testing.T) {
	var mu sync.Mutex
	var got url.Values
	var apikey string
	base := providerMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wa/api/v1/template/msg" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got, _ = url.ParseQuery(string(b))
		apikey = r.Header.Get("apikey")
		mu.Unlock()
		fmt.Fprint(w, `{"status":"submitted","messageId":"gs-m5-1"}`)
	})
	c, _ := workspace(t, "M5 Gupshup")
	run := fmt.Sprint(time.Now().UnixNano())
	ext := "m5wa-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": "+919811100022", "first_name": "Kiran"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	prov := jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "whatsapp", "provider": "gupshup_whatsapp", "is_default": true,
		"config":  map[string]any{"source": "919800000001", "app_name": "AcmeApp", "base_url": base},
		"secrets": map[string]any{"api_key": "gs-test-key", "webhook_token": "m5-webhook-token"}}, 200)
	hook := prov["webhook_url"].(string)
	// The preview shows the request with the API key masked.
	pv := jsonCall(t, c, "POST", "/v1/channels/providers/preview", map[string]any{"provider_id": prov["id"], "config": map[string]any{"source": "919800000001", "app_name": "AcmeApp", "base_url": base}}, 200)
	if b, _ := json.Marshal(pv); strings.Contains(string(b), "gs-test-key") || !strings.Contains(string(b), "template") {
		t.Errorf("preview: %s", b)
	}
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "m5 wa", "channel": "whatsapp",
		"content": map[string]any{"template_name": "7b3c-template-id", "body_params": []string{"{{contact.first_name}}", "20%"}}}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	waitFor(t, 20*time.Second, func() bool { return eventsNamed(t, c, ext, "whatsapp_sent") >= 1 })
	mu.Lock()
	if apikey != "gs-test-key" || got.Get("destination") != "919811100022" || got.Get("source") != "919800000001" ||
		!strings.Contains(got.Get("template"), `"7b3c-template-id"`) || !strings.Contains(got.Get("template"), `"Kiran"`) {
		t.Errorf("gupshup request apikey=%q %v", apikey, got)
	}
	mu.Unlock()

	// The webhook path inside the compose network (the URL shown uses the public address).
	u, _ := url.Parse(hook)
	target := apiURL + u.Path
	post := func(token, typ string) int {
		body := `{"app":"AcmeApp","timestamp":1,"version":2,"type":"message-event","payload":{"id":"wamid.m5","gsId":"gs-m5-1","type":"` + typ +
			`","destination":"919811100022","payload":{"ts":1}}}`
		resp, err := http.Post(target+"?token="+token, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("wrong", "delivered"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code := post("m5-webhook-token", "delivered"); code != http.StatusOK {
		t.Errorf("delivered: %d", code)
	}
	if code := post("m5-webhook-token", "read"); code != http.StatusOK {
		t.Errorf("read: %d", code)
	}
	waitFor(t, 20*time.Second, func() bool {
		return eventsNamed(t, c, ext, "whatsapp_delivered") == 1 && eventsNamed(t, c, ext, "whatsapp_read") == 1
	})
}

// TestM5RetentionPreviewAndApply: a per-event retention override is previewed (what would be
// deleted), saved, and applied by the cleanup; other events and recent ones stay.
func TestM5RetentionPreviewAndApply(t *testing.T) {
	c, _ := workspace(t, "M5 retention")
	run := fmt.Sprint(time.Now().UnixNano())
	ext := "m5ret-" + run
	old := time.Now().UTC().AddDate(0, 0, -400).Format(time.RFC3339)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"first_name": "Ret"}},
		map[string]any{"external_id": ext, "name": "m5_old", "ts": old},
		map[string]any{"external_id": ext, "name": "m5_old"},
		map[string]any{"external_id": ext, "name": "m5_keep", "ts": old},
	}}, 202)
	waitFor(t, 15*time.Second, func() bool {
		return eventsNamed(t, c, ext, "m5_old") == 2 && eventsNamed(t, c, ext, "m5_keep") == 1
	})
	got := jsonCall(t, c, "GET", "/v1/settings/automation", nil, 200)
	if s := got["settings"].(map[string]any); s["retries"].(map[string]any)["attempts"] != 8.0 {
		t.Errorf("defaults: %v", s)
	}
	body := map[string]any{"retention": map[string]any{"event_overrides": map[string]any{"m5_old": 30, "m5_keep": 0}}}
	var pv map[string]any
	waitFor(t, 15*time.Second, func() bool {
		pv = jsonCall(t, c, "POST", "/v1/settings/automation/preview", body, -1)
		return pv["items"] != nil
	})
	var item map[string]any
	for _, it := range pv["items"].([]any) {
		m := it.(map[string]any)
		if names, _ := m["names"].([]any); slices.Contains(names, any("m5_keep")) {
			t.Errorf("m5_keep would be deleted: %v", m)
		}
		if names, _ := m["names"].([]any); slices.Contains(names, any("m5_old")) {
			item = m
		}
	}
	if item == nil || item["rows"].(float64) < 1 {
		t.Fatalf("preview: %v", pv)
	}
	jsonCall(t, c, "PUT", "/v1/settings/automation", body, 200)
	rep := jsonCall(t, c, "POST", "/v1/settings/automation/run-retention", nil, 200)
	if r, _ := rep["report"].(map[string]any); r == nil || r["event_deletes"].(float64) < 1 {
		t.Errorf("run: %v", rep)
	}
	waitFor(t, 15*time.Second, func() bool { return eventsNamed(t, c, ext, "m5_old") == 1 })
	if eventsNamed(t, c, ext, "m5_keep") != 1 {
		t.Error("an event kept forever was deleted")
	}
	// Audited.
	audit := jsonCall(t, c, "GET", "/v1/audit", nil, 200)
	if b, _ := json.Marshal(audit); !strings.Contains(string(b), "/v1/settings/automation") {
		t.Errorf("not audited: %.400s", b)
	}
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"retention": map[string]any{}}, 200)
}
