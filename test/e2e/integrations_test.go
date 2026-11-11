//go:build e2e

package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockAPI is a stand-in for a remote API (HubSpot, Facebook…) that Pulse calls. Like receiver, it
// needs PULSE_HOOK_HOST (the tests run in a container on the compose network).
type mockAPI struct {
	mu   sync.Mutex
	reqs []mockReq
	url  string
}

type mockReq struct {
	Method, Path, Auth string
	Body               []byte
}

func newMock(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte)) *mockAPI {
	t.Helper()
	host := env("PULSE_HOOK_HOST", "")
	if host == "" {
		t.Skip("set PULSE_HOOK_HOST: run inside the compose network (make e2e)")
	}
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockAPI{url: fmt.Sprintf("http://%s:%d", host, ln.Addr().(*net.TCPAddr).Port)}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.reqs = append(m.reqs, mockReq{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), b})
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		h(w, r, b)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return m
}

func (m *mockAPI) find(match func(mockReq) bool) (mockReq, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.reqs) - 1; i >= 0; i-- {
		if match(m.reqs[i]) {
			return m.reqs[i], true
		}
	}
	return mockReq{}, false
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// TestHubSpotIntegration: contacts are pulled from a (mock) HubSpot through the field mapping
// (a reserved attribute is never set), and a journey step pushes a contact, whose HubSpot id comes
// back onto the profile.
func TestHubSpotIntegration(t *testing.T) {
	run := fmt.Sprint(time.Now().UnixNano())
	pulled := "hs-" + run + "@example.com"
	hs := newMock(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch {
		case r.URL.Path == "/crm/v3/objects/contacts" && r.Method == http.MethodGet:
			io.WriteString(w, `{"results":[]}`)
		case r.URL.Path == "/crm/v3/objects/contacts/search":
			fmt.Fprintf(w, `{"results":[{"id":"9001","properties":{"email":%q,"firstname":"Hema","hs_score":"80","lastmodifieddate":"2026-01-02T10:00:00Z"}}]}`, pulled)
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"resource not found"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/crm/v3/objects/contacts":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"7007"}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	c, _ := workspace(t, "HubSpot")
	// A reserved attribute can't be filled by a pull.
	jsonCall(t, c, "POST", "/v1/integrations", map[string]any{"kind": "hubspot", "name": "HubSpot", "config": map[string]any{"base_url": hs.url},
		"secrets": map[string]any{"access_token": "pat-test"}, "mapping": []any{map[string]any{"remote": "hs_score", "attr": "points"}}}, 400)
	id := idOf(jsonCall(t, c, "POST", "/v1/integrations", map[string]any{"kind": "hubspot", "name": "HubSpot",
		"config": map[string]any{"base_url": hs.url}, "secrets": map[string]any{"access_token": "pat-test-" + run}}, 200)["id"])
	got := jsonCall(t, c, "GET", fmt.Sprint("/v1/integrations/", id), nil, 200)["integration"].(map[string]any)
	if s := got["secrets"].(map[string]any)["access_token"].(string); strings.Contains(s, "pat-test") {
		t.Errorf("the token is returned unmasked: %q", s)
	}
	if r := jsonCall(t, c, "POST", fmt.Sprintf("/v1/integrations/%d/test", id), nil, 200); r["ok"] != true {
		t.Errorf("test connection = %v", r)
	}

	// The first pull runs within seconds of saving.
	waitFor(t, 30*time.Second, func() bool { return attrsOf(t, c, pulled)["hubspot_id"] == "9001" })
	if a := attrsOf(t, c, pulled); a["first_name"] != "Hema" || a["points"] != nil {
		t.Errorf("pulled attrs = %v", a)
	}
	if _, ok := hs.find(func(r mockReq) bool { return r.Auth == "Bearer pat-test-"+run }); !ok {
		t.Error("the stored token wasn't used")
	}

	// A journey pushes a contact to HubSpot.
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "to crm", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "qualified"}, "entry": "p",
		"nodes": map[string]any{"p": map[string]any{"type": "push_integration", "integration_id": id, "next": "x"}, "x": map[string]any{"type": "exit"}}}}, 201)
	jsonCall(t, c, "POST", fmt.Sprint("/v1/journeys/", j["id"], "/activate"), nil, 200)
	time.Sleep(3 * time.Second)
	ext, email := "push-"+run, "push-"+run+"@example.com"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext, "name": "identify", "email": email, "attrs": map[string]any{"first_name": "Pia"}},
		map[string]any{"external_id": ext, "name": "qualified"}}}, 202)
	waitFor(t, 20*time.Second, func() bool { return attrsOf(t, c, ext)["hubspot_id"] == "7007" })
	created, _ := hs.find(func(r mockReq) bool { return r.Method == http.MethodPost && r.Path == "/crm/v3/objects/contacts" })
	var body struct {
		Properties map[string]string `json:"properties"`
	}
	_ = json.Unmarshal(created.Body, &body)
	if body.Properties["email"] != email || body.Properties["firstname"] != "Pia" {
		t.Errorf("pushed %s", created.Body)
	}
	jsonCall(t, c, "POST", fmt.Sprint("/v1/journeys/", j["id"], "/pause"), nil, -1)
	// A journey can't push to an integration of another type.
	shop := idOf(jsonCall(t, c, "POST", "/v1/integrations", map[string]any{"kind": "shopify", "name": "Shop",
		"config": map[string]any{"shop": "x.myshopify.com"}, "secrets": map[string]any{"webhook_secret": "s"}}, 200)["id"])
	jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "bad", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "qualified"}, "entry": "p",
		"nodes": map[string]any{"p": map[string]any{"type": "push_integration", "integration_id": shop, "next": "x"}, "x": map[string]any{"type": "exit"}}}}, 400)
}

