package ch

// M4 dashboards: Users (DAU / WAU / MAU from the active_users_daily rollup, new vs returning from
// first_active) and Products (the events' product_code / product_category columns).

import (
	"context"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/segment"
)

// KeyCount is one value of a breakdown.
type KeyCount struct {
	Key   string `json:"key"`
	Count uint64 `json:"count"`
}

// UsersReport is the Users dashboard.
type UsersReport struct {
	Days       []analytics.UsersDay `json:"days"`
	Active     uint64               `json:"active"` // active at least once in the range
	DAU        uint64               `json:"dau"`    // on the last day
	WAU        uint64               `json:"wau"`
	MAU        uint64               `json:"mau"`
	Stickiness float64              `json:"stickiness"`
	New        uint64               `json:"new"` // first active in the range
	Returning  uint64               `json:"returning"`
	ByPlatform []KeyCount           `json:"by_platform"`
	BySource   []KeyCount           `json:"by_source"`
}

// UsersFilter narrows the Users dashboard to one platform and/or source.
type UsersFilter struct {
	Platform, Source string
	HasSource        bool // Source "" means "(none)" when set
}

func (f UsersFilter) where() (string, []any) {
	w, args := "", []any{}
	if f.Platform != "" {
		w += " AND platform = ?"
		args = append(args, f.Platform)
	}
	if f.HasSource {
		w += " AND source = ?"
		args = append(args, f.Source)
	}
	return w, args
}

// Users builds the Users dashboard for the UTC days [from, to] (both included).
func (s *Store) Users(ctx context.Context, tenant int64, from, to time.Time, f UsersFilter) (UsersReport, error) {
	rep := UsersReport{ByPlatform: []KeyCount{}, BySource: []KeyCount{}}
	fw, fargs := f.where()
	fromD, toD := from.Format(time.DateOnly), to.Format(time.DateOnly)
	// Each day's state is spread over the 30 days it counts towards (k = 0..29): DAU merges k < 1,
	// WAU k < 7 and MAU every k, for each day of the range.
	args := append([]any{uint64(tenant), from.AddDate(0, 0, -29).Format(time.DateOnly), toD}, fargs...)
	args = append(args, fromD, toD)
	rows, err := s.conn.Query(ctx, `
		SELECT toString(d), uniqMergeIf(users, k < 1), uniqMergeIf(users, k < 7), uniqMerge(users) FROM (
			SELECT addDays(day, k) AS d, k, users FROM active_users_daily ARRAY JOIN range(30) AS k
			WHERE tenant_id = ? AND day >= toDate(?) AND day <= toDate(?)`+fw+`
		) WHERE d >= toDate(?) AND d <= toDate(?) GROUP BY d ORDER BY d`, args...)
	if err != nil {
		return rep, err
	}
	got := map[string]analytics.UsersDay{}
	for rows.Next() {
		var u analytics.UsersDay
		if err := rows.Scan(&u.Day, &u.DAU, &u.WAU, &u.MAU); err != nil {
			rows.Close()
			return rep, err
		}
		got[u.Day] = u
	}
	rows.Close()
	newByDay := map[string]uint64{}
	if f.Platform == "" && !f.HasSource { // first activity isn't kept per platform or source
		nrows, err := s.conn.Query(ctx, `
			SELECT toString(f), count() FROM (SELECT contact_id, min(first) AS f FROM first_active WHERE tenant_id = ? GROUP BY contact_id)
			WHERE f >= toDate(?) AND f <= toDate(?) GROUP BY f`, uint64(tenant), fromD, toD)
		if err != nil {
			return rep, err
		}
		for nrows.Next() {
			var d string
			var n uint64
			if err := nrows.Scan(&d, &n); err != nil {
				nrows.Close()
				return rep, err
			}
			newByDay[d] = n
			rep.New += n
		}
		nrows.Close()
	}
	rep.Days = analytics.FillUsers(from, to, got, newByDay)
	if n := len(rep.Days); n > 0 {
		last := rep.Days[n-1]
		rep.DAU, rep.WAU, rep.MAU = last.DAU, last.WAU, last.MAU
	}
	rep.Stickiness = analytics.Stickiness(rep.Days)
	rargs := append([]any{uint64(tenant), fromD, toD}, fargs...)
	if err := s.conn.QueryRow(ctx, `SELECT uniqMerge(users) FROM active_users_daily WHERE tenant_id = ? AND day >= toDate(?) AND day <= toDate(?)`+fw,
		rargs...).Scan(&rep.Active); err != nil {
		return rep, err
	}
	if rep.New <= rep.Active {
		rep.Returning = rep.Active - rep.New
	}
	breakdown := func(col string, dst *[]KeyCount) error {
		rows, err := s.conn.Query(ctx, `SELECT `+col+`, uniqMerge(users) AS n FROM active_users_daily
			WHERE tenant_id = ? AND day >= toDate(?) AND day <= toDate(?)`+fw+` GROUP BY `+col+` ORDER BY n DESC LIMIT 20`, rargs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k KeyCount
			if err := rows.Scan(&k.Key, &k.Count); err != nil {
				return err
			}
			*dst = append(*dst, k)
		}
		return rows.Err()
	}
	if err := breakdown("platform", &rep.ByPlatform); err != nil {
		return rep, err
	}
	return rep, breakdown("source", &rep.BySource)
}

