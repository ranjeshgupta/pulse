//go:build integration

package segment_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"

	"pulse/internal/event"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/migrations"
)

// TestSQLAgreesWithEval is the contract between the streaming and batch paths: for every rule, the
// contacts ClickHouse returns must be exactly the contacts Eval accepts. Fixtures and rules are
// random (seeded) over a small value domain so edge cases (missing keys, non-numeric strings,
// zero counts, window boundaries, duplicate event rows) come up often.
func TestSQLAgreesWithEval(t *testing.T) {
	addr := os.Getenv("PULSE_CH_ADDR")
	if addr == "" {
		t.Skip("PULSE_CH_ADDR not set")
	}
	ctx := context.Background()
	store, err := ch.Open(ctx, addr, "pulse", "pulse", "pulse")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx, migrations.ClickHouse, "clickhouse"); err != nil {
		t.Fatal(err)
	}

	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	tenant := int64(1_000_000 + rng.Intn(1_000_000_000)) // isolated from other runs
	asOf := time.Date(2026, 11, 10, 18, 30, 0, 0, time.UTC)
	segment.Today = func() time.Time { return asOf } // relative date rules compare with the same day as the SQL
	defer func() { segment.Today = func() time.Time { return time.Now().UTC() } }()

	contacts, events := fixtures(rng, tenant, asOf, 300)
	// Segments 1 and 2 hold random members; later rows (higher versions) flip some back out, so
	// the query must read the latest state with FINAL.
	inSeg := map[int64]map[uint32]bool{1: {}, 2: {}}
	var members []ch.MemberRow
	for _, c := range contacts {
		for seg := range inSeg {
			if rng.Intn(2) == 0 {
				continue
			}
			members = append(members, ch.MemberRow{TenantID: tenant, SegmentID: seg, ContactID: c.ID, Member: true, Version: 1})
			in := rng.Intn(4) > 0
			if !in {
				members = append(members, ch.MemberRow{TenantID: tenant, SegmentID: seg, ContactID: c.ID, Member: false, Version: 2})
			}
			inSeg[seg][c.ID] = in
		}
	}
	if err := store.InsertMembers(ctx, members); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertContacts(ctx, contacts); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	// Redelivered rows: same ids again. Counts must not change.
	if err := store.InsertEvents(ctx, events[:len(events)/3]); err != nil {
		t.Fatal(err)
	}

	rules := append(fixedRules(t), randomRules(rng, asOf, 300)...)
	// Filtered event rules read their own counters, which the profile service increments only for
	// events whose properties match (segment.MatchProps).
	filters := map[string]segment.Filter{}
	for _, r := range rules {
		for _, f := range r.Filters() {
			filters[f.Key] = f
		}
	}

	// buckets[contact][counter][day] counts unique event ids, like the Redis counters.
	buckets := map[uint32]map[string]map[string]string{}
	bump := func(contact uint32, counter, day string) {
		if buckets[contact] == nil {
			buckets[contact] = map[string]map[string]string{}
		}
		if buckets[contact][counter] == nil {
			buckets[contact][counter] = map[string]string{}
		}
		n := 0
		fmt.Sscan(buckets[contact][counter][day], &n)
		buckets[contact][counter][day] = fmt.Sprint(n + 1)
	}
	seen := map[string]bool{}
	for _, e := range events {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		day := e.Ts.UTC().Format("20060102")
		bump(e.ContactID, e.Name, day)
		for _, f := range filters {
			if f.Event == e.Name && segment.MatchProps(f.Where, e.Props) {
				bump(e.ContactID, f.Key, day)
			}
		}
	}

	for i, rule := range rules {
		q, err := segment.ToSQL(rule, tenant, asOf)
		if err != nil {
			t.Fatalf("rule %d: %v", i, err)
		}
		got, err := store.SegmentIDs(ctx, q)
		if err != nil {
			t.Fatalf("rule %d: %v\n%s", i, err, q.SQL)
		}
		var want []uint32
		for _, c := range contacts {
			ok, err := segment.Eval(ctx, rule, c.Attrs, bucketCounter{buckets[c.ID], asOf, c.ID, inSeg})
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				want = append(want, c.ID)
			}
		}
		sort.Slice(want, func(a, b int) bool { return want[a] < want[b] })
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("rule %d %+v disagrees:\n SQL  %v\n Eval %v\n%s %v", i, rule, got, want, q.SQL, q.Args)
		}
	}
	t.Logf("%d rules agree over %d contacts and %d events", len(rules), len(contacts), len(events))
}

