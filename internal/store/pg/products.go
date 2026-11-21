package pg

// The product catalog (package catalog).

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const productCols = `code, name, url, image_url, price, sale_price, currency, category, in_stock, attrs, updated_at`

func scanProduct(r pgx.CollectableRow) (model.Product, error) {
	var p model.Product
	var attrs []byte
	err := r.Scan(&p.Code, &p.Name, &p.URL, &p.ImageURL, &p.Price, &p.SalePrice, &p.Currency, &p.Category, &p.InStock, &attrs, &p.UpdatedAt)
	if err == nil && len(attrs) > 2 {
		err = json.Unmarshal(attrs, &p.Attrs)
	}
	return p, err
}

// UpsertProducts adds or replaces products by code.
func (s *Store) UpsertProducts(ctx context.Context, tenant int64, ps []model.Product) error {
	b := &pgx.Batch{}
	for _, p := range ps {
		attrs, _ := json.Marshal(p.Attrs)
		if p.Attrs == nil {
			attrs = []byte("{}")
		}
		b.Queue(`INSERT INTO products (tenant_id, code, name, url, image_url, price, sale_price, currency, category, in_stock, attrs)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (tenant_id, code) DO UPDATE SET name = $3, url = $4, image_url = $5, price = $6, sale_price = $7,
				currency = $8, category = $9, in_stock = $10, attrs = $11, updated_at = now()`,
			tenant, strings.TrimSpace(p.Code), p.Name, p.URL, p.ImageURL, p.Price, p.SalePrice, strings.ToUpper(p.Currency), p.Category, p.InStock, attrs)
	}
	return s.db.SendBatch(ctx, b).Close()
}

// ListProducts pages through the catalog, filtered by code, name or category (q).
func (s *Store) ListProducts(ctx context.Context, tenant int64, q string, limit, offset int) ([]model.Product, int, error) {
	where, args := `tenant_id = $1`, []any{tenant}
	if q = strings.TrimSpace(q); q != "" {
		args = append(args, "%"+strings.ToLower(likeEscape(q))+"%")
		where += ` AND (lower(code) LIKE $2 OR lower(name) LIKE $2 OR lower(category) LIKE $2)`
	}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM products WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+productCols+` FROM products WHERE `+where+` ORDER BY updated_at DESC, code LIMIT $`+strconv.Itoa(len(args)+1)+` OFFSET $`+strconv.Itoa(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	out, err := pgx.CollectRows(rows, scanProduct)
	if out == nil {
		out = []model.Product{}
	}
	return out, total, err
}

// ProductsByCode returns the catalog's products among codes.
func (s *Store) ProductsByCode(ctx context.Context, tenant int64, codes []string) (map[string]model.Product, error) {
	out := map[string]model.Product{}
	if len(codes) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+productCols+` FROM products WHERE tenant_id = $1 AND code = ANY($2)`, tenant, codes)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, scanProduct)
	for _, p := range list {
		out[p.Code] = p
	}
	return out, err
}

func (s *Store) DeleteProduct(ctx context.Context, tenant int64, code string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM products WHERE tenant_id = $1 AND code = $2`, tenant, code)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}
