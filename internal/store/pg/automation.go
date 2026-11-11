package pg

// Settings → Automation & limits (package automation), stored in tenant_settings.automation, and
// the Postgres side of the daily retention cleanup.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/automation"
)

// AutomationSettings returns a workspace's automation settings (defaults for anything never set).
func (s *Store) AutomationSettings(ctx context.Context, tenant int64) (automation.Settings, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT automation FROM tenant_settings WHERE tenant_id = $1`, tenant).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return automation.Defaults(), nil
	} else if err != nil {
		return automation.Defaults(), err
	}
	return automation.Load(raw)
}

// PutAutomationSettings stores a workspace's automation settings. The three retention values the
// Data & privacy page had are mirrored to the old retention column (read by nothing now; kept so
// an older Pulse still finds them).
func (s *Store) PutAutomationSettings(ctx context.Context, tenant int64, a automation.Settings) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	old, _ := json.Marshal(map[string]int{"anonymous_days": a.Retention.AnonymousDays, "event_days": a.Retention.EventDays, "audit_days": a.Retention.AuditDays})
	_, err = s.db.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, automation, retention) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET automation = EXCLUDED.automation, retention = EXCLUDED.retention, updated_at = now()`,
		tenant, b, old)
	return err
}

// RetentionTenants lists every workspace with its retention settings and last cleanup run.
// Workspaces that never saved settings are included: their defaults prune logs too.
func (s *Store) RetentionTenants(ctx context.Context) ([]automation.TenantRun, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, ts.automation, r.ran_at FROM tenants t
		LEFT JOIN tenant_settings ts ON ts.tenant_id = t.id
		LEFT JOIN retention_runs r ON r.tenant_id = t.id
		ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []automation.TenantRun
	for rows.Next() {
		var tr automation.TenantRun
		var raw []byte
		var ran *time.Time
		if err := rows.Scan(&tr.Tenant, &raw, &ran); err != nil {
			return nil, err
		}
		a, err := automation.Load(raw)
		if err != nil {
			continue // unreadable settings: skip rather than delete with the wrong ones
		}
		tr.Retention = a.Retention
		if ran != nil {
			tr.LastRun = *ran
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// ClaimRetentionRun records that this worker runs the workspace's cleanup now. It succeeds only
// when the last run is still prev (zero: never ran), so two workers never run the same day twice.
func (s *Store) ClaimRetentionRun(ctx context.Context, tenant int64, prev time.Time) (bool, error) {
	var tag interface{ RowsAffected() int64 }
	var err error
	if prev.IsZero() {
		tag, err = s.db.Exec(ctx, `INSERT INTO retention_runs (tenant_id, ran_at) VALUES ($1, now()) ON CONFLICT (tenant_id) DO NOTHING`, tenant)
	} else {
		tag, err = s.db.Exec(ctx, `UPDATE retention_runs SET ran_at = now() WHERE tenant_id = $1 AND ran_at = $2`, tenant, prev)
	}
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// logDeletes are the batched deletes of each Postgres log: $1 tenant, $2 days, $3 batch size.
var logDeletes = map[string]string{
	automation.PGAudit: `DELETE FROM audit_log WHERE id IN (
		SELECT id FROM audit_log WHERE tenant_id = $1 AND at < now() - make_interval(days => $2) LIMIT $3)`,
	// finished imports only (their uploaded chunks go with them); never contacts
	automation.PGImports: `DELETE FROM imports WHERE id IN (
		SELECT id FROM imports WHERE tenant_id = $1 AND created_at < now() - make_interval(days => $2)
		AND status IN ('done', 'failed', 'cancelled') LIMIT $3)`,
	automation.PGWebhookDeliveries: `DELETE FROM webhook_deliveries WHERE id IN (
		SELECT d.id FROM webhook_deliveries d JOIN webhooks w ON w.id = d.webhook_id
		WHERE w.tenant_id = $1 AND d.at < now() - make_interval(days => $2) LIMIT $3)`,
	automation.PGIntegrationLog: `DELETE FROM integration_log WHERE id IN (
		SELECT l.id FROM integration_log l JOIN integrations i ON i.id = l.integration_id
		WHERE i.tenant_id = $1 AND l.at < now() - make_interval(days => $2) LIMIT $3)`,
	automation.PGSandbox: `DELETE FROM sandbox_messages WHERE id IN (
		SELECT id FROM sandbox_messages WHERE tenant_id = $1 AND created_at < now() - make_interval(days => $2) LIMIT $3)`,
}

// logCounts count what logDeletes would delete ($1 tenant, $2 days), plus idle anonymous visitors.
var logCounts = map[string]string{
	automation.PGAudit:   `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND at < now() - make_interval(days => $2)`,
	automation.PGImports: `SELECT count(*) FROM imports WHERE tenant_id = $1 AND created_at < now() - make_interval(days => $2) AND status IN ('done', 'failed', 'cancelled')`,
	automation.PGWebhookDeliveries: `SELECT count(*) FROM webhook_deliveries d JOIN webhooks w ON w.id = d.webhook_id
		WHERE w.tenant_id = $1 AND d.at < now() - make_interval(days => $2)`,
	automation.PGIntegrationLog: `SELECT count(*) FROM integration_log l JOIN integrations i ON i.id = l.integration_id
		WHERE i.tenant_id = $1 AND l.at < now() - make_interval(days => $2)`,
	automation.PGSandbox: `SELECT count(*) FROM sandbox_messages WHERE tenant_id = $1 AND created_at < now() - make_interval(days => $2)`,
	automation.PGAnonymous: `SELECT count(*) FROM contacts WHERE tenant_id = $1 AND external_id LIKE 'anon-%'
		AND updated_at < now() - make_interval(days => $2)`,
}

// PruneLog deletes up to limit rows of a workspace's log kind older than days; callers loop
// until fewer than limit come back.
func (s *Store) PruneLog(ctx context.Context, tenant int64, kind string, days, limit int) (int64, error) {
	q, ok := logDeletes[kind]
	if !ok {
		return 0, fmt.Errorf("unknown log %q", kind)
	}
	tag, err := s.db.Exec(ctx, q, tenant, days, limit)
	return tag.RowsAffected(), err
}

// CountLog counts the rows of a workspace's log kind older than days.
func (s *Store) CountLog(ctx context.Context, tenant int64, kind string, days int) (int64, error) {
	q, ok := logCounts[kind]
	if !ok {
		return 0, fmt.Errorf("unknown log %q", kind)
	}
	var n int64
	err := s.db.QueryRow(ctx, q, tenant, days).Scan(&n)
	return n, err
}

// LastRetentionRun is when the workspace's daily cleanup last ran (zero: never).
func (s *Store) LastRetentionRun(ctx context.Context, tenant int64) (time.Time, error) {
	var t time.Time
	err := s.db.QueryRow(ctx, `SELECT ran_at FROM retention_runs WHERE tenant_id = $1`, tenant).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return t, err
}
