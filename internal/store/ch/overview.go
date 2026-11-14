package ch

// The Overview page's summary (GET /v1/stats/summary). Each query reads a few event names of one
// workspace (the events sort key starts with tenant and name) over the newest partitions, or a few
// broadcasts' rows of message_events (sorted by tenant, source and campaign), never the whole log.

import (
	"context"
	"time"

	"pulse/internal/channel"
	"pulse/internal/model"
)

// Unsubscribes counts the contacts who unsubscribed (any channel) since weekFrom, and between
// prevFrom and weekFrom: the Contacts list's event:unsubscribed with a period shows the same people.
func (s *Store) Unsubscribes(ctx context.Context, tenant int64, prevFrom, weekFrom time.Time) (week, prev uint64, err error) {
	err = s.conn.QueryRow(ctx, `
		SELECT uniqExactIf(contact_id, ts >= ?), uniqExactIf(contact_id, ts < ?) FROM events
		WHERE tenant_id = ? AND name = 'unsubscribed' AND ts >= ?`, weekFrom, weekFrom, uint64(tenant), prevFrom).Scan(&week, &prev)
	return
}

// SendCounts are message events of one name: in the last hour, today (UTC) and over the whole
// range asked for (today and the days before).
type SendCounts struct {
	Hour  uint64 `json:"hour"`
	Today uint64 `json:"today"`
	Total uint64 `json:"total"`
}

// HealthNames are the events ChannelHealth counts: sent and failed per channel, email bounces.
func HealthNames() []string {
	names := []string{"email_bounced"}
	for _, c := range channel.All {
		names = append(names, c+"_sent", c+"_failed")
	}
	return names
}

// ChannelHealth counts sends, failures and email bounces since `since` (count(), not unique ids:
// cheap at any volume, and the pipeline's rare redeliveries don't matter to a warning).
func (s *Store) ChannelHealth(ctx context.Context, tenant int64, hourAgo, today, since time.Time) (map[string]SendCounts, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT name, countIf(ts >= ?), countIf(ts >= ?), count() FROM events
		WHERE tenant_id = ? AND name IN ? AND ts >= ?
		GROUP BY name`, hourAgo, today, uint64(tenant), HealthNames(), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SendCounts{}
	for rows.Next() {
		var name string
		var c SendCounts
		if err := rows.Scan(&name, &c.Hour, &c.Today, &c.Total); err != nil {
			return nil, err
		}
		out[name] = c
	}
	return out, rows.Err()
}

// RunKey is one broadcast run (Run: unix seconds of its scheduled time).
type RunKey struct {
	Broadcast int64
	Run       int64
}

// RunCounts are the outcomes of broadcast runs (unique contacts, approximate: uniq).
func (s *Store) RunCounts(ctx context.Context, tenant int64, runs []RunKey) (map[RunKey]model.MessageCounts, error) {
	out := map[RunKey]model.MessageCounts{}
	if len(runs) == 0 {
		return out, nil
	}
	want := map[RunKey]bool{}
	var ids, keys []uint64
	for _, r := range runs {
		want[r] = true
		ids = append(ids, uint64(r.Broadcast))
		keys = append(keys, uint64(r.Run))
	}
	rows, err := s.conn.Query(ctx, `
		SELECT source_id, run, kind, uniq(contact_id) FROM message_events
		WHERE tenant_id = ? AND source = 'broadcast' AND source_id IN ? AND run IN ?
		  AND kind IN ('sent', 'delivered', 'opened', 'clicked', 'failed', 'bounced', 'unsubscribed')
		GROUP BY source_id, run, kind`, uint64(tenant), ids, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, run, n uint64
		var kind string
		if err := rows.Scan(&id, &run, &kind, &n); err != nil {
			return nil, err
		}
		k := RunKey{int64(id), int64(run)}
		if !want[k] {
			continue // another pair of the two IN lists
		}
		c := out[k]
		addCount(&c, kind, n)
		out[k] = c
	}
	return out, rows.Err()
}