// TestShopifyWebhooks: a signed orders/create becomes order_completed (with revenue) on the
// identified contact; a bad signature is refused.
func TestShopifyWebhooks(t *testing.T) {
	c, _ := workspace(t, "Shopify")
	run := fmt.Sprint(time.Now().UnixNano())
	out := jsonCall(t, c, "POST", "/v1/integrations", map[string]any{"kind": "shopify", "name": "Store",
		"config": map[string]any{"shop": "demo-store.myshopify.com"}, "secrets": map[string]any{"webhook_secret": "shpss_" + run}}, 200)
	hookURL := out["webhook_url"].(string)
	path := hookURL[strings.Index(hookURL, "/v1/"):]
	email := "buyer-" + run + "@example.com"
	order := fmt.Sprintf(`{"id":4501,"email":%q,"order_number":1001,"name":"#1001","total_price":"2499.00","currency":"INR",
		"line_items":[{"product_id":1,"title":"Kurta","quantity":1,"price":"2499.00"}],
		"customer":{"id":77,"email":%q,"first_name":"Meera","last_name":"Iyer"}}`, email, email)
	post := func(sig string) int {
		req, _ := http.NewRequest("POST", apiURL+path, strings.NewReader(order))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Shopify-Topic", "orders/create")
		req.Header.Set("X-Shopify-Shop-Domain", "demo-store.myshopify.com")
		req.Header.Set("X-Shopify-Webhook-Id", "wh-"+run)
		req.Header.Set("X-Shopify-Hmac-Sha256", sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("bm90IHRoZSByaWdodCBvbmU="); code != http.StatusUnauthorized {
		t.Errorf("bad signature = %d", code)
	}
	m := hmac.New(sha256.New, []byte("shpss_"+run))
	m.Write([]byte(order))
	if code := post(base64.StdEncoding.EncodeToString(m.Sum(nil))); code != http.StatusOK {
		t.Fatalf("signed delivery = %d", code)
	}
	waitFor(t, 20*time.Second, func() bool { return attrsOf(t, c, email)["shopify_customer_id"] == "77" })
	if a := attrsOf(t, c, email); a["first_name"] != "Meera" {
		t.Errorf("attrs = %v", a)
	}
	// The order counts as revenue once the workspace's revenue event is order_completed.
	jsonCall(t, c, "PUT", "/v1/revenue/settings", map[string]any{"event": "order_completed", "amount_prop": "amount", "currency": "INR"}, 200)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "buyers", "rule": map[string]any{"event": "order_completed", "op": "gte", "count": 1, "days": 30}}, 201)["id"]
	waitFor(t, 30*time.Second, func() bool {
		for _, s := range segmentsOf(t, c, email) {
			if float64(s) == seg.(float64) {
				return true
			}
		}
		return false
	})
}

// TestRESTHooksE2E: a Zapier-style connector subscribes to segment entries and new contacts, and
// creates and tags contacts through its own API key.
func TestRESTHooksE2E(t *testing.T) {
	rc := newReceiver(t)
	c, _ := workspace(t, "Zapier")
	run := fmt.Sprint(time.Now().UnixNano())
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "vip", "rule": map[string]any{"tag": "vip", "op": "has"}}, 201)["id"])
	key := jsonCall(t, c, "POST", "/v1/integrations", map[string]any{"kind": "rest_hooks", "name": "Zapier"}, 200)["api_key"].(string)
	hook := func(method, path string, body any, want int) map[string]any {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, apiURL+path, strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s = %d %s", method, path, resp.StatusCode, raw)
		}
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return out
	}
	hook("GET", "/v1/hooks/me", nil, 200)
	hook("POST", "/v1/hooks/subscriptions", map[string]any{"hookUrl": rc.url, "event": "segment_entered", "segment_id": seg}, 201)
	hook("POST", "/v1/hooks/subscriptions", map[string]any{"target_url": rc.url, "event": "contact_created"}, 201)
	time.Sleep(11 * time.Second) // the webhook worker's cache refreshes every 10 s

	email := "zap-" + run + "@example.com"
	hook("POST", "/v1/hooks/contacts", map[string]any{"email": email, "attrs": map[string]any{"plan": "pro", "points": 999}}, 202)
	waitFor(t, 20*time.Second, func() bool {
		_, ok := rc.find(func(r received) bool {
			return strings.Contains(string(r.Body), `"event":"contact_created"`) && strings.Contains(string(r.Body), email)
		})
		return ok
	})
	if a := attrsOf(t, c, email); a["plan"] != "pro" || a["points"] != nil {
		t.Errorf("attrs = %v", a)
	}
	hook("POST", "/v1/hooks/tags", map[string]any{"email": email, "tags": []string{"vip"}}, 202)
	waitFor(t, 20*time.Second, func() bool {
		_, ok := rc.find(func(r received) bool {
			return strings.Contains(string(r.Body), `"event":"segment_entered"`) && strings.Contains(string(r.Body), fmt.Sprintf(`"segment_id":%d`, seg))
		})
		return ok
	})
	// The connector's subscriptions show on its page, and an admin can remove them.
	id := idOf(jsonCall(t, c, "GET", "/v1/integrations", nil, 200)["integrations"].([]any)[0].(map[string]any)["id"])
	subs := asList(jsonCall(t, c, "GET", fmt.Sprint("/v1/integrations/", id), nil, 200)["subscriptions"])
	if len(subs) != 2 {
		t.Fatalf("subscriptions = %v", subs)
	}
	// Zapier's own webhooks don't appear among the workspace's webhooks.
	if hs := asList(jsonCall(t, c, "GET", "/v1/settings/webhooks", nil, 200)["webhooks"]); len(hs) != 0 {
		t.Errorf("settings webhooks = %v", hs)
	}
	hook("DELETE", fmt.Sprintf("/v1/hooks/subscriptions/%d", idOf(subs[0].(map[string]any)["id"])), nil, 204)
}

