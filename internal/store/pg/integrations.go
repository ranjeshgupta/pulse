package pg

// Integrations (Settings → Integrations), their sync log, and audience sync state.

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"pulse/internal/integrations"
)

// errNoIntegration matches both ErrNotFound and integrations.ErrNotFound.
var errNoIntegration = errors.Join(ErrNotFound, integrations.ErrNotFound)

func noIntegration(err error) error {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return errNoIntegration
	}
	return err
}

const integrationCols = `id, tenant_id, kind, name, config, secrets, mapping, active, sync_minutes, next_sync_at, cursor,
	status, last_error, last_sync_at, key_hint, created_at, updated_at`

func scanIntegration(row pgx.Row) (integrations.Connector, error) {
	var c integrations.Connector
	err := row.Scan(&c.ID, &c.TenantID, &c.Kind, &c.Name, &c.Config, &c.Sealed, &c.Mapping, &c.Active, &c.SyncMinutes, &c.NextSyncAt,
		&c.Cursor, &c.Status, &c.LastError, &c.LastSyncAt, &c.KeyHint, &c.CreatedAt, &c.UpdatedAt)
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if c.Mapping == nil {
		c.Mapping = []integrations.FieldMap{}
	}
	return c, noIntegration(err)
}

func collectIntegrations(rows pgx.Rows, err error) ([]integrations.Connector, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (integrations.Connector, error) { return scanIntegration(r) })
}

func (s *Store) ListIntegrations(ctx context.Context, tenant int64) ([]integrations.Connector, error) {
	return collectIntegrations(s.db.Query(ctx, `SELECT `+integrationCols+` FROM integrations WHERE tenant_id = $1 ORDER BY id`, tenant))
}

func (s *Store) GetIntegration(ctx context.Context, tenant, id int64) (integrations.Connector, error) {
	return scanIntegration(s.db.QueryRow(ctx, `SELECT `+integrationCols+` FROM integrations WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// IntegrationByID finds a connector by id alone (inbound webhooks, which verify a signature next).
func (s *Store) IntegrationByID(ctx context.Context, id int64) (integrations.Connector, error) {
	return scanIntegration(s.db.QueryRow(ctx, `SELECT `+integrationCols+` FROM integrations WHERE id = $1`, id))
}

// IntegrationByKeyHash finds the REST hooks integration whose API key has this hash.
func (s *Store) IntegrationByKeyHash(ctx context.Context, hash string) (integrations.Connector, error) {
	return scanIntegration(s.db.QueryRow(ctx, `SELECT `+integrationCols+` FROM integrations WHERE key_hash = $1`, hash))
}

// SaveIntegration creates (ID 0) or updates a connector; a nil sealed keeps the stored secrets.
// A changed pull schedule takes effect at once.
func (s *Store) SaveIntegration(ctx context.Context, c integrations.Connector, sealed []byte) (int64, error) {
	if c.Mapping == nil {
		c.Mapping = []integrations.FieldMap{}
	}
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if c.ID == 0 {
		err := s.db.QueryRow(ctx, `
			INSERT INTO integrations (tenant_id, kind, name, config, secrets, mapping, active, sync_minutes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			c.TenantID, c.Kind, c.Name, c.Config, sealed, c.Mapping, c.Active, c.SyncMinutes).Scan(&c.ID)
		return c.ID, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE integrations SET name = $3, config = $4, secrets = coalesce($5, secrets), mapping = $6, active = $7,
			sync_minutes = $8, next_sync_at = CASE WHEN sync_minutes <> $8 THEN NULL ELSE next_sync_at END, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, c.TenantID, c.ID, c.Name, c.Config, sealed, c.Mapping, c.Active, c.SyncMinutes)
	if err == nil && tag.RowsAffected() == 0 {
		err = errNoIntegration
	}
	return c.ID, err
}

func (s *Store) DeleteIntegration(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM integrations WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = errNoIntegration
	}
	return err
}

