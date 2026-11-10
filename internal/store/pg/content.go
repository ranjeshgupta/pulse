package pg

// Assets (files kept in Postgres, apart from their metadata) and landing pages.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

// ErrQuota is returned when a workspace's assets would exceed their total size cap.
var ErrQuota = errors.New("the workspace's files would exceed their storage limit")

const assetCols = `id, tenant_id, public_key, title, description, filename, content_type, size, sha256, form_id, download_count, created_at, updated_at`

func scanAsset(row pgx.Row) (model.Asset, error) {
	var a model.Asset
	err := row.Scan(&a.ID, &a.TenantID, &a.PublicKey, &a.Title, &a.Description, &a.Filename, &a.ContentType, &a.Size, &a.SHA256,
		&a.FormID, &a.DownloadCount, &a.CreatedAt, &a.UpdatedAt)
	return a, notFound(err)
}

func (s *Store) ListAssets(ctx context.Context, tenant int64) ([]model.Asset, error) {
	rows, err := s.db.Query(ctx, `SELECT `+assetCols+` FROM assets WHERE tenant_id = $1 ORDER BY id DESC`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Asset, error) { return scanAsset(r) })
}

func (s *Store) GetAsset(ctx context.Context, tenant, id int64) (model.Asset, error) {
	return scanAsset(s.db.QueryRow(ctx, `SELECT `+assetCols+` FROM assets WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// AssetByKey finds an asset by the public key in its download URL (any workspace).
func (s *Store) AssetByKey(ctx context.Context, key string) (model.Asset, error) {
	return scanAsset(s.db.QueryRow(ctx, `SELECT `+assetCols+` FROM assets WHERE public_key = $1`, key))
}

// AssetsByIDs returns the workspace's assets among ids (missing ones are left out).
func (s *Store) AssetsByIDs(ctx context.Context, tenant int64, ids []int64) ([]model.Asset, error) {
	rows, err := s.db.Query(ctx, `SELECT `+assetCols+` FROM assets WHERE tenant_id = $1 AND id = ANY($2) ORDER BY id`, tenant, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Asset, error) { return scanAsset(r) })
}

// CreateAsset stores a new asset and its file, if the workspace stays within maxTotal bytes. The
// workspace row is locked meanwhile, so two uploads can't both slip under the cap.
func (s *Store) CreateAsset(ctx context.Context, a model.Asset, data []byte, maxTotal int64) (int64, error) {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM tenants WHERE id = $1 FOR UPDATE`, a.TenantID); err != nil {
			return err
		}
		var used int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(size), 0) FROM assets WHERE tenant_id = $1`, a.TenantID).Scan(&used); err != nil {
			return err
		}
		if used+int64(len(data)) > maxTotal {
			return ErrQuota
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO assets (tenant_id, public_key, title, description, filename, content_type, size, sha256, form_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			a.TenantID, a.PublicKey, a.Title, a.Description, a.Filename, a.ContentType, len(data), a.SHA256, a.FormID).Scan(&a.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO asset_files (asset_id, data) VALUES ($1, $2)`, a.ID, data)
		return err
	})
	return a.ID, err
}

// UpdateAsset changes an asset's title, description and gating form (never its file).
func (s *Store) UpdateAsset(ctx context.Context, a model.Asset) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE assets SET title = $3, description = $4, form_id = $5, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		a.TenantID, a.ID, a.Title, a.Description, a.FormID)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) DeleteAsset(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM assets WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// AssetFile returns an asset with its bytes.
func (s *Store) AssetFile(ctx context.Context, tenant, id int64) (model.Asset, []byte, error) {
	a, err := s.GetAsset(ctx, tenant, id)
	if err != nil {
		return a, nil, err
	}
	var data []byte
	err = s.db.QueryRow(ctx, `SELECT data FROM asset_files WHERE asset_id = $1`, id).Scan(&data)
	return a, data, notFound(err)
}

// CountAssetDownload adds one to an asset's download count.
func (s *Store) CountAssetDownload(ctx context.Context, tenant, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE assets SET download_count = download_count + 1 WHERE tenant_id = $1 AND id = $2`, tenant, id)
	return err
}

// TemplatesUsingAsset names the templates that attach an asset.
func (s *Store) TemplatesUsingAsset(ctx context.Context, tenant, id int64) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT name FROM templates WHERE tenant_id = $1 AND channel = 'email' AND content -> 'attachments' @> jsonb_build_array($2::bigint)
		ORDER BY name`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---- landing pages ----

const pageCols = `id, tenant_id, slug, title, meta_description, html, builder, status, start_at, end_at, created_at, updated_at`

func scanPage(row pgx.Row) (model.LandingPage, error) {
	var p model.LandingPage
	err := row.Scan(&p.ID, &p.TenantID, &p.Slug, &p.Title, &p.MetaDescription, &p.HTML, &p.Builder, &p.Status, &p.StartAt, &p.EndAt,
		&p.CreatedAt, &p.UpdatedAt)
	return p, notFound(err)
}

// ListLandingPages lists a workspace's pages without their HTML and builder state.
func (s *Store) ListLandingPages(ctx context.Context, tenant int64) ([]model.LandingPage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, tenant_id, slug, title, meta_description, '', NULL::jsonb, status, start_at, end_at, created_at, updated_at
		FROM landing_pages WHERE tenant_id = $1 ORDER BY updated_at DESC`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.LandingPage, error) { return scanPage(r) })
}

func (s *Store) GetLandingPage(ctx context.Context, tenant, id int64) (model.LandingPage, error) {
	return scanPage(s.db.QueryRow(ctx, `SELECT `+pageCols+` FROM landing_pages WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

func (s *Store) LandingPageBySlug(ctx context.Context, tenant int64, slug string) (model.LandingPage, error) {
	return scanPage(s.db.QueryRow(ctx, `SELECT `+pageCols+` FROM landing_pages WHERE tenant_id = $1 AND slug = $2`, tenant, slug))
}

// SaveLandingPage creates (ID 0) or updates a page. A slug the workspace already uses is ErrConflict.
func (s *Store) SaveLandingPage(ctx context.Context, p model.LandingPage) (int64, error) {
	if p.ID == 0 {
		err := s.db.QueryRow(ctx, `
			INSERT INTO landing_pages (tenant_id, slug, title, meta_description, html, builder, status, start_at, end_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			p.TenantID, p.Slug, p.Title, p.MetaDescription, p.HTML, nullJSON(p.Builder), p.Status, p.StartAt, p.EndAt).Scan(&p.ID)
		return p.ID, conflict(err)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE landing_pages SET slug = $3, title = $4, meta_description = $5, html = $6, builder = $7, status = $8,
			start_at = $9, end_at = $10, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`,
		p.TenantID, p.ID, p.Slug, p.Title, p.MetaDescription, p.HTML, nullJSON(p.Builder), p.Status, p.StartAt, p.EndAt)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return p.ID, conflict(err)
}

func (s *Store) DeleteLandingPage(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM landing_pages WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}
