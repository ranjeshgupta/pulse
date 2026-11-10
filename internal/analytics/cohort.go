package analytics

// Cohorts (retention): contacts grouped by when they started (first seen, or the first time in the
// date range they did an event), and the share of each group that came back (did the return event)
// in each period after.

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"pulse/internal/event"
	"pulse/internal/segment"
)

const (
	MaxCohorts        = 60 // rows in one triangle
	CohortByFirstSeen = "first_seen"
	CohortByEvent     = "event"
)

// MaxPeriods is how many periods after the start a report can follow, per period length.
var MaxPeriods = map[Period]int{Day: 60, Week: 26, Month: 12}

type CohortSpec struct {
	// By: first_seen (the contact's first event) or event (the first time they did Event in the range).
	By      string               `json:"by"`
	Event   string               `json:"event,omitempty"`
	Where   []segment.PropFilter `json:"where,omitempty"`
	Period  Period               `json:"period"`  // day | week | month
	Periods int                  `json:"periods"` // return periods 1..N after period 0
	// Return is what counts as coming back: Event (with property filters), or any activity when empty.
	Return    CohortReturn `json:"return"`
	SegmentID int64        `json:"segment_id,omitempty"`
}

type CohortReturn struct {
	Event string               `json:"event,omitempty"`
	Where []segment.PropFilter `json:"where,omitempty"`
}

func (c CohortSpec) Validate() error {
	switch c.By {
	case CohortByFirstSeen:
		if c.Event != "" || len(c.Where) > 0 {
			return errors.New("cohorts by first seen take no event")
		}
	case CohortByEvent:
		if !event.ValidName(c.Event) {
			return errors.New("choose the event that starts a cohort")
		}
		if err := segment.ValidatePropFilters("cohort event", c.Where); err != nil {
			return err
		}
	default:
		return errors.New("group contacts by first seen or by an event")
	}
	if !c.Period.valid() {
		return errors.New("the period is day, week or month")
	}
	if c.Periods < 1 || c.Periods > MaxPeriods[c.Period] {
		return fmt.Errorf("follow 1 to %d %ss", MaxPeriods[c.Period], c.Period)
	}
	if c.Return.Event == "" {
		if len(c.Return.Where) > 0 {
			return errors.New("property filters need a return event")
		}
	} else {
		if !event.ValidName(c.Return.Event) {
			return fmt.Errorf("invalid return event %q", c.Return.Event)
		}
		if err := segment.ValidatePropFilters("return event", c.Return.Where); err != nil {
			return err
		}
	}
	if c.SegmentID < 0 {
		return errors.New("invalid segment")
	}
	return nil
}

// CohortQueries compiles a cohort report over cohorts starting in [from, to):
//   - sizes: (cohort Date, contacts) — how many contacts started in each period;
//   - cells: (cohort Date, period index, contacts) — how many of them came back in period index.
//
// Return activity is read from the first cohort's start to the end of the last period anyone can
// reach. Rows are unique per (contact, cohort) and (contact, return bucket), so plain count() is
// exact without uniqExact.
func CohortQueries(tenantID int64, c CohortSpec, from, to time.Time) (sizes, cells Query, err error) {
	if err = c.Validate(); err != nil {
		return
	}
	if err = CheckRange(from, to); err != nil {
		return
	}
	if n := len(c.Period.Buckets(from, to)); n > MaxCohorts {
		err = fmt.Errorf("that's %d cohorts; pick a shorter range or a longer period (at most %d)", n, MaxCohorts)
		return
	}
	t := uint64(tenantID)
	from, to = from.UTC(), to.UTC()
	members, margs := cohortMembers(t, c, from, to)

	sizes = Query{SQL: "SELECT cohort, count() FROM (" + members + ") GROUP BY cohort ORDER BY cohort", Args: margs}

	retEnd := c.Period.Add(c.Period.Start(to.Add(-time.Nanosecond)), c.Periods+1)
	cond, cargs := ActivityCondition()
	if c.Return.Event != "" {
		f, fargs := segment.PropFilterSQL(c.Return.Where)
		cond, cargs = "name = ?"+f, append([]any{c.Return.Event}, fargs...)
	}
	rargs := append([]any{t}, cargs...)
	rargs = append(rargs, c.Period.Start(from), retEnd)
	returns := "SELECT contact_id, " + c.Period.trunc("ts") + " AS b FROM events WHERE tenant_id = ? AND " + cond +
		" AND ts >= ? AND ts < ? GROUP BY contact_id, b"

	idx := "dateDiff('day', m.cohort, r.b)"
	switch c.Period {
	case Week:
		idx = "intDiv(dateDiff('day', m.cohort, r.b), 7)" // both are Mondays
	case Month:
		idx = "dateDiff('month', m.cohort, r.b)" // both are the 1st
	}
	// The (usually smaller) cohort members are the hash side of the join; return activity streams.
	cells = Query{
		SQL: "SELECT m.cohort, toInt32(" + idx + ") AS idx, count() FROM (" + returns + ") AS r" +
			" INNER JOIN (" + members + ") AS m ON m.contact_id = r.contact_id" +
			" WHERE r.b >= m.cohort AND idx <= " + strconv.Itoa(c.Periods) + // validated integer
			" GROUP BY m.cohort, idx ORDER BY m.cohort, idx",
		Args: append(rargs, margs...),
	}
	return
}

