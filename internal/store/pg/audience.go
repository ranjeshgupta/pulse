package pg

// Audience data: static lists, attribute definitions, scoring rules and identity verification.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

// CreateList makes a static list: a segment with no rule, ready at once, never backfilled or swept.
func (s *Store) CreateList(ctx context.Context, tenant int64, name string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO segments (tenant_id, name, kind, rule, has_window, status, built_at)
		VALUES ($1, $2, 'static', NULL, false, 'ready', now()) RETURNING id`, tenant, name).Scan(&id)
	return id, err
}

// UpdateList renames a static list and, when public or description are given, changes how the
// preference center offers it.
func (s *Store) UpdateList(ctx context.Context, tenant, id int64, name string, public *bool, description *string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE segments SET name = $3, public = coalesce($4, public), description = coalesce($5, description)
		WHERE tenant_id = $1 AND id = $2 AND kind = 'static'`, tenant, id, name, public, description)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// AttributeDef describes one profile attribute for the app: a label, a type that decides which
// comparisons are offered and how imports coerce values, and whether it holds personal data.
type AttributeDef struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Type        string    `json:"type"` // string | number | boolean | date
	PII         bool      `json:"pii"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) AttributeDefs(ctx context.Context, tenant int64) ([]AttributeDef, error) {
	rows, err := s.db.Query(ctx, `
		SELECT key, label, type, pii, description, created_at FROM attribute_defs WHERE tenant_id = $1 ORDER BY key`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AttributeDef, error) {
		var d AttributeDef
		err := r.Scan(&d.Key, &d.Label, &d.Type, &d.PII, &d.Description, &d.CreatedAt)
		return d, err
	})
}

func (s *Store) PutAttributeDef(ctx context.Context, tenant int64, d AttributeDef) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO attribute_defs (tenant_id, key, label, type, pii, description) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, key) DO UPDATE SET label = EXCLUDED.label, type = EXCLUDED.type,
			pii = EXCLUDED.pii, description = EXCLUDED.description`,
		tenant, d.Key, d.Label, d.Type, d.PII, d.Description)
	return err
}

func (s *Store) DeleteAttributeDef(ctx context.Context, tenant int64, key string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM attribute_defs WHERE tenant_id = $1 AND key = $2`, tenant, key)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// PointRule adds Points to a contact's score each time they do Event.
type PointRule struct {
	ID        int64     `json:"id"`
	Event     string    `json:"event"`
	Points    int       `json:"points"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) ListPointRules(ctx context.Context, tenant int64) ([]PointRule, error) {
	rows, err := s.db.Query(ctx, `SELECT id, event, points, created_at FROM point_rules WHERE tenant_id = $1 ORDER BY event`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PointRule, error) {
		var p PointRule
		err := r.Scan(&p.ID, &p.Event, &p.Points, &p.CreatedAt)
		return p, err
	})
}

// PutPointRule creates or replaces the rule for an event.
func (s *Store) PutPointRule(ctx context.Context, tenant int64, ev string, points int) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO point_rules (tenant_id, event, points) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, event) DO UPDATE SET points = EXCLUDED.points RETURNING id`, tenant, ev, points).Scan(&id)
	return id, err
}

func (s *Store) DeletePointRule(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM point_rules WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// PointRules loads every tenant's rules for the profile role: tenant -> event -> points.
func (s *Store) PointRules(ctx context.Context) (map[int64]map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT tenant_id, event, points FROM point_rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]int{}
	for rows.Next() {
		var t int64
		var ev string
		var pts int
		if err := rows.Scan(&t, &ev, &pts); err != nil {
			return nil, err
		}
		if out[t] == nil {
			out[t] = map[string]int{}
		}
		out[t][ev] = pts
	}
	return out, rows.Err()
}

// IdentitySettings is how the web SDK proves who a visitor is: the customer's server signs user
// ids with Secret, and when Required, unsigned identifies are refused.
type IdentitySettings struct {
	Secret   string `json:"secret"`
	Required bool   `json:"required"`
}

func (s *Store) IdentitySettings(ctx context.Context, tenant int64) (IdentitySettings, error) {
	var v IdentitySettings
	err := s.db.QueryRow(ctx, `SELECT identity_secret, require_signed_identify FROM tenants WHERE id = $1`, tenant).Scan(&v.Secret, &v.Required)
	return v, notFound(err)
}

func (s *Store) SetIdentityRequired(ctx context.Context, tenant int64, required bool) error {
	_, err := s.db.Exec(ctx, `UPDATE tenants SET require_signed_identify = $2 WHERE id = $1`, tenant, required)
	return err
}

// RotateIdentitySecret replaces the signing secret; hashes made with the old one stop working.
func (s *Store) RotateIdentitySecret(ctx context.Context, tenant int64) (string, error) {
	var secret string
	err := s.db.QueryRow(ctx, `
		UPDATE tenants SET identity_secret = replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', '')
		WHERE id = $1 RETURNING identity_secret`, tenant).Scan(&secret)
	return secret, notFound(err)
}

// ListSegmentsOfKind is ListSegments filtered to rule segments or static lists.
func (s *Store) ListSegmentsOfKind(ctx context.Context, tenant int64, kind string) ([]model.Segment, error) {
	rows, err := s.db.Query(ctx, `SELECT `+segmentCols+` FROM segments WHERE tenant_id = $1 AND kind = $2 ORDER BY id`, tenant, kind)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// DistinctTags lists every tag in use in the workspace (at most 1000).
func (s *Store) DistinctTags(ctx context.Context, tenant int64) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT t FROM contacts, jsonb_array_elements_text(
			CASE WHEN jsonb_typeof(attrs->'tags') = 'array' THEN attrs->'tags' ELSE '[]'::jsonb END) AS t
		WHERE tenant_id = $1 ORDER BY t LIMIT 1000`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
