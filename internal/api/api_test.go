package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/store/pg"
)

// memStore implements Store for two tenants: key "key-1" -> tenant 1, "key-2" -> tenant 2.
// It embeds the interface, so a method a test doesn't implement panics if called.
type memStore struct {
	Store
	segments []model.Segment
	contacts map[int64]map[string]model.Contact

	tenants  map[string]model.Tenant // by slug
	users    []model.User
	sessions map[string]model.Principal
}

func (m *memStore) CreateTenant(context.Context, string, string, string, string) (int64, error) {
	return 3, nil
}

func (m *memStore) CreateWorkspace(_ context.Context, name, slug, _, siteKey string, owner model.User) (int64, int64, error) {
	if _, ok := m.tenants[slug]; ok {
		return 0, 0, pg.ErrConflict
	}
	t := model.Tenant{ID: int64(10 + len(m.tenants)), Name: name, Slug: slug, SiteKey: siteKey}
	m.tenants[slug] = t
	owner.TenantID, owner.ID, owner.Role = t.ID, int64(100+len(m.users)), "admin"
	m.users = append(m.users, owner)
	return t.ID, owner.ID, nil
}

func (m *memStore) TenantBySlug(_ context.Context, slug string) (model.Tenant, error) {
	if t, ok := m.tenants[slug]; ok {
		return t, nil
	}
	return model.Tenant{}, pg.ErrNotFound
}

func (m *memStore) Tenant(_ context.Context, id int64) (model.Tenant, error) {
	for _, t := range m.tenants {
		if t.ID == id {
			return t, nil
		}
	}
	return model.Tenant{ID: id}, nil
}

func (m *memStore) UserByEmail(_ context.Context, tenant int64, email string) (model.User, error) {
	for _, u := range m.users {
		if u.TenantID == tenant && u.Email == email {
			return u, nil
		}
	}
	return model.User{}, pg.ErrNotFound
}

func (m *memStore) User(_ context.Context, tenant, id int64) (model.User, error) {
	for _, u := range m.users {
		if u.TenantID == tenant && u.ID == id {
			return u, nil
		}
	}
	return model.User{}, pg.ErrNotFound
}

func (m *memStore) CreateSession(_ context.Context, hash string, userID, tenantID int64, _ time.Time) error {
	role := "admin"
	for _, u := range m.users {
		if u.ID == userID {
			role = u.Role
		}
	}
	m.sessions[hash] = model.Principal{TenantID: tenantID, UserID: userID, Role: role}
	return nil
}

func (m *memStore) SessionPrincipal(_ context.Context, hash string) (model.Principal, error) {
	if p, ok := m.sessions[hash]; ok {
		p.Perms, _ = auth.BuiltinPerms(p.Role)
		return p, nil
	}
	return model.Principal{}, pg.ErrNotFound
}

func (m *memStore) DeleteSession(_ context.Context, hash string) error {
	delete(m.sessions, hash)
	return nil
}

func (m *memStore) ListUsers(_ context.Context, tenant int64) ([]model.User, error) {
	return m.users, nil
}
func (m *memStore) TenantByKeyHash(_ context.Context, h string) (int64, error) {
	switch h {
	case HashKey("key-1"):
		return 1, nil
	case HashKey("key-2"):
		return 2, nil
	}
	return 0, pg.ErrNotFound
}
func (m *memStore) CreateSegment(_ context.Context, tenant int64, name string, rule []byte, hasWindow bool) (int64, error) {
	id := int64(len(m.segments) + 1)
	m.segments = append(m.segments, model.Segment{ID: id, TenantID: tenant, Name: name, Rule: rule, HasWindow: hasWindow})
	return id, nil
}
func (m *memStore) ListSegments(_ context.Context, tenant int64) ([]model.Segment, error) {
	var out []model.Segment
	for _, s := range m.segments {
		if s.TenantID == tenant {
			out = append(out, s)
		}
	}
	return out, nil
}
func (m *memStore) GetSegment(_ context.Context, tenant, id int64) (model.Segment, error) {
	for _, s := range m.segments {
		if s.TenantID == tenant && s.ID == id {
			return s, nil
		}
	}
	return model.Segment{}, pg.ErrNotFound
}
func (m *memStore) CreateTemplate(context.Context, model.Template) (int64, error) { return 1, nil }
func (m *memStore) CreateJourney(context.Context, int64, string, []byte, schedule.Window) (int64, error) {
	return 1, nil
}
func (m *memStore) SetJourneyStatus(context.Context, int64, int64, string) error { return nil }
func (m *memStore) JourneyStats(context.Context, int64, int64) (map[string]map[string]int, error) {
	return nil, nil
}
func (m *memStore) ContactByExternalID(_ context.Context, tenant int64, ext string) (model.Contact, error) {
	if c, ok := m.contacts[tenant][ext]; ok {
		return c, nil
	}
	return model.Contact{}, pg.ErrNotFound
}

type memMembers map[int64]bool

