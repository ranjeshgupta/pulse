package ch

import (
	"strings"
	"testing"
)

// Paths keep the events nearest the anchor (sorted, not the first 400 read), from the person's
// first anchor on (backwards: up to their last), with one argument per placeholder.
func TestPathsSQL(t *testing.T) {
	for _, back := range []bool{false, true} {
		q := pathsSQL(back)
		near := "e.ts >= a.at"
		if back {
			near = "e.ts <= a.at"
		}
		if !strings.Contains(q, "groupArraySorted(400)") || strings.Contains(q, "groupArray(400)") || !strings.Contains(q, near) || strings.Count(q, "?") != 13 {
			t.Errorf("backwards %v: %s", back, q)
		}
	}
}
