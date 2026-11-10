// Package schedule holds the time rules shared by journeys, on-site content and broadcasts: a
// required start, an optional end (none = runs until stopped), and an optional repeat.
package schedule

import (
	"errors"
	"time"
)

// Window is when something is live. A nil End means it never ends.
type Window struct {
	Start time.Time  `json:"start_at"`
	End   *time.Time `json:"end_at,omitempty"`
}

// Validate requires a start and an end after it.
func (w Window) Validate() error {
	if w.Start.IsZero() {
		return errors.New("a start date and time is required")
	}
	if w.End != nil && !w.End.After(w.Start) {
		return errors.New("the end must be after the start")
	}
	return nil
}

// Active reports whether t falls inside the window.
func (w Window) Active(t time.Time) bool {
	return !t.Before(w.Start) && (w.End == nil || t.Before(*w.End))
}

// State names where t sits relative to the window: upcoming, live or ended.
func (w Window) State(t time.Time) string {
	switch {
	case t.Before(w.Start):
		return "upcoming"
	case w.End != nil && !t.Before(*w.End):
		return "ended"
	}
	return "live"
}

// Repeats.
const (
	None    = "none"
	Daily   = "daily"
	Weekly  = "weekly"
	Monthly = "monthly"
)

func ValidRepeat(r string) bool { return r == None || r == Daily || r == Weekly || r == Monthly }

// Next returns the run after `after` for a series starting at start, or ok=false when the series is
// over (no repeat, or past the window's end). Runs stay on the start's wall-clock time in its zone;
// a monthly series that starts on the 31st runs on the last day of shorter months.
func Next(w Window, repeat string, after time.Time) (time.Time, bool) {
	if after.Before(w.Start) {
		return w.Start, w.Active(w.Start)
	}
	if repeat == None || repeat == "" {
		return time.Time{}, false
	}
	for n := 1; n < 100000; n++ {
		t := occurrence(w.Start, repeat, n)
		if t.After(after) {
			if w.End != nil && !t.Before(*w.End) {
				return time.Time{}, false
			}
			return t, true
		}
	}
	return time.Time{}, false
}

func occurrence(start time.Time, repeat string, n int) time.Time {
	switch repeat {
	case Daily:
		return start.AddDate(0, 0, n)
	case Weekly:
		return start.AddDate(0, 0, 7*n)
	default: // monthly, clamped to the month's last day
		y, m, d := start.Date()
		first := time.Date(y, m+time.Month(n), 1, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
		last := first.AddDate(0, 1, -1).Day()
		if d > last {
			d = last
		}
		return first.AddDate(0, 0, d-1)
	}
}