// SetIntegrationKey stores a REST hooks connector's new API key (its hash and last characters).
func (s *Store) SetIntegrationKey(ctx context.Context, tenant, id int64, hash, hint string) error {
	tag, err := s.db.Exec(ctx, `UPDATE integrations SET key_hash = $3, key_hint = $4, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id, hash, hint)
	if err == nil && tag.RowsAffected() == 0 {
		err = errNoIntegration
	}
	return err
}

// PullNow makes a connector's next pull due; restart also forgets the cursor (pull everything).
func (s *Store) PullNow(ctx context.Context, tenant, id int64, restart bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE integrations SET next_sync_at = now(), cursor = CASE WHEN $3 THEN '' ELSE cursor END
		WHERE tenant_id = $1 AND id = $2`, tenant, id, restart)
	if err == nil && tag.RowsAffected() == 0 {
		err = errNoIntegration
	}
	return err
}

func (s *Store) ClaimDuePulls(ctx context.Context, limit int) ([]integrations.Connector, error) {
	return collectIntegrations(s.db.Query(ctx, `
		UPDATE integrations SET next_sync_at = now() + make_interval(mins => sync_minutes)
		WHERE id IN (
			SELECT id FROM integrations
			WHERE active AND sync_minutes > 0 AND kind IN ('hubspot', 'salesforce') AND coalesce(next_sync_at, '-infinity') <= now()
			ORDER BY next_sync_at NULLS FIRST LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING `+integrationCols, limit))
}

func (s *Store) FinishPull(ctx context.Context, id int64, cursor, status, lastErr string, again bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE integrations SET cursor = $2, status = $3, last_error = $4, last_sync_at = now(),
			next_sync_at = CASE WHEN $5 THEN now() ELSE next_sync_at END
		WHERE id = $1`, id, cursor, status, lastErr, again)
	return err
}

func (s *Store) SetIntegrationStatus(ctx context.Context, id int64, status, lastErr string) error {
	_, err := s.db.Exec(ctx, `UPDATE integrations SET status = $2, last_error = $3 WHERE id = $1`, id, status, lastErr)
	return err
}

// LogIntegration appends to a connector's log; now and then it prunes entries older than a week.
func (s *Store) LogIntegration(ctx context.Context, id int64, e integrations.LogEntry) error {
	if len(e.Message) > 1000 {
		e.Message = e.Message[:1000]
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO integration_log (integration_id, action, ok, count, message)
		SELECT $1, $2, $3, $4, $5 WHERE EXISTS (SELECT 1 FROM integrations WHERE id = $1)`, id, e.Action, e.OK, e.Count, e.Message)
	if err == nil && rand.IntN(200) == 0 {
		_, err = s.db.Exec(ctx, `DELETE FROM integration_log WHERE at < now() - interval '7 days'`)
	}
	return err
}

