package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/eventschema"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// schemaStore adds event schemas, attribute definitions and segment refresh to memStore.
type schemaStore struct {
	*memStore
	AudienceStore
	schemas map[string]eventschema.Schema
	defs    []pg.AttributeDef
	refresh map[int64]*segment.Refresh
}

func (m *schemaStore) ListEventSchemas(_ context.Context, tenant int64) ([]eventschema.Schema, error) {
	var out []eventschema.Schema
	for _, s := range m.schemas {
		out = append(out, s)
	}
	return out, nil
}
func (m *schemaStore) GetEventSchema(_ context.Context, _ int64, name string) (eventschema.Schema, error) {
	if s, ok := m.schemas[name]; ok {
		return s, nil
	}
	return eventschema.Schema{}, pg.ErrNotFound
}
func (m *schemaStore) PutEventSchema(_ context.Context, _ int64, s eventschema.Schema) error {
	m.schemas[s.Name] = s
	return nil
}
func (m *schemaStore) DeleteEventSchema(_ context.Context, _ int64, name string) error {
	delete(m.schemas, name)
	return nil
}
func (m *schemaStore) AllEventSchemas(context.Context) (map[int64][]eventschema.Schema, error) {
	list, _ := m.ListEventSchemas(context.Background(), 1)
	return map[int64][]eventschema.Schema{1: list}, nil
}
func (m *schemaStore) AttributeDefs(context.Context, int64) ([]pg.AttributeDef, error) {
	return m.defs, nil
}
func (m *schemaStore) PutAttributeDef(_ context.Context, _ int64, d pg.AttributeDef) error {
	for i := range m.defs {
		if m.defs[i].Key == d.Key {
			m.defs[i] = d
			return nil
		}
	}
	m.defs = append(m.defs, d)
	return nil
}
func (m *schemaStore) SetSegmentRefresh(_ context.Context, _, id int64, rf *segment.Refresh) error {
	m.refresh[id] = rf
	return nil
}

func newSchemaServer(t *testing.T) (*Server, *schemaStore, *memPub) {
	t.Helper()
	s, mem, pub := newServer()
	st := &schemaStore{memStore: mem, schemas: map[string]eventschema.Schema{}, refresh: map[int64]*segment.Refresh{}}
	rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	s.Store, s.RDB = st, rdb
	s.Schemas = eventschema.NewCache(st, rdb, nil)
	return s, st, pub
}

func TestEventSchemaEnforce(t *testing.T) {
	s, st, pub := newSchemaServer(t)
	h := s.Handler()
	schema := `{"label":"Sign-up","props":[{"key":"method","type":"enum","required":true,"values":[{"value":"social"},{"value":"otp"}]},
		{"key":"social_provider","type":"enum","values":[{"value":"google"}],"required":true,"depends_on":{"prop":"method","values":["social"]}}]}`
	if w := do(t, h, "PUT", "/v1/events/signup/schema", "key-1", schema); w.Code != 200 || st.schemas["signup"].Mode != "warn" {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "PUT", "/v1/events/signup/schema", "key-1", `{"props":[{"key":"a","type":"enum"}]}`); w.Code != 400 {
		t.Errorf("invalid schema: %d", w.Code)
	}
	// Warn mode keeps every event.
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"signup","props":{"method":"sms"}}`); w.Code != 202 {
		t.Errorf("warn mode refused: %d %s", w.Code, w.Body)
	}
	enforce := strings.Replace(schema, `"label"`, `"mode":"enforce","label"`, 1)
	if w := do(t, h, "PUT", "/v1/events/signup/schema", "key-1", enforce); w.Code != 400 || !strings.Contains(w.Body.String(), "ENFORCE") {
		t.Errorf("enforce without confirm: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "PUT", "/v1/events/signup/schema", "key-1", strings.Replace(enforce, `{"mode"`, `{"confirm":"ENFORCE","mode"`, 1)); w.Code != 200 {
		t.Fatalf("enforce: %d %s", w.Code, w.Body)
	}
	before := len(pub.recs)
	w := do(t, h, "POST", "/v1/events", "key-1", `{"events":[{"external_id":"u1","name":"signup","props":{"method":"otp"}},
		{"external_id":"u2","name":"signup","props":{"method":"social"}}]}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "event 1: signup doesn't fit its schema: social_provider is required") || len(pub.recs) != before {
		t.Errorf("enforce: %d %s (%d published)", w.Code, w.Body, len(pub.recs)-before)
	}
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u2","name":"signup","props":{"method":"social","social_provider":"google"}}`); w.Code != 202 {
		t.Errorf("valid event refused: %d %s", w.Code, w.Body)
	}
	sum := do(t, h, "GET", "/v1/events/signup/summary", "key-1", "")
	if sum.Code != 200 || !strings.Contains(sum.Body.String(), `"refused":1`) {
		t.Errorf("summary: %d %s", sum.Code, sum.Body)
	}
	cat := do(t, h, "GET", "/v1/events/catalog", "key-1", "")
	if cat.Code != 200 || !strings.Contains(cat.Body.String(), `"name":"signup"`) || !strings.Contains(cat.Body.String(), `"problems":1`) {
		t.Errorf("catalog: %d %s", cat.Code, cat.Body)
	}
}

