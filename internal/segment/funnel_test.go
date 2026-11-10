package segment

import (
	"strings"
	"testing"
	"time"
)

func TestFunnelSQL(t *testing.T) {
	to := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	steps := []FunnelStep{
		{Event: "page_view", Where: []PropFilter{{Prop: "path", Op: "contains", Value: "/pricing"}}},
		{Event: "signup"},
		{Event: "purchase", Where: []PropFilter{{Prop: "amount", Op: "gte", Value: 500.0}}},
	}
	q, err := FunnelSQL(7, steps, 7, to.AddDate(0, 0, -30), to, 12)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(q.SQL, "?"); n != len(q.Args) {
		t.Fatalf("%d placeholders, %d args:\n%s", n, len(q.Args), q.SQL)
	}
	if !strings.Contains(q.SQL, "windowFunnel(604800)") || !strings.Contains(q.SQL, "segment_members") {
		t.Errorf("sql = %s", q.SQL)
	}
	if _, err := FunnelSQL(7, steps[:1], 7, to.AddDate(0, 0, -1), to, 0); err == nil {
		t.Error("one step is not a funnel")
	}
	if _, err := FunnelSQL(7, steps, 0, to.AddDate(0, 0, -1), to, 0); err == nil {
		t.Error("window must be at least a day")
	}
	if _, err := FunnelSQL(7, []FunnelStep{{Event: "a"}, {Event: "b; DROP"}}, 1, to.AddDate(0, 0, -1), to, 0); err == nil {
		t.Error("invalid event names must be refused")
	}
}

func TestFunnelBreakdownSQL(t *testing.T) {
	to := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	steps := []FunnelStep{{Event: "signup"}, {Event: "purchase", Where: []PropFilter{{Prop: "amount", Op: "gt", Value: 10.0}}}}
	q, err := FunnelBreakdownSQL(7, steps, 7, to.AddDate(0, 0, -30), to, 3, "geo_country")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(q.SQL, "?"); n != len(q.Args) {
		t.Fatalf("%d placeholders, %d args:\n%s", n, len(q.Args), q.SQL)
	}
	if q.Args[0] != "geo_country" || !strings.Contains(q.SQL, "countIf(f.level >= 2)") || !strings.Contains(q.SQL, "LIMIT 10") {
		t.Errorf("sql = %s args = %v", q.SQL, q.Args)
	}
	for _, bad := range []string{"geo_country'] OR 1=1 --", "", "a b", "x)"} {
		if _, err := FunnelBreakdownSQL(7, steps, 7, to.AddDate(0, 0, -30), to, 0, bad); err == nil {
			t.Errorf("breakdown by %q must be refused", bad)
		}
	}
}

func TestFilterHelpersBindEverything(t *testing.T) {
	where := []PropFilter{{Prop: "sku", Op: "in", Value: []any{"a", "b"}}, {Prop: "amt", Op: "gte", Value: 5.0}, {Prop: "x", Op: "exists"}}
	if err := ValidatePropFilters("report", where); err != nil {
		t.Fatal(err)
	}
	sql, args := PropFilterSQL(where)
	if n := strings.Count(sql, "?"); n != len(args) {
		t.Errorf("props: %d placeholders, %d args", n, len(args))
	}
	asql, aargs := AttrFilterSQL(append(where, PropFilter{Prop: "first_seen", Op: "within_days", Value: 7.0}), to())
	if n := strings.Count(asql, "?"); n != len(aargs) || !strings.Contains(asql, "attrs[?]") {
		t.Errorf("attrs: %d placeholders, %d args: %s", n, len(aargs), asql)
	}
	if ValidatePropFilters("report", []PropFilter{{Prop: "a'b", Op: "exists"}}) == nil ||
		ValidateAttrFilters("report", []PropFilter{{Prop: "plan", Op: "eq", Value: []any{1}}}) == nil ||
		ValidatePropFilters("report", []PropFilter{{Prop: "d", Op: "within_days", Value: 3.0}}) == nil {
		t.Error("invalid filters must be refused")
	}
}

func to() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }
