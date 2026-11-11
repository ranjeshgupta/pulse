package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/integrations"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

func TestIntegrationPermissions(t *testing.T) {
	viewer, _ := auth.BuiltinPerms(auth.RoleViewer)
	editor, _ := auth.BuiltinPerms(auth.RoleEditor)
	admin, _ := auth.BuiltinPerms(auth.RoleAdmin)
	for _, tc := range []struct {
		pattern string
		perms   auth.Perms
		want    bool
	}{
		{"GET /v1/integrations", editor, true}, // journey editors pick a CRM for push steps
		{"GET /v1/integrations", viewer, false},
		{"POST /v1/integrations", editor, false},
		{"POST /v1/integrations", admin, true},
		{"PUT /v1/integrations/{id}", editor, false},
		{"POST /v1/integrations/{id}/test", editor, false},
		{"GET /v1/integrations/{id}/fields", editor, false}, // calls the CRM with stored credentials
		{"POST /v1/integrations/{id}/push", editor, false},
		{"POST /v1/integrations/{id}/audiences", editor, false},
		{"POST /v1/integrations/{id}/audiences", auth.Perms{auth.AreaSettings: auth.Edit}, true}, // + segments edit in the handler
	} {
		method, path, _ := strings.Cut(tc.pattern, " ")
		r, _ := http.NewRequest(method, path, nil)
		r.Pattern = tc.pattern
		n := required(r)
		if got := tc.perms.Allows(n.area, n.level); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v (needs %s %v)", tc.pattern, got, tc.want, n.area, n.level)
		}
	}
	ok := func(p auth.Perms) bool { return segmentsEditOK(httptest.NewRecorder(), model.Principal{Perms: p}) }
	if ok(auth.Perms{auth.AreaSettings: auth.Edit}) {
		t.Error("linking a segment to an audience needs segments edit too")
	}
	if !ok(auth.Perms{auth.AreaSettings: auth.Edit, auth.AreaSegments: auth.Edit}) {
		t.Error("settings and segments edit may link audiences")
	}
}

// intFake adds the integration store to memStore; unimplemented methods panic.
type intFake struct {
	*memStore
	IntegrationStore
	conns  map[int64]integrations.Connector
	hashes map[string]int64
	subs   []model.Webhook
	logs   []integrations.LogEntry
}

