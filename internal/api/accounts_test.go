package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// acctStore adds stages, companies, lists and a site key to memStore.
// Methods a test doesn't use come from the nil AccountStore and panic if called.
type acctStore struct {
	*memStore
	AccountStore
	stages    []pg.Stage
	companies map[int64]pg.Company
	members   map[int64][]string // company -> external ids
	upserts   int
}

func (a *acctStore) ListStages(context.Context, int64) ([]pg.Stage, error) { return a.stages, nil }
func (a *acctStore) StageCounts(context.Context, int64) (map[string]int64, error) {
	return map[string]int64{"MQL": 3}, nil
}
func (a *acctStore) CreateStage(_ context.Context, _ int64, st pg.Stage) (int64, error) {
	for _, x := range a.stages {
		if strings.EqualFold(x.Name, st.Name) {
			return 0, pg.ErrConflict
		}
	}
	st.ID = int64(len(a.stages) + 1)
	a.stages = append(a.stages, st)
	return st.ID, nil
}
func (a *acctStore) GetCompany(_ context.Context, _ int64, id int64) (pg.Company, error) {
	if c, ok := a.companies[id]; ok {
		return c, nil
	}
	return pg.Company{}, pg.ErrNotFound
}
func (a *acctStore) UpdateCompany(_ context.Context, _ int64, c pg.Company) error {
	a.companies[c.ID] = c
	return nil
}
func (a *acctStore) UpsertCompanyByName(_ context.Context, _ int64, name string) (pg.Company, error) {
	a.upserts++
	for _, c := range a.companies {
		if strings.EqualFold(c.Name, name) {
			return c, nil
		}
	}
	c := pg.Company{ID: int64(100 + len(a.companies)), Name: name, Attrs: map[string]any{}}
	a.companies[c.ID] = c
	return c, nil
}
func (a *acctStore) MemberIDs(_ context.Context, _ int64, key, value string, after uint32, _ int) ([]string, uint32, error) {
	if key != "company_id" || after > 0 {
		return nil, 0, nil
	}
	for id, exts := range a.members {
		if value == strconv.FormatInt(id, 10) {
			return exts, uint32(len(exts)), nil
		}
	}
	return nil, 0, nil
}

func (a *acctStore) ListSegmentsOfKind(context.Context, int64, string) ([]model.Segment, error) {
	return []model.Segment{{ID: 1, Kind: "static"}, {ID: 5, Kind: "static"}}, nil
}
func (a *acctStore) TenantBySiteKey(_ context.Context, key string) (model.Tenant, error) {
	if key == "site-1" {
		return model.Tenant{ID: 1}, nil
	}
	return model.Tenant{}, pg.ErrNotFound
}

func accountServer() (*Server, *acctStore, *memPub) {
	s, mem, pub := newServer()
	mem.contacts[1]["u2"] = model.Contact{TenantID: 1, ID: 2, ExternalID: "u2", Attrs: map[string]any{}}
	mem.contacts[1]["gone"] = model.Contact{TenantID: 1, ID: 3, ExternalID: "gone", Attrs: map[string]any{"merged_into": "u2"}}
	a := &acctStore{memStore: mem, stages: []pg.Stage{{ID: 1, Name: "Lead"}, {ID: 2, Name: "MQL"}},
		companies: map[int64]pg.Company{7: {ID: 7, Name: "Acme", Attrs: map[string]any{"industry": "Retail"}}},
		members:   map[int64][]string{7: {"u1", "u2"}}}
	s.Store = a
	s.bg = func(f func()) { f() }
	return s, a, pub
}

func published(pub *memPub) []event.Event {
	var out []event.Event
	for _, r := range pub.recs {
		var e event.Event
		_ = json.Unmarshal(r.Value, &e)
		out = append(out, e)
	}
	return out
}

