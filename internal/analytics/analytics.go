// Package analytics compiles the analytics reports (cohorts, revenue attribution, custom reports and
// journey A/B test stats) to parameterized ClickHouse SQL, and does the arithmetic on their rows.
//
// Every value a user picks (event names, property and attribute keys, filter values, dates, ids) is
// a bound argument; the only text spliced into SQL comes from fixed tables in this package, chosen
// by validated enum values, or integers computed here. Every query reads one tenant's key range,
// and event queries name their events (the events table is sorted by tenant, name, contact, ts)
// and bound ts, so they read a narrow slice of the table.
package analytics

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"pulse/internal/event"
	"pulse/internal/segment"
)

// MaxRange is the longest date range any report covers.
const MaxRange = 366 * 24 * time.Hour

// Query is ClickHouse SQL with positional (?) arguments.
type Query = segment.Query

// CheckRange validates a report's date range [from, to).
func CheckRange(from, to time.Time) error {
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return errors.New("pick a date range")
	}
	if to.Sub(from) > MaxRange {
		return errors.New("pick a date range of a year or less")
	}
	return nil
}

// Period is a calendar bucket: day, week (starting Monday) or month, all in UTC.
type Period string

const (
	Day   Period = "day"
	Week  Period = "week"
	Month Period = "month"
)

func (p Period) valid() bool { return p == Day || p == Week || p == Month }

// trunc is the ClickHouse expression truncating a DateTime expression to the period's start (a Date).
func (p Period) trunc(expr string) string {
	switch p {
	case Week:
		return "toMonday(" + expr + ", 'UTC')"
	case Month:
		return "toStartOfMonth(" + expr + ", 'UTC')"
	default:
		return "toDate(" + expr + ", 'UTC')"
	}
}

// Start truncates t to the start of its period (UTC).
func (p Period) Start(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch p {
	case Week:
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	case Month:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return d
}

// Add moves t by n periods.
func (p Period) Add(t time.Time, n int) time.Time {
	switch p {
	case Week:
		return t.AddDate(0, 0, 7*n)
	case Month:
		return t.AddDate(0, n, 0)
	}
	return t.AddDate(0, 0, n)
}

// Buckets lists the period starts covering [from, to).
func (p Period) Buckets(from, to time.Time) []time.Time {
	var out []time.Time
	for b := p.Start(from); b.Before(to); b = p.Add(b, 1) {
		out = append(out, b)
	}
	return out
}

// ActivityCondition is the SQL condition (with its arguments) selecting a contact's own activity: every event
// except the ones Pulse records itself (sends and their delivery outcomes, journey bookkeeping, tags,
// points, lists, merges, stages, companies). "Any event" in a cohort report means this.
func ActivityCondition() (string, []any) {
	marks := make([]string, len(systemEvents))
	args := make([]any, len(systemEvents))
	for i, n := range systemEvents {
		marks[i] = "?"
		args[i] = n
	}
	return "NOT match(name, '^(email|sms|whatsapp|rcs|push|inbox)_(sent|failed|delivered|fallback|suppressed|capped|deferred|bounced)$')" +
		" AND name NOT IN (" + strings.Join(marks, ", ") + ")", args
}

var systemEvents = []string{
	event.TagAdded, event.TagRemoved, event.PointsChanged, event.ListJoined, event.ListLeft,
	event.ContactMerged, event.ContactMergedInto, event.MergeRequested, event.StageChanged,
	event.CompanyLinked, event.CompanyUnlinked, event.CompanyUpdated,
	"journey_entered", "journey_completed", event.JourneySplit, "webhook_failed",
}

// validEvents checks a list of event names.
func validEvents(what string, names []string, max int) error {
	if len(names) > max {
		return fmt.Errorf("%s: at most %d events", what, max)
	}
	for _, n := range names {
		if !event.ValidName(n) {
			return fmt.Errorf("%s: invalid event name %q", what, n)
		}
	}
	return nil
}

// marks returns n comma-separated placeholders.
func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// segmentScope limits a query to current members of a segment or list (col is the contact id column).
func segmentScope(col string, tenant uint64, segmentID int64) (string, []any) {
	if segmentID <= 0 {
		return "", nil
	}
	return " AND " + col + " IN (SELECT contact_id FROM segment_members FINAL WHERE tenant_id = ? AND segment_id = ? AND member = 1)",
		[]any{tenant, uint64(segmentID)}
}

// notMerged leaves out contacts merged into another profile (their events were copied to it).
func notMerged(col string, tenant uint64) (string, []any) {
	return " AND " + col + " NOT IN (SELECT id FROM contacts FINAL WHERE tenant_id = ? AND mapContains(attrs, 'merged_into'))", []any{tenant}
}
