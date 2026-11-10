package pg

// Forms, and finding contacts by email or phone (form submissions, inbound SMS).

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const formCols = `id, tenant_id, name, public_key, fields, settings, status, updated_at`

func scanForm(row pgx.Row) (model.Form, error) {
	var f model.Form
	var fields, settings []byte
	err := row.Scan(&f.ID, &f.TenantID, &f.Name, &f.PublicKey, &fields, &settings, &f.Status, &f.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(fields, &f.Fields)
	}
	if err == nil {
		err = json.Unmarshal(settings, &f.Settings)
	}
	return f, notFound(err)
}

func (s *Store) ListForms(ctx context.Context, tenant int64) ([]model.Form, error) {
	rows, err := s.db.Query(ctx, `SELECT `+formCols+` FROM forms WHERE tenant_id = $1 ORDER BY id DESC`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Form, error) { return scanForm(r) })
}

func (s *Store) GetForm(ctx context.Context, tenant, id int64) (model.Form, error) {
	return scanForm(s.db.QueryRow(ctx, `SELECT `+formCols+` FROM forms WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// FormByKey finds a form by the public key in its URL (any workspace).
func (s *Store) FormByKey(ctx context.Context, key string) (model.Form, error) {
	return scanForm(s.db.QueryRow(ctx, `SELECT `+formCols+` FROM forms WHERE public_key = $1`, key))
}

// SaveForm creates (ID 0, with PublicKey set) or updates a form. The public key never changes.
func (s *Store) SaveForm(ctx context.Context, f model.Form) (int64, error) {
	fields, err := json.Marshal(f.Fields)
	if err != nil {
		return 0, err
	}
	settings, err := json.Marshal(f.Settings)
	if err != nil {
		return 0, err
	}
	if f.ID == 0 {
		err = s.db.QueryRow(ctx, `
			INSERT INTO forms (tenant_id, name, public_key, fields, settings, status) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			f.TenantID, f.Name, f.PublicKey, fields, settings, f.Status).Scan(&f.ID)
		return f.ID, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE forms SET name = $3, fields = $4, settings = $5, status = $6, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		f.TenantID, f.ID, f.Name, fields, settings, f.Status)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return f.ID, err
}

func (s *Store) DeleteForm(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM forms WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ContactByEmail finds the contact with this email (case-insensitive). With several, the oldest wins.
func (s *Store) ContactByEmail(ctx context.Context, tenant int64, email string) (model.Contact, error) {
	return scanContact(s.db.QueryRow(ctx, `
		SELECT `+contactCols+` FROM contacts WHERE tenant_id = $1 AND lower(email) = lower($2) AND email <> ''
		ORDER BY id LIMIT 1`, tenant, email))
}

// ContactsByPhone finds every contact whose phone attribute is this E.164 number.
func (s *Store) ContactsByPhone(ctx context.Context, tenant int64, phone string) ([]model.Contact, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+contactCols+` FROM contacts WHERE tenant_id = $1 AND attrs ? 'phone' AND attrs ->> 'phone' = $2
		ORDER BY id LIMIT 20`, tenant, phone)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Contact, error) { return scanContact(r) })
}
