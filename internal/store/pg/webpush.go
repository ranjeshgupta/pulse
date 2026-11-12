package pg

// Web push settings (package webcheck), stored in tenant_settings.webpush.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"pulse/internal/webcheck"
)

// WebPushSettings returns a workspace's web push settings (defaults when never saved).
func (s *Store) WebPushSettings(ctx context.Context, tenant int64) (webcheck.Settings, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT webpush FROM tenant_settings WHERE tenant_id = $1`, tenant).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return webcheck.Defaults(), nil
	} else if err != nil {
		return webcheck.Defaults(), err
	}
	return webcheck.Load(raw)
}

// PutWebPushSettings stores them.
func (s *Store) PutWebPushSettings(ctx context.Context, tenant int64, w webcheck.Settings) error {
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, webpush) VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE SET webpush = EXCLUDED.webpush, updated_at = now()`, tenant, b)
	return err
}
