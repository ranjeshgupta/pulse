package ch

import (
	"context"
	"encoding/json"
	"time"
)

// CountVisits counts a contact's page_view events whose url contains urlContains (any page when
// empty), in the last days (0 = all history kept).
func (s *Store) CountVisits(ctx context.Context, tenant int64, contact uint32, urlContains string, days int) (uint64, error) {
	since := time.Unix(0, 0).UTC()
	if days > 0 {
		since = time.Now().UTC().AddDate(0, 0, -days)
	}
	var n uint64
	err := s.conn.QueryRow(ctx, `
		SELECT count() FROM events
		WHERE tenant_id = ? AND name = 'page_view' AND contact_id = ? AND ts >= ?
		AND (? = '' OR position(JSONExtractString(props, 'url'), ?) > 0)`,
		uint64(tenant), contact, since, urlContains, urlContains).Scan(&n)
	return n, err
}

// LatestForm returns the props of a contact's latest form_submitted (of form, 0 = any), or nil.
func (s *Store) LatestForm(ctx context.Context, tenant int64, contact uint32, form int64) (map[string]any, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT props FROM events
		WHERE tenant_id = ? AND name = 'form_submitted' AND contact_id = ?
		AND (? = 0 OR JSONExtractUInt(props, 'form_id') = ?)
		ORDER BY ts DESC LIMIT 1`, uint64(tenant), contact, uint64(form), uint64(form))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var props string
	if err := rows.Scan(&props); err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal([]byte(props), &m)
}
