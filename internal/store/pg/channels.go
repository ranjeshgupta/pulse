package pg

// Delivery providers, push devices and the sandbox inbox.

import (
	"context"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const providerCols = `id, tenant_id, channel, provider, name, config, secrets_enc IS NOT NULL AND length(secrets_enc) > 0, is_default`

func scanProvider(row pgx.Row) (model.Provider, error) {
	var p model.Provider
	err := row.Scan(&p.ID, &p.TenantID, &p.Channel, &p.Kind, &p.Name, &p.Config, &p.HasSecrets, &p.IsDefault)
	if p.Config == nil {
		p.Config = map[string]string{}
	}
	return p, notFound(err)
}

func (s *Store) ListProviders(ctx context.Context, tenant int64) ([]model.Provider, error) {
	rows, err := s.db.Query(ctx, `SELECT `+providerCols+` FROM channel_providers WHERE tenant_id = $1 ORDER BY channel, id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Provider, error) { return scanProvider(r) })
}

func (s *Store) GetProvider(ctx context.Context, tenant, id int64) (model.Provider, error) {
	return scanProvider(s.db.QueryRow(ctx, `SELECT `+providerCols+` FROM channel_providers WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// SaveProvider creates (p.ID == 0) or updates a provider. A nil sealed keeps the stored secrets.
// Making it the default clears the previous default of that channel in the same transaction.
func (s *Store) SaveProvider(ctx context.Context, p model.Provider, sealed []byte) (int64, error) {
	id := p.ID
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if p.IsDefault {
			if _, err := tx.Exec(ctx, `UPDATE channel_providers SET is_default = false WHERE tenant_id = $1 AND channel = $2 AND id <> $3`,
				p.TenantID, p.Channel, p.ID); err != nil {
				return err
			}
		}
		if p.ID == 0 {
			return tx.QueryRow(ctx, `
				INSERT INTO channel_providers (tenant_id, channel, provider, name, config, secrets_enc, is_default)
				VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
				p.TenantID, p.Channel, p.Kind, p.Name, p.Config, sealed, p.IsDefault).Scan(&id)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE channel_providers SET name = $3, config = $4, is_default = $5, secrets_enc = coalesce($6, secrets_enc)
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, p.ID, p.Name, p.Config, p.IsDefault, sealed)
		if err == nil && tag.RowsAffected() == 0 {
			err = ErrNotFound
		}
		return err
	})
	return id, err
}

func (s *Store) DeleteProvider(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM channel_providers WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// DefaultProvider returns a channel's default provider and its sealed secrets.
func (s *Store) DefaultProvider(ctx context.Context, tenant int64, ch string) (model.Provider, []byte, error) {
	var p model.Provider
	var sealed []byte
	err := s.db.QueryRow(ctx, `
		SELECT id, tenant_id, channel, provider, name, config, coalesce(secrets_enc, ''::bytea), is_default
		FROM channel_providers WHERE tenant_id = $1 AND channel = $2 AND is_default`, tenant, ch).
		Scan(&p.ID, &p.TenantID, &p.Channel, &p.Kind, &p.Name, &p.Config, &sealed, &p.IsDefault)
	return p, sealed, notFound(err)
}

// ProviderForWebhook loads a provider by id alone (webhooks are not signed in); the caller must
// verify the request with the provider's secrets before trusting it.
func (s *Store) ProviderForWebhook(ctx context.Context, id int64) (model.Provider, []byte, error) {
	var p model.Provider
	var sealed []byte
	err := s.db.QueryRow(ctx, `
		SELECT id, tenant_id, channel, provider, name, config, coalesce(secrets_enc, ''::bytea), is_default
		FROM channel_providers WHERE id = $1`, id).
		Scan(&p.ID, &p.TenantID, &p.Channel, &p.Kind, &p.Name, &p.Config, &sealed, &p.IsDefault)
	return p, sealed, notFound(err)
}

// ---- push devices ----

func (s *Store) UpsertDevice(ctx context.Context, tenant int64, contact uint32, d model.Device) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO contact_devices (tenant_id, token, contact_id, platform) VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, token) DO UPDATE SET contact_id = EXCLUDED.contact_id, platform = EXCLUDED.platform`,
		tenant, d.Token, int64(contact), d.Platform)
	return err
}

func (s *Store) ContactDevices(ctx context.Context, tenant int64, contact uint32) ([]model.Device, error) {
	rows, err := s.db.Query(ctx, `SELECT token, platform FROM contact_devices WHERE tenant_id = $1 AND contact_id = $2`, tenant, int64(contact))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Device])
}

// DeleteDevice drops a token the push service reported as dead.
func (s *Store) DeleteDevice(ctx context.Context, tenant int64, token string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM contact_devices WHERE tenant_id = $1 AND token = $2`, tenant, token)
	return err
}

// ---- sandbox inbox ----

func (s *Store) RecordSandbox(ctx context.Context, tenant int64, ch, recipient, messageID string, payload []byte) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO sandbox_messages (tenant_id, channel, recipient, message_id, payload) VALUES ($1, $2, $3, $4, $5)`,
		tenant, ch, recipient, messageID, payload)
	return err
}

func (s *Store) ListSandbox(ctx context.Context, tenant int64, ch string, limit int) ([]model.SandboxMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, channel, recipient, message_id, payload, created_at FROM sandbox_messages
		WHERE tenant_id = $1 AND ($2 = '' OR channel = $2) ORDER BY id DESC LIMIT $3`, tenant, ch, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.SandboxMessage])
}
