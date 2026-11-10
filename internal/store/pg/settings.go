package pg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"pulse/internal/delivery"
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
