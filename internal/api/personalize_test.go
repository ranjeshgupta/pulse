package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pulse/internal/auth"
	"pulse/internal/eventschema"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// schemaContent is the content store with one event schema.
type schemaContent struct {
	*contentStore
	schema eventschema.Schema
}

func (s *schemaContent) ListEventSchemas(context.Context, int64) ([]eventschema.Schema, error) {
	return []eventschema.Schema{s.schema}, nil
}
func (s *schemaContent) GetEventSchema(_ context.Context, _ int64, name string) (eventschema.Schema, error) {
	if name != s.schema.Name {
		return eventschema.Schema{}, pg.ErrNotFound
	}
	return s.schema, nil
}
func (s *schemaContent) PutEventSchema(context.Context, int64, eventschema.Schema) error { return nil }
func (s *schemaContent) DeleteEventSchema(context.Context, int64, string) error          { return nil }

// latestEvents answers LatestEvent from a fixed row.
type latestEvents struct{ props string }

func (latestEvents) SegmentCount(context.Context, segment.Query) (uint64, error) { return 0, nil }
func (latestEvents) ContactEvents(context.Context, int64, uint32, int) ([]ch.EventRow, error) {
	return nil, nil
}
func (latestEvents) Overview(context.Context, int64, int) (ch.Overview, error) {
	return ch.Overview{}, nil
}
func (l latestEvents) LatestEvent(_ context.Context, _ int64, contact uint32, name string) (ch.EventRow, bool, error) {
	if contact != 1 || name != "cart_abandoned" {
		return ch.EventRow{}, false, nil
	}
	return ch.EventRow{ID: "e1", Name: name, Ts: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), Props: l.props}, true, nil
}

