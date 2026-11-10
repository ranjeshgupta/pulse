package pg

// The app inbox.

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

// InsertInbox stores a message once per message id (a redelivered send request is a no-op).
func (s *Store) InsertInbox(ctx context.Context, m model.InboxMessage) error {
	data, _ := json.Marshal(m.Data)
	meta, _ := json.Marshal(m.Meta)
	if m.Data == nil {
		data = []byte("{}")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO inbox_messages (tenant_id, contact_id, message_id, title, body, image_url, url, category, data, meta, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT (tenant_id, message_id) DO NOTHING`,
		m.TenantID, int64(m.ContactID), m.MessageID, m.Title, m.Body, m.ImageURL, m.URL, m.Category, data, meta, m.ExpiresAt)
	return err
}

// Inbox lists a contact's live messages, newest first, before id (0: from the newest), and the unread count.
func (s *Store) Inbox(ctx context.Context, tenant int64, contact uint32, before int64, limit int) ([]model.InboxMessage, int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, message_id, title, body, image_url, url, category, data, meta, created_at, read_at, expires_at FROM inbox_messages
		WHERE tenant_id = $1 AND contact_id = $2 AND deleted_at IS NULL AND expires_at > now() AND ($3 = 0 OR id < $3)
		ORDER BY id DESC LIMIT $4`, tenant, int64(contact), before, limit)
	if err != nil {
		return nil, 0, err
	}
	ms, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.InboxMessage, error) {
		var m model.InboxMessage
		err := r.Scan(&m.ID, &m.MessageID, &m.Title, &m.Body, &m.ImageURL, &m.URL, &m.Category, &m.Data, &m.Meta, &m.CreatedAt, &m.ReadAt, &m.ExpiresAt)
		return m, err
	})
	if err != nil {
		return nil, 0, err
	}
	var unread int
	err = s.db.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE tenant_id = $1 AND contact_id = $2
		AND deleted_at IS NULL AND read_at IS NULL AND expires_at > now()`, tenant, int64(contact)).Scan(&unread)
	return ms, unread, err
}

// InboxUpdate marks messages read (action "read") or deleted ("delete"); ids empty means all of
// the contact's messages. It returns the messages that changed, for the read events.
func (s *Store) InboxUpdate(ctx context.Context, tenant int64, contact uint32, action string, ids []int64) ([]model.InboxMessage, error) {
	set := "read_at = now()"
	cond := "read_at IS NULL"
	if action == "delete" {
		set, cond = "deleted_at = now()", "true"
	}
	rows, err := s.db.Query(ctx, `
		UPDATE inbox_messages SET `+set+`
		WHERE tenant_id = $1 AND contact_id = $2 AND deleted_at IS NULL AND `+cond+` AND (coalesce(cardinality($3::bigint[]), 0) = 0 OR id = ANY($3))
		RETURNING id, message_id, meta`, tenant, int64(contact), ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.InboxMessage, error) {
		var m model.InboxMessage
		err := r.Scan(&m.ID, &m.MessageID, &m.Meta)
		return m, err
	})
}

// InboxMessage returns one of a contact's messages (for click tracking).
func (s *Store) InboxMessage(ctx context.Context, tenant int64, contact uint32, id int64) (model.InboxMessage, error) {
	var m model.InboxMessage
	err := s.db.QueryRow(ctx, `SELECT id, message_id, url, meta FROM inbox_messages
		WHERE tenant_id = $1 AND contact_id = $2 AND id = $3 AND deleted_at IS NULL AND expires_at > now()`,
		tenant, int64(contact), id).Scan(&m.ID, &m.MessageID, &m.URL, &m.Meta)
	return m, notFound(err)
}

// PurgeInbox deletes expired messages and ones deleted over a week ago.
func (s *Store) PurgeInbox(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM inbox_messages WHERE expires_at < now() OR deleted_at < now() - interval '7 days'`)
	return tag.RowsAffected(), err
}
