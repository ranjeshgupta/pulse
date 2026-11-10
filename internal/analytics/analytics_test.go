package analytics

import (
	"strings"
	"testing"
	"time"

	"pulse/internal/segment"
)

var (
	now  = time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC) // a Wednesday
	from = now.AddDate(0, 0, -28)
)

func placeholders(t *testing.T, q Query) {
	t.Helper()
	if n := strings.Count(q.SQL, "?"); n != len(q.Args) {
		t.Fatalf("%d placeholders, %d args:\n%s\n%#v", n, len(q.Args), q.SQL, q.Args)
	}
}

func TestPeriods(t *testing.T) {
	if got := Week.Start(now); got != time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) {
		t.Errorf("week start = %v, want Monday 9 Nov", got)
	}
	if got := Week.Start(time.Date(2026, 11, 8, 23, 0, 0, 0, time.UTC)); got.Day() != 2 {
		t.Errorf("Sunday's week starts %v, want 2 Nov", got)
	}
	if got := Month.Start(now); got != time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("month start = %v", got)
	}
	if n := len(Week.Buckets(from, now)); n != 5 {
		t.Errorf("%d weekly buckets in 4 weeks from a Wednesday, want 5", n)
	}
}

func TestCohortQueries(t *testing.T) {
	where := []segment.PropFilter{{Prop: "plan", Op: "in", Value: []any{"pro", "team"}}}
	specs := []CohortSpec{
		{By: CohortByFirstSeen, Period: Week, Periods: 4},
		{By: CohortByFirstSeen, Period: Day, Periods: 7, Return: CohortReturn{Event: "purchase", Where: where}, SegmentID: 9},
		{By: CohortByEvent, Event: "signup", Where: where, Period: Month, Periods: 3, SegmentID: 2},
	}
	for _, c := range specs {
		sizes, cells, err := CohortQueries(7, c, from, now)
		if err != nil {
			t.Fatal(err)
		}
		placeholders(t, sizes)
		placeholders(t, cells)
		if !strings.Contains(cells.SQL, "INNER JOIN") || sizes.Args[0] != uint64(7) {
			t.Errorf("cells = %s", cells.SQL)
		}
		if c.Return.Event == "" && !strings.Contains(cells.SQL, "NOT match(name") {
			t.Error("any activity must leave out Pulse's own events")
		}
	}
	bad := []CohortSpec{
		{By: "first_seen", Period: "year", Periods: 2},
		{By: "first_seen", Period: Week, Periods: 0},
		{By: "first_seen", Period: Week, Periods: 27},
		{By: "event", Event: "signup'; DROP TABLE events --", Period: Week, Periods: 2},
		{By: "event", Event: "signup", Where: []segment.PropFilter{{Prop: "a') OR 1=1 --", Op: "exists"}}, Period: Week, Periods: 2},
		{By: "first_seen", Period: Week, Periods: 2, Return: CohortReturn{Event: "x y"}},
		{By: "first_seen", Period: Week, Periods: 2, Return: CohortReturn{Where: []segment.PropFilter{{Prop: "a", Op: "exists"}}}},
	}
	for i, c := range bad {
		if _, _, err := CohortQueries(7, c, from, now); err == nil {
			t.Errorf("bad spec %d accepted: %+v", i, c)
		}
	}
	if _, _, err := CohortQueries(7, specs[0], now.AddDate(-2, 0, 0), now); err == nil {
		t.Error("a two-year range must be refused")
	}
	if _, _, err := CohortQueries(7, CohortSpec{By: "first_seen", Period: Day, Periods: 3}, now.AddDate(0, 0, -90), now); err == nil {
		t.Error("90 daily cohorts is over the limit")
	}
}

func TestCohortTable(t *testing.T) {
	c := CohortSpec{By: CohortByFirstSeen, Period: Week, Periods: 3}
	w1 := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	w2, w3 := w1.AddDate(0, 0, 7), w1.AddDate(0, 0, 14)
	sizes := map[int64]uint64{w1.Unix(): 10, w2.Unix(): 4}
	cells := []CohortCell{{w1, 0, 10}, {w1, 1, 6}, {w1, 2, 3}, {w2, 0, 4}, {w2, 1, 1}}
	rows := CohortTable(c, w1, now, now, sizes, cells)
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3 (the empty week too)", len(rows))
	}
	got := func(r CohortRow) []any {
		out := []any{}
		for _, p := range r.Returned {
			if p == nil {
				out = append(out, nil)
			} else {
				out = append(out, *p)
			}
		}
		return out
	}
	// now is Wed 11 Nov: weeks starting 26 Oct, 2 Nov and 9 Nov have begun.
	if g := got(rows[0]); g[0] != uint64(10) || g[1] != uint64(6) || g[2] != uint64(3) || g[3] != nil {
		t.Errorf("week 1 = %v", g)
	}
	if g := got(rows[1]); g[1] != uint64(1) || g[2] != nil {
		t.Errorf("week 2 = %v", g)
	}
	if rows[2].Start != w3 || rows[2].Size != 0 || got(rows[2])[0] != uint64(0) {
		t.Errorf("week 3 = %+v", rows[2])
	}
}

