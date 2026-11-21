package ch

// Path analysis (O6): what people do after an event (or before one), step by step.

import (
	"context"
	"errors"
	"time"
)

// PathQuery asks for the paths from Start (or to End) over [From, To).
type PathQuery struct {
	Tenant   int64
	Start    string // the first step; "" with End set
	End      string // the last step (paths are read backwards from it)
	Steps    int    // steps after Start (or before End), 1-6
	From, To time.Time
	Exclude  []string // event names left out of paths
}

// PathRow is one distinct path (names in time order, starting with Start or ending with End) and
// how many people took it.
type PathRow struct {
	Steps  []string `json:"steps"`
	People uint64   `json:"people"`
}

// systemEvents are left out of paths: Pulse's own bookkeeping and message outcomes.
const systemEvents = `^((email|sms|whatsapp|rcs|push|inapp|inbox|webpush)_|journey_|contact_merged|merge_requested|rfm_changed|tag_|list_|points_changed|stage_changed|owner_changed|company_|dwc_requested|identify$|optin_requested)`

// Paths returns the most common paths (top 500) and the number of people who did the anchor event.
func (s *Store) Paths(ctx context.Context, q PathQuery) ([]PathRow, uint64, error) {
	if (q.Start == "") == (q.End == "") {
		return nil, 0, errors.New("give a start event or an end event")
	}
	anchor, backwards := q.Start, false
	if q.End != "" {
		anchor, backwards = q.End, true
	}
	exclude := q.Exclude
	if exclude == nil {
		exclude = []string{}
	}
	// Per person: names in time order (repeats collapsed, system events and exclusions left out),
	// cut at the first anchor (or the last, backwards) and Steps names after (before) it.
	pick := `arraySlice(ns, indexOf(ns, ?), ? + 1)`
	if backwards {
		pick = `arrayReverse(arraySlice(arrayReverse(ns), indexOf(arrayReverse(ns), ?), ? + 1))`
	}
	sql := `SELECT path, count() AS people FROM (
		SELECT ` + pick + ` AS path FROM (
			SELECT contact_id, arrayFilter((x, i) -> i = 1 OR x != raw[i - 1], raw, arrayEnumerate(raw)) AS ns FROM (
				SELECT contact_id, arrayMap(p -> p.2, arraySort(groupArray(400)((ts, name)))) AS raw FROM events
				WHERE tenant_id = ? AND ts >= ? AND ts < ? AND (name = ? OR (NOT match(name, ?) AND name NOT IN ?))
				GROUP BY contact_id HAVING has(raw, ?) LIMIT 300000))
		WHERE length(path) > 0)
		GROUP BY path ORDER BY people DESC, path LIMIT 500`
	args := []any{anchor, q.Steps, uint64(q.Tenant), q.From.UTC(), q.To.UTC(), anchor, systemEvents, exclude, anchor}
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []PathRow
	for rows.Next() {
		var r PathRow
		if err := rows.Scan(&r.Steps, &r.People); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// The top 500 may leave rare paths out; the anchor's people are counted exactly.
	var people uint64
	err = s.conn.QueryRow(ctx, `SELECT uniqExact(contact_id) FROM events WHERE tenant_id = ? AND name = ? AND ts >= ? AND ts < ?`,
		uint64(q.Tenant), anchor, q.From.UTC(), q.To.UTC()).Scan(&people)
	return out, people, err
}
