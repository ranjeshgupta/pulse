package pg

// Identity keys (package identity): contact_keys holds each contact's normalised value of every key
// a workspace identifies people by; identity_backfills fills it for keys added later.

import (
	"context"
	"fmt"
	"time"

	"pulse/internal/identity"
	"pulse/internal/model"
)

// KeyValue is one identity key value.
type KeyValue struct {
	Field string
	Value string
}

// MatchIdentityKeys returns the contacts (not merged away) holding each of the given key values,
// oldest first.
func (s *Store) MatchIdentityKeys(ctx context.Context, tenant int64, kvs []KeyValue) (map[KeyValue][]model.Contact, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	fields, values := make([]string, len(kvs)), make([]string, len(kvs))
	for i, kv := range kvs {
		fields[i], values[i] = kv.Field, kv.Value
	}
	rows, err := s.db.Query(ctx, `
		SELECT k.key, k.value, c.tenant_id, c.id, c.external_id, c.email, c.attrs, c.created_at, c.updated_at
		FROM (SELECT DISTINCT * FROM unnest($2::text[], $3::text[]) AS u(key, value)) u
		JOIN contact_keys k ON k.tenant_id = $1 AND k.key = u.key AND k.value = u.value
		JOIN contacts c ON c.tenant_id = $1 AND c.id = k.contact_id
		WHERE NOT c.attrs ? 'merged_into'
		ORDER BY k.key, k.value, c.id`, tenant, fields, values)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[KeyValue][]model.Contact{}
	for rows.Next() {
		var kv KeyValue
		var c model.Contact
		var id int64
		if err := rows.Scan(&kv.Field, &kv.Value, &c.TenantID, &id, &c.ExternalID, &c.Email, &c.Attrs, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.ID = uint32(id)
		if c.Attrs == nil {
			c.Attrs = map[string]any{}
		}
		out[kv] = append(out[kv], c)
	}
	return out, rows.Err()
}

// ContactKeys is one contact's key values after a change (empty: it has none any more).
type ContactKeys struct {
	ContactID uint32
	Values    map[string]string
}

// ReplaceContactKeys stores the key values of contacts whose values changed: their rows for the
// given fields are replaced by the new values.
func (s *Store) ReplaceContactKeys(ctx context.Context, tenant int64, fields []string, cs []ContactKeys) error {
	if len(cs) == 0 || len(fields) == 0 {
		return nil
	}
	ids := make([]int64, len(cs))
	var keys, values []string
	var owners []int64
	for i, c := range cs {
		ids[i] = int64(c.ContactID)
		for k, v := range c.Values {
			keys, values, owners = append(keys, k), append(values, v), append(owners, int64(c.ContactID))
		}
	}
	_, err := s.db.Exec(ctx, `
		WITH del AS (
			DELETE FROM contact_keys WHERE tenant_id = $1 AND contact_id = ANY($2) AND key = ANY($3)
		)
		INSERT INTO contact_keys (tenant_id, key, value, contact_id)
		SELECT $1, u.key, u.value, u.contact_id FROM unnest($4::text[], $5::text[], $6::bigint[]) AS u(key, value, contact_id)
		ON CONFLICT DO NOTHING`, tenant, ids, fields, keys, values, owners)
	if err != nil {
		return fmt.Errorf("replace contact keys: %w", err)
	}
	return nil
}

// SyncIdentityKeys follows a change of a workspace's keys: rows of fields no longer listed are
// dropped, and new fields get a backfill.
func (s *Store) SyncIdentityKeys(ctx context.Context, tenant int64, keys []identity.Key) error {
	var fields []string
	for _, k := range keys {
		if k.Field != identity.ExternalID {
			fields = append(fields, k.Field)
		}
	}
	for _, q := range []string{
		`DELETE FROM contact_keys WHERE tenant_id = $1 AND NOT key = ANY($2)`,
		`DELETE FROM identity_backfills WHERE tenant_id = $1 AND NOT key = ANY($2)`,
		`INSERT INTO identity_backfills (tenant_id, key) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`,
	} {
		if _, err := s.db.Exec(ctx, q, tenant, fields); err != nil {
			return fmt.Errorf("sync identity keys: %w", err)
		}
	}
	return nil
}

// IdentityBackfill is the state of one key's backfill.
type IdentityBackfill struct {
	TenantID int64      `json:"-"`
	Field    string     `json:"field"`
	NextID   int64      `json:"next_id"`
	DoneAt   *time.Time `json:"done_at,omitempty"`
	// Contacts is the workspace's highest contact id (how far NextID has to go).
	Contacts int64 `json:"contacts"`
}

// IdentityBackfills lists a workspace's backfills (done ones too).
func (s *Store) IdentityBackfills(ctx context.Context, tenant int64) ([]IdentityBackfill, error) {
	rows, err := s.db.Query(ctx, `
		SELECT b.tenant_id, b.key, b.next_id, b.done_at, coalesce(t.next_contact, 0)
		FROM identity_backfills b LEFT JOIN tenant_counters t ON t.tenant_id = b.tenant_id
		WHERE b.tenant_id = $1 ORDER BY b.key`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdentityBackfill
	for rows.Next() {
		var b IdentityBackfill
		if err := rows.Scan(&b.TenantID, &b.Field, &b.NextID, &b.DoneAt, &b.Contacts); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PendingIdentityBackfills lists the backfills due to run, across workspaces.
func (s *Store) PendingIdentityBackfills(ctx context.Context, limit int) ([]IdentityBackfill, error) {
	rows, err := s.db.Query(ctx, `
		SELECT tenant_id, key, next_id FROM identity_backfills
		WHERE done_at IS NULL AND start_after <= now() ORDER BY start_after LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdentityBackfill
	for rows.Next() {
		var b IdentityBackfill
		if err := rows.Scan(&b.TenantID, &b.Field, &b.NextID); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// keyExpr is the SQL twin of identity.FromContact over a contacts row c, with the field in $4.
// Keep the two in step: a value normalised differently is never matched.
const keyExpr = `(SELECT CASE
		WHEN v <> '' AND octet_length(v) <= 255 THEN
			CASE WHEN $4 = 'phone' THEN nullif((CASE WHEN v LIKE '+%' THEN '+' ELSE '' END) || regexp_replace(v, '[^0-9]', '', 'g'), '+')
			ELSE lower(v) END
		WHEN $4 = 'email' AND octet_length(c.external_id) <= 255 AND c.external_id ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$' THEN lower(c.external_id)
		WHEN $4 = 'phone' AND c.external_id ~ '^\+[1-9][0-9]{6,14}$' THEN c.external_id
	END FROM (SELECT btrim(coalesce(c.attrs->>$4, ''), E' \t\n\r\v\f') AS v) x)`

// BackfillIdentityKey fills contact_keys for one key over the next chunk of contacts (in id order)
// and records how far it got. done is true once every contact was read.
func (s *Store) BackfillIdentityKey(ctx context.Context, b IdentityBackfill, chunk int) (done bool, err error) {
	var last *int64
	var n int
	err = s.db.QueryRow(ctx, `
		WITH c AS (
			SELECT id, external_id, attrs FROM contacts WHERE tenant_id = $1 AND id >= $2 ORDER BY id LIMIT $3
		), ins AS (
			INSERT INTO contact_keys (tenant_id, key, value, contact_id)
			SELECT $1, $4, k.v, k.id FROM (SELECT c.id, `+keyExpr+` AS v FROM c WHERE NOT c.attrs ? 'merged_into') k
			WHERE k.v IS NOT NULL AND k.v <> ''
			ON CONFLICT DO NOTHING
		)
		SELECT max(id), count(*) FROM c`, b.TenantID, b.NextID, chunk, b.Field).Scan(&last, &n)
	if err != nil {
		return false, fmt.Errorf("backfill identity key: %w", err)
	}
	done = n < chunk
	next := b.NextID
	if last != nil {
		next = *last + 1
	}
	_, err = s.db.Exec(ctx, `
		UPDATE identity_backfills SET next_id = $3, done_at = CASE WHEN $4 THEN now() END
		WHERE tenant_id = $1 AND key = $2 AND done_at IS NULL`, b.TenantID, b.Field, next, done)
	return done, err
}

// DuplicatesByKey groups contacts sharing a value of an identity key (from contact_keys).
func (s *Store) DuplicatesByKey(ctx context.Context, tenant int64, field string, limit, perGroup int) ([]DuplicateGroup, error) {
	rows, err := s.db.Query(ctx, `
		WITH d AS (
			SELECT k.value AS v FROM contact_keys k JOIN contacts c ON c.tenant_id = k.tenant_id AND c.id = k.contact_id
			WHERE k.tenant_id = $1 AND k.key = $2 AND NOT c.attrs ? 'merged_into'
			GROUP BY 1 HAVING count(*) > 1 ORDER BY 1 LIMIT $3
		)
		SELECT v, n, `+contactCols+` FROM (
			SELECT d.v, count(*) OVER (PARTITION BY d.v) AS n, row_number() OVER (PARTITION BY d.v ORDER BY c.id) AS rn, c.*
			FROM d JOIN contact_keys k ON k.tenant_id = $1 AND k.key = $2 AND k.value = d.v
			JOIN contacts c ON c.tenant_id = $1 AND c.id = k.contact_id AND NOT c.attrs ? 'merged_into'
		) x WHERE rn <= $4 ORDER BY v, id`, tenant, field, limit, perGroup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DuplicateGroup
	for rows.Next() {
		var v string
		var n, id int64
		var c model.Contact
		if err := rows.Scan(&v, &n, &c.TenantID, &id, &c.ExternalID, &c.Email, &c.Attrs, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.ID = uint32(id)
		if len(out) == 0 || out[len(out)-1].Value != v {
			out = append(out, DuplicateGroup{Value: v, Total: int(n)})
		}
		g := &out[len(out)-1]
		g.Contacts = append(g.Contacts, c)
	}
	return out, rows.Err()
}

// identityChunk is how many contacts one backfill statement reads.
const identityChunk = 5000

// RunIdentityBackfills advances every due backfill by up to budget chunks in all. It returns
// whether any backfill is still unfinished, so the caller can come back sooner.
func (s *Store) RunIdentityBackfills(ctx context.Context, budget int) (more bool, err error) {
	bs, err := s.PendingIdentityBackfills(ctx, 20)
	if err != nil {
		return false, err
	}
	for _, b := range bs {
		for budget > 0 {
			budget--
			done, err := s.BackfillIdentityKey(ctx, b, identityChunk)
			if err != nil {
				return true, err
			}
			if done {
				break
			}
			b.NextID, err = s.backfillNext(ctx, b)
			if err != nil {
				return true, err
			}
		}
		if budget == 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) backfillNext(ctx context.Context, b IdentityBackfill) (int64, error) {
	var next int64
	err := s.db.QueryRow(ctx, `SELECT next_id FROM identity_backfills WHERE tenant_id = $1 AND key = $2`, b.TenantID, b.Field).Scan(&next)
	return next, err
}