// ProductEvents name the events the Products dashboard reads.
type ProductEvents struct {
	View, Cart, Purchase, AmountProp string
}

// ProductRow is one product's (or category's) numbers.
type ProductRow struct {
	Code      string  `json:"code"`
	Category  string  `json:"category,omitempty"`
	Views     uint64  `json:"views"`
	Viewers   uint64  `json:"viewers"`
	Carts     uint64  `json:"carts"`
	Carters   uint64  `json:"carters"`
	Purchases uint64  `json:"purchases"`
	Buyers    uint64  `json:"buyers"`
	Revenue   float64 `json:"revenue"`
	Abandoned uint64  `json:"abandoned"` // added to cart, didn't buy it in the range
	// BoughtNoCart (totals only): people who bought a product with no add-to-cart event in the
	// range, from a buy-now button, an order sent by a server or app, or a cart added earlier. It's
	// why people buying can outnumber people adding to cart.
	BoughtNoCart uint64 `json:"bought_no_cart,omitempty"`
}

// ProductDay is one day of the Products dashboard.
type ProductDay struct {
	Day       string  `json:"day"`
	Views     uint64  `json:"views"`
	Carts     uint64  `json:"carts"`
	Purchases uint64  `json:"purchases"`
	Revenue   float64 `json:"revenue"`
}

// ProductsReport is the Products dashboard.
type ProductsReport struct {
	Totals     ProductRow   `json:"totals"` // Code empty; contacts counted once across products
	Products   []ProductRow `json:"products"`
	Categories []ProductRow `json:"categories"`
	Days       []ProductDay `json:"days"`
}