func TestEnforceNeedsSettingsAccess(t *testing.T) {
	s, st, _ := newSchemaServer(t)
	h := s.Handler()
	hash, _ := auth.HashPassword("long enough pw")
	st.tenants["acme"] = model.Tenant{ID: 1, Slug: "acme"}
	st.users = append(st.users, model.User{ID: 7, TenantID: 1, Email: "ed@acme.io", Role: auth.RoleEditor, PasswordHash: hash})
	cookie := sessionFrom(t, browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"ed@acme.io","password":"long enough pw"}`, nil, false))
	if w := browser(t, h, "PUT", "/v1/events/login/schema", `{"props":[]}`, cookie, true); w.Code != 200 {
		t.Fatalf("editor saving a schema: %d %s", w.Code, w.Body)
	}
	if w := browser(t, h, "PUT", "/v1/events/login/schema", `{"mode":"enforce","confirm":"ENFORCE","props":[]}`, cookie, true); w.Code != http.StatusForbidden {
		t.Errorf("editor turning on enforce: %d", w.Code)
	}
}

func TestSuggestedSchemasAndListField(t *testing.T) {
	s, st, _ := newSchemaServer(t)
	h := s.Handler()
	w := do(t, h, "POST", "/v1/events/schemas/suggested", "key-1", `{"names":["purchase","signup"]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"added":["purchase","signup"]`) {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	if len(st.defs) != 1 || st.defs[0].Key != "products" || st.defs[0].Type != "list" {
		t.Errorf("list field: %+v", st.defs)
	}
	if w := do(t, h, "POST", "/v1/events/schemas/suggested", "key-1", ""); !strings.Contains(w.Body.String(), `"added":["login","add_to_cart","cart_abandoned","checkout_abandoned","amp_poll_answered","amp_form_submitted"]`) {
		t.Errorf("add the rest: %s", w.Body)
	}
	var out struct {
		Schemas []struct {
			Name   string `json:"name"`
			Exists bool   `json:"exists"`
		} `json:"schemas"`
	}
	_ = json.Unmarshal(do(t, h, "GET", "/v1/events/schemas/suggested", "key-1", "").Body.Bytes(), &out)
	for _, sc := range out.Schemas {
		if !sc.Exists {
			t.Errorf("%s not marked as added", sc.Name)
		}
	}
	st.defs[0].Type = "string"
	if w := do(t, h, "PUT", "/v1/events/order/schema", "key-1", `{"props":[{"key":"sku","type":"string","add_to_list":"products"}]}`); w.Code != 400 {
		t.Errorf("adding to a text field: %d", w.Code)
	}
}

func TestSegmentRefreshAndExplain(t *testing.T) {
	s, st, _ := newSchemaServer(t)
	h := s.Handler()
	if w := do(t, h, "POST", "/v1/segments", "key-1", `{"name":"x","rule":{"attr":"city","op":"eq","value":"Pune"},"refresh":{"mode":"daily","at":"6:00"}}`); w.Code != 400 {
		t.Errorf("bad time: %d", w.Code)
	}
	w := do(t, h, "POST", "/v1/segments", "key-1", `{"name":"Pune","rule":{"none":[{"attr":"city","op":"eq","value":"Delhi"}]},"refresh":{"mode":"daily","at":"06:30","tz":"Asia/Kolkata"}}`)
	if w.Code != 201 || st.refresh[1] == nil || st.refresh[1].At != "06:30" {
		t.Fatalf("create: %d %s %+v", w.Code, w.Body, st.refresh)
	}
	st.contacts[1]["u1"] = model.Contact{TenantID: 1, ID: 1, ExternalID: "u1", Attrs: map[string]any{"city": "Pune"}}
	w = do(t, h, "GET", "/v1/contacts/u1/segments/1/explain", "key-1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"none","match":true`) || !strings.Contains(w.Body.String(), `"value":"Pune"`) {
		t.Errorf("explain: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "GET", "/v1/contacts/nobody/segments/1/explain", "key-1", ""); w.Code != 404 {
		t.Errorf("unknown contact: %d", w.Code)
	}
}

func TestListAndEnumFields(t *testing.T) {
	s, st, _ := newSchemaServer(t)
	h := s.Handler()
	for body, want := range map[string]int{
		`{"type":"list"}`: 200,
		`{"type":"enum","options":[{"value":"gold","label":"Gold"},{"value":"silver"}]}`: 200,
		`{"type":"enum"}`: 400,
		`{"type":"enum","options":[{"value":"a"},{"value":"a"}]}`: 400,
		`{"type":"string","options":[{"value":"a"}]}`:             400,
	} {
		if w := do(t, h, "PUT", "/v1/attributes/tier", "key-1", body); w.Code != want {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if len(st.defs) != 1 {
		t.Errorf("defs %+v", st.defs)
	}
}

func TestChangingEnforcedSchemaNeedsSettingsAccess(t *testing.T) {
	s, st, _ := newSchemaServer(t)
	h := s.Handler()
	st.schemas["login"] = eventschema.Schema{Name: "login", Mode: eventschema.ModeEnforce}
	hash, _ := auth.HashPassword("long enough pw")
	st.tenants["acme"] = model.Tenant{ID: 1, Slug: "acme"}
	st.users = append(st.users, model.User{ID: 7, TenantID: 1, Email: "ed@acme.io", Role: auth.RoleEditor, PasswordHash: hash})
	cookie := sessionFrom(t, browser(t, h, "POST", "/v1/auth/login", `{"workspace":"acme","email":"ed@acme.io","password":"long enough pw"}`, nil, false))
	for _, body := range []string{`{"mode":"enforce","props":[{"key":"x","type":"string"}]}`, `{"mode":"warn","props":[]}`} {
		if w := browser(t, h, "PUT", "/v1/events/login/schema", body, cookie, true); w.Code != http.StatusForbidden {
			t.Errorf("editor changing an enforced schema (%s): %d", body, w.Code)
		}
	}
	if w := browser(t, h, "DELETE", "/v1/events/login/schema", "", cookie, true); w.Code != http.StatusForbidden {
		t.Errorf("editor deleting an enforced schema: %d", w.Code)
	}
}

func TestNewListFieldNeedsSegmentsAccess(t *testing.T) {
	s, st, _ := newSchemaServer(t)
	sc := eventschema.Schema{Name: "order", Props: []eventschema.Prop{{Key: "sku", Type: eventschema.TypeString, AddToList: "skus"}}}
	contactsOnly := model.Principal{TenantID: 1, Perms: auth.Perms{auth.AreaContacts: auth.Edit}}
	if err := s.ensureListFields(context.Background(), contactsOnly, sc); err != errNeedsFieldAccess {
		t.Errorf("contacts edit only: %v", err)
	}
	if len(st.defs) != 0 {
		t.Errorf("field created: %+v", st.defs)
	}
	both := model.Principal{TenantID: 1, Perms: auth.Perms{auth.AreaContacts: auth.Edit, auth.AreaSegments: auth.Edit}}
	if err := s.ensureListFields(context.Background(), both, sc); err != nil || len(st.defs) != 1 {
		t.Errorf("with segments edit: %v %+v", err, st.defs)
	}
}

// fakeNames answers the event names query with what ClickHouse has seen, most sent first. The
// other Analytics methods come from latestEvents.
type fakeNames struct {
	latestEvents
	names []string
}

func (f fakeNames) MessageReport(context.Context, ch.Scope, string, int) (ch.Report, error) {
	return ch.Report{}, nil
}
func (f fakeNames) FunnelLevels(context.Context, segment.Query, int) ([]uint64, error) {
	return nil, nil
}
func (f fakeNames) EventNames(context.Context, int64) ([]string, error) {
	return append([]string(nil), f.names...), nil
}

func TestEventNamesIncludeDescribedEvents(t *testing.T) {
	s, _, _ := newSchemaServer(t)
	s.Analytics = fakeNames{names: []string{"page_view", "purchase"}}
	h := s.Handler()
	for _, name := range []string{"webinar_joined", "purchase"} {
		if w := do(t, h, "PUT", "/v1/events/"+name+"/schema", "key-1", `{"props":[]}`); w.Code != 200 {
			t.Fatalf("put %s: %d %s", name, w.Code, w.Body)
		}
	}
	w := do(t, h, "GET", "/v1/events/names", "key-1", "")
	var got struct{ Names []string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("names: %d %s", w.Code, w.Body)
	}
	if want := []string{"page_view", "purchase", "webinar_joined"}; !slices.Equal(got.Names, want) {
		t.Fatalf("names = %v, want %v (sent ones first, then described ones not yet sent, no repeats)", got.Names, want)
	}
}
