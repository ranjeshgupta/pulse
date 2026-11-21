package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"pulse/internal/model"
	"pulse/internal/store/pg"
)

func TestHasNULEscape(t *testing.T) {
	for in, want := range map[string]bool{
		`{"a":"x\u0000"}`:  true,
		`{"a\u0000":1}`:    true,
		`{"a":"\\u0000"}`:  false, // an escaped backslash, then the text u0000
		`{"a":"\\\u0000"}`: true,
		`{"a":"\u00001"}`:  true,
		`{"a":"\u0001"}`:   false,
		`{"a":"x"}`:        false,
		`{"a":"\u000"}`:    false,
		`{"a":"ends with\`: false,
	} {
		if got := hasNULEscape([]byte(in)); got != want {
			t.Errorf("hasNULEscape(%s) = %v, want %v", in, got, want)
		}
	}
}

// Bodies the database can't store, or that aren't the JSON a route expects, get a 4xx that says
// what is wrong; none reaches a handler.
func TestDecodeErrors(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	for _, tc := range []struct {
		body string
		code int
		want string
	}{
		{`{"name":"Acme\u0000"}`, 400, "NUL"},
		{`{"name":"Acme","x":{"\u0000":1}}`, 400, "NUL"},
		{``, 400, "body is empty"},
		{`   `, 400, "body is empty"},
		{`{"name": 5}`, 400, `name\" should be text, not number`},
		{`[1]`, 400, "expected an object, got array"},
		{`{"name": "x", `, 400, "invalid JSON"},
		{`{"name": x}`, 400, "invalid JSON at byte 10"},
		{`{"name":"` + strings.Repeat("x", maxBody) + `"}`, 413, "too large (the limit is 4 MB)"},
	} {
		w := do(t, h, "POST", "/v1/tenants", "admin", tc.body)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%.40q: %d %s, want %d %q", tc.body, w.Code, w.Body, tc.code, tc.want)
		}
	}
	// Segment rules are checked the same way (a handler with its own fields and a rule).
	if w := do(t, h, "POST", "/v1/segments", "key-1", `{"name":"x","rule":{"attr":"city","op":"eq","value":"\u0000"}}`); w.Code != 400 {
		t.Errorf("NUL in a rule: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/tenants", "admin", `{"name":"Acme"}`); w.Code != 201 {
		t.Errorf("a good body: %d %s", w.Code, w.Body)
	}
}

