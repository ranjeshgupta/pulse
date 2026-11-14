package pg

// Two-step sign-in: each user's authenticator-app secret (sealed by the caller), the last code step
// used and hashed recovery codes (user_two_factor), and the workspace policy
// (tenant_settings.two_factor).

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"pulse/internal/auth"
	"pulse/internal/model"
)

// TwoFactor returns a user's setup; ErrNotFound when they never started one.
func (s *Store) TwoFactor(ctx context.Context, tenant, user int64) (model.TwoFactor, error) {
	var t model.TwoFactor
	err := s.db.QueryRow(ctx, `
		SELECT secret_enc, pending_enc, enabled_at, last_step, cardinality(recovery_hashes)
		FROM user_two_factor WHERE tenant_id = $1 AND user_id = $2`, tenant, user).
		Scan(&t.Secret, &t.Pending, &t.EnabledAt, &t.LastStep, &t.RecoveryLeft)
	return t, notFound(err)
}

// StartTwoFactor stores a new, unconfirmed secret. A confirmed one stays in use until the new one
// is confirmed (moving to a new phone). Without replace (the caller checked a current code or the
// password) it refuses with ErrConflict when two-step sign-in is already on.
func (s *Store) StartTwoFactor(ctx context.Context, tenant, user int64, sealed []byte, replace bool) error {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO user_two_factor (user_id, tenant_id, pending_enc) VALUES ($2, $1, $3)
		ON CONFLICT (user_id) DO UPDATE SET pending_enc = EXCLUDED.pending_enc, updated_at = now()
		WHERE user_two_factor.tenant_id = EXCLUDED.tenant_id AND ($4 OR user_two_factor.secret_enc IS NULL)`,
		tenant, user, sealed, replace)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrConflict
	}
	return err
}

// EnableTwoFactor makes the pending secret (the one the caller checked a code against) the one in
// use, with step as the last code used and fresh recovery codes. ErrNotFound when the pending
// secret changed meanwhile; without replace, ErrConflict when two-step sign-in is already on.
func (s *Store) EnableTwoFactor(ctx context.Context, tenant, user int64, pending []byte, step int64, recovery []string, replace bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE user_two_factor SET secret_enc = pending_enc, pending_enc = NULL, enabled_at = now(),
			last_step = $4, recovery_hashes = $5, updated_at = now()
		WHERE tenant_id = $1 AND user_id = $2 AND pending_enc = $3 AND ($6 OR secret_enc IS NULL)`,
		tenant, user, pending, step, recovery, replace)
	if err == nil && tag.RowsAffected() == 0 {
		var on bool
		if s.db.QueryRow(ctx, `SELECT secret_enc IS NOT NULL FROM user_two_factor WHERE tenant_id = $1 AND user_id = $2`,
			tenant, user).Scan(&on) == nil && on && !replace {
			return ErrConflict
		}
		err = ErrNotFound
	}
	return err
}

// UseTwoFactorStep records step as used; false when it (or a later one) already was, so two
// requests with the same code can't both pass.
func (s *Store) UseTwoFactorStep(ctx context.Context, tenant, user, step int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE user_two_factor SET last_step = $3, updated_at = now()
		WHERE tenant_id = $1 AND user_id = $2 AND secret_enc IS NOT NULL AND last_step < $3`, tenant, user, step)
	return err == nil && tag.RowsAffected() == 1, err
}

// UseRecoveryCode removes a recovery code; false when it isn't one of the user's unused codes.
func (s *Store) UseRecoveryCode(ctx context.Context, tenant, user int64, hash string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE user_two_factor SET recovery_hashes = array_remove(recovery_hashes, $3), updated_at = now()
		WHERE tenant_id = $1 AND user_id = $2 AND secret_enc IS NOT NULL AND $3 = ANY (recovery_hashes)`, tenant, user, hash)
	return err == nil && tag.RowsAffected() == 1, err
}

// SetRecoveryCodes replaces a user's recovery codes.
func (s *Store) SetRecoveryCodes(ctx context.Context, tenant, user int64, hashes []string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE user_two_factor SET recovery_hashes = $3, updated_at = now()
		WHERE tenant_id = $1 AND user_id = $2 AND secret_enc IS NOT NULL`, tenant, user, hashes)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// DeleteTwoFactor turns two-step sign-in off for a user (their own choice, or an admin's reset).
func (s *Store) DeleteTwoFactor(ctx context.Context, tenant, user int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM user_two_factor WHERE tenant_id = $1 AND user_id = $2`, tenant, user)
	return err
}

// TwoFactorPolicy returns who in a workspace must use two-step sign-in (optional when never saved).
func (s *Store) TwoFactorPolicy(ctx context.Context, tenant int64) (auth.TwoFactorPolicy, error) {
	var p auth.TwoFactorPolicy
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT two_factor FROM tenant_settings WHERE tenant_id = $1`, tenant).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return p, err
		}
	}
	return p, p.Normalize()
}

// PutTwoFactorPolicy stores it.
func (s *Store) PutTwoFactorPolicy(ctx context.Context, tenant int64, p auth.TwoFactorPolicy) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, two_factor) VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE SET two_factor = EXCLUDED.two_factor, updated_at = now()`, tenant, b)
	return err
}