// Attribution fixture: one contact's touches before two purchases.
//
//	day 1 broadcast 1 opened, day 3 journey 2 clicked, day 4 broadcast 1 clicked, day 5 buys 100;
//	day 20 buys 60 with the last touch (day 4) 16 days before: outside a 7-day window.
func attributionFixture(window time.Duration) []Purchase {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	b1, j2 := TouchKey{Kind: "broadcast", ID: 1}, TouchKey{Kind: "journey", ID: 2}
	touches := []Touch{{b1, day(1)}, {j2, day(3)}, {b1, day(4)}}
	var ps []Purchase
	for _, buy := range []struct {
		amount float64
		at     time.Time
	}{{100, day(5)}, {60, day(20)}} {
		if p, ok := Summarize(buy.amount, buy.at, window, touches); ok {
			ps = append(ps, p)
		}
	}
	return ps
}

func credits(cs []Credit) map[string][2]float64 {
	out := map[string][2]float64{}
	for _, c := range cs {
		out[keyString(c.TouchKey)] = [2]float64{c.Revenue, c.Conversions}
	}
	return out
}

func TestAttributionModels(t *testing.T) {
	week := 7 * 24 * time.Hour
	ps := attributionFixture(week)
	if len(ps) != 1 {
		t.Fatalf("%d attributed purchases, want 1 (the second is outside the window)", len(ps))
	}
	cases := map[string]map[string][2]float64{
		LastTouch:  {"broadcast|1": {100, 1}},
		FirstTouch: {"broadcast|1": {100, 1}}, // day 1 open
		Linear:     {"broadcast|1": {50, 0.5}, "journey|2": {50, 0.5}},
	}
	for model, want := range cases {
		got := credits(Attribute(model, ps))
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", model, got, want)
		}
		for k, w := range want {
			if got[k] != w {
				t.Errorf("%s %s = %v, want %v", model, k, got[k], w)
			}
		}
	}

	// A 2-day window only sees the day 3 and day 4 touches: first touch is now the journey.
	ps = attributionFixture(2 * 24 * time.Hour)
	if got := credits(Attribute(FirstTouch, ps)); got["journey|2"] != [2]float64{100, 1} {
		t.Errorf("first touch in 2 days = %v", got)
	}
	// 30 days: the second purchase counts too, all to broadcast 1 (last touch, day 4).
	ps = attributionFixture(30 * 24 * time.Hour)
	if got := credits(Attribute(LastTouch, ps)); got["broadcast|1"] != [2]float64{160, 2} {
		t.Errorf("last touch in 30 days = %v", got)
	}
	// Linear splits three ways evenly and still adds up.
	three := []Purchase{{Amount: 100, Keys: []TouchKey{{Kind: "utm", Campaign: "a"}, {Kind: "utm", Campaign: "b"}, {Kind: "utm", Campaign: "c"}, {Kind: "utm", Campaign: "a"}}}}
	sum, conv := 0.0, 0.0
	for _, c := range Attribute(Linear, three) {
		sum += c.Revenue
		conv += c.Conversions
	}
	if len(Attribute(Linear, three)) != 3 || sum < 99.98 || sum > 100.02 || conv < 0.998 || conv > 1.002 {
		t.Errorf("linear thirds: sum %v, conversions %v", sum, conv)
	}
}

