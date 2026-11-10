package segment

import (
	"context"
	"testing"
	"time"
)

func TestDateOps(t *testing.T) {
	Today = func() time.Time { return time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC) }
	defer func() { Today = func() time.Time { return time.Now().UTC() } }()
	attrs := map[string]any{"renewal": "2026-12-01", "last_seen": "2026-11-08", "first_seen": "2025-01-05T10:00:00Z", "bad": "soon"}
	for _, tc := range []struct {
		attr, op string
		value    any
		want     bool
	}{
		{"renewal", "after", "2026-11-30", true},
		{"renewal", "before", "2026-11-30", false},
		{"last_seen", "within_days", 7.0, true},
		{"last_seen", "within_days", 1.0, false},
		{"first_seen", "older_than_days", 365.0, true},
		{"last_seen", "older_than_days", 30.0, false},
		{"bad", "after", "2000-01-01", false},
		{"missing", "older_than_days", 1.0, false},
	} {
		r := Rule{Attr: tc.attr, Op: tc.op, Value: tc.value}
		if err := r.Validate(); err != nil {
			t.Fatalf("%v: %v", r, err)
		}
		got, _ := Eval(context.Background(), r, attrs, nil)
		if got != tc.want {
			t.Errorf("%s %s %v = %v, want %v", tc.attr, tc.op, tc.value, got, tc.want)
		}
		if !r.HasWindow() && (tc.op == "within_days" || tc.op == "older_than_days") {
			t.Error("relative date rules must be swept")
		}
		q, err := ToSQL(r, 1, Today())
		if err != nil || len(q.Args) == 0 {
			t.Errorf("sql: %v", err)
		}
	}
	if (Rule{Attr: "x", Op: "after", Value: "11/30/2026"}).Validate() == nil {
		t.Error("dates must be YYYY-MM-DD")
	}
	ev := Rule{Event: "purchase", Op: "gte", Count: new(int), Days: 7, Where: []PropFilter{{Prop: "at", Op: "within_days", Value: 3.0}}}
	if ev.Validate() == nil {
		t.Error("relative dates aren't allowed in event property filters")
	}
}
