package pg

// Campaigns (M3): reachability rebuild reads, saved test contacts and campaign tags.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
	"pulse/internal/reach"
)

// TenantIDs lists every workspace.
func (s *Store) TenantIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM tenants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// ReachRows pages through a workspace's contacts with the attributes reachability reads.
func (s *Store) ReachRows(ctx context.Context, tenant int64, afterID uint32, limit int, since *time.Time) ([]reach.Row, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, email, jsonb_strip_nulls(jsonb_build_object(
			'phone', attrs->'phone', 'merged_into', attrs->'merged_into',
			'email_bounced', attrs->'email_bounced', 'sms_bounced', attrs->'sms_bounced',
			'optout_all', attrs->'optout_all', 'optout_email', attrs->'optout_email', 'optout_sms', attrs->'optout_sms',
			'optout_whatsapp', attrs->'optout_whatsapp', 'optout_rcs', attrs->'optout_rcs',
			'optout_push', attrs->'optout_push', 'optout_inbox', attrs->'optout_inbox'))
		FROM contacts WHERE tenant_id = $1 AND id > $2 AND ($4::timestamptz IS NULL OR updated_at >= $4)
		ORDER BY id LIMIT $3`, tenant, int64(afterID), limit, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (reach.Row, error) {
		var id int64
		var email string
		var raw []byte
		if err := r.Scan(&id, &email, &raw); err != nil {
			return reach.Row{}, err
		}
		attrs := map[string]any{}
		_ = json.Unmarshal(raw, &attrs)
		if email != "" {
			attrs["email"] = email
		}
		return reach.Row{ID: uint32(id), Attrs: attrs}, nil
	})
}

// PushContacts lists contacts with a push device.
func (s *Store) PushContacts(ctx context.Context, tenant int64) ([]uint32, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT contact_id FROM contact_devices WHERE tenant_id = $1`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (uint32, error) {
		var id int64
		err := r.Scan(&id)
		return uint32(id), err
	})
}

// HasDevices reports whether a contact still has a push device.
func (s *Store) HasDevices(ctx context.Context, tenant int64, contact uint32) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contact_devices WHERE tenant_id = $1 AND contact_id = $2)`,
		tenant, int64(contact)).Scan(&ok)
	return ok, err
}

// ---- test contacts ----

const testContactCols = `id, name, external_id, email, phone, push_token, created_at`

func (s *Store) ListTestContacts(ctx context.Context, tenant int64) ([]model.TestContact, error) {
	rows, err := s.db.Query(ctx, `SELECT `+testContactCols+` FROM test_contacts WHERE tenant_id = $1 ORDER BY name, id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.TestContact])
}

func (s *Store) GetTestContact(ctx context.Context, tenant, id int64) (model.TestContact, error) {
	rows, err := s.db.Query(ctx, `SELECT `+testContactCols+` FROM test_contacts WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err != nil {
		return model.TestContact{}, err
	}
	tc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[model.TestContact])
	return tc, notFound(err)
}

// SaveTestContact creates (ID 0) or updates a test contact. A workspace keeps at most 50.
func (s *Store) SaveTestContact(ctx context.Context, tenant int64, tc model.TestContact) (int64, error) {
	if tc.ID == 0 {
		var id int64
		err := s.db.QueryRow(ctx, `
			INSERT INTO test_contacts (tenant_id, name, external_id, email, phone, push_token)
			SELECT $1, $2, $3, $4, $5, $6 WHERE (SELECT count(*) FROM test_contacts WHERE tenant_id = $1) < 50
			RETURNING id`, tenant, tc.Name, tc.ExternalID, tc.Email, tc.Phone, tc.PushToken).Scan(&id)
		if err == pgx.ErrNoRows {
			return 0, errorString("a workspace can keep at most 50 test contacts")
		}
		return id, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE test_contacts SET name = $3, external_id = $4, email = $5, phone = $6, push_token = $7
		WHERE tenant_id = $1 AND id = $2`, tenant, tc.ID, tc.Name, tc.ExternalID, tc.Email, tc.Phone, tc.PushToken)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return tc.ID, err
}

func (s *Store) DeleteTestContact(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM test_contacts WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ---- tags ----

// CampaignTags lists the tags used on broadcasts and journeys, most used first.
func (s *Store) CampaignTags(ctx context.Context, tenant int64) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t FROM (
			SELECT unnest(tags) AS t FROM broadcasts WHERE tenant_id = $1
			UNION ALL SELECT unnest(tags) FROM journeys WHERE tenant_id = $1) x
		GROUP BY t ORDER BY count(*) DESC, t LIMIT 200`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