// TestAudienceSync: a segment linked to a (mock) Facebook Custom Audience uploads its members as
// SHA-256 hashes, leaves out a contact unsubscribed from everything, and removes a contact who
// leaves the segment.
func TestAudienceSync(t *testing.T) {
	fb := newMock(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/customaudiences"):
			io.WriteString(w, `{"id":"238000111"}`)
		default:
			io.WriteString(w, `{"audience_id":"238000111","num_received":1}`)
		}
	})
	c, _ := workspace(t, "Audiences")
	run := fmt.Sprint(time.Now().UnixNano())
	city := "Audience-" + run
	in, out := "in-"+run+"@example.com", "optout-"+run+"@example.com"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": in, "name": "identify", "email": in, "attrs": map[string]any{"city": city, "phone": "+919800000001"}},
		map[string]any{"external_id": out, "name": "identify", "email": out, "attrs": map[string]any{"city": city}},
		map[string]any{"external_id": out, "name": "unsubscribed", "props": map[string]any{"channel": "all"}},
	}}, 202)
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "city", "rule": map[string]any{"attr": "city", "op": "eq", "value": city}}, 201)["id"])
	waitFor(t, 20*time.Second, func() bool { return len(segmentsOf(t, c, in)) > 0 && len(segmentsOf(t, c, out)) > 0 })

	id := idOf(jsonCall(t, c, "POST", "/v1/integrations", map[string]any{"kind": "facebook_audiences", "name": "Meta",
		"config": map[string]any{"ad_account_id": "act_42", "base_url": fb.url}, "secrets": map[string]any{"access_token": "EAAB" + run}}, 200)["id"])
	link := jsonCall(t, c, "POST", fmt.Sprintf("/v1/integrations/%d/audiences", id), map[string]any{"segment_id": seg, "audience_name": "City buyers"}, 201)
	if link["audience_id"] != "238000111" {
		t.Fatalf("link = %v", link)
	}
	upload := func(method, email string) bool {
		_, ok := fb.find(func(r mockReq) bool {
			return r.Method == method && r.Path == "/238000111/users" && strings.Contains(string(r.Body), sha(email))
		})
		return ok
	}
	waitFor(t, 30*time.Second, func() bool { return upload(http.MethodPost, in) })
	if upload(http.MethodPost, out) {
		t.Error("a contact unsubscribed from everything was uploaded")
	}
	if r, _ := fb.find(func(r mockReq) bool { return r.Method == http.MethodPost && r.Path == "/238000111/users" }); strings.Contains(string(r.Body), in) {
		t.Error("a plain email address was sent")
	}
	status := func() map[string]any {
		return asList(jsonCall(t, c, "GET", fmt.Sprint("/v1/integrations/", id), nil, 200)["audiences"])[0].(map[string]any)
	}
	waitFor(t, 15*time.Second, func() bool { s := status(); return s["uploaded"] == 1.0 && s["status"] == "ok" })

	// Leaving the segment removes the contact, with the same hashes.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": in, "name": "identify", "attrs": map[string]any{"city": "Elsewhere"}}, 202)
	waitFor(t, 30*time.Second, func() bool { return upload(http.MethodDelete, in) })
	waitFor(t, 15*time.Second, func() bool { return status()["uploaded"] == 0.0 })
	// Linking needs segments edit as well as settings edit; an admin has both, so a second link of
	// the same pair is only refused as a duplicate.
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/integrations/%d/audiences", id), map[string]any{"segment_id": seg, "audience_id": "238000111"}, 409)
}
