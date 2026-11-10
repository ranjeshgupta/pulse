package ch

// Read queries for the web app: segment size previews, contact timelines, dashboard numbers.

import (
	"context"
	"time"

	"pulse/internal/segment"
)

// SegmentCount runs a compiled segment query as a count, for the live preview while editing a rule.
func (s *Store) SegmentCount(ctx context.Context, q segment.Query) (uint64, error) {
	var n uint64
	err := s.conn.QueryRow(ctx, "SELECT count() FROM ("+q.SQL+")", q.Args...).Scan(&n)
	return n, err
}

type EventRow struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Ts    time.Time `json:"ts"`
	Props string    `json:"props"` // JSON
}

// ContactEvents returns a contact's most recent events (the profile timeline).
func (s *Store) ContactEvents(ctx context.Context, tenant int64, contact uint32, limit int) ([]EventRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT id, name, ts, props FROM events
		WHERE tenant_id = ? AND contact_id = ?
		ORDER BY ts DESC LIMIT 1 BY id
		LIMIT ?`, uint64(tenant), contact, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventRow{}
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.Name, &e.Ts, &e.Props); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type DayCount struct {
	Day   string `json:"day"`
	Count uint64 `json:"count"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

type Overview struct {
	EventsPerMinute []DayCount  `json:"events_per_minute"` // last 60 minutes, "2026-11-10T12:31" keys
	EventsPerDay    []DayCount  `json:"events_per_day"`
	TopEvents       []NameCount `json:"top_events"`
	Messages        []NameCount `json:"messages"` // email_sent, sms_failed, ... over the window
}

// Overview summarizes the last `days` days. Counts use uniq(id) (approximate, cheap), which also
// ignores rows redelivered by the pipeline.
func (s *Store) Overview(ctx context.Context, tenant int64, days int) (Overview, error) {
	o := Overview{EventsPerMinute: []DayCount{}, EventsPerDay: []DayCount{}, TopEvents: []NameCount{}, Messages: []NameCount{}}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format(time.DateOnly)

	// The live trace: per minute over the last hour. Reads only the newest part of the newest partition.
	mrows, err := s.conn.Query(ctx, `
		SELECT formatDateTime(toStartOfMinute(ts), '%Y-%m-%dT%H:%i', 'UTC') AS m, uniq(id) FROM events
		WHERE tenant_id = ? AND ts >= now() - INTERVAL 60 MINUTE
		GROUP BY m ORDER BY m`, uint64(tenant))
	if err != nil {
		return o, err
	}
	for mrows.Next() {
		var d DayCount
		if err := mrows.Scan(&d.Day, &d.Count); err != nil {
			mrows.Close()
			return o, err
		}
		o.EventsPerMinute = append(o.EventsPerMinute, d)
	}
	mrows.Close()

	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(ts, 'UTC')) AS d, uniq(id) FROM events
		WHERE tenant_id = ? AND toDate(ts, 'UTC') >= toDate(?)
		GROUP BY d ORDER BY d`, uint64(tenant), since)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			rows.Close()
			return o, err
		}
		o.EventsPerDay = append(o.EventsPerDay, d)
	}
	rows.Close()

	scanNames := func(sql string, dst *[]NameCount) error {
		rows, err := s.conn.Query(ctx, sql, uint64(tenant), since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n NameCount
			if err := rows.Scan(&n.Name, &n.Count); err != nil {
				return err
			}
			*dst = append(*dst, n)
		}
		return rows.Err()
	}
	if err := scanNames(`
		SELECT name, uniq(id) AS n FROM events
		WHERE tenant_id = ? AND toDate(ts, 'UTC') >= toDate(?)
		GROUP BY name ORDER BY n DESC LIMIT 10`, &o.TopEvents); err != nil {
		return o, err
	}
	err = scanNames(`
		SELECT name, uniq(id) AS n FROM events
		WHERE tenant_id = ? AND toDate(ts, 'UTC') >= toDate(?)
		  AND match(name, '^(email|sms|whatsapp|rcs|push)_(sent|failed|delivered|fallback|opened|clicked|suppressed)$')
		GROUP BY name ORDER BY name`, &o.Messages)
	return o, err
}