func TestContactStage(t *testing.T) {
	s, _, pub := accountServer()
	h := s.Handler()
	for body, code := range map[string]int{`{"stage":"Nope"}`: 400, `{"stage":""}`: 400} {
		if w := do(t, h, "PUT", "/v1/contacts/u1/stage", "key-1", body); w.Code != code {
			t.Errorf("%s: %d, want %d", body, w.Code, code)
		}
	}
	if w := do(t, h, "PUT", "/v1/contacts/nobody/stage", "key-1", `{"stage":"MQL"}`); w.Code != 404 {
		t.Errorf("unknown contact: %d", w.Code)
	}
	if w := do(t, h, "PUT", "/v1/contacts/gone/stage", "key-1", `{"stage":"MQL"}`); w.Code != 409 {
		t.Errorf("merged contact: %d", w.Code)
	}
	if w := do(t, h, "PUT", "/v1/contacts/u1/stage", "key-1", `{"stage":"mql"}`); w.Code != 202 {
		t.Fatalf("set stage: %d %s", w.Code, w.Body)
	}
	evs := published(pub)
	if len(evs) != 1 || evs[0].Name != event.StageChanged || evs[0].Props["stage"] != "MQL" {
		t.Errorf("published %+v; want stage_changed with the stage's own spelling", evs)
	}
	// Servers can send stage_changed as an event too: checked and spelled the same way.
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"stage_changed","props":{"stage":"Unknown"}}`); w.Code != 400 {
		t.Errorf("unknown stage by event: %d", w.Code)
	}
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"stage_changed","props":{"stage":"lead","rename":true}}`); w.Code != 202 {
		t.Errorf("stage by event: %d %s", w.Code, w.Body)
	}
	if e := published(pub)[1]; e.Props["stage"] != "Lead" || e.Props["rename"] != nil {
		t.Errorf("stage event = %+v", e)
	}
}