func (s *Store) IntegrationLog(ctx context.Context, tenant, id int64, limit int) ([]integrations.LogEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT l.id, l.at, l.action, l.ok, l.count, l.message FROM integration_log l JOIN integrations i ON i.id = l.integration_id
		WHERE i.tenant_id = $1 AND i.id = $2 ORDER BY l.id DESC LIMIT $3`, tenant, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[integrations.LogEntry])
}

func (s *Store) ExternalIDByEmail(ctx context.Context, tenant int64, email string) (string, error) {
	c, err := s.ContactByEmail(ctx, tenant, email)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return c.ExternalID, err
}

// ---- audience sync ----

const audienceCols = `a.id, a.tenant_id, a.integration_id, a.segment_id, a.audience_id, a.audience_name, a.active, a.status,
	a.last_error, a.uploaded, a.added, a.removed, a.skipped, a.last_sync_at, a.next_full_at, a.created_at`

func scanAudience(row pgx.Row, queued bool) (integrations.AudienceSync, error) {
	var a integrations.AudienceSync
	dest := []any{&a.ID, &a.TenantID, &a.IntegrationID, &a.SegmentID, &a.AudienceID, &a.AudienceName, &a.Active, &a.Status,
		&a.LastError, &a.Uploaded, &a.Added, &a.Removed, &a.Skipped, &a.LastSyncAt, &a.NextFullAt, &a.CreatedAt}
	if queued {
		dest = append(dest, &a.Queued)
	}
	err := row.Scan(dest...)
	return a, notFound(err)
}

func collectAudiences(rows pgx.Rows, err error, queued bool) ([]integrations.AudienceSync, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (integrations.AudienceSync, error) { return scanAudience(r, queued) })
}

// ListAudienceSyncs lists a connector's linked segments, with how many contacts wait to be checked.
func (s *Store) ListAudienceSyncs(ctx context.Context, tenant, integration int64) ([]integrations.AudienceSync, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+audienceCols+`, (SELECT count(*) FROM audience_queue q WHERE q.sync_id = a.id)
		FROM audience_syncs a WHERE a.tenant_id = $1 AND a.integration_id = $2 ORDER BY a.id`, tenant, integration)
	return collectAudiences(rows, err, true)
}

func (s *Store) GetAudienceSync(ctx context.Context, tenant, integration, id int64) (integrations.AudienceSync, error) {
	return scanAudience(s.db.QueryRow(ctx, `SELECT `+audienceCols+` FROM audience_syncs a
		WHERE a.tenant_id = $1 AND a.integration_id = $2 AND a.id = $3`, tenant, integration, id), false)
}

// CreateAudienceSync links a segment; its first full upload is due at once.
func (s *Store) CreateAudienceSync(ctx context.Context, a integrations.AudienceSync) (int64, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO audience_syncs (tenant_id, integration_id, segment_id, audience_id, audience_name)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, a.TenantID, a.IntegrationID, a.SegmentID, a.AudienceID, a.AudienceName).Scan(&a.ID)
	return a.ID, conflict(err)
}

// SetAudienceSync pauses or resumes a link; resuming reconciles at once.
func (s *Store) SetAudienceSync(ctx context.Context, tenant, integration, id int64, active bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE audience_syncs SET active = $4, updated_at = now(), next_full_at = CASE WHEN $4 THEN now() ELSE next_full_at END
		WHERE tenant_id = $1 AND integration_id = $2 AND id = $3`, tenant, integration, id, active)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ReconcileNow makes a link's full reconcile due.
func (s *Store) ReconcileNow(ctx context.Context, tenant, integration, id int64) error {
	tag, err := s.db.Exec(ctx, `UPDATE audience_syncs SET next_full_at = now() WHERE tenant_id = $1 AND integration_id = $2 AND id = $3`,
		tenant, integration, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) DeleteAudienceSync(ctx context.Context, tenant, integration, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM audience_syncs WHERE tenant_id = $1 AND integration_id = $2 AND id = $3`, tenant, integration, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) ActiveAudienceSyncs(ctx context.Context) ([]integrations.AudienceSync, error) {
	rows, err := s.db.Query(ctx, `SELECT `+audienceCols+` FROM audience_syncs a JOIN integrations i ON i.id = a.integration_id
		WHERE a.active AND i.active`)
	return collectAudiences(rows, err, false)
}

func (s *Store) QueueAudience(ctx context.Context, sync int64, contacts []uint32) error {
	if len(contacts) == 0 {
		return nil
	}
	ids := make([]int64, len(contacts))
	for i, c := range contacts {
		ids[i] = int64(c)
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO audience_queue (sync_id, contact_id)
		SELECT $1, c FROM unnest($2::bigint[]) c WHERE EXISTS (SELECT 1 FROM audience_syncs WHERE id = $1)
		ON CONFLICT DO NOTHING`, sync, ids)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		return nil // the link was deleted meanwhile
	}
	return err
}