// Products builds the Products dashboard for [from, to): views, carts, purchases and revenue per
// product (top 100 by revenue, then purchases), per category, per day, and abandoned carts. Only
// events naming a product (a product_code property) count, in the totals too, so they agree with
// the product rows; orders without one show on the Revenue dashboard instead.
func (s *Store) Products(ctx context.Context, tenant int64, from, to time.Time, pe ProductEvents) (ProductsReport, error) {
	rep := ProductsReport{Products: []ProductRow{}, Categories: []ProductRow{}, Days: []ProductDay{}}
	amount, aargs := segment.PropValueSQL(pe.AmountProp)
	// One row per event id (the pipeline can deliver one twice before ClickHouse merges copies).
	src := `SELECT id, any(name) AS n, any(contact_id) AS c, any(product_code) AS code, any(product_category) AS cat,
		any(toFloat64OrZero(` + amount + `)) AS amt, any(ts) AS t FROM events
		WHERE tenant_id = ? AND name IN (?, ?, ?) AND ts >= ? AND ts < ? AND product_code != '' GROUP BY id`
	srcArgs := append(append([]any{}, aargs...), uint64(tenant), pe.View, pe.Cart, pe.Purchase, from, to)
	cols := `countIf(n = ?), uniqExactIf(c, n = ?), countIf(n = ?), uniqExactIf(c, n = ?), countIf(n = ?), uniqExactIf(c, n = ?), sumIf(amt, n = ?)`
	colArgs := []any{pe.View, pe.View, pe.Cart, pe.Cart, pe.Purchase, pe.Purchase, pe.Purchase}
	scan := func(sc interface{ Scan(...any) error }, r *ProductRow, lead ...any) error {
		return sc.Scan(append(lead, &r.Views, &r.Viewers, &r.Carts, &r.Carters, &r.Purchases, &r.Buyers, &r.Revenue)...)
	}
	if err := scan(s.conn.QueryRow(ctx, "SELECT "+cols+" FROM ("+src+")", append(colArgs, srcArgs...)...), &rep.Totals); err != nil {
		return rep, err
	}
	// abandoned: contacts (per product, and overall) who added to cart and didn't buy it
	abandoned := func(key string) (map[string]uint64, error) {
		rows, err := s.conn.Query(ctx, `SELECT k, countIf(carted AND NOT bought) FROM (
			SELECT `+key+` AS k, c, max(n = ?) AS carted, max(n = ?) AS bought FROM (`+src+`) GROUP BY k, c) GROUP BY k`,
			append([]any{pe.Cart, pe.Purchase}, srcArgs...)...)
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
	all, err := abandoned("''")
	if err != nil {
		return rep, err
	}
	rep.Totals.Abandoned = all[""]
	if err := s.conn.QueryRow(ctx, `SELECT countIf(bought AND NOT carted) FROM (
		SELECT c, max(n = ?) AS carted, max(n = ?) AS bought FROM (`+src+`) GROUP BY c)`,
		append([]any{pe.Cart, pe.Purchase}, srcArgs...)...).Scan(&rep.Totals.BoughtNoCart); err != nil {
		return rep, err
	}
	group := func(key, catExpr string, dst *[]ProductRow) error {
		rows, err := s.conn.Query(ctx, "SELECT "+key+" AS k, "+catExpr+", "+cols+" FROM ("+src+") WHERE "+key+" != ''"+
			" GROUP BY k ORDER BY 9 DESC, 7 DESC, k LIMIT 100", append(colArgs, srcArgs...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r ProductRow
			if err := scan(rows, &r, &r.Code, &r.Category); err != nil {
				return err
			}
			*dst = append(*dst, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		ab, err := abandoned(key)
		if err != nil {
			return err
		}
		for i := range *dst {
			(*dst)[i].Abandoned = ab[(*dst)[i].Code]
		}
		return nil
	}
	if err := group("code", "any(cat)", &rep.Products); err != nil {
		return rep, err
	}
	if err := group("cat", "''", &rep.Categories); err != nil {
		return rep, err
	}
	rows, err := s.conn.Query(ctx, `SELECT toString(toDate(t, 'UTC')) AS d, countIf(n = ?), countIf(n = ?), countIf(n = ?), sumIf(amt, n = ?)
		FROM (`+src+`) GROUP BY d ORDER BY d`, append([]any{pe.View, pe.Cart, pe.Purchase, pe.Purchase}, srcArgs...)...)
	if err != nil {
		return rep, err
	}
	defer rows.Close()
	for rows.Next() {
		var d ProductDay
		if err := rows.Scan(&d.Day, &d.Views, &d.Carts, &d.Purchases, &d.Revenue); err != nil {
			return rep, err
		}
		rep.Days = append(rep.Days, d)
	}
	return rep, rows.Err()
}