type bucketCounter struct {
	byEvent map[string]map[string]string
	asOf    time.Time
	contact uint32
	inSeg   map[int64]map[uint32]bool
}

func (b bucketCounter) Count(_ context.Context, ev string, days int) (int, error) {
	return segment.SumWindow(b.byEvent[ev], b.asOf, days), nil
}

func (b bucketCounter) InSegment(_ context.Context, seg int64) (bool, error) {
	return b.inSeg[seg][b.contact], nil
}

var (
	cities = []any{"Delhi", "Pune", "delhi", ""}
	ages   = []any{17.0, 18.0, 34.5, "40", "n/a", true}
	plans  = []any{"free", "pro", "team"}
	names  = []string{"purchase", "login", "view"}
	tagSet = []string{"vip", "beta", "churn"}
	skus   = []any{"A1", "B2", 7.0, ""}
	amts   = []any{0.0, 99.5, 250.0, "300", "n/a", true}
)

func fixtures(rng *rand.Rand, tenant int64, asOf time.Time, n int) ([]ch.ContactSnapshot, []event.Event) {
	var cs []ch.ContactSnapshot
	var evs []event.Event
	for id := uint32(1); id <= uint32(n); id++ {
		attrs := map[string]any{}
		for key, domain := range map[string][]any{"city": cities, "age": ages, "plan": plans, "vip": {true, false}} {
			if rng.Intn(4) > 0 { // 25% of keys missing
				attrs[key] = domain[rng.Intn(len(domain))]
			}
		}
		if rng.Intn(4) > 0 { // dates: plain, RFC 3339, and junk
			d := asOf.AddDate(0, 0, -rng.Intn(60)).Add(time.Duration(rng.Intn(48)-24) * time.Hour)
			attrs["seen"] = []any{d.Format(time.DateOnly), d.Format(time.RFC3339), "soon", ""}[rng.Intn(4)]
		}
		if rng.Intn(3) > 0 {
			tags := []any{}
			for _, tg := range tagSet {
				if rng.Intn(2) == 0 {
					tags = append(tags, tg)
				}
			}
			attrs["tags"] = tags
		}
		cs = append(cs, ch.ContactSnapshot{TenantID: tenant, ID: id, ExternalID: fmt.Sprint("u", id), Attrs: attrs, Version: 1})
		for k := rng.Intn(6); k > 0; k-- {
			// From 12 days ago to 1 day in the future, so windows and their edges are exercised.
			ts := asOf.Add(-time.Duration(rng.Int63n(int64(13 * 24 * time.Hour)))).Add(24 * time.Hour)
			props := map[string]any{}
			if rng.Intn(4) > 0 {
				props["sku"] = skus[rng.Intn(len(skus))]
			}
			if rng.Intn(4) > 0 {
				props["amount"] = amts[rng.Intn(len(amts))]
			}
			if rng.Intn(5) == 0 {
				props["coupon"] = nil
			}
			evs = append(evs, event.Event{
				ID: event.NewID(), TenantID: tenant, ContactID: id, Name: names[rng.Intn(len(names))], Ts: ts.UTC(), Props: props,
			})
		}
	}
	return cs, evs
}