func TestNULInAddressIsRefused(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	for _, p := range []string{"/v1/contacts/%00", "/v1/contacts/u1/events?limit=%00", "/v1/segments?q=a%00b", "/v1/attributes/%00"} {
		w := do(t, h, "GET", p, "key-1", "")
		if w.Code != 400 || !strings.Contains(w.Body.String(), "NUL") {
			t.Errorf("GET %s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := do(t, h, "GET", "/v1/contacts/u1", "key-1", ""); w.Code != 200 {
		t.Errorf("ordinary address: %d %s", w.Code, w.Body)
	}
}

func TestPanicIsAnsweredNotDropped(t *testing.T) {
	s, _, _ := newServer()
	h := s.inputGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		var m map[string]int
		m["boom"]++ // nil map write
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/anything", nil))
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"internal error"`) || strings.Contains(w.Body.String(), "goroutine") {
		t.Errorf("panic: %d %s", w.Code, w.Body)
	}
}

func TestDatabaseInputErrorsAre4xx(t *testing.T) {
	s, _, _ := newServer()
	for code, want := range map[string]int{"22021": 400, "22P05": 400, "22001": 400, "22003": 400, "22008": 400, "22023": 400,
		"23505": 409, "23503": 400, "23514": 400, "40001": 500, "42P01": 500} {
		w := httptest.NewRecorder()
		s.fail(w, fmt.Errorf("saving: %w", &pgconn.PgError{Code: code, Message: "secret detail"}))
		if w.Code != want || strings.Contains(w.Body.String(), "secret detail") {
			t.Errorf("SQLSTATE %s: %d %s, want %d without the database's message", code, w.Code, w.Body, want)
		}
	}
}

func TestAuditDetailDropsNUL(t *testing.T) {
	out := string(redactJSON([]byte(`{"name":"a\u0000b","n\u0000":["\u0000"],"password":"\u0000"}`)))
	if strings.Contains(out, `\u0000`) || !strings.Contains(out, `"a�b"`) || !strings.Contains(out, `"password":"[redacted]"`) {
		t.Errorf("audit detail = %s", out)
	}
	if out := string(redactJSON([]byte(`"top\u0000"`))); strings.Contains(out, `\u0000`) {
		t.Errorf("audit detail = %s", out)
	}
}

// A journey may only name segments and lists of its own workspace.
func TestJourneyRefersToOwnSegmentsOnly(t *testing.T) {
	s, store, _ := newServer()
	store.segments = []model.Segment{{ID: 1, TenantID: 1}, {ID: 2, TenantID: 2, Kind: "static"}}
	h := s.Handler()
	graph := func(trigger, nodes string) string {
		return `{"name":"j","graph":{"trigger":` + trigger + `,"entry":"a","nodes":{` + nodes + `}}}`
	}
	exit := `"a":{"type":"exit"}`
	for _, tc := range []struct{ body, want string }{
		{graph(`{"type":"segment_entered","segment_id":2}`, exit), "the trigger's segment 2 doesn't exist"},
		{graph(`{"type":"segment_entered","segment_id":99}`, exit), "the trigger's segment 99 doesn't exist"},
		{graph(`{"type":"event","event":"x"}`, `"a":{"type":"update_list","add_lists":[2],"next":"b"},"b":{"type":"exit"}`), `segment or list 2 doesn't exist`},
	} {
		if w := do(t, h, "POST", "/v1/journeys", "key-1", tc.body); w.Code != 400 || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want 400 %q", tc.body, w.Code, w.Body, tc.want)
		}
	}
	if w := do(t, h, "POST", "/v1/journeys", "key-1", graph(`{"type":"segment_entered","segment_id":1}`, exit)); w.Code != 201 {
		t.Errorf("own segment: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/journeys", "key-2", graph(`{"type":"segment_entered","segment_id":2}`, exit)); w.Code != 201 {
		t.Errorf("workspace 2's own segment: %d %s", w.Code, w.Body)
	}
}

func TestSegmentPreviewRefersToOwnSegmentsOnly(t *testing.T) {
	s, store, _ := newServer()
	s.Analytics = latestEvents{}
	store.segments = []model.Segment{{ID: 1, TenantID: 1}, {ID: 2, TenantID: 2}}
	h := s.Handler()
	if w := do(t, h, "POST", "/v1/segments/preview", "key-1", `{"rule":{"segment":2,"op":"in"}}`); w.Code != 400 || !strings.Contains(w.Body.String(), "segment 2 doesn't exist") {
		t.Errorf("another workspace's segment: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/segments/preview", "key-1", `{"rule":{"segment":1,"op":"in"}}`); w.Code != 200 {
		t.Errorf("own segment: %d %s", w.Code, w.Body)
	}
}

type focusOrderStore struct {
	*memStore
	items []model.FocusItem
	set   []int64
}

func (f *focusOrderStore) ListFocusItems(_ context.Context, tenant int64, _ bool) ([]model.FocusItem, error) {
	var out []model.FocusItem
	for _, it := range f.items {
		if it.TenantID == tenant {
			out = append(out, it)
		}
	}
	return out, nil
}
func (f *focusOrderStore) GetFocusItem(context.Context, int64, int64) (model.FocusItem, error) {
	return model.FocusItem{}, pg.ErrNotFound
}
func (f *focusOrderStore) SaveFocusItem(context.Context, model.FocusItem) (int64, error) {
	return 0, nil
}
func (f *focusOrderStore) DeleteFocusItem(context.Context, int64, int64) error { return nil }
func (f *focusOrderStore) ListDWCSlots(context.Context, int64, bool) ([]model.DWCSlot, error) {
	return nil, nil
}
func (f *focusOrderStore) GetDWCSlot(context.Context, int64, int64) (model.DWCSlot, error) {
	return model.DWCSlot{}, pg.ErrNotFound
}
func (f *focusOrderStore) SaveDWCSlot(context.Context, model.DWCSlot) (int64, error) { return 0, nil }
func (f *focusOrderStore) DeleteDWCSlot(context.Context, int64, int64) error         { return nil }
func (f *focusOrderStore) SetFocusPriorities(_ context.Context, _ int64, ids []int64) error {
	f.set = ids
	return nil
}

func TestFocusOrderRefusesOtherWorkspacesItems(t *testing.T) {
	s, mem, _ := newServer()
	st := &focusOrderStore{memStore: mem, items: []model.FocusItem{{ID: 1, TenantID: 1}, {ID: 2, TenantID: 1}, {ID: 3, TenantID: 2}}}
	s.Store = st
	h := s.Handler()
	if w := do(t, h, "PUT", "/v1/focus-items/order", "key-1", `{"ids":[2,3]}`); w.Code != 400 || !strings.Contains(w.Body.String(), "focus item 3 doesn't exist") || st.set != nil {
		t.Errorf("another workspace's item: %d %s (set %v)", w.Code, w.Body, st.set)
	}
	if w := do(t, h, "PUT", "/v1/focus-items/order", "key-1", `{"ids":[2,1]}`); w.Code != 204 || len(st.set) != 2 {
		t.Errorf("own items: %d %s (set %v)", w.Code, w.Body, st.set)
	}
}

// One event can't carry absurdly large or deep props or attrs, whatever the body limit allows.
func TestEventSizeLimits(t *testing.T) {
	s, _, pub := newServer()
	h := s.Handler()
	deep := `"x"`
	for range 25 {
		deep = `{"a":` + deep + `}`
	}
	many := make([]string, 501)
	for i := range many {
		many[i] = fmt.Sprintf(`"k%d":1`, i)
	}
	list := `[` + strings.TrimSuffix(strings.Repeat(`1,`, 10001), ",") + `]`
	for _, tc := range []struct{ body, want string }{
		{`{"external_id":"u1","name":"x","props":{"p":` + deep + `}}`, "nested more than 20 levels"},
		{`{"external_id":"u1","name":"x","props":{"big":"` + strings.Repeat("x", 65<<10) + `"}}`, "at most 64 KB"},
		{`{"external_id":"u1","name":"x","attrs":{` + strings.Join(many, ",") + `}}`, "attrs has 501 keys; the limit is 500"},
		{`{"external_id":"u1","name":"x","props":{"l":` + list + `}}`, "at most 10000 values"},
		{`{"events":[{"external_id":"u1","name":"ok"},{"external_id":"u1","name":"x","props":{"p":` + deep + `}}]}`, "event 1: props.p"},
	} {
		if w := do(t, h, "POST", "/v1/events", "key-1", tc.body); w.Code != 400 || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%.60s: %d %.200s, want 400 %q", tc.body, w.Code, w.Body, tc.want)
		}
	}
	if len(pub.recs) != 0 {
		t.Errorf("%d events published from refused batches", len(pub.recs))
	}
	ok := `{"external_id":"u1","name":"purchase","props":{"items":[{"sku":"a","qty":2}],"note":"` + strings.Repeat("x", 60<<10) + `"}}`
	if w := do(t, h, "POST", "/v1/events", "key-1", ok); w.Code != 202 {
		t.Errorf("an ordinary event: %d %s", w.Code, w.Body)
	}
}
