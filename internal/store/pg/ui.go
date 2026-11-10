package pg

// Queries behind the web app: editing segments and journeys, contact search, counts.

import (
	"context"
	"errors"
	"strings"

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

const journeyCols = `id, tenant_id, name, graph, status, start_at, end_at, split_winners`

func scanJourney(row pgx.Row) (model.Journey, error) {
	var j model.Journey
	err := row.Scan(&j.ID, &j.TenantID, &j.Name, &j.Graph, &j.Status, &j.StartAt, &j.EndAt, &j.SplitWinners)
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
		return errorString("only journeys that never ran can be deleted; pause it instead")
	}
	return nil
}

// SearchContacts lists contacts newest first, filtered by external id, email or name.
// afterID pages through results (pass the last id of the previous page).
func (s *Store) SearchContacts(ctx context.Context, tenant int64, q string, afterID uint32, limit int) ([]model.Contact, error) {
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(q)) + "%"
	rows, err := s.db.Query(ctx, `
		SELECT `+contactCols+` FROM contacts
		WHERE tenant_id = $1 AND ($2 = 0 OR id < $2)
		  AND ($3 = '%%' OR external_id ILIKE $3 OR email ILIKE $3 OR attrs->>'name' ILIKE $3 OR attrs->>'phone' ILIKE $3)
		ORDER BY id DESC LIMIT $4`, tenant, int64(afterID), like, limit)
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
		SELECT (SELECT count(*) FROM contacts WHERE tenant_id = $1),
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
