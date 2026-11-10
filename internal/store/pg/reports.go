package pg

// Saved analytics reports (cohort and custom reports) and the workspace's revenue settings.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"pulse/internal/analytics"
	"pulse/internal/model"
)

const savedReportCols = `id, tenant_id, kind, name, spec, updated_at`

func scanSavedReport(row pgx.Row) (model.SavedReport, error) {
	var r model.SavedReport
	err := row.Scan(&r.ID, &r.TenantID, &r.Kind, &r.Name, &r.Spec, &r.UpdatedAt)
	return r, notFound(err)
}

func (s *Store) ListSavedReports(ctx context.Context, tenant int64, kind string) ([]model.SavedReport, error) {
	rows, err := s.db.Query(ctx, `SELECT `+savedReportCols+` FROM saved_reports WHERE tenant_id = $1 AND kind = $2 ORDER BY id DESC`, tenant, kind)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.SavedReport, error) { return scanSavedReport(r) })
}

func (s *Store) GetSavedReport(ctx context.Context, tenant int64, kind string, id int64) (model.SavedReport, error) {
	return scanSavedReport(s.db.QueryRow(ctx, `SELECT `+savedReportCols+` FROM saved_reports WHERE tenant_id = $1 AND kind = $2 AND id = $3`, tenant, kind, id))
}

// SaveReport creates (ID 0) or updates a saved report.
func (s *Store) SaveReport(ctx context.Context, r model.SavedReport) (int64, error) {
	if r.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO saved_reports (tenant_id, kind, name, spec) VALUES ($1, $2, $3, $4) RETURNING id`,
			r.TenantID, r.Kind, r.Name, []byte(r.Spec)).Scan(&r.ID)
		return r.ID, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE saved_reports SET name = $4, spec = $5, updated_at = now() WHERE tenant_id = $1 AND kind = $2 AND id = $3`,
		r.TenantID, r.Kind, r.ID, r.Name, []byte(r.Spec))
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return r.ID, err
}

func (s *Store) DeleteSavedReport(ctx context.Context, tenant int64, kind string, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM saved_reports WHERE tenant_id = $1 AND kind = $2 AND id = $3`, tenant, kind, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// RevenueSettings returns the workspace's revenue event settings (defaults when never set).
func (s *Store) RevenueSettings(ctx context.Context, tenant int64) (analytics.RevenueSettings, error) {
	rs := analytics.DefaultRevenue()
	var b []byte
	err := s.db.QueryRow(ctx, `SELECT revenue FROM tenant_settings WHERE tenant_id = $1`, tenant).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return rs, nil
	} else if err != nil {
		return rs, err
	}
	return rs, json.Unmarshal(b, &rs)
}

func (s *Store) PutRevenueSettings(ctx context.Context, tenant int64, rs analytics.RevenueSettings) error {
	b, _ := json.Marshal(rs)
	_, err := s.db.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, revenue) VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE SET revenue = EXCLUDED.revenue, updated_at = now()`, tenant, b)
	return err
}
