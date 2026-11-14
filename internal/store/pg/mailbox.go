package pg

// The bounce mailboxes (IMAP) of a workspace: any number, each with its folders.

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/bounces"
)

const mailboxCols = `id, tenant_id, name, enabled, host, port, tls, username, folders, password, last_check_at, last_error, processed, skipped`

func scanMailbox(row pgx.Row) (bounces.Mailbox, []byte, error) {
	var m bounces.Mailbox
	var sealed, folders []byte
	err := row.Scan(&m.ID, &m.TenantID, &m.Name, &m.Enabled, &m.Host, &m.Port, &m.TLS, &m.Username, &folders, &sealed, &m.LastCheckAt,
		&m.LastError, &m.Processed, &m.Skipped)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(folders, &m.Folders)
	}
	m.HasPassword = len(sealed) > 0
	return m, sealed, err
}

// BounceMailboxes lists a workspace's mailboxes, oldest first.
func (s *Store) BounceMailboxes(ctx context.Context, tenant int64) ([]bounces.Mailbox, error) {
	rows, err := s.db.Query(ctx, `SELECT `+mailboxCols+` FROM bounce_mailboxes WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []bounces.Mailbox{}
	for rows.Next() {
		m, _, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// BounceMailbox returns one mailbox and its sealed password (ErrNotFound when it isn't the workspace's).
func (s *Store) BounceMailbox(ctx context.Context, tenant, id int64) (bounces.Mailbox, []byte, error) {
	return scanMailbox(s.db.QueryRow(ctx, `SELECT `+mailboxCols+` FROM bounce_mailboxes WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// SaveBounceMailbox creates (ID 0) or updates a mailbox; sealed is the password to keep (nil:
// none). A saved, enabled mailbox is checked right away. It returns the mailbox's id.
func (s *Store) SaveBounceMailbox(ctx context.Context, m bounces.Mailbox, sealed []byte) (int64, error) {
	folders, err := json.Marshal(m.Folders)
	if err != nil {
		return 0, err
	}
	if m.ID == 0 {
		err = s.db.QueryRow(ctx, `
			INSERT INTO bounce_mailboxes (tenant_id, name, enabled, host, port, tls, username, folders, password, next_check_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now()) RETURNING id`,
			m.TenantID, m.Name, m.Enabled, m.Host, m.Port, m.TLS, m.Username, folders, sealed).Scan(&m.ID)
		return m.ID, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE bounce_mailboxes SET name = $3, enabled = $4, host = $5, port = $6, tls = $7, username = $8, folders = $9,
			password = $10, next_check_at = now(), last_error = '', updated_at = now()
		WHERE tenant_id = $1 AND id = $2`,
		m.TenantID, m.ID, m.Name, m.Enabled, m.Host, m.Port, m.TLS, m.Username, folders, sealed)
	if err == nil && tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return m.ID, err
}

func (s *Store) DeleteBounceMailbox(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM bounce_mailboxes WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CheckBounceMailboxNow makes an enabled mailbox due at once.
func (s *Store) CheckBounceMailboxNow(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `UPDATE bounce_mailboxes SET next_check_at = now() WHERE tenant_id = $1 AND id = $2 AND enabled`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ClaimDueMailboxes(ctx context.Context, limit int, every time.Duration) ([]bounces.Mailbox, [][]byte, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE bounce_mailboxes SET next_check_at = now() + make_interval(secs => $2)
		WHERE id IN (
			SELECT id FROM bounce_mailboxes
			WHERE enabled AND coalesce(next_check_at, '-infinity') <= now()
			ORDER BY next_check_at NULLS FIRST LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING `+mailboxCols, limit, every.Seconds())
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ms []bounces.Mailbox
	var sealed [][]byte
	for rows.Next() {
		m, pw, err := scanMailbox(rows)
		if err != nil {
			return nil, nil, err
		}
		ms, sealed = append(ms, m), append(sealed, pw)
	}
	return ms, sealed, rows.Err()
}

func (s *Store) FinishMailboxCheck(ctx context.Context, id int64, processed, skipped int, lastErr string) error {
	if len(lastErr) > 1000 {
		lastErr = lastErr[:1000]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE bounce_mailboxes SET last_check_at = now(), last_error = $2, processed = processed + $3, skipped = skipped + $4
		WHERE id = $1`, id, lastErr, processed, skipped)
	return err
}
