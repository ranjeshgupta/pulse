package profile

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
)

func TestMerge(t *testing.T) {
	cur := map[string]any{"city": "Pune", "age": 30.0, "plan": "free"}
	e := &event.Event{Attrs: map[string]any{"city": "Delhi", "age": 30, "vip": true}, Email: "a@b.c"}
	merged, changed := Merge(cur, e)

	// age 30.0 -> 30 has the same canonical value, so it is not a change.
	if want := []string{"city", "email", "email_domain", "vip"}; !reflect.DeepEqual(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
	if merged["city"] != "Delhi" || merged["plan"] != "free" || merged["email"] != "a@b.c" {
		t.Errorf("merged = %v", merged)
	}
	if cur["city"] != "Pune" {
		t.Error("Merge modified its input")
	}
	if _, changed := Merge(merged, &event.Event{Attrs: map[string]any{"city": "Delhi"}}); len(changed) != 0 {
		t.Errorf("re-applying the same value reported changes: %v", changed)
	}
}

type memStore struct {
	contacts map[string]model.Contact
	nextID   uint32
	saves    int
	points   map[int64]map[string]int
	stages   map[int64][]pg.Stage
}

func (m *memStore) LookupContacts(_ context.Context, _ int64, exts []string) (map[string]model.Contact, error) {
	found := map[string]model.Contact{}
	for _, ext := range exts {
		if c, ok := m.contacts[ext]; ok {
			found[ext] = c
		}
	}
	return found, nil
}

func (m *memStore) PointRules(context.Context) (map[int64]map[string]int, error) {
	return m.points, nil
}

func (m *memStore) StageRules(context.Context) (map[int64][]pg.Stage, error) {
	return m.stages, nil
}

func (m *memStore) ResolveContacts(_ context.Context, tenant int64, exts []string) (map[string]model.Contact, map[string]bool, error) {
	found, created := map[string]model.Contact{}, map[string]bool{}
	for _, ext := range exts {
		if c, ok := m.contacts[ext]; ok {
			found[ext] = c
			continue
		}
		m.nextID++
		// CreatedAt far in the past: "new" must come from the created flag in these tests.
		c := model.Contact{TenantID: tenant, ID: m.nextID, ExternalID: ext, Attrs: map[string]any{}, CreatedAt: time.Unix(0, 0)}
		m.contacts[ext] = c
		found[ext], created[ext] = c, true
	}
	return found, created, nil
}

func (m *memStore) SaveContactsAttrs(_ context.Context, updates []pg.AttrUpdate) error {
	m.saves++
	for _, u := range updates {
		for k, c := range m.contacts {
			if c.ID == u.ID {
				c.Email, c.Attrs = u.Email, u.Attrs
				m.contacts[k] = c
			}
		}
	}
	return nil
}

type memPub struct{ recs []*kgo.Record }

func (p *memPub) Publish(_ context.Context, recs ...*kgo.Record) error {
	p.recs = append(p.recs, recs...)
	return nil
}

// Several events of one contact in one batch must see each other's attribute changes, in order.
func TestHandleBatchFoldsInOrder(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, pub := &memStore{contacts: map[string]model.Contact{}}, &memPub{}
	svc := New(store, rdb, pub)
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	mk := func(id, ext, name string, attrs map[string]any) *kgo.Record {
		b, _ := json.Marshal(event.Event{ID: id, TenantID: 1, ExternalID: ext, Name: name, Ts: ts, Attrs: attrs})
		return &kgo.Record{Topic: event.TopicRaw, Value: b}
	}
	recs := []*kgo.Record{
		mk("1", "u1", "identify", map[string]any{"city": "Pune"}),
		mk("2", "u2", "identify", map[string]any{"city": "Goa"}),
		mk("3", "u1", "identify", map[string]any{"city": "Delhi", "plan": "pro"}),
		mk("4", "u1", "purchase", nil),
	}
	if err := svc.HandleBatch(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	if len(pub.recs) != 4 || store.saves != 1 {
		t.Fatalf("published %d (want 4), profile write batches %d (want 1)", len(pub.recs), store.saves)
	}
	var out []event.Event
	for _, r := range pub.recs {
		var e event.Event
		_ = json.Unmarshal(r.Value, &e)
		out = append(out, e)
	}
	if !out[0].NewContact || out[2].NewContact || out[3].NewContact {
		t.Error("only a contact's first event in the batch should be flagged new")
	}
	if !reflect.DeepEqual(out[2].ChangedAttrs, []string{"city", "plan"}) || out[2].Profile["city"] != "Delhi" {
		t.Errorf("third event = %+v, want it to see city Pune -> Delhi", out[2])
	}
	if out[3].Profile["plan"] != "pro" || len(out[3].ChangedAttrs) != 0 {
		t.Errorf("purchase should carry the folded profile and change nothing: %+v", out[3])
	}
	if c := store.contacts["u1"]; c.Attrs["city"] != "Delhi" || c.Attrs["plan"] != "pro" {
		t.Errorf("stored profile = %v, want the final folded state", c.Attrs)
	}
	if err := svc.HandleBatch(context.Background(), recs); err != nil || len(pub.recs) != 4 {
		t.Errorf("redelivered batch published again (%d records, err %v)", len(pub.recs), err)
	}
}

func TestHandleEnrichesAndCountsOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, pub := &memStore{contacts: map[string]model.Contact{}}, &memPub{}
	svc := New(store, rdb, pub)
	ctx := context.Background()
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	raw, _ := json.Marshal(event.Event{ID: "e1", TenantID: 1, ExternalID: "u1", Name: "purchase", Ts: ts, Attrs: map[string]any{"city": "Delhi"}})
	rec := &kgo.Record{Topic: event.TopicRaw, Value: raw}
	for i := 0; i < 2; i++ { // redelivery
		if err := svc.Handle(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if len(pub.recs) != 1 {
		t.Fatalf("published %d enriched events, want 1 (redelivery must be skipped)", len(pub.recs))
	}
	var out event.Event
	_ = json.Unmarshal(pub.recs[0].Value, &out)
	if out.ContactID != 1 || !out.NewContact || out.Profile["city"] != "Delhi" || !reflect.DeepEqual(out.ChangedAttrs, []string{"city", "date_added", "date_identified", "date_modified", "first_seen", "last_seen"}) || out.Profile["last_seen"] != "2026-11-10" {
		t.Errorf("enriched = %+v", out)
	}
	if string(pub.recs[0].Key) != "1:1" || pub.recs[0].Topic != event.TopicEnriched {
		t.Errorf("record key/topic = %s/%s", pub.recs[0].Key, pub.recs[0].Topic)
	}
	n, err := segment.NewRedisCounter(rdb, 1, 1, ts).Count(ctx, "purchase", segment.Window{Days: 1})
	if err != nil || n != 1 {
		t.Errorf("purchase count = %d (err %v), want 1", n, err)
	}
}

func TestMergeConsentEvents(t *testing.T) {
	cur := map[string]any{"city": "Pune"}
	merged, changed := Merge(cur, &event.Event{Name: "unsubscribed", Props: map[string]any{"channel": "whatsapp"}})
	if merged["optout_whatsapp"] != true || !reflect.DeepEqual(changed, []string{"optout_whatsapp"}) {
		t.Errorf("unsubscribe: %v %v", merged, changed)
	}
	merged, _ = Merge(merged, &event.Event{Name: "resubscribed", Props: map[string]any{"channel": "whatsapp"}})
	if merged["optout_whatsapp"] != false || merged["city"] != "Pune" {
		t.Errorf("resubscribe: %v", merged)
	}
}

func TestTagsAndPoints(t *testing.T) {
	cur := map[string]any{"tags": []any{"beta"}, "points": 5.0}
	m, changed := Merge(cur, &event.Event{Name: event.TagAdded, Props: map[string]any{"tags": []any{"vip", "bad tag", "beta"}}})
	if !reflect.DeepEqual(m["tags"], []any{"beta", "vip"}) || !reflect.DeepEqual(changed, []string{"tags"}) {
		t.Errorf("tag_added: tags %v changed %v", m["tags"], changed)
	}
	m, _ = Merge(m, &event.Event{Name: event.TagRemoved, Props: map[string]any{"tag": "beta"}})
	if !reflect.DeepEqual(m["tags"], []any{"vip"}) {
		t.Errorf("tag_removed: %v", m["tags"])
	}
	m, _ = Merge(m, &event.Event{Name: event.PointsChanged, Props: map[string]any{"delta": -2}})
	if m["points"] != 3.0 {
		t.Errorf("points_changed: %v", m["points"])
	}
	m, changed = MergeScored(m, &event.Event{Name: "purchase"}, 10)
	if m["points"] != 13.0 || !reflect.DeepEqual(changed, []string{"points"}) {
		t.Errorf("scoring: %v %v", m["points"], changed)
	}
	if m, _ := Merge(m, &event.Event{Attrs: map[string]any{"merged_into": "x", "tags": "b;a,a"}}); m["merged_into"] != nil || !reflect.DeepEqual(m["tags"], []any{"a", "b"}) {
		t.Errorf("attrs: merged_into must be ignored and tags normalized: %v", m)
	}
}

// identify with an anonymous_id folds the visitor into the known contact, moves its counters,
// marks it merged, and later events for the visitor land on the known contact.
func TestIdentityMerge(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, pub := &memStore{contacts: map[string]model.Contact{}, points: map[int64]map[string]int{1: {"view": 1}}}, &memPub{}
	svc := New(store, rdb, pub)
	ctx := context.Background()
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	send := func(evs ...event.Event) []event.Event {
		var recs []*kgo.Record
		for _, e := range evs {
			b, _ := json.Marshal(e)
			recs = append(recs, &kgo.Record{Value: b})
		}
		before := len(pub.recs)
		if err := svc.HandleBatch(ctx, recs); err != nil {
			t.Fatal(err)
		}
		var out []event.Event
		for _, r := range pub.recs[before:] {
			var e event.Event
			_ = json.Unmarshal(r.Value, &e)
			out = append(out, e)
		}
		return out
	}
	send(event.Event{ID: "a1", TenantID: 1, ExternalID: "anon-x", Name: "view", Ts: ts, Attrs: map[string]any{"utm": "ad", "city": "Goa"}},
		event.Event{ID: "a2", TenantID: 1, ExternalID: "anon-x", Name: "view", Ts: ts})
	send(event.Event{ID: "k1", TenantID: 1, ExternalID: "u1", Name: "identify", Ts: ts, Attrs: map[string]any{"city": "Pune"}})
	out := send(event.Event{ID: "k2", TenantID: 1, ExternalID: "u1", Name: "identify", Ts: ts, Props: map[string]any{"anonymous_id": "anon-x"}})

	if len(out) != 3 || out[1].Name != event.ContactMerged || out[2].Name != event.ContactMergedInto {
		t.Fatalf("published %+v", out)
	}
	known, anon := store.contacts["u1"], store.contacts["anon-x"]
	if known.Attrs["city"] != "Pune" || known.Attrs["utm"] != "ad" || known.Attrs["points"] != 2.0 {
		t.Errorf("known profile = %v: known values win, visitor's fill in, points add up", known.Attrs)
	}
	if anon.Attrs["merged_into"] != "u1" || !out[1].NewContact {
		t.Errorf("visitor = %v; merged event should re-evaluate every segment", anon.Attrs)
	}
	if n, _ := segment.NewRedisCounter(rdb, 1, known.ID, ts).Count(ctx, "view", segment.Window{Days: 1}); n != 2 {
		t.Errorf("known contact's view count = %d, want the visitor's 2", n)
	}
	if mr.Exists(segment.CounterKey(1, anon.ID, "view")) {
		t.Error("visitor's counters should be gone")
	}
	late := send(event.Event{ID: "a3", TenantID: 1, ExternalID: "anon-x", Name: "view", Ts: ts})
	if late[0].ContactID != known.ID || late[0].ExternalID != "u1" {
		t.Errorf("late visitor event went to %d/%s, want the known contact", late[0].ContactID, late[0].ExternalID)
	}
	if again := send(event.Event{ID: "k3", TenantID: 1, ExternalID: "u1", Name: "identify", Ts: ts, Props: map[string]any{"anonymous_id": "anon-x"}}); len(again) != 1 {
		t.Errorf("a second identify must not merge again: %+v", again)
	}
}

// An identify that arrives before the visitor's own first events (another partition) still merges:
// the visitor is created already merged, and its events then follow it to the known contact.
func TestIdentifyBeforeVisitorEvents(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, pub := &memStore{contacts: map[string]model.Contact{}, points: map[int64]map[string]int{}}, &memPub{}
	svc := New(store, rdb, pub)
	ctx := context.Background()
	ts := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	send := func(e event.Event) []event.Event {
		b, _ := json.Marshal(e)
		before := len(pub.recs)
		if err := svc.HandleBatch(ctx, []*kgo.Record{{Value: b}}); err != nil {
			t.Fatal(err)
		}
		var out []event.Event
		for _, r := range pub.recs[before:] {
			var e event.Event
			_ = json.Unmarshal(r.Value, &e)
			out = append(out, e)
		}
		return out
	}
	send(event.Event{ID: "k1", TenantID: 1, ExternalID: "u1", Name: "identify", Ts: ts, Props: map[string]any{"anonymous_id": "anon-q"}})
	if store.contacts["anon-q"].Attrs["merged_into"] != "u1" {
		t.Fatalf("visitor = %+v, want it created merged into u1", store.contacts["anon-q"])
	}
	late := send(event.Event{ID: "a1", TenantID: 1, ExternalID: "anon-q", Name: "page_view", Ts: ts})
	if len(late) == 0 || late[0].ExternalID != "u1" || late[0].ContactID != store.contacts["u1"].ID {
		t.Errorf("the visitor's first page view went to %+v, want u1", late)
	}
	// Only anonymous visitors are created this way; a server can't conjure a merged contact.
	send(event.Event{ID: "k2", TenantID: 1, ExternalID: "u1", Name: "identify", Ts: ts, Props: map[string]any{"anonymous_id": "cust-9"}})
	if _, ok := store.contacts["cust-9"]; ok {
		t.Error("a non-anonymous id was created by an identify")
	}
}

// A rule saved while the service runs applies to the next events once the version key changes.
func TestScoringReloadsOnVersionBump(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := &memStore{contacts: map[string]model.Contact{}, points: map[int64]map[string]int{}}
	svc := New(store, rdb, &memPub{})
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	ctx := context.Background()
	if svc.scoring(ctx)[1]["demo"] != 0 {
		t.Fatal("no rules yet")
	}
	store.points = map[int64]map[string]int{1: {"demo": 40}}
	clock = clock.Add(2 * time.Second)
	if svc.scoring(ctx)[1]["demo"] != 0 {
		t.Fatal("without a bump the cached rules stay")
	}
	rdb.Incr(ctx, ScoringVersionKey)
	clock = clock.Add(2 * time.Second)
	if got := svc.scoring(ctx)[1]["demo"]; got != 40 {
		t.Errorf("after a bump: %d points, want 40", got)
	}
}
