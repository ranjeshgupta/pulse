package pg

// Outbound webhooks and their delivery log.

import (
	"context"
	"math/rand/v2"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
	"pulse/internal/webhooks"
)

const webhookCols = `id, tenant_id, name, url, events, active, secret, created_at, updated_at,
	coalesce(integration_id, 0), segment_id, form_id`

func scanWebhook(row pgx.Row) (model.Webhook, error) {
	var h model.Webhook
	err := row.Scan(&h.ID, &h.TenantID, &h.Name, &h.URL, &h.Events, &h.Active, &h.Sealed, &h.CreatedAt, &h.UpdatedAt,
		&h.IntegrationID, &h.SegmentID, &h.FormID)
	return h, notFound(err)
}

// ListWebhooks lists the workspace's own webhooks (not REST hook subscriptions of integrations).
func (s *Store) ListWebhooks(ctx context.Context, tenant int64) ([]model.Webhook, error) {
	rows, err := s.db.Query(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE tenant_id = $1 AND integration_id IS NULL ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Webhook, error) { return scanWebhook(r) })
}

func (s *Store) GetWebhook(ctx context.Context, tenant, id int64) (model.Webhook, error) {
	return scanWebhook(s.db.QueryRow(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE tenant_id = $1 AND id = $2 AND integration_id IS NULL`, tenant, id))
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
		WHERE tenant_id = $1 AND id = $2 AND integration_id IS NULL`, h.TenantID, h.ID, h.Name, h.URL, h.Events, h.Active, h.Sealed)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return h.ID, err
}

func (s *Store) DeleteWebhook(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM webhooks WHERE tenant_id = $1 AND id = $2 AND integration_id IS NULL`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ActiveWebhookRows lists every active webhook of every workspace (secrets still sealed).
func (s *Store) ActiveWebhookRows(ctx context.Context) ([]model.Webhook, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+webhookCols+` FROM webhooks w
		WHERE active AND (integration_id IS NULL OR EXISTS (SELECT 1 FROM integrations i WHERE i.id = w.integration_id AND i.active))`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Webhook, error) { return scanWebhook(r) })
}

// RecordDelivery logs an attempt; now and then it prunes attempts older than a week.
func (s *Store) RecordDelivery(ctx context.Context, d webhooks.Delivery) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO webhook_deliveries (webhook_id, event_id, event_name, attempt, status, error, duration_ms, final, test)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9 WHERE EXISTS (SELECT 1 FROM webhooks WHERE id = $1)`,
		d.WebhookID, d.EventID, d.EventName, d.Attempt, d.Status, d.Error, d.DurationMS, d.Final, d.Test)
	if err == nil && rand.IntN(200) == 0 {
		_, err = s.db.Exec(ctx, `DELETE FROM webhook_deliveries WHERE at < now() - interval '7 days'`)
	}
	return err
}

// ---- REST hook subscriptions (owned by an integration) ----

// HookSubscriptions lists an integration's subscriptions.
func (s *Store) HookSubscriptions(ctx context.Context, tenant, integration int64) ([]model.Webhook, error) {
	rows, err := s.db.Query(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE tenant_id = $1 AND integration_id = $2 ORDER BY id`, tenant, integration)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Webhook, error) { return scanWebhook(r) })
}

// SubscribeHook adds a subscription for an integration.
func (s *Store) SubscribeHook(ctx context.Context, h model.Webhook) (int64, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO webhooks (tenant_id, name, url, events, active, secret, integration_id, segment_id, form_id)
		VALUES ($1, $2, $3, $4, true, $5, $6, $7, $8) RETURNING id`,
		h.TenantID, h.Name, h.URL, h.Events, h.Sealed, h.IntegrationID, h.SegmentID, h.FormID).Scan(&h.ID)
	return h.ID, err
}

// UnsubscribeHook removes one of an integration's subscriptions.
func (s *Store) UnsubscribeHook(ctx context.Context, tenant, integration, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM webhooks WHERE tenant_id = $1 AND integration_id = $2 AND id = $3`, tenant, integration, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// DeleteHookSubscription removes a subscription whose receiver answered 410 Gone.
func (s *Store) DeleteHookSubscription(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM webhooks WHERE id = $1 AND integration_id IS NOT NULL`, id)
	return err
}

// HookDeliveries are the latest delivery attempts of an integration's subscriptions.
func (s *Store) HookDeliveries(ctx context.Context, tenant, integration int64, limit int) ([]model.WebhookDelivery, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.id, d.event_id, d.event_name, d.at, d.attempt, d.status, d.error, d.duration_ms, d.final, d.test
		FROM webhook_deliveries d JOIN webhooks h ON h.id = d.webhook_id
		WHERE h.tenant_id = $1 AND h.integration_id = $2 ORDER BY d.id DESC LIMIT $3`, tenant, integration, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.WebhookDelivery])
}

func (s *Store) WebhookDeliveries(ctx context.Context, tenant, id int64, limit int) ([]model.WebhookDelivery, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.id, d.event_id, d.event_name, d.at, d.attempt, d.status, d.error, d.duration_ms, d.final, d.test
		FROM webhook_deliveries d JOIN webhooks h ON h.id = d.webhook_id
		WHERE h.tenant_id = $1 AND h.id = $2 ORDER BY d.id DESC LIMIT $3`, tenant, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.WebhookDelivery])
}
