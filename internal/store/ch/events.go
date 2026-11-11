package ch

// The event catalog: which events a workspace sends, how often, and what properties they carry.

import (
	"context"
	"time"
)

// CatalogRow is one event name's volume in the period.
type CatalogRow struct {
	Name     string    `json:"name"`
	Count    uint64    `json:"count"`
	Contacts uint64    `json:"contacts"` // approximate (uniq)
	LastSeen time.Time `json:"last_seen"`
}

// EventCatalog lists the event names sent since `since`, most frequent first (200 at most).
func (s *Store) EventCatalog(ctx context.Context, tenant int64, since time.Time) ([]CatalogRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT name, count() AS c, uniq(contact_id), max(ts) FROM events
		WHERE tenant_id = ? AND ts >= ?
		GROUP BY name ORDER BY c DESC, name LIMIT 200`, uint64(tenant), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogRow{}
	for rows.Next() {
		var r CatalogRow
		if err := rows.Scan(&r.Name, &r.Count, &r.Contacts, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EventVolume counts one event per UTC day since `since` (days without events are left out).
func (s *Store) EventVolume(ctx context.Context, tenant int64, name string, since time.Time) ([]DayCount, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(ts, 'UTC')) AS d, count() FROM events
		WHERE tenant_id = ? AND name = ? AND ts >= ?
		GROUP BY d ORDER BY d`, uint64(tenant), name, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayCount{}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// EventSamples returns the props (JSON) of up to limit events of one name since `since`: the
// first rows ClickHouse reads, not a random sample, so it stays cheap on large workspaces.
func (s *Store) EventSamples(ctx context.Context, tenant int64, name string, since time.Time, limit int) ([]string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT props FROM events WHERE tenant_id = ? AND name = ? AND ts >= ? LIMIT ?`, uint64(tenant), name, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