func TestTemplateVariablesAreValidated(t *testing.T) {
	s, _, _ := contentServer(t)
	h := s.Handler()
	for body, want := range map[string]string{
		`{"name":"t","channel":"sms","content":{"text":"Hi {{contact.first_name | shout}}"}}`:     "unknown pipe",
		`{"name":"t","channel":"sms","content":{"text":"Hi {{campaign.owner}}"}}`:                 "campaign has",
		`{"name":"t","channel":"sms","content":{"text":"Hi"},"missing_values":"maybe"}`:           "missing_values",
		`{"name":"t","channel":"push","content":{"title":"{{contact.x | truncate}}","body":"b"}}`: "truncate needs",
	} {
		if w := do(t, h, "POST", "/v1/templates", "key-1", body); w.Code != 400 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	ok := `{"name":"t","channel":"sms","missing_values":"skip","content":{"text":"Hi {{contact.first_name | default:\"there\"}}, {{.Attrs.city}}"}}`
	if w := do(t, h, "POST", "/v1/templates", "key-1", ok); w.Code != 201 {
		t.Errorf("valid: %d %s", w.Code, w.Body)
	}
}

func TestPreviewWithASampleEvent(t *testing.T) {
	s, _, _ := contentServer(t)
	h := s.Handler()
	req := map[string]any{
		"template": map[string]any{"name": "t", "channel": "email", "missing_values": "skip",
			"subject": `{{event.items.0.name}} is waiting, {{contact.first_name}}`,
			"html":    `<p>{{event.items.0.name}} for {{event.total | money}}</p><a href="{{links.unsubscribe}}">u</a>`},
		"event":    map[string]any{"total": 2499, "items": []any{map[string]any{"name": "Yoga mat"}}},
		"campaign": "Cart rescue",
	}
	b, _ := json.Marshal(req)
	w := do(t, h, "POST", "/v1/templates/preview", "key-1", string(b))
	var out struct {
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
		Missing []string `json:"missing"`
		Skipped bool     `json:"skipped"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out.Subject != "Yoga mat is waiting, Asha" || !strings.Contains(out.HTML, "Yoga mat for ₹2,499.00") ||
		!strings.Contains(out.HTML, "/u/preview") || len(out.Missing) != 0 || out.Skipped {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}
	// Without the event the product is missing, and the skip policy says the contact gets nothing.
	delete(req, "event")
	b, _ = json.Marshal(req)
	w = do(t, h, "POST", "/v1/templates/preview", "key-1", string(b))
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if strings.Join(out.Missing, ",") != "event.items.0.name,event.total" || !out.Skipped {
		t.Errorf("missing: %s", w.Body)
	}
	big := map[string]any{"template": req["template"], "event": map[string]any{"x": strings.Repeat("a", 17<<10)}}
	b, _ = json.Marshal(big)
	if w := do(t, h, "POST", "/v1/templates/preview", "key-1", string(b)); w.Code != 400 || !strings.Contains(w.Body.String(), "16 KB") {
		t.Errorf("large event: %d %s", w.Code, w.Body)
	}
}

func TestSampleEvent(t *testing.T) {
	s, cs, _ := contentServer(t)
	sc := &schemaContent{contentStore: cs, schema: eventschema.Schema{Name: "cart_abandoned", Props: []eventschema.Prop{
		{Key: "cart_id", Type: eventschema.TypeString},
		{Key: "total", Type: eventschema.TypeNumber},
		{Key: "item_name", Path: "items[].name", Type: eventschema.TypeString},
		{Key: "card_last4", Type: eventschema.TypeString, Personal: true},
	}}}
	s.Store = sc
	s.Analytics = latestEvents{props: `{"cart_id":"c9","total":2499,"card_last4":"4242","items":[{"name":"Yoga mat"}]}`}
	h := s.Handler()
	get := func(q string) (int, map[string]any) {
		w := do(t, h, "GET", "/v1/templates/sample-event?"+q, "key-1", "")
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	// The contact's latest real event, personal properties removed.
	code, m := get("event=cart_abandoned&external_id=u1")
	props, _ := m["props"].(map[string]any)
	if code != 200 || m["source"] != "latest" || props["cart_id"] != "c9" || props["card_last4"] != nil {
		t.Errorf("latest: %d %v", code, m)
	}
	// No such event for the contact: the schema's example.
	code, m = get("event=cart_abandoned")
	props, _ = m["props"].(map[string]any)
	items, _ := props["items"].([]any)
	if code != 200 || m["source"] != "schema" || props["total"] != 1299.0 || len(items) != 1 || props["card_last4"] != nil {
		t.Errorf("schema: %d %v", code, m)
	}
	if code, m = get("event=signup"); m["source"] != "none" {
		t.Errorf("none: %d %v", code, m)
	}
	if code, _ = get("event=bad%20name"); code != 400 {
		t.Errorf("bad name: %d", code)
	}
	if code, _ = get("event=cart_abandoned&external_id=nobody"); code != 404 {
		t.Errorf("unknown contact: %d", code)
	}
}

func TestSampleEventNeedsContactsAccess(t *testing.T) {
	viewer, _ := auth.BuiltinPerms(auth.RoleViewer)
	for _, tc := range []struct {
		perms auth.Perms
		want  bool
	}{
		{viewer, true},
		{auth.Perms{auth.AreaContent: auth.Edit}, false}, // a contact's real event is contact data
		{auth.Perms{auth.AreaContacts: auth.View}, true},
	} {
		r, _ := http.NewRequest("GET", "/v1/templates/sample-event", nil)
		r.Pattern = "GET /v1/templates/sample-event"
		n := required(r)
		if got := tc.perms.Allows(n.area, n.level); got != tc.want {
			t.Errorf("%v: allowed = %v", tc.perms, got)
		}
	}
}

// uiContent gives the content store the UI store's interface (only Template is used).
type uiContent struct {
	*memStore
	UIStore
	cs *contentStore
}

func (u uiContent) Template(ctx context.Context, tenant, id int64) (model.Template, error) {
	return u.cs.Template(ctx, tenant, id)
}

func TestTestSendCarriesTheSampleEvent(t *testing.T) {
	s, cs, pub := contentServer(t)
	s.Store = uiContent{memStore: cs.memStore, cs: cs}
	cs.templates[5] = model.Template{ID: 5, TenantID: 1, Name: "Cart", Channel: "sms", Content: json.RawMessage(`{"text":"{{event.product}}"}`)}
	h := s.Handler()
	w := do(t, h, "POST", "/v1/channels/test", "key-1", `{"template_id":5,"external_id":"u1","event":{"product":"Yoga mat"}}`)
	if w.Code != 202 || len(pub.recs) != 1 {
		t.Fatalf("test send: %d %s", w.Code, w.Body)
	}
	var m model.Message
	_ = json.Unmarshal(pub.recs[0].Value, &m)
	if string(m.Event) != `{"product":"Yoga mat"}` || m.Campaign != "Test send" {
		t.Errorf("message = %+v", m)
	}
}

// inappStore serves one live in-app campaign and a tenant with an identity secret.
type inappWeb struct {
	*memStore
	tenant model.Tenant
	msgs   []model.InAppMessage
}

func (s *inappWeb) TenantBySiteKey(_ context.Context, key string) (model.Tenant, error) {
	if key != s.tenant.SiteKey {
		return model.Tenant{}, pg.ErrNotFound
	}
	return s.tenant, nil
}
func (s *inappWeb) ListInApp(context.Context, int64, bool) ([]model.InAppMessage, error) {
	return s.msgs, nil
}
func (s *inappWeb) GetInApp(context.Context, int64, int64) (model.InAppMessage, error) {
	return s.msgs[0], nil
}
func (s *inappWeb) SaveInApp(context.Context, model.InAppMessage) (int64, error) { return 1, nil }
func (s *inappWeb) DeleteInApp(context.Context, int64, int64) error              { return nil }

// In-app text shows a contact's own data only to a verified identity (identity secret + user hash):
// anyone can send someone else's ID, or an anon- ID.
func TestInAppPersonalisesOnlyVerifiedIdentities(t *testing.T) {
	s, mem, _ := newServer()
	mem.contacts[1]["u1"] = model.Contact{TenantID: 1, ID: 1, ExternalID: "u1", Attrs: map[string]any{"first_name": "Asha", "phone": "+919800000001"}}
	mem.contacts[1]["anon-x"] = model.Contact{TenantID: 1, ID: 2, ExternalID: "anon-x", Attrs: map[string]any{"first_name": "Ravi"}}
	st := &inappWeb{memStore: mem, tenant: model.Tenant{ID: 1, SiteKey: "sk_w", IdentitySecret: "secret-1"},
		msgs: []model.InAppMessage{{ID: 7, TenantID: 1, Layout: "modal", Status: "active", StartAt: time.Now().Add(-time.Hour),
			Content:   model.InAppContent{Title: `Hi {{contact.first_name | default:"there"}}`, Body: "Call {{contact.phone}}"},
			Targeting: model.InAppTargeting{TriggerEvent: "app_open", Frequency: "always"}}}}
	s.Store = st
	h := s.Handler()
	title := func(id, hash string) (string, string) {
		r := httptest.NewRequest("POST", "/v1/web/inapp", strings.NewReader(`{"id":"`+id+`","user_hash":"`+hash+`","platform":"android"}`))
		r.Header.Set("X-Pulse-Site-Key", "sk_w")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out struct {
			Messages []struct{ Content model.InAppContent } `json:"messages"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 200 || len(out.Messages) != 1 {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body)
		}
		return out.Messages[0].Content.Title, out.Messages[0].Content.Body
	}
	if ti, b := title("u1", UserHash("secret-1", "u1")); ti != "Hi Asha" || b != "Call +919800000001" {
		t.Errorf("verified: %q %q", ti, b)
	}
	for _, tc := range [][2]string{{"u1", ""}, {"u1", "bad"}, {"anon-x", ""}, {"", ""}} {
		if ti, b := title(tc[0], tc[1]); ti != "Hi there" || b != "Call " {
			t.Errorf("unverified %v: %q %q", tc, ti, b)
		}
	}
	// Without an identity secret nothing can be verified: always the defaults.
	st.tenant.IdentitySecret = ""
	if ti, _ := title("u1", UserHash("", "u1")); ti != "Hi there" {
		t.Errorf("no secret: %q", ti)
	}
}

func TestVerifiedIdentity(t *testing.T) {
	tn := model.Tenant{IdentitySecret: "s"}
	if !verifiedIdentity(tn, "u1", UserHash("s", "u1")) || verifiedIdentity(tn, "u1", "") || verifiedIdentity(tn, "anon-1", UserHash("s", "anon-1")) ||
		verifiedIdentity(model.Tenant{}, "u1", UserHash("", "u1")) {
		t.Error("verifiedIdentity")
	}
}
