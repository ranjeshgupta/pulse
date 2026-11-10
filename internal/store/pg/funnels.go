package pg

// Saved funnels. Results are computed on demand in ClickHouse.

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const funnelCols = `id, tenant_id, name, steps, window_days, updated_at`

func scanFunnel(row pgx.Row) (model.Funnel, error) {
	var f model.Funnel
	var steps []byte
	err := row.Scan(&f.ID, &f.TenantID, &f.Name, &steps, &f.WindowDays, &f.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(steps, &f.Steps)
	}
	return f, notFound(err)
}

func (s *Store) ListFunnels(ctx context.Context, tenant int64) ([]model.Funnel, error) {
	rows, err := s.db.Query(ctx, `SELECT `+funnelCols+` FROM funnels WHERE tenant_id = $1 ORDER BY id DESC`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Funnel, error) { return scanFunnel(r) })
}

func (s *Store) GetFunnel(ctx context.Context, tenant, id int64) (model.Funnel, error) {
	return scanFunnel(s.db.QueryRow(ctx, `SELECT `+funnelCols+` FROM funnels WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// SaveFunnel creates (ID 0) or updates a funnel.
func (s *Store) SaveFunnel(ctx context.Context, f model.Funnel) (int64, error) {
	steps, err := json.Marshal(f.Steps)
	if err != nil {
		return 0, err
	}
	if f.ID == 0 {
		err = s.db.QueryRow(ctx, `INSERT INTO funnels (tenant_id, name, steps, window_days) VALUES ($1, $2, $3, $4) RETURNING id`,
			f.TenantID, f.Name, steps, f.WindowDays).Scan(&f.ID)
		return f.ID, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE funnels SET name = $3, steps = $4, window_days = $5, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		f.TenantID, f.ID, f.Name, steps, f.WindowDays)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return f.ID, err
}

func (s *Store) DeleteFunnel(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM funnels WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}
