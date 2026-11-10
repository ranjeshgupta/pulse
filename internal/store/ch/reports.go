package ch

// Reports: message outcomes and engagement of broadcasts and journeys (from message_events, which a
// materialized view fills from the event log), and funnels.
//
// Every count is unique contacts (uniqExact), so a contact who opens an email five times counts
// once, and rows the pipeline redelivered never inflate a number.

import (
	"context"
	"time"

	"pulse/internal/model"
	"pulse/internal/segment"
)

// addCount sets the field of c for one outcome kind.
func addCount(c *model.MessageCounts, kind string, n uint64) {
	switch kind {
	case "sent":
		c.Sent = n
	case "delivered":
		c.Delivered = n
	case "read":
		c.Read = n
	case "failed":
		c.Failed = n
	case "fallback":
		c.Fallback = n
	case "suppressed":
		c.Suppressed = n
	case "capped":
		c.Capped = n
	case "deferred":
		c.Deferred = n
	case "opened":
		c.Opened = n
	case "clicked":
		c.Clicked = n
	case "unsubscribed":
		c.Unsubscribed = n
	}
}

// Scope selects the messages of one broadcast (optionally one run) or one journey.
type Scope struct {
	Tenant   int64
	Source   string // broadcast | journey
	SourceID int64
	Run      int64 // broadcasts: 0 = every run
}

func (sc Scope) where() (string, []any) {
	w := "tenant_id = ? AND source = ? AND source_id = ?"
	args := []any{uint64(sc.Tenant), sc.Source, uint64(sc.SourceID)}
	if sc.Run != 0 {
		w += " AND run = ?"
		args = append(args, uint64(sc.Run))
	}
	return w, args
}

