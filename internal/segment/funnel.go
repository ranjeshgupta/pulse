package segment

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/event"
)

const (
	MaxFunnelSteps  = 8
	MaxFunnelWindow = 90 // days
)

// FunnelStep is one step: an event, optionally narrowed by property filters (as in event rules).
type FunnelStep struct {
	Event string       `json:"event"`
	Where []PropFilter `json:"where,omitempty"`
}

// ValidateFunnel checks steps and the window (days) a contact has to complete them in.
func ValidateFunnel(steps []FunnelStep, windowDays int) error {
	if len(steps) < 2 || len(steps) > MaxFunnelSteps {
		return fmt.Errorf("a funnel needs 2 to %d steps", MaxFunnelSteps)
	}
	if windowDays < 1 || windowDays > MaxFunnelWindow {
		return fmt.Errorf("the conversion window must be 1 to %d days", MaxFunnelWindow)
	}
	for i, st := range steps {
		if !event.ValidName(st.Event) {
			return fmt.Errorf("step %d: choose an event", i+1)
		}
		if len(st.Where) > maxWhere {
			return fmt.Errorf("step %d: at most %d property filters", i+1, maxWhere)
		}
		for _, f := range st.Where {
			if !attr.ValidKey(f.Prop) {
				return fmt.Errorf("step %d: invalid property name %q", i+1, f.Prop)
			}
			if err := validCompare(fmt.Sprintf("step %d property %q", i+1, f.Prop), f.Op, f.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// FunnelSQL compiles a funnel over events in [from, to): for each contact, the furthest step they
// reached in order with every step inside the window after the first (ClickHouse windowFunnel).
// It returns (level, contacts) rows. segmentID > 0 limits it to current members of a segment or list.
func FunnelSQL(tenantID int64, steps []FunnelStep, windowDays int, from, to time.Time, segmentID int64) (Query, error) {
	if err := ValidateFunnel(steps, windowDays); err != nil {
		return Query{}, err
	}
	if !to.After(from) {
		return Query{}, errors.New("the date range is empty")
	}
	b := &sqlBuilder{tenant: uint64(tenantID), asOf: to.UTC()}
	conds := make([]string, len(steps))
	for i, st := range steps {
		b.args = append(b.args, st.Event)
		conds[i] = "(name = ?" + b.props(st.Where) + ")"
	}
	names := map[string]bool{}
	marks := []string{}
	for _, st := range steps {
		if !names[st.Event] {
			names[st.Event] = true
			marks = append(marks, "?")
			b.args = append(b.args, st.Event)
		}
	}
	b.args = append(b.args, b.tenant, from.UTC(), to.UTC(), b.tenant)
	scope := ""
	if segmentID > 0 {
		b.args = append(b.args, b.tenant, uint64(segmentID))
		scope = " AND contact_id IN (SELECT contact_id FROM segment_members FINAL WHERE tenant_id = ? AND segment_id = ? AND member = 1)"
	}
	window := strconv.Itoa(windowDays * 86400) // seconds; an integer we computed, safe to inline
	return Query{
		SQL: "SELECT level, count() FROM (" +
			"SELECT contact_id, windowFunnel(" + window + ")(toDateTime(ts), " + strings.Join(conds, ", ") + ") AS level" +
			" FROM events WHERE name IN (" + strings.Join(marks, ", ") + ") AND tenant_id = ? AND ts >= ? AND ts < ?" +
			// Merged visitors' events were copied to the known contact; count them once, there.
			" AND contact_id NOT IN (SELECT id FROM contacts FINAL WHERE tenant_id = ? AND mapContains(attrs, 'merged_into'))" +
			scope + " GROUP BY contact_id) WHERE level > 0 GROUP BY level ORDER BY level",
		Args: b.args,
	}, nil
}