func (f *intFake) GetIntegration(_ context.Context, tenant, id int64) (integrations.Connector, error) {
	c, ok := f.conns[id]
	if !ok || c.TenantID != tenant {
		return c, pg.ErrNotFound
	}
	return c, nil
}
func (f *intFake) IntegrationByID(_ context.Context, id int64) (integrations.Connector, error) {
	c, ok := f.conns[id]
	if !ok {
		return c, pg.ErrNotFound
	}
	return c, nil
}
func (f *intFake) IntegrationByKeyHash(_ context.Context, h string) (integrations.Connector, error) {
	id, ok := f.hashes[h]
	if !ok {
		return integrations.Connector{}, pg.ErrNotFound
	}
	return f.conns[id], nil
}
func (f *intFake) ListIntegrations(_ context.Context, tenant int64) ([]integrations.Connector, error) {
	var out []integrations.Connector
	for _, c := range f.conns {
		if c.TenantID == tenant {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *intFake) SaveIntegration(_ context.Context, c integrations.Connector, sealed []byte) (int64, error) {
	if c.ID == 0 {
		c.ID = int64(len(f.conns) + 1)
	}
	if sealed != nil {
		c.Sealed = sealed
	} else {
		c.Sealed = f.conns[c.ID].Sealed
	}
	c.Secrets = nil
	f.conns[c.ID] = c
	return c.ID, nil
}
func (f *intFake) SetIntegrationKey(_ context.Context, _, id int64, hash, hint string) error {
	f.hashes[hash] = id
	c := f.conns[id]
	c.KeyHint = hint
	f.conns[id] = c
	return nil
}
func (f *intFake) IntegrationLog(context.Context, int64, int64, int) ([]integrations.LogEntry, error) {
	return f.logs, nil
}
func (f *intFake) LogIntegration(_ context.Context, _ int64, e integrations.LogEntry) error {
	f.logs = append(f.logs, e)
	return nil
}
func (f *intFake) SetIntegrationStatus(context.Context, int64, string, string) error { return nil }
func (f *intFake) ExternalIDByEmail(_ context.Context, _ int64, email string) (string, error) {
	if email == "known@example.com" {
		return "u-known", nil
	}
	return "", nil
}
func (f *intFake) ContactsByPhone(context.Context, int64, string) ([]model.Contact, error) {
	return nil, nil
}
func (f *intFake) HookSubscriptions(_ context.Context, _, id int64) ([]model.Webhook, error) {
	return f.subs, nil
}
func (f *intFake) SubscribeHook(_ context.Context, h model.Webhook) (int64, error) {
	h.ID = int64(len(f.subs) + 1)
	f.subs = append(f.subs, h)
	return h.ID, nil
}

func intServer(t *testing.T) (*Server, *intFake, *memPub) {
	t.Helper()
	s, mem, pub := newServer()
	box, err := auth.NewBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &intFake{memStore: mem, conns: map[int64]integrations.Connector{}, hashes: map[string]int64{}}
	s.Store, s.Box = f, box
	return s, f, pub
}

func TestIntegrationSecretsAreMasked(t *testing.T) {
	s, f, _ := intServer(t)
	h := s.Handler()
	w := do(t, h, "POST", "/v1/integrations", "key-1", `{"kind":"hubspot","name":"CRM","secrets":{"access_token":"pat-na1-supersecret-9876"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	w = do(t, h, "GET", "/v1/integrations", "key-1", "")
	if strings.Contains(w.Body.String(), "supersecret") || !strings.Contains(w.Body.String(), "9876") {
		t.Errorf("list leaks or hides the secret: %s", w.Body)
	}
	// Saving with the masked value keeps the stored token.
	var list struct {
		Integrations []integrations.Connector `json:"integrations"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	masked := list.Integrations[0].Masked["access_token"]
	body, _ := json.Marshal(map[string]any{"name": "CRM 2", "secrets": map[string]string{"access_token": masked}})
	if w := do(t, h, "PUT", "/v1/integrations/1", "key-1", string(body)); w.Code != http.StatusOK {
		t.Fatalf("update = %d %s", w.Code, w.Body)
	}
	c := f.conns[1]
	if err := s.integrationSvc().OpenSecrets(&c); err != nil || c.Secrets["access_token"] != "pat-na1-supersecret-9876" {
		t.Errorf("stored token after a masked save = %v %v", c.Secrets, err)
	}
	if w := do(t, h, "GET", "/v1/integrations/1", "key-2", ""); w.Code != http.StatusNotFound {
		t.Errorf("another workspace sees it: %d", w.Code)
	}
	bad := `{"kind":"hubspot","name":"x","secrets":{"access_token":"t"},"mapping":[{"remote":"score","attr":"points"}]}`
	if w := do(t, h, "POST", "/v1/integrations", "key-1", bad); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "kept by Pulse") {
		t.Errorf("a reserved attribute mapping = %d %s", w.Code, w.Body)
	}
}

func TestShopifyWebhook(t *testing.T) {
	s, f, pub := intServer(t)
	sealed, _ := json.Marshal(map[string]string{"webhook_secret": "shpss"})
	f.conns[5] = integrations.Connector{ID: 5, TenantID: 1, Kind: integrations.KindShopify, Name: "Shop", Active: true,
		Config: map[string]string{"shop": "demo.myshopify.com"}, Sealed: s.Box.Seal(sealed)}
	h := s.Handler()
	body := integrations.SampleShopifyOrder("known@example.com")
	send := func(sig string) int {
		req, _ := http.NewRequest("POST", "/v1/webhooks/shopify/5", strings.NewReader(string(body)))
		req.Header.Set("X-Shopify-Hmac-Sha256", sig)
		req.Header.Set("X-Shopify-Topic", "orders/create")
		req.Header.Set("X-Shopify-Webhook-Id", "b54557e4-bdd9-4b37-8a5f-bf7d70bcd043")
		req.Header.Set("X-Shopify-Shop-Domain", "demo.myshopify.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if code := send("bad"); code != http.StatusUnauthorized {
		t.Errorf("a bad signature = %d", code)
	}
	if code := send(integrations.SignShopify("shpss", body)); code != http.StatusOK {
		t.Fatalf("signed = %d", code)
	}
	var e event.Event
	_ = json.Unmarshal(pub.recs[len(pub.recs)-1].Value, &e)
	if e.Name != "order_completed" || e.ExternalID != "u-known" || e.Props["amount"] != 1499.0 || e.ID != "shopify:b54557e4-bdd9-4b37-8a5f-bf7d70bcd043" {
		t.Errorf("event = %+v", e)
	}
}

func TestRESTHooks(t *testing.T) {
	s, f, pub := intServer(t)
	_, _ = f.CreateSegment(context.Background(), 1, "VIP", []byte(`{}`), false)
	h := s.Handler()
	w := do(t, h, "POST", "/v1/integrations", "key-1", `{"kind":"rest_hooks","name":"Zapier"}`)
	var created struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if !strings.HasPrefix(created.APIKey, "pzk_") {
		t.Fatalf("create = %s", w.Body)
	}
	if w := do(t, h, "GET", "/v1/hooks/me", "pk_wrong", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("a workspace key isn't a hook key: %d", w.Code)
	}
	if w := do(t, h, "GET", "/v1/hooks/me", created.APIKey, ""); w.Code != http.StatusOK {
		t.Errorf("me = %d %s", w.Code, w.Body)
	}
	w = do(t, h, "POST", "/v1/hooks/subscriptions", created.APIKey, `{"hookUrl":"https://hooks.zapier.com/x","event":"segment_entered","segment_id":1}`)
	if w.Code != http.StatusCreated || len(f.subs) != 1 || f.subs[0].SegmentID != 1 || f.subs[0].IntegrationID != 1 {
		t.Fatalf("subscribe = %d %s %+v", w.Code, w.Body, f.subs)
	}
	if w := do(t, h, "POST", "/v1/hooks/subscriptions", created.APIKey, `{"target_url":"https://x","event":"purchase","segment_id":1}`); w.Code != http.StatusBadRequest {
		t.Errorf("segment_id on a non-segment event = %d", w.Code)
	}
	w = do(t, h, "POST", "/v1/hooks/contacts", created.APIKey, `{"email":"New@Example.com","attrs":{"plan":"pro","tags":["x"],"optout_all":false}}`)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"ignored_attrs"`) {
		t.Fatalf("contact = %d %s", w.Code, w.Body)
	}
	var e event.Event
	_ = json.Unmarshal(pub.recs[len(pub.recs)-1].Value, &e)
	if e.ExternalID != "new@example.com" || e.Attrs["plan"] != "pro" || e.Attrs["tags"] != nil || e.Attrs["optout_all"] != nil {
		t.Errorf("identify = %+v", e)
	}
	if w := do(t, h, "POST", "/v1/hooks/events", created.APIKey, `{"external_id":"u1","name":"tag_added","props":{"tags":["x"]}}`); w.Code != http.StatusBadRequest {
		t.Errorf("a platform event through the event action = %d", w.Code)
	}
	if w := do(t, h, "POST", "/v1/hooks/tags", created.APIKey, `{"external_id":"u1","tags":["vip"]}`); w.Code != http.StatusAccepted {
		t.Errorf("tags = %d %s", w.Code, w.Body)
	}
	// A turned-off integration's key stops working.
	c := f.conns[1]
	c.Active = false
	f.conns[1] = c
	if w := do(t, h, "GET", "/v1/hooks/me", created.APIKey, ""); w.Code != http.StatusForbidden {
		t.Errorf("turned off = %d", w.Code)
	}
}
