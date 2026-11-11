package pg

// The bounce mailbox (IMAP) of a workspace.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/bounces"
)

const mailboxCols = `tenant_id, enabled, host, port, tls, username, folder, password, last_check_at, last_error, processed, skipped`

func scanMailbox(row pgx.Row) (bounces.Mailbox, []byte, error) {
	var m bounces.Mailbox
	var sealed []byte
	err := row.Scan(&m.TenantID, &m.Enabled, &m.Host, &m.Port, &m.TLS, &m.Username, &m.Folder, &sealed, &m.LastCheckAt,
		&m.LastError, &m.Processed, &m.Skipped)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, ErrNotFound
	}
	m.HasPassword = len(sealed) > 0
	return m, sealed, err
}

// BounceMailbox returns the workspace's mailbox and its sealed password (ErrNotFound when none).
func (s *Store) BounceMailbox(ctx context.Context, tenant int64) (bounces.Mailbox, []byte, error) {
	return scanMailbox(s.db.QueryRow(ctx, `SELECT `+mailboxCols+` FROM bounce_mailboxes WHERE tenant_id = $1`, tenant))
}

// SaveBounceMailbox creates or replaces the settings; sealed is the password to keep (nil: none).
// A saved, enabled mailbox is checked right away.
func (s *Store) SaveBounceMailbox(ctx context.Context, m bounces.Mailbox, sealed []byte) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO bounce_mailboxes (tenant_id, enabled, host, port, tls, username, folder, password, next_check_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (tenant_id) DO UPDATE SET enabled = $2, host = $3, port = $4, tls = $5, username = $6, folder = $7,
			password = $8, next_check_at = now(), last_error = '', updated_at = now()`,
		m.TenantID, m.Enabled, m.Host, m.Port, m.TLS, m.Username, m.Folder, sealed)
	return err
}

func (s *Store) DeleteBounceMailbox(ctx context.Context, tenant int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM bounce_mailboxes WHERE tenant_id = $1`, tenant)
	return err
}

// CheckBounceMailboxNow makes an enabled mailbox due at once.
func (s *Store) CheckBounceMailboxNow(ctx context.Context, tenant int64) error {
	tag, err := s.db.Exec(ctx, `UPDATE bounce_mailboxes SET next_check_at = now() WHERE tenant_id = $1 AND enabled`, tenant)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ClaimDueMailboxes(ctx context.Context, limit int, every time.Duration) ([]bounces.Mailbox, [][]byte, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE bounce_mailboxes SET next_check_at = now() + make_interval(secs => $2)
		WHERE tenant_id IN (
			SELECT tenant_id FROM bounce_mailboxes
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

func (s *Store) FinishMailboxCheck(ctx context.Context, tenant int64, processed, skipped int, lastErr string) error {
	if len(lastErr) > 1000 {
		lastErr = lastErr[:1000]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE bounce_mailboxes SET last_check_at = now(), last_error = $2, processed = processed + $3, skipped = skipped + $4
		WHERE tenant_id = $1`, tenant, lastErr, processed, skipped)
	return err
}
