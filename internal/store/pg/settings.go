package pg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"pulse/internal/automation"
	"pulse/internal/delivery"
	"pulse/internal/model"
)

// DeliveryRules returns a workspace's frequency caps and quiet hours (defaults when never set).
func (s *Store) DeliveryRules(ctx context.Context, tenant int64) (delivery.Rules, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT delivery FROM tenant_settings WHERE tenant_id = $1`, tenant).Scan(&raw)
	r := delivery.Default()
	if errors.Is(err, pgx.ErrNoRows) {
		return r, nil
	} else if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return delivery.Default(), err
	}
	if r.Caps == nil {
		r.Caps = map[string]delivery.Cap{}
	}
	return r, nil
}

func (s *Store) PutDeliveryRules(ctx context.Context, tenant int64, r delivery.Rules) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, delivery) VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE SET delivery = EXCLUDED.delivery, updated_at = now()`, tenant, b)
	return err
}

// DataSettings returns a workspace's tracking and retention settings (defaults when never set).
// Retention lives in the automation settings (Settings → Automation & limits) now; these are its
// three original values.
func (s *Store) DataSettings(ctx context.Context, tenant int64) (model.DataSettings, error) {
	d := model.DefaultDataSettings()
	var tracking, auto []byte
	err := s.db.QueryRow(ctx, `SELECT tracking, automation FROM tenant_settings WHERE tenant_id = $1`, tenant).Scan(&tracking, &auto)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	} else if err != nil {
		return d, err
	}
	if err := json.Unmarshal(tracking, &d.Tracking); err != nil {
		return d, err
	}
	a, err := automation.Load(auto)
	d.Retention = model.RetentionSettings{AnonymousDays: a.Retention.AnonymousDays, EventDays: a.Retention.EventDays, AuditDays: a.Retention.AuditDays}
	return d, err
}

// PutDataSettings stores tracking, and merges the three retention values into the automation
// settings, keeping every other automation setting (per-name retention, retries, ...) as it is.
func (s *Store) PutDataSettings(ctx context.Context, tenant int64, d model.DataSettings) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var auto []byte
		err := tx.QueryRow(ctx, `SELECT automation FROM tenant_settings WHERE tenant_id = $1 FOR UPDATE`, tenant).Scan(&auto)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		a, err := automation.Load(auto)
		if err != nil {
			return err
		}
		a.Retention.AnonymousDays, a.Retention.EventDays, a.Retention.AuditDays = d.Retention.AnonymousDays, d.Retention.EventDays, d.Retention.AuditDays
		tracking, _ := json.Marshal(d.Tracking)
		retention, _ := json.Marshal(d.Retention)
		ab, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO tenant_settings (tenant_id, tracking, retention, automation) VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id) DO UPDATE SET tracking = EXCLUDED.tracking, retention = EXCLUDED.retention,
				automation = EXCLUDED.automation, updated_at = now()`,
			tenant, tracking, retention, ab)
		return err
	})
}

// IdleAnonymous returns up to limit anonymous visitors not updated for days.
func (s *Store) IdleAnonymous(ctx context.Context, tenant int64, days, limit int) ([]uint32, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM contacts WHERE tenant_id = $1 AND external_id LIKE 'anon-%'
		AND updated_at < now() - make_interval(days => $2) ORDER BY updated_at LIMIT $3`, tenant, days, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (uint32, error) {
		var id int64
		err := r.Scan(&id)
		return uint32(id), err
	})
}

// DeleteContacts removes contacts and what Postgres keeps for them (devices, inbox, journey state,
// files uploaded through forms).
func (s *Store) DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error {
	big := make([]int64, len(ids))
	for i, id := range ids {
		big[i] = int64(id)
	}
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		for _, q := range []string{
			`DELETE FROM contact_devices WHERE tenant_id = $1 AND contact_id = ANY($2)`,
			`DELETE FROM inbox_messages WHERE tenant_id = $1 AND contact_id = ANY($2)`,
			`DELETE FROM journey_enrollments WHERE tenant_id = $1 AND contact_id = ANY($2)`,
			// files they uploaded through forms
			`DELETE FROM assets WHERE tenant_id = $1 AND upload_form_id IS NOT NULL AND upload_external_id <> ''
				AND upload_external_id IN (SELECT external_id FROM contacts WHERE tenant_id = $1 AND id = ANY($2))`,
			`DELETE FROM contacts WHERE tenant_id = $1 AND id = ANY($2)`,
		} {
			if _, err := tx.Exec(ctx, q, tenant, big); err != nil {
				return err
			}
		}
		return nil
	})
}
