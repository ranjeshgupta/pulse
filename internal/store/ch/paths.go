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
	sql := pathsSQL(backwards)
	args := []any{anchor, q.Steps, uint64(q.Tenant), anchor, q.From.UTC(), q.To.UTC(), uint64(q.Tenant), q.From.UTC(), q.To.UTC(),
		anchor, systemEvents, exclude, anchor}
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

// pathsSQL reads, per person, names in time order (repeats collapsed, system events and exclusions
// left out) from their first anchor on (backwards: up to their last), at most 400 events nearest
// it, cut at the anchor and Steps names after (before) it.
func pathsSQL(backwards bool) string {
	pick := `arraySlice(ns, indexOf(ns, ?), ? + 1)`
	at, near, keep := `min(ts)`, `e.ts >= a.at`, `arrayMap(p -> p.2, groupArraySorted(400)((e.ts, e.name)))`
	if backwards {
		pick = `arrayReverse(arraySlice(arrayReverse(ns), indexOf(arrayReverse(ns), ?), ? + 1))`
		at, near = `max(ts)`, `e.ts <= a.at`
		keep = `arrayReverse(arrayMap(p -> p.2, groupArraySorted(400)((-toUnixTimestamp64Milli(e.ts), e.name))))` // the latest 400
	}
	return `SELECT path, count() AS people FROM (
		SELECT ` + pick + ` AS path FROM (
			SELECT contact_id, arrayFilter((x, i) -> i = 1 OR x != raw[i - 1], raw, arrayEnumerate(raw)) AS ns FROM (
				SELECT e.contact_id AS contact_id, ` + keep + ` AS raw FROM events AS e
				INNER JOIN (SELECT contact_id, ` + at + ` AS at FROM events WHERE tenant_id = ? AND name = ? AND ts >= ? AND ts < ?
					GROUP BY contact_id) AS a ON a.contact_id = e.contact_id
				WHERE e.tenant_id = ? AND e.ts >= ? AND e.ts < ? AND ` + near + `
				  AND (e.name = ? OR (NOT match(e.name, ?) AND e.name NOT IN ?))
				GROUP BY e.contact_id HAVING has(raw, ?) LIMIT 300000))
		WHERE length(path) > 0)
		GROUP BY path ORDER BY people DESC, path LIMIT 500`
}
