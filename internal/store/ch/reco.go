package ch

// Recommendation lists (package catalog) from events carrying product codes.

import (
	"context"
	"time"

	"pulse/internal/catalog"
)

// codesExpr is an event's product codes: product_code and items[].product_code.
const codesExpr = `arrayFilter(x -> x != '', arrayDistinct(arrayConcat([product_code],
	arrayMap(i -> JSONExtractString(i, 'product_code'), JSONExtractArrayRaw(props, 'items')))))`

// hasCodes keeps events that may carry product codes (cheap before parsing items).
const hasCodes = `(product_code != '' OR position(props, '"items"') > 0)`

// RecoLists works out a workspace's popular (most buyers, 30 days), trending (most viewers,
// 7 days) and bought-together (90 days: per product, the 10 products most often bought by the
// same people) lists. buyEvent is the revenue event.
func (s *Store) RecoLists(ctx context.Context, tenant int64, buyEvent string, now time.Time) (catalog.Lists, error) {
	l := catalog.Lists{Also: map[string][]string{}}
	top := func(q string, args ...any) ([]string, error) {
		rows, err := s.conn.Query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var c string
			var n uint64
			if err := rows.Scan(&c, &n); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	var err error
	if l.Popular, err = top(`SELECT code, uniqExact(contact_id) AS n FROM events ARRAY JOIN `+codesExpr+` AS code
		WHERE tenant_id = ? AND name = ? AND ts >= ? AND `+hasCodes+` GROUP BY code ORDER BY n DESC, code LIMIT 50`,
		uint64(tenant), buyEvent, now.Add(-30*24*time.Hour).UTC()); err != nil {
		return l, err
	}
	if l.Trending, err = top(`SELECT code, uniqExact(contact_id) AS n FROM events ARRAY JOIN `+codesExpr+` AS code
		WHERE tenant_id = ? AND name != ? AND ts >= ? AND `+hasCodes+` GROUP BY code ORDER BY n DESC, code LIMIT 50`,
		uint64(tenant), buyEvent, now.Add(-7*24*time.Hour).UTC()); err != nil {
		return l, err
	}
	rows, err := s.conn.Query(ctx, `SELECT a, arrayMap(x -> x.2, arraySlice(arrayReverseSort(groupArray((n, b))), 1, 10)) FROM (
			SELECT a, b, count() AS n FROM (
				SELECT contact_id, groupUniqArray(50)(code) AS cs FROM events ARRAY JOIN `+codesExpr+` AS code
				WHERE tenant_id = ? AND name = ? AND ts >= ? AND `+hasCodes+` GROUP BY contact_id HAVING length(cs) >= 2)
			ARRAY JOIN cs AS a ARRAY JOIN cs AS b WHERE a != b GROUP BY a, b)
		GROUP BY a LIMIT 20000`, uint64(tenant), buyEvent, now.Add(-90*24*time.Hour).UTC())
	if err != nil {
		return l, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		var bs []string
		if err := rows.Scan(&a, &bs); err != nil {
			return l, err
		}
		l.Also[a] = bs
	}
	return l, rows.Err()
}
