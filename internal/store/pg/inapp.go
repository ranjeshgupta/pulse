package pg

// In-app campaigns.

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const inappCols = `id, tenant_id, name, layout, content, targeting, priority, status, start_at, end_at, updated_at`

func scanInApp(row pgx.Row) (model.InAppMessage, error) {
	var m model.InAppMessage
	var content, targeting []byte
	err := row.Scan(&m.ID, &m.TenantID, &m.Name, &m.Layout, &content, &targeting, &m.Priority, &m.Status, &m.StartAt, &m.EndAt, &m.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(content, &m.Content)
	}
	if err == nil {
		err = json.Unmarshal(targeting, &m.Targeting)
	}
	return m, notFound(err)
}

// ListInApp lists a workspace's campaigns; activeOnly skips drafts.
func (s *Store) ListInApp(ctx context.Context, tenant int64, activeOnly bool) ([]model.InAppMessage, error) {
	rows, err := s.db.Query(ctx, `SELECT `+inappCols+` FROM inapp_messages WHERE tenant_id = $1 AND (NOT $2 OR status = 'active')
		ORDER BY priority DESC, id DESC`, tenant, activeOnly)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.InAppMessage, error) { return scanInApp(r) })
}

func (s *Store) GetInApp(ctx context.Context, tenant, id int64) (model.InAppMessage, error) {
	return scanInApp(s.db.QueryRow(ctx, `SELECT `+inappCols+` FROM inapp_messages WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

func (s *Store) SaveInApp(ctx context.Context, m model.InAppMessage) (int64, error) {
	content, _ := json.Marshal(m.Content)
	targeting, _ := json.Marshal(m.Targeting)
	if m.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO inapp_messages (tenant_id, name, layout, content, targeting, priority, status, start_at, end_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			m.TenantID, m.Name, m.Layout, content, targeting, m.Priority, m.Status, m.StartAt, m.EndAt).Scan(&m.ID)
		return m.ID, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE inapp_messages SET name = $3, layout = $4, content = $5, targeting = $6, priority = $7, status = $8,
		start_at = $9, end_at = $10, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		m.TenantID, m.ID, m.Name, m.Layout, content, targeting, m.Priority, m.Status, m.StartAt, m.EndAt)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return m.ID, err
}

func (s *Store) DeleteInApp(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM inapp_messages WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}
