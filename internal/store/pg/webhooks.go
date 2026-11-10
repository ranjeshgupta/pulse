package pg

// Outbound webhooks and their delivery log.

import (
	"context"
	"math/rand/v2"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
	"pulse/internal/webhooks"
)

const webhookCols = `id, tenant_id, name, url, events, active, secret, created_at, updated_at`

func scanWebhook(row pgx.Row) (model.Webhook, error) {
	var h model.Webhook
	err := row.Scan(&h.ID, &h.TenantID, &h.Name, &h.URL, &h.Events, &h.Active, &h.Sealed, &h.CreatedAt, &h.UpdatedAt)
	return h, notFound(err)
}

func (s *Store) ListWebhooks(ctx context.Context, tenant int64) ([]model.Webhook, error) {
	rows, err := s.db.Query(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Webhook, error) { return scanWebhook(r) })
}

func (s *Store) GetWebhook(ctx context.Context, tenant, id int64) (model.Webhook, error) {
	return scanWebhook(s.db.QueryRow(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// SaveWebhook creates (ID 0, Sealed set) or updates a webhook; a non-nil Sealed replaces the secret.
func (s *Store) SaveWebhook(ctx context.Context, h model.Webhook) (int64, error) {
	if h.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO webhooks (tenant_id, name, url, events, active, secret) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			h.TenantID, h.Name, h.URL, h.Events, h.Active, h.Sealed).Scan(&h.ID)
		return h.ID, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE webhooks SET name = $3, url = $4, events = $5, active = $6, secret = coalesce($7, secret), updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, h.TenantID, h.ID, h.Name, h.URL, h.Events, h.Active, h.Sealed)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return h.ID, err
}

func (s *Store) DeleteWebhook(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM webhooks WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ActiveWebhookRows lists every active webhook of every workspace (secrets still sealed).
func (s *Store) ActiveWebhookRows(ctx context.Context) ([]model.Webhook, error) {
	rows, err := s.db.Query(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE active`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Webhook, error) { return scanWebhook(r) })
}

// RecordDelivery logs an attempt; now and then it prunes attempts older than a week.
func (s *Store) RecordDelivery(ctx context.Context, d webhooks.Delivery) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO webhook_deliveries (webhook_id, event_id, event_name, attempt, status, error, duration_ms, final)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8 WHERE EXISTS (SELECT 1 FROM webhooks WHERE id = $1)`,
		d.WebhookID, d.EventID, d.EventName, d.Attempt, d.Status, d.Error, d.DurationMS, d.Final)
	if err == nil && rand.IntN(200) == 0 {
		_, err = s.db.Exec(ctx, `DELETE FROM webhook_deliveries WHERE at < now() - interval '7 days'`)
	}
	return err
}

func (s *Store) WebhookDeliveries(ctx context.Context, tenant, id int64, limit int) ([]model.WebhookDelivery, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.id, d.event_id, d.event_name, d.at, d.attempt, d.status, d.error, d.duration_ms, d.final
		FROM webhook_deliveries d JOIN webhooks h ON h.id = d.webhook_id
		WHERE h.tenant_id = $1 AND h.id = $2 ORDER BY d.id DESC LIMIT $3`, tenant, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.WebhookDelivery])
}