// cohortMembers is (contact_id, cohort) for every contact who started in [from, to).
func cohortMembers(t uint64, c CohortSpec, from, to time.Time) (string, []any) {
	if c.By == CohortByFirstSeen {
		seg, sargs := segmentScope("id", t, c.SegmentID)
		args := append([]any{t}, sargs...)
		args = append(args, from, to)
		return "SELECT id AS contact_id, " + c.Period.trunc("fs") + " AS cohort FROM (" +
			"SELECT id, parseDateTime64BestEffortOrNull(attrs['first_seen'], 3, 'UTC') AS fs FROM contacts FINAL" +
			" WHERE tenant_id = ? AND NOT mapContains(attrs, 'merged_into')" + seg +
			") WHERE fs >= ? AND fs < ?", args
	}
	f, fargs := segment.PropFilterSQL(c.Where)
	merged, margs := notMerged("contact_id", t)
	seg, sargs := segmentScope("contact_id", t, c.SegmentID)
	args := append([]any{t, c.Event}, fargs...)
	args = append(args, from, to)
	args = append(append(args, margs...), sargs...)
	return "SELECT contact_id, " + c.Period.trunc("min(ts)") + " AS cohort FROM events" +
		" WHERE tenant_id = ? AND name = ?" + f + " AND ts >= ? AND ts < ?" + merged + seg +
		" GROUP BY contact_id", args
}

// CohortRow is one cohort: when it started, how many contacts, and how many came back in each
// period 0..N. Returned[i] is nil for periods that haven't started yet.
type CohortRow struct {
	Start    time.Time `json:"start"`
	Size     uint64    `json:"size"`
	Returned []*uint64 `json:"returned"`
}

// CohortCell is one (cohort, period) count from the cells query.
type CohortCell struct {
	Cohort time.Time
	Index  int
	N      uint64
}

// CohortTable assembles the triangle: every period start in [from, to) gets a row (empty cohorts
// too, so the table has no gaps), and periods that start after now are nil.
// sizes is keyed by the cohort start's Unix seconds.
func CohortTable(c CohortSpec, from, to, now time.Time, sizes map[int64]uint64, cells []CohortCell) []CohortRow {
	byKey := map[[2]int64]uint64{}
	for _, cl := range cells {
		byKey[[2]int64{cl.Cohort.UTC().Unix(), int64(cl.Index)}] = cl.N
	}
	rows := []CohortRow{}
	for _, start := range c.Period.Buckets(from, to) {
		r := CohortRow{Start: start, Size: sizes[start.Unix()], Returned: make([]*uint64, c.Periods+1)}
		for i := 0; i <= c.Periods; i++ {
			if c.Period.Add(start, i).After(now) {
				continue // not reached yet
			}
			n := byKey[[2]int64{start.Unix(), int64(i)}]
			r.Returned[i] = &n
		}
		rows = append(rows, r)
	}
	return rows
}

// CohortAverage is the retention of all cohorts together for each period: contacts who came back
// over the size of the cohorts that have reached that period (nil when none has).
func CohortAverage(rows []CohortRow, periods int) []*float64 {
	out := make([]*float64, periods+1)
	for i := 0; i <= periods; i++ {
		var back, size uint64
		for _, r := range rows {
			if i < len(r.Returned) && r.Returned[i] != nil {
				back += *r.Returned[i]
				size += r.Size
			}
		}
		if size > 0 {
			v := float64(back) / float64(size)
			out[i] = &v
		}
	}
	return out
}