func (m memMembers) IsMember(_ context.Context, _, seg int64, _ uint32) (bool, error) {
	return m[seg], nil
}
func (m memMembers) Size(_ context.Context, _, seg int64) (int64, error) {
	if m[seg] {
		return 1, nil
	}
	return 0, nil
}

type memPub struct {
	recs []*kgo.Record
	err  error
}

func (p *memPub) Publish(_ context.Context, recs ...*kgo.Record) error {
	if p.err != nil {
		return p.err
	}
	p.recs = append(p.recs, recs...)
	return nil
}

func newServer() (*Server, *memStore, *memPub) {
	store := &memStore{
		contacts: map[int64]map[string]model.Contact{1: {"u1": {TenantID: 1, ID: 1, ExternalID: "u1"}}},
		tenants:  map[string]model.Tenant{},
		sessions: map[string]model.Principal{},
	}
	pub := &memPub{}
	return New(Deps{Store: store, Members: memMembers{1: true}, Pub: pub, AdminToken: "admin", AllowSignup: true,
		PublicURL: "http://localhost:7080", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}), store, pub
}

func do(t *testing.T, h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAuth(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	if w := do(t, h, "GET", "/v1/segments", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no key: %d", w.Code)
	}
	if w := do(t, h, "GET", "/v1/segments", "wrong", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("bad key: %d", w.Code)
	}
	if w := do(t, h, "POST", "/v1/tenants", "key-1", `{"name":"x"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("tenant key must not create tenants: %d", w.Code)
	}
	w := do(t, h, "POST", "/v1/tenants", "admin", `{"name":"Acme"}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"api_key":"pk_`) {
		t.Errorf("create tenant: %d %s", w.Code, w.Body)
	}
}

func TestIngestSingleAndBatch(t *testing.T) {
	s, _, pub := newServer()
	h := s.Handler()

	w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"purchase","tenant_id":999,"props":{"amount":499}}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("single: %d %s", w.Code, w.Body)
	}
	w = do(t, h, "POST", "/v1/events", "key-1", `{"events":[{"external_id":"u2","name":"signup"},{"external_id":"u3","name":"signup","email":"u3@x.y"}]}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("batch: %d %s", w.Code, w.Body)
	}
	if len(pub.recs) != 3 {
		t.Fatalf("published %d, want 3", len(pub.recs))
	}
	var e event.Event
	_ = json.Unmarshal(pub.recs[0].Value, &e)
	if e.TenantID != 1 {
		t.Errorf("tenant id taken from body (%d); must come from the API key", e.TenantID)
	}
	if string(pub.recs[0].Key) != "1:u1" || pub.recs[0].Topic != event.TopicRaw {
		t.Errorf("record %s / %s", pub.recs[0].Key, pub.recs[0].Topic)
	}
}

func TestIngestValidation(t *testing.T) {
	s, _, pub := newServer()
	h := s.Handler()
	for _, body := range []string{
		`not json`,
		`{"name":"x"}`,
		`{"events":[]}`,
		`{"events":[{"external_id":"u","name":"ok"},{"external_id":"u","name":"bad name"}]}`,
	} {
		if w := do(t, h, "POST", "/v1/events", "key-1", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
	if len(pub.recs) != 0 {
		t.Error("a rejected batch must publish nothing")
	}
	pub.err = errors.New("broker down")
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u","name":"x"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("publish failure: %d, want 503", w.Code)
	}
}

func TestSegmentsAreTenantScoped(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	w := do(t, h, "POST", "/v1/segments", "key-1", `{"name":"Delhi","rule":{"attr":"city","op":"eq","value":"Delhi"}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/segments", "key-1", `{"name":"bad","rule":{"attr":"x'--","op":"exists"}}`); w.Code != http.StatusBadRequest {
		t.Errorf("invalid rule: %d", w.Code)
	}
	if w := do(t, h, "GET", "/v1/segments/1", "key-1", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"size":1`) {
		t.Errorf("own segment: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "GET", "/v1/segments/1", "key-2", ""); w.Code != http.StatusNotFound {
		t.Errorf("other tenant's segment: %d, want 404", w.Code)
	}
}

func TestContactShowsSegments(t *testing.T) {
	s, store, _ := newServer()
	store.segments = []model.Segment{{ID: 1, TenantID: 1}, {ID: 2, TenantID: 1}}
	w := do(t, s.Handler(), "GET", "/v1/contacts/u1", "key-1", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"segments":[1]`) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := do(t, s.Handler(), "GET", "/v1/contacts/u1", "key-2", ""); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant contact lookup: %d, want 404", w.Code)
	}
}

func TestJourneyValidation(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	bad := `{"name":"loop","graph":{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"wait","seconds":5,"next":"a"}}}}`
	if w := do(t, h, "POST", "/v1/journeys", "key-1", bad); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "cycle") {
		t.Errorf("cyclic journey: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/journeys/1/explode", "key-1", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown action: %d", w.Code)
	}
}
