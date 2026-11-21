package ch

// Customer value (package rfm): buyers' recency, frequency and revenue from the revenue event.

import (
	"context"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/rfm"
)

// buyersSQL is one row per buyer since since: (buyer, first, last, orders, revenue).
func buyersSQL(tenant int64, ev, amountProp string, since, now time.Time) (string, []any) {
	p, args := analytics.PurchasesSQL(tenant, analytics.RevenueSettings{Event: ev, AmountProp: amountProp}, since, now.Add(time.Hour))
	return "SELECT buyer, min(at) AS first, max(at) AS last, count() AS n, sum(amount) AS m FROM (" + p + ") GROUP BY buyer", args
}

// latestContacts is each contact's latest snapshot: (id, external_id, attrs).
const latestContacts = `SELECT id, argMax(external_id, version) AS external_id, argMax(attrs, version) AS attrs
	FROM contacts WHERE tenant_id = ? GROUP BY id`

func (s *Store) RFMCuts(ctx context.Context, tenant int64, ev, amountProp string, since, now time.Time) (rfm.Cuts, error) {
	b, args := buyersSQL(tenant, ev, amountProp, since, now)
	var r, f, m []float64
	var c rfm.Cuts
	err := s.conn.QueryRow(ctx, `SELECT quantiles(0.2, 0.4, 0.6, 0.8)(dateDiff('second', last, ?) / 86400.0),
		quantiles(0.2, 0.4, 0.6, 0.8)(toFloat64(n)), quantiles(0.2, 0.4, 0.6, 0.8)(m), count() FROM (`+b+`)`,
		append([]any{now.UTC()}, args...)...).Scan(&r, &f, &m, &c.Buyers)
	if err != nil {
		return c, err
	}
	copy(c.Recency[:], r)
	copy(c.Frequency[:], f)
	copy(c.Monetary[:], m)
	return c, nil
}

func (s *Store) RFMBuyers(ctx context.Context, tenant int64, ev, amountProp string, since, now time.Time, fn func(rfm.Buyer) error) error {
	b, args := buyersSQL(tenant, ev, amountProp, since, now)
	rows, err := s.conn.Query(ctx, `SELECT b.buyer, c.external_id, b.first, b.last, b.n, b.m FROM (`+b+`) AS b
		INNER JOIN (`+latestContacts+`) AS c ON c.id = b.buyer`, append(args, uint64(tenant))...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var x rfm.Buyer
		if err := rows.Scan(&x.Contact, &x.ExternalID, &x.First, &x.Last, &x.Orders, &x.Revenue); err != nil {
			return err
		}
		if err := fn(x); err != nil {
			return err
		}
	}
	return rows.Err()
}

// RFMGroupRow is one customer-value group and churn risk in the report.
type RFMGroupRow struct {
	Group     string  `json:"group"`
	Churn     string  `json:"churn_risk"`
	Contacts  uint64  `json:"contacts"`
	Revenue   float64 `json:"revenue"`
	Orders    float64 `json:"avg_orders"`
	DaysSince float64 `json:"avg_days_since"`
}

// RFMReport groups the scored buyers (by their rfm_group and churn_risk attributes) with their
// revenue, average orders and days since the last purchase.
func (s *Store) RFMReport(ctx context.Context, tenant int64, ev, amountProp string, now time.Time) ([]RFMGroupRow, error) {
	b, args := buyersSQL(tenant, ev, amountProp, now.Add(-rfm.Window), now)
	q := `SELECT c.attrs['rfm_group'] AS g, c.attrs['churn_risk'] AS r, count(), sum(b.m), avg(b.n), avg(dateDiff('second', b.last, ?) / 86400.0)
		FROM (` + b + `) AS b INNER JOIN (` + latestContacts + `) AS c ON c.id = b.buyer
		WHERE g != '' GROUP BY g, r`
	rows, err := s.conn.Query(ctx, q, append(append([]any{now.UTC()}, args...), uint64(tenant))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RFMGroupRow
	for rows.Next() {
		var x RFMGroupRow
		if err := rows.Scan(&x.Group, &x.Churn, &x.Contacts, &x.Revenue, &x.Orders, &x.DaysSince); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
