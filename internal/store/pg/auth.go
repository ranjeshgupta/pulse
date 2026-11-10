package pg

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"pulse/internal/model"
)

// ErrConflict is returned when a unique value (workspace slug, user email) is taken.
var ErrConflict = errors.New("already exists")

func conflict(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrConflict
	}
	return err
}

// ---- workspaces ----

// CreateTenant creates a workspace without users (admin API and tests).
func (s *Store) CreateTenant(ctx context.Context, name, slug, keyHash, siteKey string) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		id, err = createTenant(ctx, tx, name, slug, keyHash, siteKey)
		return err
	})
	return id, conflict(err)
}

func createTenant(ctx context.Context, tx pgx.Tx, name, slug, keyHash, siteKey string) (int64, error) {
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name, slug, api_key_hash, site_key) VALUES ($1, $2, $3, $4) RETURNING id`,
		name, slug, keyHash, siteKey).Scan(&id); err != nil {
		return 0, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO tenant_counters (tenant_id) VALUES ($1)`, id)
	return id, err
}

// CreateWorkspace creates a workspace and its first (admin) user in one transaction.
func (s *Store) CreateWorkspace(ctx context.Context, name, slug, keyHash, siteKey string, owner model.User) (tenantID, userID int64, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if tenantID, err = createTenant(ctx, tx, name, slug, keyHash, siteKey); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`INSERT INTO users (tenant_id, email, name, password_hash, role) VALUES ($1, $2, $3, $4, 'admin') RETURNING id`,
			tenantID, normEmail(owner.Email), owner.Name, owner.PasswordHash).Scan(&userID)
	})
	return tenantID, userID, conflict(err)
}

func scanTenant(row pgx.Row) (model.Tenant, error) {
	var t model.Tenant
	err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.SiteKey, &t.IdentitySecret, &t.RequireSignedIdentify)
	return t, notFound(err)
}

func (s *Store) Tenant(ctx context.Context, id int64) (model.Tenant, error) {
	return scanTenant(s.db.QueryRow(ctx, `SELECT id, name, slug, site_key, identity_secret, require_signed_identify FROM tenants WHERE id = $1`, id))
}

func (s *Store) TenantBySlug(ctx context.Context, slug string) (model.Tenant, error) {
	return scanTenant(s.db.QueryRow(ctx, `SELECT id, name, slug, site_key, identity_secret, require_signed_identify FROM tenants WHERE slug = $1`, slug))
}

func (s *Store) TenantBySiteKey(ctx context.Context, key string) (model.Tenant, error) {
	return scanTenant(s.db.QueryRow(ctx, `SELECT id, name, slug, site_key, identity_secret, require_signed_identify FROM tenants WHERE site_key = $1`, key))
}

func (s *Store) RotateAPIKey(ctx context.Context, tenant int64, keyHash string) error {
	_, err := s.db.Exec(ctx, `UPDATE tenants SET api_key_hash = $2 WHERE id = $1`, tenant, keyHash)
	return err
}

// ---- users ----

const userCols = `id, tenant_id, email, name, role, coalesce(password_hash, ''), coalesce(sso_subject, ''), created_at, last_login_at`

func scanUser(row pgx.Row) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.SSOSubject, &u.CreatedAt, &u.LastLoginAt)
	u.SSO = u.SSOSubject != ""
	return u, notFound(err)
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func (s *Store) UserByEmail(ctx context.Context, tenant int64, email string) (model.User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE tenant_id = $1 AND email = $2`, tenant, normEmail(email)))
}

func (s *Store) ListUsers(ctx context.Context, tenant int64) ([]model.User, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userCols+` FROM users WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.User, error) { return scanUser(r) })
}

func (s *Store) CreateUser(ctx context.Context, u model.User) (int64, error) {
	var id int64
	var hash any
	if u.PasswordHash != "" {
		hash = u.PasswordHash
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, name, password_hash, role) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		u.TenantID, normEmail(u.Email), u.Name, hash, u.Role).Scan(&id)
	return id, conflict(err)
}

func (s *Store) DeleteUser(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM users WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// SSOUser finds the user for a verified SSO identity: by subject, then by email (linking the subject),
// then creates one when the workspace allows it.
func (s *Store) SSOUser(ctx context.Context, tenant int64, subject, email, name string, autoCreate bool, role string) (model.User, error) {
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE tenant_id = $1 AND sso_subject = $2`, tenant, subject))
	if !errors.Is(err, ErrNotFound) {
		return u, err
	}
	u, err = scanUser(s.db.QueryRow(ctx,
		`UPDATE users SET sso_subject = $3 WHERE tenant_id = $1 AND email = $2 AND sso_subject IS NULL RETURNING `+userCols,
		tenant, normEmail(email), subject))
	if !errors.Is(err, ErrNotFound) || !autoCreate {
		return u, err
	}
	return scanUser(s.db.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, name, sso_subject, role) VALUES ($1, $2, $3, $4, $5) RETURNING `+userCols,
		tenant, normEmail(email), name, subject, role))
}

// ---- sessions ----

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID, tenantID int64, expires time.Time) error {
	if _, err := s.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash, userID, tenantID, expires); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID)
	return err
}

// SessionPrincipal resolves a live session to its user.
func (s *Store) SessionPrincipal(ctx context.Context, tokenHash string) (model.Principal, error) {
	var p model.Principal
	err := s.db.QueryRow(ctx, `
		SELECT u.tenant_id, u.id, u.role FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).Scan(&p.TenantID, &p.UserID, &p.Role)
	return p, notFound(err)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) User(ctx context.Context, tenant, id int64) (model.User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// ---- SSO settings ----

// GetSSO returns the config and the sealed client secret.
func (s *Store) GetSSO(ctx context.Context, tenant int64) (model.SSOConfig, []byte, error) {
	var c model.SSOConfig
	var secret []byte
	err := s.db.QueryRow(ctx, `
		SELECT enabled, issuer, discovery_url, client_id, client_secret_enc, auto_create_users, default_role
		FROM tenant_sso WHERE tenant_id = $1`, tenant).
		Scan(&c.Enabled, &c.Issuer, &c.DiscoveryURL, &c.ClientID, &secret, &c.AutoCreateUsers, &c.DefaultRole)
	c.HasClientSecret = len(secret) > 0
	return c, secret, notFound(err)
}

// PutSSO saves the config. A nil sealedSecret keeps the stored one.
func (s *Store) PutSSO(ctx context.Context, tenant int64, c model.SSOConfig, sealedSecret []byte) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO tenant_sso (tenant_id, enabled, issuer, discovery_url, client_id, client_secret_enc, auto_create_users, default_role)
		VALUES ($1, $2, $3, $4, $5, coalesce($6, ''::bytea), $7, $8)
		ON CONFLICT (tenant_id) DO UPDATE SET
			enabled = EXCLUDED.enabled, issuer = EXCLUDED.issuer, discovery_url = EXCLUDED.discovery_url,
			client_id = EXCLUDED.client_id, auto_create_users = EXCLUDED.auto_create_users,
			default_role = EXCLUDED.default_role, updated_at = now(),
			client_secret_enc = coalesce($6, tenant_sso.client_secret_enc)`,
		tenant, c.Enabled, c.Issuer, c.DiscoveryURL, c.ClientID, sealedSecret, c.AutoCreateUsers, c.DefaultRole)
	return err
}