func TestStageValidation(t *testing.T) {
	s, a, _ := accountServer()
	h := s.Handler()
	for _, body := range []string{`{"name":""}`, `{"name":"` + strings.Repeat("x", 65) + `"}`, `{"name":"Hot","min_points":-1}`} {
		if w := do(t, h, "POST", "/v1/stages", "key-1", body); w.Code != 400 {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
	if w := do(t, h, "POST", "/v1/stages", "key-1", `{"name":"lead"}`); w.Code != 409 {
		t.Errorf("duplicate name: %d", w.Code)
	}
	if w := do(t, h, "POST", "/v1/stages", "key-1", `{"name":" SQL ","min_points":100}`); w.Code != 201 || a.stages[2].Name != "SQL" || *a.stages[2].MinPoints != 100 {
		t.Errorf("create: %d %+v", w.Code, a.stages)
	}
	w := do(t, h, "GET", "/v1/stages", "key-1", "")
	if !strings.Contains(w.Body.String(), `"name":"MQL","description":"","sort_order":0,"min_points":null,"contacts":3`) {
		t.Errorf("list: %s", w.Body)
	}
	// A journey can only move contacts to a stage that exists.
	graph := func(stage string) string {
		return `{"name":"j","graph":{"trigger":{"type":"event","event":"signup"},"entry":"s","nodes":{"s":{"type":"change_stage","stage":"` + stage + `","next":"x"},"x":{"type":"exit"}}}}`
	}
	if w := do(t, h, "POST", "/v1/journeys", "key-1", graph("Nope")); w.Code != 400 || !strings.Contains(w.Body.String(), "no stage called") {
		t.Errorf("journey with unknown stage: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/journeys", "key-1", graph("MQL")); w.Code != 201 {
		t.Errorf("journey: %d %s", w.Code, w.Body)
	}
}

func TestCompanyLinks(t *testing.T) {
	s, a, pub := accountServer()
	h := s.Handler()
	for body, code := range map[string]int{`{}`: 400, `{"company_id":"7"}`: 400, `{"company_id":0}`: 400, `{"company_id":99}`: 400} {
		if w := do(t, h, "PUT", "/v1/contacts/u1/company", "key-1", body); w.Code != code {
			t.Errorf("%s: %d, want %d", body, w.Code, code)
		}
	}
	if w := do(t, h, "PUT", "/v1/contacts/u1/company", "key-1", `{"company_id":7}`); w.Code != 202 {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "PUT", "/v1/contacts/u1/company", "key-1", `{"company_id":null}`); w.Code != 202 {
		t.Fatalf("unlink: %d %s", w.Code, w.Body)
	}
	evs := published(pub)
	if evs[0].Name != event.CompanyLinked || evs[0].Props["name"] != "Acme" || evs[0].Props["company_id"] != 7.0 ||
		evs[0].Props["attrs"].(map[string]any)["industry"] != "Retail" || evs[1].Name != event.CompanyUnlinked || evs[1].Props["company_id"] != nil {
		t.Errorf("published %+v", evs)
	}

	// Changing a company refreshes its members' copies.
	pub.recs = nil
	for _, body := range []string{`{"name":""}`, `{"name":"A","attrs":{"id":1}}`, `{"name":"A","attrs":{"x":{"nested":1}}}`, `{"name":"A","domain":"a b.com"}`,
		`{"name":"A","attrs":{"` + strings.Repeat("k", 57) + `":1}}`} {
		if w := do(t, h, "PUT", "/v1/companies/7", "key-1", body); w.Code != 400 {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
	if w := do(t, h, "PUT", "/v1/companies/7", "key-1", `{"name":"Acme Ltd","domain":"ACME.com","attrs":{"industry":"Retail","size":120}}`); w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	evs = published(pub)
	if len(evs) != 2 || evs[0].Name != event.CompanyUpdated || evs[1].ExternalID != "u2" || evs[0].Props["name"] != "Acme Ltd" || a.companies[7].Domain != "acme.com" {
		t.Errorf("fan-out %+v", evs)
	}

	// An identify from a server naming a company links the contact, creating the company once.
	pub.recs = nil
	w := do(t, h, "POST", "/v1/events", "key-1", `{"events":[{"id":"e1","external_id":"n1","name":"identify","attrs":{"company":"Globex"}},
		{"external_id":"n2","name":"identify","attrs":{"company":"globex "}},{"external_id":"n3","name":"purchase","attrs":{"company":"Initech"}}]}`)
	if w.Code != 202 {
		t.Fatalf("ingest: %d %s", w.Code, w.Body)
	}
	evs = published(pub)
	if len(evs) != 5 || evs[1].Name != event.CompanyLinked || evs[1].ExternalID != "n1" || evs[1].ID != "e1:company" ||
		evs[3].Props["company_id"] != evs[1].Props["company_id"] || a.upserts != 1 || evs[4].Name != "purchase" {
		t.Errorf("published %+v (upserts %d)", evs, a.upserts)
	}
	if !strings.Contains(w.Body.String(), `"accepted":3`) {
		t.Errorf("response %s: want the 3 events the caller sent", w.Body)
	}
	// Company events are recorded by Pulse itself.
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"company_linked","props":{"company_id":7}}`); w.Code != 400 {
		t.Errorf("company_linked by event: %d", w.Code)
	}
}

func TestMergeValidation(t *testing.T) {
	s, _, pub := accountServer()
	h := s.Handler()
	for body, code := range map[string]int{
		`{"winner":"u1"}`:                  400,
		`{"winner":"u1","loser":"u1"}`:     400, // into itself
		`{"winner":"u1","loser":"gone"}`:   409, // loser already merged
		`{"winner":"gone","loser":"u1"}`:   409, // winner merged away
		`{"winner":"u1","loser":"nobody"}`: 404,
	} {
		if w := do(t, h, "POST", "/v1/contacts/merge", "key-1", body); w.Code != code {
			t.Errorf("%s: %d, want %d (%s)", body, w.Code, code, w.Body)
		}
	}
	if len(pub.recs) != 0 {
		t.Fatal("refused merges published events")
	}
	if w := do(t, h, "POST", "/v1/contacts/merge", "key-1", `{"winner":"u1","loser":"u2"}`); w.Code != 202 {
		t.Fatalf("merge: %d %s", w.Code, w.Body)
	}
	// memMembers says every contact is in segment 1, so the winner joins list 1 (not list 5).
	evs := published(pub)
	if len(evs) != 2 || evs[0].Name != event.MergeRequested || evs[0].ExternalID != "u1" || evs[0].Props["loser"] != "u2" ||
		evs[1].Name != event.ListJoined || evs[1].Props["list_id"] != 1.0 || string(pub.recs[1].Key) != "1:u1" {
		t.Errorf("published %+v", evs)
	}
	if w := do(t, h, "POST", "/v1/events", "key-1", `{"external_id":"u1","name":"merge_requested","props":{"loser":"u2"}}`); w.Code != 400 {
		t.Errorf("merge by event: %d", w.Code)
	}
	if w := do(t, h, "GET", "/v1/contacts/duplicates?by=name", "key-1", ""); w.Code != 400 {
		t.Errorf("duplicates by name: %d", w.Code)
	}
}

// A browser can't set stage or company attributes, send their events, or merge a known contact.
func TestBrowserCantSetAccounts(t *testing.T) {
	s, _, pub := accountServer()
	h := s.Handler()
	web := func(body string) int {
		r := httptest.NewRequest("POST", "/v1/web/events", strings.NewReader(body))
		r.Header.Set("X-Pulse-Site-Key", "site-1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, name := range []string{"stage_changed", "company_linked", "company_updated", "company_unlinked", "merge_requested"} {
		if code := web(`{"events":[{"external_id":"u1","name":"` + name + `"}]}`); code != 400 {
			t.Errorf("%s from a browser: %d", name, code)
		}
	}
	if code := web(`{"events":[{"external_id":"u1","name":"identify","attrs":{"stage":"Customer","stage_changed_at":"x","company_id":7,"company_name":"A","company_size":9,"company":"Acme"},
		"props":{"anonymous_id":"u2"}},{"external_id":"u1","name":"identify","props":{"anonymous_id":"anon-abc"}}]}`); code != 202 {
		t.Fatalf("identify: %d", code)
	}
	evs := published(pub)
	if len(evs) != 2 || len(evs[0].Attrs) != 1 || evs[0].Attrs["company"] != "Acme" || evs[0].Props["anonymous_id"] != nil || evs[1].Props["anonymous_id"] != "anon-abc" {
		t.Errorf("published %+v: reserved attributes and known-contact merges must be dropped, no company linked", evs)
	}
}

func TestAccountRoutePermissions(t *testing.T) {
	viewer, _ := auth.BuiltinPerms(auth.RoleViewer)
	editor, _ := auth.BuiltinPerms(auth.RoleEditor)
	for _, tc := range []struct {
		pattern string
		perms   auth.Perms
		want    bool
	}{
		{"GET /v1/stages", viewer, true},
		{"POST /v1/stages", viewer, false},
		{"POST /v1/stages", auth.Perms{auth.AreaSegments: auth.Edit}, true},
		{"POST /v1/stages", auth.Perms{auth.AreaContacts: auth.Edit}, false},
		{"PUT /v1/contacts/{external_id}/stage", auth.Perms{auth.AreaContacts: auth.Edit}, true},
		{"PUT /v1/contacts/{external_id}/stage", viewer, false},
		{"GET /v1/companies", viewer, true},
		{"PUT /v1/companies/{id}", editor, true},
		{"DELETE /v1/companies/{id}", auth.Perms{auth.AreaSegments: auth.Edit}, false},
		{"POST /v1/contacts/merge", viewer, false},
		{"POST /v1/contacts/merge", auth.Perms{auth.AreaContacts: auth.Edit}, true},
		{"GET /v1/contacts/duplicates", viewer, true},
	} {
		method, path, _ := strings.Cut(tc.pattern, " ")
		r, _ := http.NewRequest(method, path, nil)
		r.Pattern = tc.pattern
		n := required(r)
		if got := tc.perms.Allows(n.area, n.level); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v (needs %s %v)", tc.pattern, got, tc.want, n.area, n.level)
		}
	}
}
