package pg

// Queries behind the web app: editing segments and journeys, contact search, counts.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
	"pulse/internal/schedule"
)

// UpdateSegment changes a segment's rule and queues it for a rebuild.
func (s *Store) UpdateSegment(ctx context.Context, tenant, id int64, name string, rule []byte, hasWindow bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE segments SET name = $3, rule = $4, has_window = $5, status = 'new'
		WHERE tenant_id = $1 AND id = $2 AND kind = 'rule'`, tenant, id, name, rule, hasWindow)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) DeleteSegment(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM segments WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

const journeyCols = `id, tenant_id, name, graph, status, start_at, end_at, split_winners, archived_at`

func scanJourney(row pgx.Row) (model.Journey, error) {
	var j model.Journey
	err := row.Scan(&j.ID, &j.TenantID, &j.Name, &j.Graph, &j.Status, &j.StartAt, &j.EndAt, &j.SplitWinners, &j.ArchivedAt)
	return j, notFound(err)
}

func (s *Store) ListJourneys(ctx context.Context, tenant int64) ([]model.Journey, error) {
	rows, err := s.db.Query(ctx, `SELECT `+journeyCols+` FROM journeys WHERE tenant_id = $1 ORDER BY id DESC`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Journey, error) { return scanJourney(r) })
}

func (s *Store) GetJourney(ctx context.Context, tenant, id int64) (model.Journey, error) {
	return scanJourney(s.db.QueryRow(ctx, `SELECT `+journeyCols+` FROM journeys WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// ErrJourneyActive is returned when editing or deleting a running journey.
var ErrJourneyActive = errorString("pause the journey before changing it")

type errorString string

func (e errorString) Error() string { return string(e) }

// IsUserError reports whether err is a message meant for the person making the change
// (a conflict like "people still have this role"), not an internal failure.
func IsUserError(err error) bool {
	var e errorString
	return errors.As(err, &e)
}

// UpdateJourney saves a new graph. Running journeys must be paused first, so contacts in flight
// never find their current node gone.
func (s *Store) UpdateJourney(ctx context.Context, tenant, id int64, name string, graph []byte, w schedule.Window) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE journeys SET name = $3, graph = $4, start_at = $5, end_at = $6, updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND status <> 'active'`, tenant, id, name, graph, w.Start, w.End)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetJourney(ctx, tenant, id); err != nil {
			return err
		}
		return ErrJourneyActive
	}
	return nil
}

// DeleteJourney removes a journey that has never been activated (no enrollments).
func (s *Store) DeleteJourney(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM journeys WHERE tenant_id = $1 AND id = $2 AND status <> 'active'
		AND NOT EXISTS (SELECT 1 FROM journey_enrollments WHERE journey_id = $2)`, tenant, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetJourney(ctx, tenant, id); err != nil {
			return err
		}
		return errorString("only journeys that never ran can be deleted; pause and archive it instead")
	}
	return nil
}

// SetJourneyArchived archives a journey (hidden from the list, enrollments kept) or restores it.
// An active journey must be paused first.
func (s *Store) SetJourneyArchived(ctx context.Context, tenant, id int64, archived bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE journeys SET archived_at = CASE WHEN $3 THEN coalesce(archived_at, now()) END, updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND status <> 'active'`, tenant, id, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetJourney(ctx, tenant, id); err != nil {
			return err
		}
		return errorString("pause the journey before archiving it")
	}
	return nil
}

// JourneysThatRan lists which of a workspace's journeys have ever enrolled anyone (those can be
// archived but not deleted).
func (s *Store) JourneysThatRan(ctx context.Context, tenant int64) (map[int64]bool, error) {
	rows, err := s.db.Query(ctx, `
		SELECT j.id FROM journeys j WHERE j.tenant_id = $1
		AND EXISTS (SELECT 1 FROM journey_enrollments e WHERE e.journey_id = j.id)`, tenant)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// ListContacts lists contacts newest first, through the primary key, leaving out visitors merged into
// another contact (as filtered views do). afterID pages through them
// (pass the last id of the previous page). Filtered views run in ClickHouse (see api/contactview.go).
func (s *Store) ListContacts(ctx context.Context, tenant int64, afterID uint32, limit int) ([]model.Contact, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+contactCols+` FROM contacts
		WHERE tenant_id = $1 AND ($2 = 0 OR id < $2) AND NOT attrs ? 'merged_into'
		ORDER BY id DESC LIMIT $3`, tenant, int64(afterID), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Contact, error) { return scanContact(r) })
}

// Counts are the headline numbers on the dashboard.
type Counts struct {
	Contacts       int64 `json:"contacts"`
	Segments       int64 `json:"segments"`
	ActiveJourneys int64 `json:"active_journeys"`
	Templates      int64 `json:"templates"`
}

func (s *Store) Counts(ctx context.Context, tenant int64) (Counts, error) {
	var c Counts
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM contacts WHERE tenant_id = $1 AND NOT attrs ? 'merged_into'),
		       (SELECT count(*) FROM segments WHERE tenant_id = $1),
		       (SELECT count(*) FROM journeys WHERE tenant_id = $1 AND status = 'active'),
		       (SELECT count(*) FROM templates WHERE tenant_id = $1)`, tenant).
		Scan(&c.Contacts, &c.Segments, &c.ActiveJourneys, &c.Templates)
	return c, err
}

// AttrKeys lists every attribute name used by a workspace's contacts (export columns).
func (s *Store) AttrKeys(ctx context.Context, tenant int64) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT jsonb_object_keys(attrs) FROM contacts WHERE tenant_id = $1`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
