package pg

// Storage for the journey builder's newer steps: jumps, form context, journey states, company
// changes, inbox counts and removing contacts from journeys.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// JourneyState is what a contact has done in one journey.
type JourneyState struct {
	Active    bool // in it now (active or waiting)
	Completed bool // finished it at least once (completed or converted)
}

// Jump moves an enrollment to target, counts the jump and points a pending safety timer there.
func (s *Store) Jump(ctx context.Context, enrollment int64, target string) (int, error) {
	var jumps int
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			UPDATE journey_enrollments SET jumps = jumps + 1, node = $2, updated_at = now() WHERE id = $1 RETURNING jumps`,
			enrollment, target).Scan(&jumps); err != nil {
			return notFound(err)
		}
		_, err := tx.Exec(ctx, `UPDATE journey_timers SET node = $2 WHERE enrollment_id = $1 AND wait_node IS NULL`, enrollment, target)
		return err
	})
	return jumps, err
}

// EnrollmentJumps returns an enrollment's jump count.
func (s *Store) EnrollmentJumps(ctx context.Context, enrollment int64) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT jumps FROM journey_enrollments WHERE id = $1`, enrollment).Scan(&n)
	return n, notFound(err)
}

// SetEnrollmentForm records the form submission (form_submitted props) an enrollment started or resumed with.
func (s *Store) SetEnrollmentForm(ctx context.Context, enrollment int64, props map[string]any) error {
	b, err := json.Marshal(props)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE journey_enrollments SET form = $2 WHERE id = $1`, enrollment, b)
	return err
}

// SetEnrollmentTrigger records the properties of the event that started an enrollment (already
// filtered and capped, see eventschema.MessageProps), for {{event.x}} in its messages.
func (s *Store) SetEnrollmentTrigger(ctx context.Context, enrollment int64, props json.RawMessage) error {
	_, err := s.db.Exec(ctx, `UPDATE journey_enrollments SET trigger = $2 WHERE id = $1`, enrollment, nullJSON(props))
	return err
}

// EnrollmentTrigger returns the recorded trigger event properties (nil when there are none).
func (s *Store) EnrollmentTrigger(ctx context.Context, enrollment int64) (json.RawMessage, error) {
	var b []byte
	err := s.db.QueryRow(ctx, `SELECT trigger FROM journey_enrollments WHERE id = $1`, enrollment).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) || len(b) == 0 {
		return nil, nil
	}
	return b, err
}

// EnrollmentForm returns the recorded form submission, or nil.
func (s *Store) EnrollmentForm(ctx context.Context, enrollment int64) (map[string]any, error) {
	var b []byte
	err := s.db.QueryRow(ctx, `SELECT form FROM journey_enrollments WHERE id = $1`, enrollment).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) || len(b) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

// JourneyStates says, per journey, whether the contact is in it now and whether they finished it.
// Journeys the contact never entered are missing.
func (s *Store) JourneyStates(ctx context.Context, tenant int64, contact uint32, journeys []int64) (map[int64]JourneyState, error) {
	rows, err := s.db.Query(ctx, `
		SELECT journey_id, bool_or(status IN ('active', 'waiting')), bool_or(status IN ('completed', 'converted'))
		FROM journey_enrollments WHERE tenant_id = $1 AND contact_id = $2 AND journey_id = ANY($3) GROUP BY journey_id`,
		tenant, int64(contact), journeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]JourneyState{}
	for rows.Next() {
		var id int64
		var st JourneyState
		if err := rows.Scan(&id, &st.Active, &st.Completed); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// RemoveFromJourneys ends a contact's live enrollments in these journeys (status removed).
func (s *Store) RemoveFromJourneys(ctx context.Context, tenant int64, contact uint32, journeys []int64) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE journey_enrollments SET status = 'removed', updated_at = now()
			WHERE tenant_id = $1 AND contact_id = $2 AND journey_id = ANY($3) AND status IN ('active', 'waiting')
			RETURNING id`, tenant, int64(contact), journeys)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(ids) == 0 {
			return err
		}
		n = len(ids)
		if _, err := tx.Exec(ctx, `DELETE FROM journey_timers WHERE enrollment_id = ANY($1)`, ids); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM journey_event_waits WHERE enrollment_id = ANY($1)`, ids)
		return err
	})
	return n, err
}

// UnreadInbox counts the contact's unread, unexpired app inbox messages.
func (s *Store) UnreadInbox(ctx context.Context, tenant int64, contact uint32) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FROM inbox_messages WHERE tenant_id = $1 AND contact_id = $2
		AND read_at IS NULL AND deleted_at IS NULL AND expires_at > now()`, tenant, int64(contact)).Scan(&n)
	return n, err
}

// MergeCompanyAttrs sets attributes on a company (keeping the others) and returns it.
func (s *Store) MergeCompanyAttrs(ctx context.Context, tenant, id int64, attrs map[string]any) (Company, error) {
	tag, err := s.db.Exec(ctx, `UPDATE companies SET attrs = attrs || $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id, attrs)
	if err == nil && tag.RowsAffected() == 0 {
		return Company{}, ErrNotFound
	}
	if err != nil {
		return Company{}, err
	}
	return s.GetCompany(ctx, tenant, id)
}

// AddCompanyScore adds delta to the company's score attribute, once per key.
func (s *Store) AddCompanyScore(ctx context.Context, tenant, id int64, key string, delta int) (Company, bool, error) {
	applied := false
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO journey_effects (tenant_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenant, key)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		tag, err = tx.Exec(ctx, `
			UPDATE companies SET attrs = attrs || jsonb_build_object('score',
				coalesce(CASE WHEN jsonb_typeof(attrs->'score') = 'number' THEN (attrs->>'score')::numeric END, 0) + $3),
				updated_at = now()
			WHERE tenant_id = $1 AND id = $2`, tenant, id, delta)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		applied = err == nil
		return err
	})
	if err != nil {
		return Company{}, false, err
	}
	c, err := s.GetCompany(ctx, tenant, id)
	return c, applied, err
}

// PruneJourneyEffects forgets effect keys older than 30 days (steps are retried within minutes).
func (s *Store) PruneJourneyEffects(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `DELETE FROM journey_effects WHERE created_at < now() - interval '30 days'`)
	return err
}

// JourneyIDs reports which of these journey ids belong to the workspace.
func (s *Store) JourneyIDs(ctx context.Context, tenant int64, ids []int64) (map[int64]bool, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM journeys WHERE tenant_id = $1 AND id = ANY($2)`, tenant, ids)
	if err != nil {
		return nil, err
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	out := map[int64]bool{}
	for _, id := range got {
		out[id] = true
	}
	return out, err
}