// groupCounts runs "SELECT <key>, kind, uniqExact(contact_id) ... GROUP BY <key>, kind".
func (s *Store) groupCounts(ctx context.Context, sc Scope, key string) (map[string]model.MessageCounts, error) {
	w, args := sc.where()
	rows, err := s.conn.Query(ctx, "SELECT "+key+" AS k, kind, uniqExact(contact_id) FROM message_events WHERE "+w+
		" GROUP BY k, kind", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.MessageCounts{}
	for rows.Next() {
		var k, kind string
		var n uint64
		if err := rows.Scan(&k, &kind, &n); err != nil {
			return nil, err
		}
		c := out[k]
		addCount(&c, kind, n)
		out[k] = c
	}
	return out, rows.Err()
}

// VariantCounts are a broadcast run's outcomes per A/B variant (the broadcast.Stats interface).
func (s *Store) VariantCounts(ctx context.Context, tenant, broadcast, run int64) (map[string]model.MessageCounts, error) {
	return s.groupCounts(ctx, Scope{Tenant: tenant, Source: "broadcast", SourceID: broadcast, Run: run}, "variant")
}

type LinkCount struct {
	URL      string `json:"url"`
	Contacts uint64 `json:"contacts"`
	Clicks   uint64 `json:"clicks"`
}

type DayKindCount struct {
	Day   string `json:"day"`
	Kind  string `json:"kind"`
	Count uint64 `json:"count"`
}

// Report is everything the report pages show for one scope.
type Report struct {
	Totals    model.MessageCounts            `json:"totals"`
	ByVariant map[string]model.MessageCounts `json:"by_variant,omitempty"` // broadcasts
	ByRun     map[string]model.MessageCounts `json:"by_run,omitempty"`     // broadcasts, keyed by run (unix seconds)
	ByNode    map[string]model.MessageCounts `json:"by_node,omitempty"`    // journeys, keyed by step id
	ByChannel map[string]model.MessageCounts `json:"by_channel"`
	Links     []LinkCount                    `json:"links"`
	Daily     []DayKindCount                 `json:"daily"` // sent, opened, clicked per UTC day
}

// MessageReport builds the report for a broadcast or journey. With conversionEvent set (broadcasts),
// a recipient converts by doing that event within conversionDays of their message being sent.
func (s *Store) MessageReport(ctx context.Context, sc Scope, conversionEvent string, conversionDays int) (Report, error) {
	rep := Report{Links: []LinkCount{}, Daily: []DayKindCount{}}
	totals, err := s.groupCounts(ctx, sc, "''")
	if err != nil {
		return rep, err
	}
	rep.Totals = totals[""]
	if rep.ByChannel, err = s.groupCounts(ctx, sc, "channel"); err != nil {
		return rep, err
	}
	if sc.Source == "broadcast" {
		if rep.ByVariant, err = s.groupCounts(ctx, sc, "variant"); err != nil {
			return rep, err
		}
		delete(rep.ByVariant, "") // not an A/B test
		if sc.Run == 0 {
			if rep.ByRun, err = s.groupCounts(ctx, sc, "toString(run)"); err != nil {
				return rep, err
			}
		}
	} else if rep.ByNode, err = s.groupCounts(ctx, sc, "node"); err != nil {
		return rep, err
	}
	if conversionEvent != "" && conversionDays > 0 {
		total, err := s.conversions(ctx, sc, conversionEvent, conversionDays, "''")
		if err != nil {
			return rep, err
		}
		rep.Totals.Converted = total[""]
		if len(rep.ByVariant) > 0 {
			byVariant, err := s.conversions(ctx, sc, conversionEvent, conversionDays, "variant")
			if err != nil {
				return rep, err
			}
			for k, c := range rep.ByVariant {
				c.Converted = byVariant[k]
				rep.ByVariant[k] = c
			}
		}
	}

	w, args := sc.where()
	lrows, err := s.conn.Query(ctx, "SELECT url, uniqExact(contact_id) AS c, uniqExact(id) FROM message_events WHERE "+w+
		" AND kind = 'clicked' AND url != '' GROUP BY url ORDER BY c DESC LIMIT 20", args...)
	if err != nil {
		return rep, err
	}
	for lrows.Next() {
		var l LinkCount
		if err := lrows.Scan(&l.URL, &l.Contacts, &l.Clicks); err != nil {
			lrows.Close()
			return rep, err
		}
		rep.Links = append(rep.Links, l)
	}
	lrows.Close()

	drows, err := s.conn.Query(ctx, "SELECT toString(toDate(ts, 'UTC')) AS d, kind, uniqExact(contact_id) FROM message_events WHERE "+w+
		" AND kind IN ('sent', 'opened', 'clicked') AND ts >= now() - INTERVAL 60 DAY GROUP BY d, kind ORDER BY d", args...)
	if err != nil {
		return rep, err
	}
	defer drows.Close()
	for drows.Next() {
		var d DayKindCount
		if err := drows.Scan(&d.Day, &d.Kind, &d.Count); err != nil {
			return rep, err
		}
		rep.Daily = append(rep.Daily, d)
	}
	return rep, drows.Err()
}

// conversions counts recipients who did ev within days of their send, grouped by key ("”" for
// one total, "variant" per A/B variant).
func (s *Store) conversions(ctx context.Context, sc Scope, ev string, days int, key string) (map[string]uint64, error) {
	w, args := sc.where()
	q := `
		SELECT k, uniqExact(m.contact_id)
		FROM (SELECT contact_id, ` + key + ` AS k, min(ts) AS sent_at FROM message_events
		      WHERE ` + w + ` AND kind = 'sent' GROUP BY contact_id, k) AS m
		INNER JOIN (SELECT contact_id, ts FROM events WHERE tenant_id = ? AND name = ?) AS e ON e.contact_id = m.contact_id
		WHERE e.ts >= m.sent_at AND e.ts < m.sent_at + toIntervalDay(?)
		GROUP BY k`
	args = append(args, uint64(sc.Tenant), ev, days)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var k string
		var n uint64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// FunnelLevels runs a compiled funnel: how many contacts reached each step (index 0 = step 1).
func (s *Store) FunnelLevels(ctx context.Context, q segment.Query, steps int) ([]uint64, error) {
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reached := make([]uint64, steps)
	for rows.Next() {
		var level uint8
		var n uint64
		if err := rows.Scan(&level, &n); err != nil {
			return nil, err
		}
		for i := 0; i < int(level) && i < steps; i++ {
			reached[i] += n // reaching step k means reaching every step before it
		}
	}
	return reached, rows.Err()
}

// EventNames lists the tenant's event names seen in the last 90 days, most frequent first
// (for pickers in funnels, forms and conversion goals).
func (s *Store) EventNames(ctx context.Context, tenant int64) ([]string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT name FROM events WHERE tenant_id = ? AND ts >= ?
		GROUP BY name ORDER BY count() DESC LIMIT 200`, uint64(tenant), time.Now().AddDate(0, 0, -90))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

type FormSubmission struct {
	ExternalID string    `json:"external_id"`
	Ts         time.Time `json:"ts"`
	Props      string    `json:"props"` // JSON: fields, page_url, source
}

type FormStats struct {
	Total      uint64           `json:"total"`
	Last30Days uint64           `json:"last_30_days"`
	Contacts   uint64           `json:"contacts"`
	Recent     []FormSubmission `json:"recent"`
	PerDay     []DayCount       `json:"per_day"`
}

// FormSubmissions summarizes a form's form_submitted events and lists the latest ones.
func (s *Store) FormSubmissions(ctx context.Context, tenant, form int64, limit int) (FormStats, error) {
	st := FormStats{Recent: []FormSubmission{}, PerDay: []DayCount{}}
	const where = "tenant_id = ? AND name = 'form_submitted' AND JSONExtractUInt(props, 'form_id') = ?"
	args := []any{uint64(tenant), uint64(form)}
	if err := s.conn.QueryRow(ctx, "SELECT uniqExact(id), uniqExactIf(id, ts >= now() - INTERVAL 30 DAY), uniqExact(contact_id) FROM events WHERE "+where,
		args...).Scan(&st.Total, &st.Last30Days, &st.Contacts); err != nil {
		return st, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT c.external_id, e.ts, e.props FROM
			(SELECT contact_id, ts, props FROM events WHERE `+where+` ORDER BY ts DESC LIMIT 1 BY id LIMIT ?) AS e
		LEFT JOIN (SELECT id, external_id FROM contacts FINAL WHERE tenant_id = ?) AS c ON c.id = e.contact_id
		ORDER BY e.ts DESC`, uint64(tenant), uint64(form), limit, uint64(tenant))
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var f FormSubmission
		if err := rows.Scan(&f.ExternalID, &f.Ts, &f.Props); err != nil {
			rows.Close()
			return st, err
		}
		st.Recent = append(st.Recent, f)
	}
	rows.Close()
	drows, err := s.conn.Query(ctx, "SELECT toString(toDate(ts, 'UTC')) AS d, uniqExact(id) FROM events WHERE "+where+
		" AND ts >= now() - INTERVAL 30 DAY GROUP BY d ORDER BY d", args...)
	if err != nil {
		return st, err
	}
	defer drows.Close()
	for drows.Next() {
		var d DayCount
		if err := drows.Scan(&d.Day, &d.Count); err != nil {
			return st, err
		}
		st.PerDay = append(st.PerDay, d)
	}
	return st, drows.Err()
}
