package pg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

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
func (s *Store) DataSettings(ctx context.Context, tenant int64) (model.DataSettings, error) {
	d := model.DefaultDataSettings()
	var tracking, retention []byte
	err := s.db.QueryRow(ctx, `SELECT tracking, retention FROM tenant_settings WHERE tenant_id = $1`, tenant).Scan(&tracking, &retention)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	} else if err != nil {
		return d, err
	}
	if err := json.Unmarshal(tracking, &d.Tracking); err != nil {
		return d, err
	}
	return d, json.Unmarshal(retention, &d.Retention)
}

func (s *Store) PutDataSettings(ctx context.Context, tenant int64, d model.DataSettings) error {
	tracking, _ := json.Marshal(d.Tracking)
	retention, _ := json.Marshal(d.Retention)
	_, err := s.db.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, tracking, retention) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET tracking = EXCLUDED.tracking, retention = EXCLUDED.retention, updated_at = now()`,
		tenant, tracking, retention)
	return err
}

// Retentions lists the workspaces that delete data after a while, with their settings.
func (s *Store) Retentions(ctx context.Context) (map[int64]model.RetentionSettings, error) {
	rows, err := s.db.Query(ctx, `SELECT tenant_id, retention FROM tenant_settings
		WHERE (retention->>'anonymous_days')::int > 0 OR (retention->>'event_days')::int > 0 OR (retention->>'audit_days')::int > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]model.RetentionSettings{}
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var r model.RetentionSettings
		if json.Unmarshal(raw, &r) == nil {
			out[id] = r
		}
	}
	return out, rows.Err()
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

// PruneAudit deletes audit entries older than days.
func (s *Store) PruneAudit(ctx context.Context, tenant int64, days int) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id = $1 AND at < now() - make_interval(days => $2)`, tenant, days)
	return tag.RowsAffected(), err
}
