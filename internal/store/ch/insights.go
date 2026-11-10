package ch

// Analytics reports: cohorts, revenue attribution, custom reports, funnel breakdowns and journey
// A/B test stats. The SQL is compiled (and unit-tested) in internal/analytics and internal/segment;
// these methods run it and scan the rows.

import (
	"context"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/segment"
)

// CohortCounts runs a cohort report's two queries (analytics.CohortQueries).
func (s *Store) CohortCounts(ctx context.Context, sizes, cells segment.Query) (map[int64]uint64, []analytics.CohortCell, error) {
	out := map[int64]uint64{}
	rows, err := s.conn.Query(ctx, sizes.SQL, sizes.Args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var d time.Time
		var n uint64
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		out[day(d).Unix()] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	crows, err := s.conn.Query(ctx, cells.SQL, cells.Args...)
	if err != nil {
		return nil, nil, err
	}
	defer crows.Close()
	var cs []analytics.CohortCell
	for crows.Next() {
		var c analytics.CohortCell
		var idx int32
		if err := crows.Scan(&c.Cohort, &idx, &c.N); err != nil {
			return nil, nil, err
		}
		c.Cohort, c.Index = day(c.Cohort), int(idx)
		cs = append(cs, c)
	}
	return out, cs, crows.Err()
}

// day normalizes a scanned Date to midnight UTC.
func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// RevenueTotals runs analytics.RevenueTotalsSQL.
func (s *Store) RevenueTotals(ctx context.Context, q segment.Query) (orders uint64, revenue float64, buyers uint64, err error) {
	err = s.conn.QueryRow(ctx, q.SQL, q.Args...).Scan(&orders, &revenue, &buyers)
	return
}

// AttributedPurchases runs analytics.AttributionSQL: each purchase's touch summary.
func (s *Store) AttributedPurchases(ctx context.Context, q segment.Query) ([]analytics.Purchase, error) {
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []analytics.Purchase
	for rows.Next() {
		var p analytics.Purchase
		var first, last string
		var keys []string
		if err := rows.Scan(&p.Amount, &first, &last, &keys); err != nil {
			return nil, err
		}
		if p.First, err = analytics.ParseTouchKey(first); err != nil {
			return nil, err
		}
		if p.Last, err = analytics.ParseTouchKey(last); err != nil {
			return nil, err
		}
		for _, k := range keys {
			tk, err := analytics.ParseTouchKey(k)
			if err != nil {
				return nil, err
			}
			p.Keys = append(p.Keys, tk)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Revenue is a broadcast's or journey's revenue from recipients.
type Revenue struct {
	Revenue float64 `json:"revenue"`
	Orders  uint64  `json:"orders"`
	Buyers  uint64  `json:"buyers"`
}

// RecipientRevenue runs analytics.RecipientRevenueSQL, by key.
func (s *Store) RecipientRevenue(ctx context.Context, q segment.Query) (map[string]Revenue, error) {
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Revenue{}
	for rows.Next() {
		var k string
		var r Revenue
		if err := rows.Scan(&k, &r.Revenue, &r.Orders, &r.Buyers); err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, rows.Err()
}

// ReportRows runs analytics.ReportSQL.
func (s *Store) ReportRows(ctx context.Context, q segment.Query) ([]analytics.ReportRow, error) {
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analytics.ReportRow{}
	for rows.Next() {
		var r analytics.ReportRow
		if err := rows.Scan(&r.Group, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BreakdownRow is one attribute value of a funnel breakdown: contacts reaching each step.
type BreakdownRow struct {
	Value   string   `json:"value"`
	Reached []uint64 `json:"reached"`
}

// FunnelBreakdown runs segment.FunnelBreakdownSQL.
func (s *Store) FunnelBreakdown(ctx context.Context, q segment.Query, steps int) ([]BreakdownRow, error) {
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BreakdownRow
	for rows.Next() {
		r := BreakdownRow{Reached: make([]uint64, steps)}
		dest := []any{&r.Value}
		for i := range r.Reached {
			dest = append(dest, &r.Reached[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SplitStats reads a journey split step's per-path test results.
func (s *Store) SplitStats(ctx context.Context, sq analytics.SplitQuery) ([]analytics.PathStat, error) {
	q, err := analytics.SplitStatsSQL(sq)
	if err != nil {
		return nil, err
	}
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []analytics.PathStat
	for rows.Next() {
		var p analytics.PathStat
		var path int64
		if err := rows.Scan(&path, &p.Entrants, &p.Hits, &p.FirstAt); err != nil {
			return nil, err
		}
		p.Path = int(path)
		p.Rate = analytics.Rate(p)
		out = append(out, p)
	}
	return out, rows.Err()
}
