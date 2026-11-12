package ch

import (
	"context"
	"strings"
)

// Filtered views of the Contacts list. where is a segment rule's condition (segment.WhereSQL) or
// empty; text is a case-insensitive quick find over the external ID, email, name and phone.

// viewTextCols are the columns free text searches.
var viewTextCols = []string{"external_id", "attrs['email']", "attrs['name']", "attrs['first_name']", "attrs['last_name']", "attrs['phone']"}

// viewWhere builds the shared condition: the tenant, never merged-away profiles, the rule, the text.
func viewWhere(tenant int64, where string, args []any, text string) (string, []any) {
	var sb strings.Builder
	sb.WriteString("tenant_id = ? AND NOT mapContains(attrs, 'merged_into')")
	all := []any{uint64(tenant)}
	if where != "" {
		sb.WriteString(" AND " + where)
		all = append(all, args...)
	}
	if text != "" {
		parts := make([]string, len(viewTextCols))
		for i, c := range viewTextCols {
			parts[i] = "positionCaseInsensitiveUTF8(" + c + ", ?) > 0"
			all = append(all, text)
		}
		sb.WriteString(" AND (" + strings.Join(parts, " OR ") + ")")
	}
	return sb.String(), all
}

// ContactPage returns up to limit matching contact ids below before (0: from the newest), newest
// first, so pages follow each other by keyset like the unfiltered list.
func (s *Store) ContactPage(ctx context.Context, tenant int64, where string, args []any, text string, before uint32, limit int) ([]uint32, error) {
	cond, all := viewWhere(tenant, where, args, text)
	if before > 0 {
		cond += " AND id < ?"
		all = append(all, before)
	}
	all = append(all, limit)
	rows, err := s.conn.Query(ctx, "SELECT id FROM contacts FINAL WHERE "+cond+" ORDER BY id DESC LIMIT ?", all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ContactCount counts a view exactly up to limit. Above that it returns an approximate count
// (uniq over the rows without FINAL, so a contact whose older version matched may still count),
// so a broad filter on a very large workspace stays quick.
func (s *Store) ContactCount(ctx context.Context, tenant int64, where string, args []any, text string, limit int) (uint64, bool, error) {
	cond, all := viewWhere(tenant, where, args, text)
	var n uint64
	err := s.conn.QueryRow(ctx, "SELECT count() FROM (SELECT id FROM contacts FINAL WHERE "+cond+" LIMIT ?)", append(all, limit+1)...).Scan(&n)
	if err != nil || n <= uint64(limit) {
		return n, false, err
	}
	var approx uint64
	err = s.conn.QueryRow(ctx, "SELECT uniq(id) FROM contacts WHERE "+cond, all...).Scan(&approx)
	return max(approx, n), true, err
}

// ContactFilter returns which of ids match the view (where and text), newest first. The Contacts
// list uses it when a view starts from a Redis bitmap (segments, reachability, a journey) and also
// has rule filters, report filters or text: the bitmap gives candidates, ClickHouse checks them.
func (s *Store) ContactFilter(ctx context.Context, tenant int64, where string, args []any, text string, ids []uint32) ([]uint32, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cond, all := viewWhere(tenant, where, args, text)
	lo, hi := ids[0], ids[0]
	for _, id := range ids {
		lo, hi = min(lo, id), max(hi, id)
	}
	// The range lets ClickHouse skip granules by the sort key; has() then checks each row.
	all = append(all, lo, hi, ids)
	rows, err := s.conn.Query(ctx, "SELECT id FROM contacts FINAL WHERE "+cond+" AND id >= ? AND id <= ? AND has(?, id) ORDER BY id DESC", all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
