//go:build e2e

package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// receiver is an HTTP endpoint the Pulse container can call. These tests run in a container on the
// compose network (make e2e does this) whose name is PULSE_HOOK_HOST; without it they're skipped.
type receiver struct {
	mu   sync.Mutex
	reqs []received
	fail func(body []byte) bool
	url  string
}

type received struct {
	Header http.Header
	Body   []byte
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	host := env("PULSE_HOOK_HOST", "")
	if host == "" {
		t.Skip("set PULSE_HOOK_HOST: run inside the compose network (make e2e)")
	}
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	rc := &receiver{url: fmt.Sprintf("http://%s:%d/hook", host, ln.Addr().(*net.TCPAddr).Port)}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.reqs = append(rc.reqs, received{r.Header.Clone(), b})
		fail := rc.fail
		rc.mu.Unlock()
		if fail != nil && fail(b) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"id":"gw-1"}`))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return rc
}

func (rc *receiver) find(match func(received) bool) (received, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, r := range rc.reqs {
		if match(r) {
			return r, true
		}
	}
	return received{}, false
}

func validSig(secret, header string, body []byte) bool {
	var ts, sig string
	for _, p := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(p, "=")
		if k == "t" {
			ts = v
		} else if k == "v1" {
			sig = v
		}
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	return hmac.Equal([]byte(hex.EncodeToString(m.Sum(nil))), []byte(sig))
}

// TestRolesAndAudit: a custom role limits what a teammate can do, and the audit log records
// changes (with secrets removed) and sign-ins.
func TestRolesAndAudit(t *testing.T) {
	owner, _ := workspace(t, "Roles")
	me := jsonCall(t, owner, "GET", "/v1/auth/me", nil, 200)
	slug := me["workspace"].(map[string]any)["slug"].(string)
	run := fmt.Sprint(time.Now().UnixNano())

	role := jsonCall(t, owner, "POST", "/v1/roles", map[string]any{"name": "Analyst", "permissions": map[string]any{
		"reports": "view", "segments": "view", "contacts": "view"}}, 200)
	jsonCall(t, owner, "POST", "/v1/roles", map[string]any{"name": "Bad", "permissions": map[string]any{"rockets": "view"}}, 400)
	email := "analyst-" + run + "@example.com"
	jsonCall(t, owner, "POST", "/v1/users", map[string]any{"email": email, "name": "Ana", "role": role["key"], "password": "analyst password 1"}, 201)

	jar, _ := cookiejar.New(nil)
	ana := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	jsonCall(t, ana, "POST", "/v1/auth/login", map[string]any{"workspace": slug, "email": email, "password": "analyst password 1"}, 200)
	perms := jsonCall(t, ana, "GET", "/v1/auth/me", nil, 200)["permissions"].(map[string]any)
	if perms["reports"] != "view" || perms["journeys"] != nil {
		t.Errorf("permissions = %v", perms)
	}
	jsonCall(t, ana, "GET", "/v1/segments", nil, 200)
	jsonCall(t, ana, "GET", "/v1/stats/overview", nil, 200)
	jsonCall(t, ana, "POST", "/v1/segments", map[string]any{"name": "x", "rule": map[string]any{"attr": "city", "op": "eq", "value": "Pune"}}, 403)
	jsonCall(t, ana, "GET", "/v1/journeys", nil, 403)
	jsonCall(t, ana, "GET", "/v1/users", nil, 403)

	// The role can't be deleted while someone has it; the last admin can't be demoted.
	jsonCall(t, owner, "DELETE", fmt.Sprint("/v1/roles/", idOf(role["id"])), nil, 409)
	ownerID := idOf(me["user"].(map[string]any)["id"])
	jsonCall(t, owner, "PUT", fmt.Sprintf("/v1/users/%d/role", ownerID), map[string]any{"role": "viewer"}, 400) // own role

	// Upgrading the analyst takes effect on their next request.
	var anaID int64
	for _, u := range asList(jsonCall(t, owner, "GET", "/v1/users", nil, 200)["users"]) {
		if u.(map[string]any)["email"] == email {
			anaID = idOf(u.(map[string]any)["id"])
		}
	}
	jsonCall(t, owner, "PUT", fmt.Sprintf("/v1/users/%d/role", anaID), map[string]any{"role": "editor"}, 200)
	jsonCall(t, ana, "POST", "/v1/segments", map[string]any{"name": "Pune " + run, "rule": map[string]any{"attr": "city", "op": "eq", "value": "Pune"}}, 201)

	var entries []any
	waitFor(t, 5*time.Second, func() bool {
		entries = asList(jsonCall(t, owner, "GET", "/v1/audit", nil, 200)["entries"])
		return len(entries) >= 6
	})
	raw, _ := json.Marshal(entries)
	all := string(raw)
	for _, want := range []string{`"actor":"` + email + `"`, `"action":"POST /v1/segments"`, `"action":"sign-in"`, `"action":"PUT /v1/users/{id}/role"`, `[redacted]`} {
		if !strings.Contains(all, want) {
			t.Errorf("audit log is missing %s", want)
		}
	}
	if strings.Contains(all, "analyst password 1") {
		t.Error("the audit log contains a password")
	}
	jsonCall(t, ana, "GET", "/v1/audit", nil, 403) // editors can't read it

	// A custom role with settings access can't hand out more than it has, or change its own role.
	ops := jsonCall(t, owner, "POST", "/v1/roles", map[string]any{"name": "Ops", "permissions": map[string]any{"settings": "edit", "contacts": "view"}}, 200)
	opsEmail := "ops-" + run + "@example.com"
	jsonCall(t, owner, "POST", "/v1/users", map[string]any{"email": opsEmail, "name": "Ops", "role": ops["key"], "password": "ops password 12"}, 201)
	jar2, _ := cookiejar.New(nil)
	op := &http.Client{Jar: jar2, Timeout: 15 * time.Second}
	jsonCall(t, op, "POST", "/v1/auth/login", map[string]any{"workspace": slug, "email": opsEmail, "password": "ops password 12"}, 200)
	jsonCall(t, op, "POST", "/v1/users", map[string]any{"email": "x-" + run + "@example.com", "role": "admin", "password": "sneaky password 1"}, 403)
	jsonCall(t, op, "POST", "/v1/roles", map[string]any{"name": "Super", "permissions": map[string]any{"journeys": "publish"}}, 403)
	jsonCall(t, op, "PUT", fmt.Sprint("/v1/roles/", idOf(ops["id"])), map[string]any{"name": "Ops", "permissions": map[string]any{"settings": "edit"}}, 403)
	jsonCall(t, op, "PUT", fmt.Sprintf("/v1/users/%d/role", ownerID), map[string]any{"role": "viewer"}, 403) // an admin outranks them
	jsonCall(t, op, "POST", "/v1/users", map[string]any{"email": "v-" + run + "@example.com", "role": ops["key"], "password": "fine password 12"}, 201)
}

// TestOutboundWebhooks: events and segment entries are posted, signed, to the customer's URL;
// failures are logged for retry.
func TestOutboundWebhooks(t *testing.T) {
	c, _ := workspace(t, "Hooks")
	rc := newReceiver(t)
	rc.fail = func(b []byte) bool { return strings.Contains(string(b), `"event":"fail_me"`) }
	run := fmt.Sprint(time.Now().UnixNano())
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "buyers", "rule": map[string]any{"event": "purchase", "op": "gte", "count": 1, "days": 7}}, 201)["id"])
	id := idOf(jsonCall(t, c, "POST", "/v1/settings/webhooks", map[string]any{"name": "CRM", "url": rc.url, "active": true,
		"events": []any{"purchase", "segment_entered", "fail_me"}}, 200)["id"])
	h := jsonCall(t, c, "GET", fmt.Sprint("/v1/settings/webhooks/", id), nil, 200)["webhook"].(map[string]any)
	secret := h["secret"].(string)
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("secret = %q", secret)
	}
	if r := jsonCall(t, c, "POST", fmt.Sprintf("/v1/settings/webhooks/%d/test", id), nil, 200); r["ok"] != true {
		t.Fatalf("test delivery = %v", r)
	}
	time.Sleep(11 * time.Second) // the worker's webhook cache refreshes every 10 s

	ext := "hook-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext, "name": "purchase", "props": map[string]any{"amount": 900}},
		map[string]any{"external_id": ext, "name": "page_view"},
		map[string]any{"external_id": ext, "name": "fail_me"},
	}}, 202)
	var got received
	took := waitFor(t, 15*time.Second, func() bool {
		var ok bool
		got, ok = rc.find(func(r received) bool { return strings.Contains(string(r.Body), `"event":"purchase"`) })
		return ok
	})
	t.Logf("event -> webhook delivered in %v", took)
	if !validSig(secret, got.Header.Get("X-Pulse-Signature"), got.Body) || got.Header.Get("X-Pulse-Event") != "purchase" {
		t.Errorf("bad signature or headers: %v", got.Header)
	}
	var p struct {
		Contact struct {
			ExternalID string `json:"external_id"`
		} `json:"contact"`
		Props map[string]any `json:"props"`
	}
	_ = json.Unmarshal(got.Body, &p)
	if p.Contact.ExternalID != ext || p.Props["amount"] != 900.0 {
		t.Errorf("payload = %s", got.Body)
	}
	waitFor(t, 15*time.Second, func() bool {
		r, ok := rc.find(func(r received) bool { return strings.Contains(string(r.Body), `"event":"segment_entered"`) })
		return ok && strings.Contains(string(r.Body), fmt.Sprintf(`"segment_id":%d`, seg))
	})
	if _, ok := rc.find(func(r received) bool { return strings.Contains(string(r.Body), `"page_view"`) }); ok {
		t.Error("an event the webhook doesn't subscribe to was delivered")
	}
	waitFor(t, 10*time.Second, func() bool {
		for _, d := range asList(jsonCall(t, c, "GET", fmt.Sprint("/v1/settings/webhooks/", id), nil, 200)["deliveries"]) {
			dm := d.(map[string]any)
			if dm["event_name"] == "fail_me" && dm["status"] == 500.0 && dm["final"] == false {
				return true
			}
		}
		return false
	})
}

// TestBackgroundImport: a large CSV is uploaded, queued and imported in the background, with
// progress, bad rows reported, and every contact added to a list.
func TestBackgroundImport(t *testing.T) {
	c, _ := workspace(t, "BigImport")
	run := fmt.Sprint(time.Now().UnixNano())
	list := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Imported"}, 201)["id"])
	jsonCall(t, c, "PUT", "/v1/attributes/age", map[string]any{"label": "Age", "type": "number"}, 200)
	var b strings.Builder
	b.WriteString("external_id,email,age,city\n")
	const n = 20000
	for i := 0; i < n; i++ {
		age := fmt.Sprint(20 + i%50)
		if i == 7 {
			age = "old" // a bad row
		}
		fmt.Fprintf(&b, "bi-%s-%d,bi-%s-%d@example.com,%s,Pune\n", run, i, run, i, age)
	}
	req, _ := http.NewRequest("POST", apiURL+fmt.Sprintf("/v1/imports?list_id=%d&filename=big.csv", list), strings.NewReader(b.String()))
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("X-Pulse-CSRF", "1")
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 202 {
		t.Fatalf("upload: %v %v", err, resp.StatusCode)
	}
	var started map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&started)
	resp.Body.Close()
	id := idOf(started["id"])
	var im map[string]any
	took := waitFor(t, 60*time.Second, func() bool {
		im = jsonCall(t, c, "GET", fmt.Sprint("/v1/imports/", id), nil, 200)
		return im["status"] == "done" || im["status"] == "failed"
	})
	if im["status"] != "done" || im["rows_done"] != float64(n-1) || im["rows_failed"] != 1.0 || im["filename"] != "big.csv" {
		t.Fatalf("import = %v", im)
	}
	t.Logf("%d-row background import finished in %v: %s", n, took, im["message"])
	if errs := asList(im["errors"]); len(errs) != 1 || errs[0].(map[string]any)["row"] != 9.0 {
		t.Errorf("errors = %v, want row 9 (line 9 of the file)", errs)
	}
	waitFor(t, 30*time.Second, func() bool {
		return jsonCall(t, c, "GET", fmt.Sprint("/v1/segments/", list), nil, 200)["size"] == float64(n-1)
	})
	if a := attrsOf(t, c, fmt.Sprintf("bi-%s-3", run)); a["age"] != 23.0 {
		t.Errorf("age should be a number: %v", a)
	}
}

// TestBounces: a hard bounce stops email to the address; a complaint unsubscribes the contact.
func TestBounces(t *testing.T) {
	c, _ := workspace(t, "Bounces")
	run := fmt.Sprint(time.Now().UnixNano())
	u := jsonCall(t, c, "GET", "/v1/settings/bounces", nil, 200)["url"].(string)
	hard, soft, spam := "hb-"+run+"@example.com", "sb-"+run+"@example.com", "cp-"+run+"@example.com"
	var evs []any
	for _, e := range []string{hard, soft, spam} {
		evs = append(evs, map[string]any{"external_id": e, "name": "identify", "email": e})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+spam, nil, -1)["contact"] != nil })

	post := func(body string) int {
		resp, err := http.Post(u, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(`[{"email":"` + hard + `","event":"bounce","type":"bounce","reason":"550 no such user"},{"email":"` + soft + `","event":"deferred"}]`); code != 200 {
		t.Fatalf("sendgrid webhook: %d", code)
	}
	if code := post(`{"RecordType":"SpamComplaint","Email":"` + spam + `"}`); code != 200 {
		t.Fatalf("postmark webhook: %d", code)
	}
	if code := post(`{"hello":"world"}`); code != 400 {
		t.Errorf("unknown body: %d, want 400", code)
	}
	if resp, _ := http.Post(apiURL+"/v1/bounces/wrong-token", "application/json", strings.NewReader("[]")); resp.StatusCode != 404 {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}
	waitFor(t, 10*time.Second, func() bool {
		return attrsOf(t, c, hard)["email_bounced"] == true && attrsOf(t, c, spam)["optout_email"] == true
	})
	if attrsOf(t, c, soft)["email_bounced"] != nil {
		t.Error("a soft bounce must not stop email")
	}
	if !slices.Contains(contactEvents(t, c, soft), "email_bounced") {
		t.Error("the soft bounce should be recorded")
	}
	// Email to the hard-bounced address is suppressed.
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "x", "channel": "email", "subject": "Hi", "html": "<p>Hi</p>"}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": hard}, 202)
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(contactEvents(t, c, hard), "email_suppressed") })
}

// TestWebPushAndGateway: browsers get the workspace's VAPID key; mobile apps register device
// tokens; the custom HTTP gateway receives signed messages.
func TestWebPushAndGateway(t *testing.T) {
	c, siteKey := workspace(t, "Gateway")
	run := fmt.Sprint(time.Now().UnixNano())
	var cfg struct {
		Key string `json:"vapid_public_key"`
	}
	getJSON(t, apiURL+"/v1/web/push-config?site_key="+siteKey, &cfg)
	var again struct {
		Key string `json:"vapid_public_key"`
	}
	getJSON(t, apiURL+"/v1/web/push-config?site_key="+siteKey, &again)
	if len(cfg.Key) != 87 || again.Key != cfg.Key {
		t.Errorf("VAPID key = %q then %q, want one stable 87-character key", cfg.Key, again.Key)
	}
	ext := "gw-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": "+919811122233", "first_name": "Ravi"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	for _, pl := range []string{"android", "ios"} {
		b, _ := json.Marshal(map[string]any{"external_id": ext, "token": "tok-" + pl + "-" + run, "platform": pl})
		req, _ := http.NewRequest("POST", apiURL+"/v1/web/devices", strings.NewReader(string(b)))
		req.Header.Set("X-Pulse-Site-Key", siteKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 204 {
			t.Fatalf("register %s device: %v %v", pl, err, resp.StatusCode)
		}
	}

	rc := newReceiver(t)
	const secret = "gateway-signing-secret"
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "http_sms", "name": "My gateway",
		"is_default": true, "config": map[string]any{"url": rc.url}, "secrets": map[string]any{"signing_secret": secret}}, 200)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "otp", "channel": "sms", "content": map[string]any{"text": "Hi {{.Attrs.first_name}}"}}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	var got received
	waitFor(t, 15*time.Second, func() bool {
		var ok bool
		got, ok = rc.find(func(r received) bool { return strings.Contains(string(r.Body), "+919811122233") })
		return ok
	})
	if !validSig(secret, got.Header.Get("X-Pulse-Signature"), got.Body) || !strings.Contains(string(got.Body), `"text":"Hi Ravi"`) {
		t.Errorf("gateway request: %v %s", got.Header, got.Body)
	}
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "sms_sent") })
}