func TestTouchKeys(t *testing.T) {
	for _, k := range []TouchKey{{Kind: "broadcast", ID: 12}, {Kind: "journey", ID: 3}, {Kind: "utm", Source: "google", Medium: "cpc", Campaign: "a|b:c"}} {
		got, err := ParseTouchKey(keyString(k))
		if err != nil || got != k {
			t.Errorf("round trip %+v = %+v, %v", k, got, err)
		}
	}
	for _, bad := range []string{"", "broadcast|x", "other|1", "utm|only"} {
		if _, err := ParseTouchKey(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestAttributionSQL(t *testing.T) {
	rs := DefaultRevenue()
	for _, fam := range []string{TouchMessages, TouchUTM} {
		q, err := AttributionSQL(4, rs, fam, 7, from, now)
		if err != nil {
			t.Fatal(err)
		}
		placeholders(t, q)
	}
	placeholders(t, RevenueTotalsSQL(4, rs, from, now))
	placeholders(t, RecipientRevenueSQL(4, rs, "tenant_id = ? AND source = ? AND source_id = ?", []any{uint64(4), "broadcast", uint64(2)}, "variant", 3))
	bad := []RevenueSettings{
		{Event: "purchase'--", AmountProp: "amount"},
		{Event: "purchase", AmountProp: "amount') OR 1=1"},
		{Event: "purchase", AmountProp: "amount", Currency: "rupees"},
	}
	for _, b := range bad {
		if _, err := AttributionSQL(4, b, TouchMessages, 7, from, now); err == nil {
			t.Errorf("settings %+v accepted", b)
		}
	}
	if _, err := AttributionSQL(4, rs, TouchMessages, 91, from, now); err == nil {
		t.Error("window over 90 days accepted")
	}
	if _, err := AttributionSQL(4, rs, "sql", 7, from, now); err == nil {
		t.Error("unknown family accepted")
	}
}

func TestReportSQL(t *testing.T) {
	where := []segment.PropFilter{{Prop: "amount", Op: "gte", Value: 100.0}, {Prop: "sku", Op: "exists"}}
	ok := []ReportSpec{
		{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupEvent}, Chart: ChartBars},
		{Source: SourceEvents, Metric: Metric{Type: MetricSum, Key: "amount"}, GroupBy: GroupBy{Type: GroupProp, Key: "sku"}, Events: []string{"purchase"}, Where: where, SegmentID: 3, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricAvg, Key: "amount"}, GroupBy: GroupBy{Type: GroupAttr, Key: "plan"}, Events: []string{"purchase", "refund"}, Chart: ChartBars},
		{Source: SourceEvents, Metric: Metric{Type: MetricUniqueContacts}, GroupBy: GroupBy{Type: GroupAttr, Key: "geo_country"}, Chart: ChartBars},
		{Source: SourceEvents, Metric: Metric{Type: MetricSum, Key: "amount"}, GroupBy: GroupBy{Type: GroupWeek}, Events: []string{"purchase"}, Chart: ChartLine},
		{Source: SourceMessages, Metric: Metric{Type: MetricUniqueContacts}, GroupBy: GroupBy{Type: GroupKind}, MessageSource: "broadcast", Channels: []string{"email"}, Kinds: []string{"sent", "opened"}, Chart: ChartBars},
		{Source: SourceMessages, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupDay}, Chart: ChartLine},
		{Source: SourceMessages, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupCampaign}, SegmentID: 4, Chart: ChartTable},
		{Source: SourceContacts, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupAttr, Key: "plan"}, Where: []segment.PropFilter{{Prop: "first_seen", Op: "within_days", Value: 30.0}}, Chart: ChartBars},
		{Source: SourceContacts, Metric: Metric{Type: MetricAvg, Key: "points"}, GroupBy: GroupBy{Type: GroupMonth}, Chart: ChartLine},
		{Source: SourceContacts, Metric: Metric{Type: MetricSum, Key: "points"}, GroupBy: GroupBy{Type: GroupNone}, SegmentID: 2, Chart: ChartTable},
	}
	for i, s := range ok {
		q, err := ReportSQL(5, s, from, now)
		if err != nil {
			t.Fatalf("spec %d: %v", i, err)
		}
		placeholders(t, q)
		if !strings.Contains(q.SQL, "LIMIT 100") {
			t.Errorf("spec %d is not bounded: %s", i, q.SQL)
		}
	}
	bad := []ReportSpec{
		{Source: "users; DROP TABLE x", Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupNone}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: "max(id)"}, GroupBy: GroupBy{Type: GroupNone}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricSum, Key: "amount) FROM system.users --"}, GroupBy: GroupBy{Type: GroupNone}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupProp, Key: "x'"}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: "id"}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupNone}, Events: []string{"a b"}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupNone}, Where: []segment.PropFilter{{Prop: "p", Op: "eq; --", Value: "x"}}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupEvent}, Chart: ChartLine},
		{Source: SourceMessages, Metric: Metric{Type: MetricSum, Key: "amount"}, GroupBy: GroupBy{Type: GroupNone}, Chart: ChartTable},
		{Source: SourceMessages, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupKind}, Kinds: []string{"sent' OR 1=1"}, Chart: ChartTable},
		{Source: SourceMessages, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupAttr, Key: "plan"}, Chart: ChartTable},
		{Source: SourceContacts, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupEvent}, Chart: ChartTable},
		{Source: SourceContacts, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupNone}, Channels: []string{"email"}, Chart: ChartTable},
		{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupNone}, Chart: "pie"},
	}
	for i, s := range bad {
		if _, err := ReportSQL(5, s, from, now); err == nil {
			t.Errorf("bad spec %d accepted: %+v", i, s)
		}
	}
	day := ReportSpec{Source: SourceEvents, Metric: Metric{Type: MetricCount}, GroupBy: GroupBy{Type: GroupDay}, Chart: ChartLine}
	if _, err := ReportSQL(5, day, now.AddDate(0, 0, -120), now); err == nil {
		t.Error("grouping 120 days by day must be refused")
	}
	if _, err := ReportSQL(5, ok[0], now.AddDate(0, 0, -400), now); err == nil {
		t.Error("a range over a year must be refused")
	}
}