func (s *Store) QueuedAudienceSyncs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT sync_id FROM audience_queue`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func (s *Store) ClaimAudienceQueue(ctx context.Context, sync int64, limit int) ([]uint32, error) {
	rows, err := s.db.Query(ctx, `
		DELETE FROM audience_queue WHERE (sync_id, contact_id) IN (
			SELECT sync_id, contact_id FROM audience_queue WHERE sync_id = $1 ORDER BY queued_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING contact_id`, sync, limit)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	out := make([]uint32, len(ids))
	for i, id := range ids {
		out[i] = uint32(id)
	}
	return out, err
}

func (s *Store) AudienceMembers(ctx context.Context, sync int64, contacts []uint32) (map[uint32]integrations.Identity, error) {
	ids := make([]int64, len(contacts))
	for i, c := range contacts {
		ids[i] = int64(c)
	}
	rows, err := s.db.Query(ctx, `SELECT contact_id, email_hash, phone_hash FROM audience_members WHERE sync_id = $1 AND contact_id = ANY($2)`, sync, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32]integrations.Identity{}
	for rows.Next() {
		var id int64
		var m integrations.Identity
		if err := rows.Scan(&id, &m.EmailHash, &m.PhoneHash); err != nil {
			return nil, err
		}
		m.ContactID = uint32(id)
		out[m.ContactID] = m
	}
	return out, rows.Err()
}

func (s *Store) UploadedAfter(ctx context.Context, sync int64, after uint32, limit int) ([]uint32, error) {
	rows, err := s.db.Query(ctx, `SELECT contact_id FROM audience_members WHERE sync_id = $1 AND contact_id > $2 ORDER BY contact_id LIMIT $3`,
		sync, int64(after), limit)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	out := make([]uint32, len(ids))
	for i, id := range ids {
		out[i] = uint32(id)
	}
	return out, err
}

func (s *Store) ApplyAudience(ctx context.Context, sync int64, o integrations.Outcome) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		inserted := 0
		if len(o.Keep) > 0 {
			ids, emails, phones := make([]int64, len(o.Keep)), make([]string, len(o.Keep)), make([]string, len(o.Keep))
			for i, k := range o.Keep {
				ids[i], emails[i], phones[i] = int64(k.ContactID), k.EmailHash, k.PhoneHash
			}
			rows, err := tx.Query(ctx, `
				INSERT INTO audience_members (sync_id, contact_id, email_hash, phone_hash)
				SELECT $1, * FROM unnest($2::bigint[], $3::text[], $4::text[])
				ON CONFLICT (sync_id, contact_id) DO UPDATE SET email_hash = excluded.email_hash, phone_hash = excluded.phone_hash
				RETURNING (xmax = 0)`, sync, ids, emails, phones)
			if err != nil {
				return err
			}
			news, err := pgx.CollectRows(rows, pgx.RowTo[bool])
			if err != nil {
				return err
			}
			for _, n := range news {
				if n {
					inserted++
				}
			}
		}
		deleted := int64(0)
		if len(o.Forget) > 0 {
			ids := make([]int64, len(o.Forget))
			for i, c := range o.Forget {
				ids[i] = int64(c)
			}
			tag, err := tx.Exec(ctx, `DELETE FROM audience_members WHERE sync_id = $1 AND contact_id = ANY($2)`, sync, ids)
			if err != nil {
				return err
			}
			deleted = tag.RowsAffected()
		}
		_, err := tx.Exec(ctx, `
			UPDATE audience_syncs SET uploaded = greatest(uploaded + $2 - $3, 0), added = added + $4, removed = removed + $5,
				status = CASE WHEN status = 'syncing' THEN status ELSE 'ok' END, last_error = '', last_sync_at = now()
			WHERE id = $1`, sync, inserted, deleted, o.Added, o.Removed)
		return err
	})
}

func (s *Store) AudienceError(ctx context.Context, sync int64, msg string) error {
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	_, err := s.db.Exec(ctx, `UPDATE audience_syncs SET status = 'error', last_error = $2 WHERE id = $1`, sync, msg)
	return err
}

func (s *Store) ClaimDueReconciles(ctx context.Context, every time.Duration, limit int) ([]integrations.AudienceSync, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE audience_syncs a SET next_full_at = now() + make_interval(secs => $1), status = 'syncing'
		WHERE a.id IN (
			SELECT s.id FROM audience_syncs s JOIN integrations i ON i.id = s.integration_id
			WHERE s.active AND i.active AND s.next_full_at <= now()
			ORDER BY s.next_full_at LIMIT $2 FOR UPDATE OF s SKIP LOCKED)
		RETURNING `+audienceCols, every.Seconds(), limit)
	return collectAudiences(rows, err, false)
}

func (s *Store) FinishReconcile(ctx context.Context, sync int64, skipped int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audience_syncs SET skipped = $2, status = 'ok', last_error = '', last_sync_at = now(),
			uploaded = (SELECT count(*) FROM audience_members WHERE sync_id = $1)
		WHERE id = $1`, sync, skipped)
	return err
}
