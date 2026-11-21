package pg

// Focus items and Dynamic Web Content slots.

import (
	"context"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const focusCols = `id, tenant_id, name, kind, content, targeting, status, start_at, end_at, updated_at`

func scanFocus(row pgx.Row) (model.FocusItem, error) {
	var f model.FocusItem
	err := row.Scan(&f.ID, &f.TenantID, &f.Name, &f.Kind, &f.Content, &f.Targeting, &f.Status, &f.StartAt, &f.EndAt, &f.UpdatedAt)
	return f, notFound(err)
}

// ListFocusItems lists a workspace's focus items; activeOnly is for the decision API.
func (s *Store) ListFocusItems(ctx context.Context, tenant int64, activeOnly bool) ([]model.FocusItem, error) {
	rows, err := s.db.Query(ctx, `SELECT `+focusCols+` FROM focus_items
		WHERE tenant_id = $1 AND (NOT $2 OR status = 'active') ORDER BY coalesce((targeting->>'priority')::int, 0), id DESC`, tenant, activeOnly)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.FocusItem, error) { return scanFocus(r) })
}

func (s *Store) GetFocusItem(ctx context.Context, tenant, id int64) (model.FocusItem, error) {
	return scanFocus(s.db.QueryRow(ctx, `SELECT `+focusCols+` FROM focus_items WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

func (s *Store) SaveFocusItem(ctx context.Context, f model.FocusItem) (int64, error) {
	if f.ID == 0 {
		err := s.db.QueryRow(ctx, `
			INSERT INTO focus_items (tenant_id, name, kind, content, targeting, status, start_at, end_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			f.TenantID, f.Name, f.Kind, f.Content, f.Targeting, f.Status, f.StartAt, f.EndAt).Scan(&f.ID)
		return f.ID, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE focus_items SET name = $3, kind = $4, content = $5, targeting = $6, status = $7, start_at = $8, end_at = $9, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, f.TenantID, f.ID, f.Name, f.Kind, f.Content, f.Targeting, f.Status, f.StartAt, f.EndAt)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return f.ID, err
}

func (s *Store) DeleteFocusItem(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM focus_items WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

const dwcCols = `id, tenant_id, slot, name, platform, default_html, default_card, variants, status, start_at, end_at, updated_at`

func scanDWC(row pgx.Row) (model.DWCSlot, error) {
	var d model.DWCSlot
	err := row.Scan(&d.ID, &d.TenantID, &d.Slot, &d.Name, &d.Platform, &d.DefaultHTML, &d.DefaultCard, &d.Variants, &d.Status, &d.StartAt, &d.EndAt, &d.UpdatedAt)
	if d.Variants == nil {
		d.Variants = []model.DWCVariant{}
	}
	return d, notFound(err)
}

func (s *Store) ListDWCSlots(ctx context.Context, tenant int64, activeOnly bool) ([]model.DWCSlot, error) {
	rows, err := s.db.Query(ctx, `SELECT `+dwcCols+` FROM dwc_slots
		WHERE tenant_id = $1 AND (NOT $2 OR status = 'active') ORDER BY slot`, tenant, activeOnly)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.DWCSlot, error) { return scanDWC(r) })
}

func (s *Store) GetDWCSlot(ctx context.Context, tenant, id int64) (model.DWCSlot, error) {
	return scanDWC(s.db.QueryRow(ctx, `SELECT `+dwcCols+` FROM dwc_slots WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

func (s *Store) SaveDWCSlot(ctx context.Context, d model.DWCSlot) (int64, error) {
	if d.ID == 0 {
		err := s.db.QueryRow(ctx, `
			INSERT INTO dwc_slots (tenant_id, slot, name, default_html, variants, status, start_at, end_at, platform, default_card) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			d.TenantID, d.Slot, d.Name, d.DefaultHTML, d.Variants, d.Status, d.StartAt, d.EndAt, d.Platform, d.DefaultCard).Scan(&d.ID)
		return d.ID, conflict(err)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE dwc_slots SET slot = $3, name = $4, default_html = $5, variants = $6, status = $7, start_at = $8, end_at = $9,
			platform = $10, default_card = $11, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, d.TenantID, d.ID, d.Slot, d.Name, d.DefaultHTML, d.Variants, d.Status, d.StartAt, d.EndAt, d.Platform, d.DefaultCard)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return d.ID, conflict(err)
}

func (s *Store) DeleteDWCSlot(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM dwc_slots WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// SetFocusPriorities orders focus items: the i-th id gets priority i+1 (in its targeting). Ids of
// other workspaces are ignored.
func (s *Store) SetFocusPriorities(ctx context.Context, tenant int64, ids []int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE focus_items f SET targeting = jsonb_set(f.targeting, '{priority}', to_jsonb(o.ord::int)), updated_at = now()
		FROM unnest($2::bigint[]) WITH ORDINALITY AS o(id, ord)
		WHERE f.tenant_id = $1 AND f.id = o.id`, tenant, ids)
	return err
}