func TestFillTime(t *testing.T) {
	rows := FillTime(Day, now.AddDate(0, 0, -2), now, []ReportRow{{"2026-11-10", 4}})
	if len(rows) != 3 || rows[0].Group != "2026-11-09" || rows[1].Value != 4 || rows[2].Value != 0 {
		t.Errorf("filled = %+v", rows)
	}
}

func TestSplitStats(t *testing.T) {
	q, err := SplitStatsSQL(SplitQuery{Tenant: 3, Journey: 9, Step: "ab", Metric: SplitClicked, Nodes: []string{"mailA", "mailB"}, Since: from})
	if err != nil {
		t.Fatal(err)
	}
	placeholders(t, q)
	q, err = SplitStatsSQL(SplitQuery{Tenant: 3, Journey: 9, Step: "ab", Metric: SplitConverted, Goal: "purchase", Since: from})
	if err != nil {
		t.Fatal(err)
	}
	placeholders(t, q)
	q, err = SplitStatsSQL(SplitQuery{Tenant: 3, Journey: 9, Step: "ab", Since: from})
	if err != nil || strings.Contains(q.SQL, "JOIN") {
		t.Fatalf("entrants only: %v %s", err, q.SQL)
	}
	placeholders(t, q)
	if _, err := SplitStatsSQL(SplitQuery{Metric: SplitOpened}); err == nil {
		t.Error("opened with no send steps accepted")
	}
	if _, err := SplitStatsSQL(SplitQuery{Metric: SplitConverted, Goal: "x'"}); err == nil {
		t.Error("bad goal accepted")
	}
}

func TestPickWinner(t *testing.T) {
	stats := []PathStat{{Path: 0, Entrants: 10, Hits: 2}, {Path: 1, Entrants: 12, Hits: 6}, {Path: 2, Entrants: 10, Hits: 5}}
	if w, ok := PickWinner(stats, 3, 10); !ok || w != 1 {
		t.Errorf("winner = %d %v, want path 1 (50%%)", w, ok)
	}
	if _, ok := PickWinner(stats, 3, 11); ok {
		t.Error("paths 0 and 2 have fewer than 11 entrants: no decision yet")
	}
	if _, ok := PickWinner(stats[:2], 3, 1); ok {
		t.Error("path 2 has no entrants: no decision yet")
	}
	// Equal rates: more hits wins; equal everything: the first path.
	tie := []PathStat{{Path: 0, Entrants: 10, Hits: 5}, {Path: 1, Entrants: 20, Hits: 10}}
	if w, _ := PickWinner(tie, 2, 1); w != 1 {
		t.Errorf("tie on rate: %d, want 1 (more hits)", w)
	}
	if w, _ := PickWinner([]PathStat{{Path: 0, Entrants: 4}, {Path: 1, Entrants: 4}}, 2, 1); w != 0 {
		t.Errorf("no hits anywhere: %d, want the first path", w)
	}
}

func TestCohortAverage(t *testing.T) {
	n := func(v uint64) *uint64 { return &v }
	rows := []CohortRow{{Size: 10, Returned: []*uint64{n(10), n(5), nil}}, {Size: 30, Returned: []*uint64{n(30), nil, nil}}, {Size: 0, Returned: []*uint64{n(0), n(0), n(0)}}}
	avg := CohortAverage(rows, 2)
	if *avg[0] != 1 || *avg[1] != 0.5 || avg[2] != nil {
		t.Errorf("average = %v %v %v", *avg[0], *avg[1], avg[2])
	}
}
