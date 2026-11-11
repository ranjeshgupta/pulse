package segengine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
)

type memStore struct {
	segs     []model.Segment
	contacts []model.Contact
}

func (m *memStore) ContactsByIDs(_ context.Context, tenant int64, ids []uint32) ([]model.Contact, error) {
	var out []model.Contact
	for _, c := range m.contacts {
		for _, id := range ids {
			if c.TenantID == tenant && c.ID == id {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func (m *memStore) AllSegments(context.Context) ([]model.Segment, error) { return m.segs, nil }
func (m *memStore) ClaimSegmentWork(context.Context, time.Duration) (model.Segment, string, bool, error) {
	return model.Segment{}, "", false, nil
}
func (m *memStore) MarkSegmentReady(context.Context, int64) error          { return nil }
func (m *memStore) SetNextRefresh(context.Context, int64, time.Time) error { return nil }

type fixedIDs []uint32

func (f fixedIDs) SegmentIDs(context.Context, segment.Query) ([]uint32, error) { return f, nil }
func (f fixedIDs) DayCounts(context.Context, segment.Query) ([]ch.ContactDayCount, error) {
	return []ch.ContactDayCount{{ContactID: 5, Day: time.Now().UTC().Format("20060102"), Count: 3}}, nil
}

type memPub struct{ recs []*kgo.Record }

func (p *memPub) Publish(_ context.Context, recs ...*kgo.Record) error {
	p.recs = append(p.recs, recs...)
	return nil
}

func (p *memPub) changes(t *testing.T) []model.MembershipChange {
	var out []model.MembershipChange
	for _, r := range p.recs {
		var c model.MembershipChange
		if err := json.Unmarshal(r.Value, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	p.recs = nil
	return out
}

func setup(t *testing.T, segs ...model.Segment) (*Engine, *redis.Client, *memPub) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	pub := &memPub{}
	e := New(&memStore{segs: segs}, fixedIDs{2, 5}, rdb, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute)
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, rdb, pub
}

func enriched(t *testing.T, e event.Event) *kgo.Record {
	b, _ := json.Marshal(e)
	return &kgo.Record{Topic: event.TopicEnriched, Value: b}
}

var (
	delhi    = model.Segment{ID: 1, TenantID: 1, Rule: json.RawMessage(`{"attr":"city","op":"eq","value":"Delhi"}`)}
	inactive = model.Segment{ID: 2, TenantID: 1, Rule: json.RawMessage(`{"event":"login","op":"eq","count":0,"days":30}`)}
	other    = model.Segment{ID: 3, TenantID: 2, Rule: json.RawMessage(`{"attr":"city","op":"eq","value":"Delhi"}`)}
)

func TestCandidatesUseTheIndex(t *testing.T) {
	e, _, _ := setup(t, delhi, inactive, other)
	ids := func(cs []*compiled) []int64 {
		var out []int64
		for _, c := range cs {
			out = append(out, c.ID)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	cases := []struct {
		ev   event.Event
		want []int64
	}{
		{event.Event{TenantID: 1, Name: "page_view"}, nil},                                       // touches nothing
		{event.Event{TenantID: 1, Name: "login"}, []int64{2}},                                    // event ref
		{event.Event{TenantID: 1, Name: "identify", ChangedAttrs: []string{"city"}}, []int64{1}}, // attr ref
		{event.Event{TenantID: 1, Name: "page_view", NewContact: true}, []int64{1, 2}},           // new contact: all of the tenant
		{event.Event{TenantID: 9, Name: "login"}, nil},                                           // other tenant
	}
	for _, tc := range cases {
		if got := ids(e.candidates(&tc.ev)); !equal(got, tc.want) {
			t.Errorf("%+v: candidates %v, want %v", tc.ev, got, tc.want)
		}
	}
}

func TestStreamingEntersAndExits(t *testing.T) {
	e, rdb, pub := setup(t, delhi)
	ctx := context.Background()

	ev := event.Event{TenantID: 1, ContactID: 7, Name: "identify", NewContact: true, Profile: map[string]any{"city": "Delhi"}}
	if err := e.HandleEvent(ctx, enriched(t, ev)); err != nil {
		t.Fatal(err)
	}
	ch := pub.changes(t)
	if len(ch) != 1 || !ch[0].Entered || ch[0].ContactID != 7 || ch[0].SegmentID != 1 || ch[0].Source != "stream" {
		t.Fatalf("changes = %+v, want one stream entry", ch)
	}
	if bit, _ := rdb.GetBit(ctx, segment.MemberKey(1, 1), 7).Result(); bit != 1 {
		t.Error("membership bit not set")
	}

	// Same state again: no duplicate change.
	ev.NewContact, ev.ChangedAttrs = false, []string{"city"}
	_ = e.HandleEvent(ctx, enriched(t, ev))
	if ch := pub.changes(t); len(ch) != 0 {
		t.Errorf("unchanged membership published %+v", ch)
	}

	ev.Profile = map[string]any{"city": "Pune"}
	_ = e.HandleEvent(ctx, enriched(t, ev))
	if ch := pub.changes(t); len(ch) != 1 || ch[0].Entered {
		t.Errorf("changes = %+v, want one exit", ch)
	}
}

func TestHandleBatchOrderAndCounters(t *testing.T) {
	buyers := model.Segment{ID: 4, TenantID: 1, Rule: json.RawMessage(`{"event":"purchase","op":"gte","count":2,"days":7}`)}
	e, rdb, pub := setup(t, delhi, buyers)
	ctx := context.Background()
	// Contact 8 has two purchases today (as the profile stage would have counted them).
	for i := 0; i < 2; i++ {
		if err := segment.Incr(ctx, rdb, 1, 8, "purchase", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	recs := []*kgo.Record{
		enriched(t, event.Event{TenantID: 1, ContactID: 7, Name: "identify", ChangedAttrs: []string{"city"}, Profile: map[string]any{"city": "Delhi"}}),
		enriched(t, event.Event{TenantID: 1, ContactID: 8, Name: "purchase"}),
		enriched(t, event.Event{TenantID: 1, ContactID: 7, Name: "identify", ChangedAttrs: []string{"city"}, Profile: map[string]any{"city": "Pune"}}),
	}
	if err := e.HandleBatch(ctx, recs); err != nil {
		t.Fatal(err)
	}
	ch := pub.changes(t)
	if len(ch) != 3 {
		t.Fatalf("changes = %+v, want enter(7), enter(8 buyers), exit(7)", ch)
	}
	if !(ch[0].ContactID == 7 && ch[0].Entered) || !(ch[1].ContactID == 8 && ch[1].SegmentID == 4 && ch[1].Entered) || !(ch[2].ContactID == 7 && !ch[2].Entered) {
		t.Errorf("changes out of order or wrong: %+v", ch)
	}
	if bit, _ := rdb.GetBit(ctx, segment.MemberKey(1, 1), 7).Result(); bit != 0 {
		t.Error("final membership of contact 7 should be out")
	}
}

func TestRebuildPublishesDiff(t *testing.T) {
	e, rdb, pub := setup(t, delhi)
	ctx := context.Background()
	mem := segment.Membership{RDB: rdb}
	// Contacts 2 and 9 are members before the rebuild; the query returns 2 and 5.
	_, _ = mem.SetMember(ctx, 1, 1, 2, true)
	_, _ = mem.SetMember(ctx, 1, 1, 9, true)

	in, out, err := e.Rebuild(ctx, delhi, "sweep")
	if err != nil {
		t.Fatal(err)
	}
	if in != 1 || out != 1 {
		t.Errorf("entered %d exited %d, want 1 and 1", in, out)
	}
	ch := pub.changes(t)
	if len(ch) != 2 || ch[0].ContactID != 5 || !ch[0].Entered || ch[1].ContactID != 9 || ch[1].Entered || ch[0].Source != "sweep" {
		t.Errorf("changes = %+v", ch)
	}
	if n, _ := mem.Size(ctx, 1, 1); n != 2 {
		t.Errorf("size after rebuild = %d, want 2", n)
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A segment that references another is evaluated after it, in the same pass, so one event moves a
// contact through both.
func TestDependentSegmentsInOnePass(t *testing.T) {
	vipDelhi := model.Segment{ID: 10, TenantID: 1, Rule: json.RawMessage(`{"all":[{"segment":1,"op":"in"},{"tag":"vip","op":"has"}]}`)}
	notVipDelhi := model.Segment{ID: 11, TenantID: 1, Rule: json.RawMessage(`{"all":[{"segment":1,"op":"in"},{"segment":10,"op":"not_in"}]}`)}
	loop1 := model.Segment{ID: 20, TenantID: 1, Rule: json.RawMessage(`{"segment":21,"op":"in"}`)}
	loop2 := model.Segment{ID: 21, TenantID: 1, Rule: json.RawMessage(`{"segment":20,"op":"in"}`)}
	e, _, pub := setup(t, notVipDelhi, vipDelhi, delhi, loop1, loop2)
	ctx := context.Background()
	if n := len((*e.idx.Load())[1].all); n != 3 {
		t.Fatalf("index has %d segments, want 3 (the cycle is skipped)", n)
	}
	ev := event.Event{TenantID: 1, ContactID: 7, Name: "identify", ChangedAttrs: []string{"city"}, Profile: map[string]any{"city": "Delhi", "tags": []any{"vip"}}}
	if err := e.HandleEvent(ctx, enriched(t, ev)); err != nil {
		t.Fatal(err)
	}
	in := map[int64]bool{}
	for _, c := range pub.changes(t) {
		in[c.SegmentID] = c.Entered
	}
	if !in[1] || !in[10] || in[11] {
		t.Errorf("memberships %v, want Delhi and VIP Delhi, not the other", in)
	}
	// Merged away: out of everything, including static lists.
	ev.Name, ev.ChangedAttrs = event.ContactMergedInto, []string{"merged_into"}
	ev.Profile = map[string]any{"city": "Delhi", "tags": []any{"vip"}, "merged_into": "u1"}
	if err := e.HandleEvent(ctx, enriched(t, ev)); err != nil {
		t.Fatal(err)
	}
	for _, c := range pub.changes(t) {
		if c.Entered {
			t.Errorf("merged contact entered %d", c.SegmentID)
		}
	}
}

// Filtered counters: the engine counts only matching events, once, and seeds them on rebuild.
func TestFilteredCounters(t *testing.T) {
	bigBuyer := model.Segment{ID: 30, TenantID: 1, Rule: json.RawMessage(`{"event":"purchase","op":"gte","count":2,"days":7,"where":[{"prop":"amount","op":"gte","value":100}]}`)}
	e, rdb, pub := setup(t, bigBuyer)
	ctx := context.Background()
	now := time.Now().UTC()
	buy := func(id string, amount float64) *kgo.Record {
		return enriched(t, event.Event{ID: id, TenantID: 1, ContactID: 7, Name: "purchase", Ts: now, Props: map[string]any{"amount": amount}})
	}
	if err := e.HandleBatch(ctx, []*kgo.Record{buy("a", 150), buy("b", 20), buy("a", 150)}); err != nil {
		t.Fatal(err)
	}
	if c := pub.changes(t); len(c) != 0 {
		t.Fatalf("one big purchase (counted once) is not enough: %+v", c)
	}
	if err := e.HandleEvent(ctx, buy("c", 300)); err != nil {
		t.Fatal(err)
	}
	if c := pub.changes(t); len(c) != 1 || !c[0].Entered {
		t.Fatalf("second big purchase should enter: %+v", c)
	}
	if _, _, err := e.Rebuild(ctx, bigBuyer, "backfill"); err != nil {
		t.Fatal(err)
	}
	key := segment.CounterName("purchase", []segment.PropFilter{{Prop: "amount", Op: "gte", Value: 100.0}})
	if n, _ := segment.NewRedisCounter(rdb, 1, 5, now).Count(ctx, key, segment.Window{Days: 1}); n != 3 {
		t.Errorf("seeded count for contact 5 = %d, want 3 from ClickHouse", n)
	}
}

// A list edit (source manual) re-evaluates the segments built on that list.
func TestHandleMembershipFollowsLists(t *testing.T) {
	list := model.Segment{ID: 40, TenantID: 1, Kind: "static"}
	onList := model.Segment{ID: 41, TenantID: 1, Rule: json.RawMessage(`{"all":[{"segment":40,"op":"in"},{"attr":"city","op":"eq","value":"Delhi"}]}`)}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	pub := &memPub{}
	store := &memStore{segs: []model.Segment{list, onList}, contacts: []model.Contact{{TenantID: 1, ID: 7, Attrs: map[string]any{"city": "Delhi"}}}}
	e := New(store, fixedIDs{}, rdb, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute)
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// list_joined sets the list bit and evaluates its dependents in the same pass.
	join := enriched(t, event.Event{ID: "j", TenantID: 1, ContactID: 9, Name: event.ListJoined, Props: map[string]any{"list_id": 40},
		Profile: map[string]any{"city": "Delhi"}})
	if err := e.HandleEvent(ctx, join); err != nil {
		t.Fatal(err)
	}
	if c := pub.changes(t); len(c) != 2 || c[0].SegmentID != 40 || c[1].SegmentID != 41 || !c[1].Entered {
		t.Fatalf("list_joined changes = %+v", c)
	}
	_, _ = segment.Membership{RDB: rdb}.SetMember(ctx, 1, 40, 7, true)
	b, _ := json.Marshal(model.MembershipChange{TenantID: 1, SegmentID: 40, ContactID: 7, Entered: true, Source: "manual", At: time.Now()})
	if err := e.HandleMembership(ctx, []*kgo.Record{{Value: b}}); err != nil {
		t.Fatal(err)
	}
	c := pub.changes(t)
	if len(c) != 1 || c[0].SegmentID != 41 || !c[0].Entered || c[0].Source != SourceDependent {
		t.Fatalf("changes = %+v, want contact 7 to enter segment 41", c)
	}
	// Its own output is not followed again.
	b, _ = json.Marshal(c[0])
	if err := e.HandleMembership(ctx, []*kgo.Record{{Value: b}}); err != nil || len(pub.changes(t)) != 0 {
		t.Errorf("dependent changes must not loop (err %v)", err)
	}
}

// A list created after the last refresh still takes its first member.
func TestListJoinRefreshesTheIndex(t *testing.T) {
	store := &memStore{}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	pub := &memPub{}
	e := New(store, fixedIDs{}, rdb, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute)
	clock := time.Now()
	e.now = func() time.Time { return clock }
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.segs = []model.Segment{{ID: 50, TenantID: 1, Kind: "static"}}
	clock = clock.Add(2 * time.Second)
	join := enriched(t, event.Event{ID: "j", TenantID: 1, ContactID: 3, Name: event.ListJoined, Props: map[string]any{"list_id": 50}})
	if err := e.HandleEvent(context.Background(), join); err != nil {
		t.Fatal(err)
	}
	if c := pub.changes(t); len(c) != 1 || c[0].SegmentID != 50 || !c[0].Entered {
		t.Fatalf("changes = %+v, want the contact to join list 50", c)
	}
}

func TestDailyAndLongRangeSegmentsStayOutOfTheStream(t *testing.T) {
	daily := model.Segment{ID: 4, TenantID: 1, Rule: delhi.Rule, Refresh: &segment.Refresh{Mode: "daily", At: "06:00", TZ: "UTC"}}
	old := model.Segment{ID: 5, TenantID: 1, Rule: json.RawMessage(`{"event":"login","op":"gte","count":1,"from":"2020-01-01","to":"2020-12-31"}`)}
	recent := model.Segment{ID: 6, TenantID: 1, Rule: json.RawMessage(`{"event":"login","op":"gte","count":1,"from":"` +
		time.Now().UTC().AddDate(0, 0, -10).Format(time.DateOnly) + `"}`)}
	e, _, _ := setup(t, delhi, daily, old, recent)
	ev := event.Event{TenantID: 1, Name: "login", ChangedAttrs: []string{"city"}, NewContact: true}
	var got []int64
	for _, c := range e.candidates(&ev) {
		got = append(got, c.ID)
	}
	if !equal(got, []int64{1, 6}) {
		t.Errorf("streamed segments %v, want [1 6]", got)
	}
}
