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