func fixedRules(t *testing.T) []segment.Rule {
	var out []segment.Rule
	for _, js := range []string{
		`{"attr":"city","op":"eq","value":"Delhi"}`,
		`{"attr":"city","op":"ne","value":"Delhi"}`,
		`{"attr":"city","op":"eq","value":""}`,
		`{"attr":"city","op":"exists"}`,
		`{"attr":"age","op":"gte","value":18}`,
		`{"attr":"age","op":"lt","value":40}`,
		`{"attr":"age","op":"eq","value":"40"}`,
		`{"attr":"vip","op":"eq","value":true}`,
		`{"attr":"plan","op":"in","value":["pro","team"]}`,
		`{"attr":"city","op":"contains","value":"elh"}`,
		`{"event":"purchase","op":"gte","count":1,"days":7}`,
		`{"event":"login","op":"eq","count":0,"days":30}`,
		`{"event":"view","op":"lt","count":2,"days":3}`,
		`{"event":"purchase","op":"ne","count":1,"days":1}`,
		`{"all":[{"attr":"city","op":"eq","value":"Delhi"},{"event":"purchase","op":"gte","count":1,"days":7}]}`,
		`{"any":[{"attr":"plan","op":"eq","value":"pro"},{"event":"login","op":"eq","count":0,"days":10}]}`,
		`{"tag":"vip","op":"has"}`,
		`{"tag":"vip","op":"not_has"}`,
		`{"segment":1,"op":"in"}`,
		`{"segment":2,"op":"not_in"}`,
		`{"event":"purchase","op":"gte","count":1,"days":14,"where":[{"prop":"sku","op":"eq","value":"A1"}]}`,
		`{"event":"purchase","op":"gte","count":1,"days":14,"where":[{"prop":"sku","op":"eq","value":7}]}`,
		`{"event":"view","op":"eq","count":0,"days":14,"where":[{"prop":"amount","op":"gte","value":100}]}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"amount","op":"eq","value":true}]}`,
		`{"event":"login","op":"gte","count":1,"days":14,"where":[{"prop":"coupon","op":"exists"}]}`,
		`{"event":"login","op":"gte","count":1,"days":14,"where":[{"prop":"sku","op":"in","value":["A1","",7]},{"prop":"amount","op":"lt","value":260}]}`,
	} {
		r, err := segment.Parse([]byte(js))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func randomRules(rng *rand.Rand, asOf time.Time, n int) []segment.Rule {
	ops := []string{"eq", "ne", "gt", "gte", "lt", "lte"}
	var leaf func() segment.Rule
	leaf = func() segment.Rule {
		if rng.Intn(2) == 0 {
			c := rng.Intn(4)
			return segment.Rule{Event: names[rng.Intn(len(names))], Op: ops[rng.Intn(len(ops))], Count: &c, Days: 1 + rng.Intn(14)}
		}
		switch k := rng.Intn(11); k {
		case 6:
			return segment.Rule{Tag: tagSet[rng.Intn(len(tagSet))], Op: []string{"has", "not_has"}[rng.Intn(2)]}
		case 7:
			return segment.Rule{Segment: int64(1 + rng.Intn(2)), Op: []string{"in", "not_in"}[rng.Intn(2)]}
		case 8:
			c := rng.Intn(3)
			f := []segment.PropFilter{
				{Prop: "sku", Op: []string{"eq", "ne"}[rng.Intn(2)], Value: skus[rng.Intn(len(skus))]},
				{Prop: "amount", Op: ops[2+rng.Intn(4)], Value: []float64{0, 99.5, 250, 300}[rng.Intn(4)]},
				{Prop: []string{"sku", "amount", "coupon", "nope"}[rng.Intn(4)], Op: "exists"},
			}[rng.Intn(3)]
			return segment.Rule{Event: names[rng.Intn(len(names))], Op: ops[rng.Intn(len(ops))], Count: &c, Days: 1 + rng.Intn(14), Where: []segment.PropFilter{f}}
		case 0:
			return segment.Rule{Attr: "city", Op: []string{"eq", "ne"}[rng.Intn(2)], Value: cities[rng.Intn(len(cities))]}
		case 1:
			return segment.Rule{Attr: "age", Op: ops[2+rng.Intn(4)], Value: []float64{0, 17, 18, 34.5, 40}[rng.Intn(5)]}
		case 2:
			return segment.Rule{Attr: "plan", Op: "in", Value: []any{plans[rng.Intn(3)], plans[rng.Intn(3)]}}
		case 3:
			return segment.Rule{Attr: []string{"city", "age", "plan", "vip", "nope"}[rng.Intn(5)], Op: "exists"}
		case 4:
			return segment.Rule{Attr: "vip", Op: "eq", Value: rng.Intn(2) == 0}
		case 9:
			return segment.Rule{Attr: "seen", Op: []string{"within_days", "older_than_days"}[rng.Intn(2)], Value: float64(1 + rng.Intn(45))}
		case 10:
			d := asOf.AddDate(0, 0, -rng.Intn(60)).Format(time.DateOnly)
			return segment.Rule{Attr: "seen", Op: []string{"before", "after"}[rng.Intn(2)], Value: d}
		default:
			return segment.Rule{Attr: "city", Op: "contains", Value: []string{"D", "un", ""}[rng.Intn(3)]}
		}
	}
	var gen func(depth int) segment.Rule
	gen = func(depth int) segment.Rule {
		if depth >= 3 || rng.Intn(3) == 0 {
			return leaf()
		}
		kids := make([]segment.Rule, 1+rng.Intn(3))
		for i := range kids {
			kids[i] = gen(depth + 1)
		}
		if rng.Intn(2) == 0 {
			return segment.Rule{All: kids}
		}
		return segment.Rule{Any: kids}
	}
	out := make([]segment.Rule, n)
	for i := range out {
		out[i] = gen(0)
	}
	return out
}
