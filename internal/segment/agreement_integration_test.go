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

func (b bucketCounter) Count(_ context.Context, ev string, w segment.Window) (int, error) {
	start, end := w.Bounds(b.asOf)
	return segment.SumBetween(b.byEvent[ev], start, end), nil
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
	prods  = []any{"A1", "B2", "C3", 7.0}
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
		switch rng.Intn(5) { // a list field: a JSON array, a plain string, or missing
		case 0, 1, 2:
			list := []any{}
			for _, p := range prods {
				if rng.Intn(2) == 0 {
					list = append(list, p)
				}
			}
			attrs["products"] = list
		case 3:
			attrs["products"] = "A1"
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
			// From 12 days ago to 1 day in the future, so windows and their edges are exercised, and
			// some from 360-420 days ago for absolute ranges older than the counters.
			ts := asOf.Add(-time.Duration(rng.Int63n(int64(13 * 24 * time.Hour)))).Add(24 * time.Hour)
			if rng.Intn(6) == 0 {
				ts = asOf.AddDate(0, 0, -360-rng.Intn(60)).Add(time.Duration(rng.Intn(24)) * time.Hour)
			}
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
			if rng.Intn(3) == 0 { // a nested object
				product := map[string]any{}
				if rng.Intn(4) > 0 {
					product["code"] = skus[rng.Intn(len(skus))]
				}
				if rng.Intn(2) == 0 {
					product["price"] = amts[rng.Intn(len(amts))]
				}
				props["product"] = product
			}
			switch rng.Intn(4) { // a list of objects, an empty list, or not a list
			case 0, 1:
				items := []any{}
				for n := rng.Intn(4); n > 0; n-- {
					it := map[string]any{}
					if rng.Intn(4) > 0 {
						it["code"] = skus[rng.Intn(len(skus))]
					}
					if rng.Intn(2) == 0 {
						it["qty"] = float64(rng.Intn(4))
					}
					items = append(items, it)
				}
				props["items"] = items
			case 2:
				props["items"] = "A1"
			}
			if rng.Intn(3) == 0 {
				props["tags"] = []any{prods[rng.Intn(len(prods))], prods[rng.Intn(len(prods))]}
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
		`{"attr":"city","op":"not_contains","value":"elh"}`,
		`{"attr":"city","op":"starts_with","value":"D"}`,
		`{"attr":"city","op":"ends_with","value":"hi"}`,
		`{"attr":"plan","op":"not_in","value":["pro","team"]}`,
		`{"attr":"city","op":"not_exists"}`,
		`{"attr":"age","op":"between","value":[18,40]}`,
		`{"attr":"seen","op":"on","value":"2026-11-01"}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"coupon","op":"not_exists"}]}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"sku","op":"starts_with","value":"A"},{"prop":"amount","op":"between","value":[0,260]}]}`,
		`{"event":"purchase","op":"eq","count":0,"days":14,"where":[{"prop":"sku","op":"not_in","value":["A1",7]}]}`,
		`{"none":[{"attr":"city","op":"eq","value":"Delhi"},{"tag":"vip","op":"has"}]}`,
		`{"all":[{"attr":"plan","op":"exists"},{"none":[{"event":"purchase","op":"gte","count":1,"days":14}]}]}`,
		`{"attr":"products","op":"has","value":"A1"}`,
		`{"attr":"products","op":"not_has","value":"7"}`,
		`{"attr":"products","op":"has","value":"C3"}`,
		`{"attr":"seen","op":"between","value":["2026-10-01","2026-10-31"]}`,
		`{"event":"purchase","op":"gte","count":1,"from":"2026-11-01","to":"2026-11-05"}`,
		`{"event":"view","op":"eq","count":0,"from":"2026-11-08"}`,
		`{"event":"login","op":"gte","count":1,"from":"2025-09-01","to":"2025-11-30"}`,
		`{"event":"purchase","op":"lt","count":2,"from":"2025-10-15"}`,
		`{"event":"purchase","op":"gte","count":1,"from":"2026-12-01"}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"product.code","op":"eq","value":"A1"}]}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"product.price","op":"gt","value":100}]}`,
		`{"event":"login","op":"gte","count":1,"days":14,"where":[{"prop":"product.code","op":"not_exists"}]}`,
		`{"event":"purchase","op":"gte","count":1,"days":14,"where":[{"prop":"items[].code","op":"eq","value":"B2"}]}`,
		`{"event":"purchase","op":"eq","count":0,"days":14,"where":[{"prop":"items[].code","op":"ne","value":"A1"}]}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"items[].qty","op":"gte","value":2}]}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"items[].code","op":"not_exists"}]}`,
		`{"event":"login","op":"gte","count":1,"days":14,"where":[{"prop":"items[].code","op":"exists"}]}`,
		`{"event":"login","op":"gte","count":1,"days":14,"where":[{"prop":"tags","op":"has","value":"B2"}]}`,
		`{"event":"view","op":"gte","count":1,"days":14,"where":[{"prop":"tags[]","op":"in","value":["C3",7]}]}`,
		`{"event":"purchase","op":"gte","count":1,"from":"2025-10-01","to":"2025-12-31","where":[{"prop":"items[].code","op":"in","value":["A1","B2"]}]}`,
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
		switch k := rng.Intn(17); k {
		case 14:
			return segment.Rule{Attr: "products", Op: []string{"has", "not_has"}[rng.Intn(2)], Value: []string{"A1", "B2", "C3", "7", "Z"}[rng.Intn(5)]}
		case 15:
			from := asOf.AddDate(0, 0, -rng.Intn(60))
			return segment.Rule{Attr: "seen", Op: "between", Value: []any{from.Format(time.DateOnly), from.AddDate(0, 0, rng.Intn(20)).Format(time.DateOnly)}}
		case 16:
			c := rng.Intn(3)
			// Absolute ranges: recent, or starting 330-430 days back (older than the counters).
			from := asOf.AddDate(0, 0, -rng.Intn(14))
			if rng.Intn(2) == 0 {
				from = asOf.AddDate(0, 0, -330-rng.Intn(100))
			}
			r := segment.Rule{Event: names[rng.Intn(len(names))], Op: ops[rng.Intn(len(ops))], Count: &c, From: from.Format(time.DateOnly)}
			if rng.Intn(2) == 0 {
				r.To = from.AddDate(0, 0, rng.Intn(60)).Format(time.DateOnly)
			}
			if rng.Intn(2) == 0 {
				r.Where = []segment.PropFilter{[]segment.PropFilter{
					{Prop: "items[].code", Op: []string{"eq", "ne", "in"}[rng.Intn(3)], Value: skus[rng.Intn(len(skus))]},
					{Prop: "product.code", Op: []string{"eq", "ne", "exists", "not_exists"}[rng.Intn(4)]},
					{Prop: "items[].qty", Op: ops[2+rng.Intn(4)], Value: float64(rng.Intn(4))},
					{Prop: "product.price", Op: "between", Value: []any{0.0, []float64{99.5, 250, 300}[rng.Intn(3)]}},
					{Prop: "tags", Op: []string{"has", "not_has"}[rng.Intn(2)], Value: []string{"A1", "B2", "7"}[rng.Intn(3)]},
				}[rng.Intn(5)]}
				f := &r.Where[0]
				switch f.Op {
				case "exists", "not_exists":
					f.Value = nil
				case "eq", "ne":
					if f.Value == nil {
						f.Value = skus[rng.Intn(len(skus))]
					}
				case "in":
					f.Value = []any{skus[rng.Intn(len(skus))], skus[rng.Intn(len(skus))]}
				}
			}
			return r
		case 11:
			return segment.Rule{Attr: []string{"city", "plan"}[rng.Intn(2)], Op: []string{"not_contains", "starts_with", "ends_with"}[rng.Intn(3)], Value: []string{"D", "un", "", "pro", "i"}[rng.Intn(5)]}
		case 12:
			lo := []float64{0, 17, 18, 34.5}[rng.Intn(4)]
			if rng.Intn(2) == 0 {
				return segment.Rule{Attr: "age", Op: "between", Value: []any{lo, lo + float64(rng.Intn(30))}}
			}
			return segment.Rule{Attr: []string{"city", "age", "plan", "nope"}[rng.Intn(4)], Op: "not_exists"}
		case 13:
			if rng.Intn(2) == 0 {
				return segment.Rule{Attr: "seen", Op: "on", Value: asOf.AddDate(0, 0, -rng.Intn(60)).Format(time.DateOnly)}
			}
			c := rng.Intn(3)
			f := []segment.PropFilter{
				{Prop: "sku", Op: []string{"not_in", "in"}[rng.Intn(2)], Value: []any{skus[rng.Intn(len(skus))], skus[rng.Intn(len(skus))]}},
				{Prop: []string{"sku", "coupon", "amount"}[rng.Intn(3)], Op: "not_exists"},
				{Prop: "amount", Op: "between", Value: []any{0.0, []float64{99.5, 250, 300}[rng.Intn(3)]}},
				{Prop: "sku", Op: []string{"starts_with", "ends_with", "not_contains"}[rng.Intn(3)], Value: []string{"A", "2", ""}[rng.Intn(3)]},
			}[rng.Intn(4)]
			return segment.Rule{Event: names[rng.Intn(len(names))], Op: ops[rng.Intn(len(ops))], Count: &c, Days: 1 + rng.Intn(14), Where: []segment.PropFilter{f}}
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
		switch rng.Intn(5) {
		case 0, 1:
			return segment.Rule{All: kids}
		case 2, 3:
			return segment.Rule{Any: kids}
		}
		return segment.Rule{None: kids}
	}
	out := make([]segment.Rule, n)
	for i := range out {
		out[i] = gen(0)
	}
	return out
}
