package pg

// Reusable content blocks (O6): {{block.key}} in templates.

import (
	"context"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const blockCols = `id, tenant_id, key, name, html, text, updated_at`

func scanBlock(r pgx.Row) (model.ContentBlock, error) {
	var b model.ContentBlock
	err := r.Scan(&b.ID, &b.TenantID, &b.Key, &b.Name, &b.HTML, &b.Text, &b.UpdatedAt)
	return b, notFound(err)
}

// ListBlocks lists a workspace's content blocks by name.
func (s *Store) ListBlocks(ctx context.Context, tenant int64) ([]model.ContentBlock, error) {
	rows, err := s.db.Query(ctx, `SELECT `+blockCols+` FROM content_blocks WHERE tenant_id = $1 ORDER BY name`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.ContentBlock, error) { return scanBlock(r) })
}

// GetBlock loads one block.
func (s *Store) GetBlock(ctx context.Context, tenant, id int64) (model.ContentBlock, error) {
	return scanBlock(s.db.QueryRow(ctx, `SELECT `+blockCols+` FROM content_blocks WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// SaveBlock creates (ID 0) or updates a block; its key is unique in the workspace.
func (s *Store) SaveBlock(ctx context.Context, b model.ContentBlock) (int64, error) {
	if b.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO content_blocks (tenant_id, key, name, html, text) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			b.TenantID, b.Key, b.Name, b.HTML, b.Text).Scan(&b.ID)
		return b.ID, conflict(err)
	}
	tag, err := s.db.Exec(ctx, `UPDATE content_blocks SET key = $3, name = $4, html = $5, text = $6, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		b.TenantID, b.ID, b.Key, b.Name, b.HTML, b.Text)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return b.ID, conflict(err)
}

// DeleteBlock removes a block (templates using it then render it empty).
func (s *Store) DeleteBlock(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM content_blocks WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// TemplatesUsingBlock lists the templates whose content names {{block.key}}.
func (s *Store) TemplatesUsingBlock(ctx context.Context, tenant int64, key string) ([]model.Template, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, channel FROM templates WHERE tenant_id = $1
		AND (html ~ $2 OR subject ~ $2 OR content::text ~ $2 OR translations::text ~ $2) ORDER BY name LIMIT 200`, tenant, `\{\{\s*block\.`+key+`\s*\}\}`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Template
	for rows.Next() {
		var t model.Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Channel); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TemplateIDsUsingBlock is every template naming {{block.key}} (no limit: approval checks need all).
func (s *Store) TemplateIDsUsingBlock(ctx context.Context, tenant int64, key string) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM templates WHERE tenant_id = $1
		AND (html ~ $2 OR subject ~ $2 OR content::text ~ $2 OR translations::text ~ $2)`, tenant, `\{\{\s*block\.`+key+`\s*\}\}`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
