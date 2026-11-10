// Package pg is the Postgres store for mutable state: tenants, contacts, segments, templates,
// journeys, enrollments and timers. Every query that touches tenant data takes the tenant id.
package pg

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pulse/internal/model"
	"pulse/internal/schedule"
)

// ErrNotFound is returned when a row does not exist (or belongs to another tenant).
var ErrNotFound = errors.New("not found")

type Store struct {
	db *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	for i := 0; ; i++ {
		if err = db.Ping(ctx); err == nil || i == 30 {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() { s.db.Close() }

// Migrate applies every *.sql file in dir that has not been applied yet, in name order.
func (s *Store) Migrate(ctx context.Context, files fs.FS, dir string) error {
	if _, err := s.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	names, err := fs.Glob(files, dir+"/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var done bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// ---- tenants ----

func (s *Store) TenantByKeyHash(ctx context.Context, keyHash string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `SELECT id FROM tenants WHERE api_key_hash = $1`, keyHash).Scan(&id)
	return id, notFound(err)
}

// ---- contacts ----

const contactCols = `tenant_id, id, external_id, email, attrs, created_at, updated_at`

func scanContact(row pgx.Row) (model.Contact, error) {
	var c model.Contact
	var id int64
	err := row.Scan(&c.TenantID, &id, &c.ExternalID, &c.Email, &c.Attrs, &c.CreatedAt, &c.UpdatedAt)
	c.ID = uint32(id)
	if c.Attrs == nil {
		c.Attrs = map[string]any{}
	}
	return c, notFound(err)
}

// ResolveContacts returns the contacts for a batch of external ids, creating missing ones with fresh
// dense ids: one lookup, one id reservation and one multi-row insert. created lists the ids made here.
func (s *Store) ResolveContacts(ctx context.Context, tenant int64, externalIDs []string) (map[string]model.Contact, map[string]bool, error) {
	found := make(map[string]model.Contact, len(externalIDs))
	created := map[string]bool{}
	lookup := func(ids []string) error { return s.lookupContacts(ctx, tenant, ids, found) }
	if err := lookup(externalIDs); err != nil {
		return nil, nil, err
	}

	var missing []string
	for _, ext := range externalIDs {
		if _, ok := found[ext]; !ok {
			missing = append(missing, ext)
		}
	}
	if len(missing) == 0 {
		return found, created, nil
	}

	// One UPDATE reserves ids for the whole batch; the row lock is held only for that statement,
	// so partitions creating contacts in parallel barely contend.
	var end int64
	if err := s.db.QueryRow(ctx,
		`UPDATE tenant_counters SET next_contact = next_contact + $2 WHERE tenant_id = $1 RETURNING next_contact`,
		tenant, len(missing)).Scan(&end); err != nil {
		return nil, nil, fmt.Errorf("allocate contact ids: %w", notFound(err))
	}
	ids := make([]int64, len(missing))
	for i := range ids {
		ids[i] = end - int64(len(missing)) + int64(i)
	}
	rows, err := s.db.Query(ctx, `
		INSERT INTO contacts (tenant_id, id, external_id)
		SELECT $1, unnest($2::bigint[]), unnest($3::text[])
		ON CONFLICT (tenant_id, external_id) DO NOTHING
		RETURNING `+contactCols, tenant, ids, missing)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		found[c.ExternalID], created[c.ExternalID] = c, true
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var raced []string // created concurrently elsewhere; their reserved ids are skipped
	for _, ext := range missing {
		if !created[ext] {
			raced = append(raced, ext)
		}
	}
	if len(raced) > 0 {
		if err := lookup(raced); err != nil {
			return nil, nil, err
		}
	}
	return found, created, nil
}

// LookupContacts returns the contacts that exist for these external ids; it never creates any.
func (s *Store) LookupContacts(ctx context.Context, tenant int64, externalIDs []string) (map[string]model.Contact, error) {
	found := make(map[string]model.Contact, len(externalIDs))
	return found, s.lookupContacts(ctx, tenant, externalIDs, found)
}

func (s *Store) lookupContacts(ctx context.Context, tenant int64, ids []string, found map[string]model.Contact) error {
	rows, err := s.db.Query(ctx,
		`SELECT `+contactCols+` FROM contacts WHERE tenant_id = $1 AND external_id = ANY($2)`, tenant, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return err
		}
		found[c.ExternalID] = c
	}
	return rows.Err()
}

// AttrUpdate is one contact's new attribute set.
type AttrUpdate struct {
	TenantID int64
	ID       uint32
	Email    string
	Attrs    map[string]any
}

// SaveContactsAttrs writes many profiles with one UPDATE statement per tenant.
func (s *Store) SaveContactsAttrs(ctx context.Context, updates []AttrUpdate) error {
	type cols struct {
		ids    []int64
		emails []string
		attrs  []map[string]any
	}
	byTenant := map[int64]*cols{}
	for _, u := range updates {
		c := byTenant[u.TenantID]
		if c == nil {
			c = &cols{}
			byTenant[u.TenantID] = c
		}
		c.ids, c.emails, c.attrs = append(c.ids, int64(u.ID)), append(c.emails, u.Email), append(c.attrs, u.Attrs)
	}
	for tenant, c := range byTenant {
		if _, err := s.db.Exec(ctx, `
			UPDATE contacts c SET attrs = u.attrs, email = u.email, updated_at = now()
			FROM unnest($2::bigint[], $3::text[], $4::jsonb[]) AS u(id, email, attrs)
			WHERE c.tenant_id = $1 AND c.id = u.id`, tenant, c.ids, c.emails, c.attrs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ContactByExternalID(ctx context.Context, tenant int64, externalID string) (model.Contact, error) {
	return scanContact(s.db.QueryRow(ctx,
		`SELECT `+contactCols+` FROM contacts WHERE tenant_id = $1 AND external_id = $2`, tenant, externalID))
}

// ContactsByIDs loads several contacts of one tenant by dense id.
func (s *Store) ContactsByIDs(ctx context.Context, tenant int64, ids []uint32) ([]model.Contact, error) {
	ids64 := make([]int64, len(ids))
	for i, id := range ids {
		ids64[i] = int64(id)
	}
	rows, err := s.db.Query(ctx, `SELECT `+contactCols+` FROM contacts WHERE tenant_id = $1 AND id = ANY($2)`, tenant, ids64)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ContactByID(ctx context.Context, tenant int64, id uint32) (model.Contact, error) {
	return scanContact(s.db.QueryRow(ctx,
		`SELECT `+contactCols+` FROM contacts WHERE tenant_id = $1 AND id = $2`, tenant, int64(id)))
}

// ---- segments ----

const segmentCols = `id, tenant_id, name, kind, rule, has_window, status, built_at, public, description`

func scanSegments(rows pgx.Rows) ([]model.Segment, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Segment, error) {
		var s model.Segment
		err := r.Scan(&s.ID, &s.TenantID, &s.Name, &s.Kind, &s.Rule, &s.HasWindow, &s.Status, &s.BuiltAt, &s.Public, &s.Description)
		return s, err
	})
}

func (s *Store) CreateSegment(ctx context.Context, tenant int64, name string, rule []byte, hasWindow bool) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO segments (tenant_id, name, rule, has_window) VALUES ($1, $2, $3, $4) RETURNING id`,
		tenant, name, rule, hasWindow).Scan(&id)
	return id, err
}

func (s *Store) ListSegments(ctx context.Context, tenant int64) ([]model.Segment, error) {
	rows, err := s.db.Query(ctx, `SELECT `+segmentCols+` FROM segments WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

func (s *Store) GetSegment(ctx context.Context, tenant, id int64) (model.Segment, error) {
	rows, err := s.db.Query(ctx, `SELECT `+segmentCols+` FROM segments WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err != nil {
		return model.Segment{}, err
	}
	segs, err := scanSegments(rows)
	if err != nil || len(segs) == 0 {
		return model.Segment{}, errors.Join(err, ErrNotFound)
	}
	return segs[0], nil
}

// AllSegments loads every segment of every tenant for the streaming index.
func (s *Store) AllSegments(ctx context.Context) ([]model.Segment, error) {
	rows, err := s.db.Query(ctx, `SELECT `+segmentCols+` FROM segments ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// ClaimSegmentWork claims one segment that needs a backfill (status 'new') or, failing that,
// a sweep (event windows, not swept within sweepEvery). Returns ok=false when there is nothing to do.
// A crashed worker's 'building' claim expires after 10 minutes.
func (s *Store) ClaimSegmentWork(ctx context.Context, sweepEvery time.Duration) (seg model.Segment, source string, ok bool, err error) {
	rows, err := s.db.Query(ctx, `
		UPDATE segments SET status = 'building', swept_at = now()
		WHERE id = (
			SELECT id FROM segments
			WHERE status = 'new'
			   OR (status = 'building' AND swept_at < now() - interval '10 minutes')
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+segmentCols)
	if err != nil {
		return seg, "", false, err
	}
	if segs, err := scanSegments(rows); err != nil || len(segs) == 1 {
		if err != nil {
			return seg, "", false, err
		}
		return segs[0], "backfill", true, nil
	}

	rows, err = s.db.Query(ctx, `
		UPDATE segments SET swept_at = now()
		WHERE id = (
			SELECT id FROM segments
			WHERE status = 'ready' AND has_window AND (swept_at IS NULL OR swept_at < now() - $1::interval)
			ORDER BY swept_at NULLS FIRST LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+segmentCols, sweepEvery)
	if err != nil {
		return seg, "", false, err
	}
	segs, err := scanSegments(rows)
	if err != nil || len(segs) == 0 {
		return seg, "", false, err
	}
	return segs[0], "sweep", true, nil
}

func (s *Store) MarkSegmentReady(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE segments SET status = 'ready', built_at = now(), swept_at = now() WHERE id = $1`, id)
	return err
}

// ---- templates ----

const templateCols = `id, tenant_id, name, channel, subject, html, content, builder, transactional, updated_at`

func scanTemplate(row pgx.Row) (model.Template, error) {
	var t model.Template
	err := row.Scan(&t.ID, &t.TenantID, &t.Name, &t.Channel, &t.Subject, &t.HTML, &t.Content, &t.Builder, &t.Transactional, &t.UpdatedAt)
	return t, notFound(err)
}

func orEmptyJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (s *Store) CreateTemplate(ctx context.Context, t model.Template) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO templates (tenant_id, name, channel, subject, html, content, builder, transactional) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		t.TenantID, t.Name, t.Channel, t.Subject, t.HTML, orEmptyJSON(t.Content), nullJSON(t.Builder), t.Transactional).Scan(&id)
	return id, err
}

func (s *Store) UpdateTemplate(ctx context.Context, t model.Template) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE templates SET name = $3, subject = $4, html = $5, content = $6, builder = $7, transactional = $8, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`,
		t.TenantID, t.ID, t.Name, t.Subject, t.HTML, orEmptyJSON(t.Content), nullJSON(t.Builder), t.Transactional)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) Template(ctx context.Context, tenant, id int64) (model.Template, error) {
	return scanTemplate(s.db.QueryRow(ctx, `SELECT `+templateCols+` FROM templates WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// ListTemplates lists a workspace's templates, optionally for one channel. The (large) builder
// state is left out of lists.
func (s *Store) ListTemplates(ctx context.Context, tenant int64, ch string) ([]model.Template, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, tenant_id, name, channel, subject, '', content, NULL::jsonb, transactional, updated_at FROM templates
		WHERE tenant_id = $1 AND ($2 = '' OR channel = $2) ORDER BY updated_at DESC`, tenant, ch)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Template, error) { return scanTemplate(r) })
}

func (s *Store) DeleteTemplate(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM templates WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ---- journeys ----

func (s *Store) CreateJourney(ctx context.Context, tenant int64, name string, graph []byte, w schedule.Window) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO journeys (tenant_id, name, graph, start_at, end_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		tenant, name, graph, w.Start, w.End).Scan(&id)
	return id, err
}

func (s *Store) SetJourneyStatus(ctx context.Context, tenant, id int64, status string) error {
	tag, err := s.db.Exec(ctx, `UPDATE journeys SET status = $3 WHERE tenant_id = $1 AND id = $2`, tenant, id, status)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) ActiveJourneys(ctx context.Context) ([]model.Journey, error) {
	rows, err := s.db.Query(ctx, `SELECT `+journeyCols+` FROM journeys WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Journey, error) { return scanJourney(r) })
}

// RecordSplitWinner records a split step's automatic A/B winner once: false when the step already
// has one (another journey worker decided first) or the journey is gone.
func (s *Store) RecordSplitWinner(ctx context.Context, tenant, journey int64, step string, decision []byte) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE journeys SET split_winners = split_winners || jsonb_build_object($3::text, $4::jsonb)
		WHERE tenant_id = $1 AND id = $2 AND NOT split_winners ? $3`, tenant, journey, step, decision)
	return err == nil && tag.RowsAffected() == 1, err
}

// JourneyStats counts enrollments by status and by current node.
func (s *Store) JourneyStats(ctx context.Context, tenant, id int64) (map[string]map[string]int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT status, node, count(*) FROM journey_enrollments WHERE tenant_id = $1 AND journey_id = $2 GROUP BY 1, 2`,
		tenant, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{"status": {}, "node": {}}
	for rows.Next() {
		var status, node string
		var n int
		if err := rows.Scan(&status, &node, &n); err != nil {
			return nil, err
		}
		out["status"][status] += n
		out["node"][node] += n
	}
	return out, rows.Err()
}

// ---- enrollments and timers ----

// Reentry is a journey's rule for contacts who have been through it before.
type Reentry struct {
	Mode string // once | always | after_days
	Days int
}

// Enroll starts a contact on a journey at entryNode, unless the re-entry rule says no. run reports
// whether the caller should execute the enrollment: true for a new one, and also for a live one that
// never left its entry node (a previous attempt failed after the insert; sends are idempotent).
// Only one enrollment per contact can be live; the partial unique index enforces it under races.
func (s *Store) Enroll(ctx context.Context, tenant, journey int64, contact uint32, entryNode string, re Reentry) (en model.Enrollment, run bool, err error) {
	en = model.Enrollment{TenantID: tenant, JourneyID: journey, ContactID: contact}
	var last struct {
		id      int64
		node    string
		status  string
		created time.Time
		jumps   int
	}
	err = s.db.QueryRow(ctx, `
		SELECT id, node, status, created_at, jumps FROM journey_enrollments
		WHERE journey_id = $1 AND contact_id = $2 ORDER BY created_at DESC LIMIT 1`, journey, int64(contact)).
		Scan(&last.id, &last.node, &last.status, &last.created, &last.jumps)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return en, false, err
	case last.status == "active" || last.status == "waiting":
		en.ID, en.Jumps = last.id, last.jumps
		return en, last.status == "active" && last.node == entryNode, nil
	case re.Mode == "always":
	case re.Mode == "after_days" && time.Since(last.created) >= time.Duration(re.Days)*24*time.Hour:
	default:
		return en, false, nil // once (the default), or too soon
	}
	err = s.db.QueryRow(ctx, `
		INSERT INTO journey_enrollments (tenant_id, journey_id, contact_id, node) VALUES ($1, $2, $3, $4)
		ON CONFLICT (journey_id, contact_id) WHERE status IN ('active', 'waiting') DO NOTHING RETURNING id`,
		tenant, journey, int64(contact), entryNode).Scan(&en.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return en, false, nil // entered concurrently through another trigger
	}
	return en, err == nil, err
}

// SetNode records where an enrollment is. Finishing it also removes any timer or event wait left.
func (s *Store) SetNode(ctx context.Context, enrollment int64, node, status string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE journey_enrollments SET node = $2, status = $3, updated_at = now() WHERE id = $1`, enrollment, node, status); err != nil {
			return err
		}
		if status != "completed" {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM journey_timers WHERE enrollment_id = $1`, enrollment); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM journey_event_waits WHERE enrollment_id = $1`, enrollment)
		return err
	})
}

// Wait parks the enrollment at waitNode and schedules it to resume at resumeNode.
func (s *Store) Wait(ctx context.Context, enrollment int64, waitNode, resumeNode string, due time.Time) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		return parkTx(ctx, tx, enrollment, waitNode, resumeNode, due)
	})
}

func parkTx(ctx context.Context, tx pgx.Tx, enrollment int64, waitNode, resumeNode string, due time.Time) error {
	if _, err := tx.Exec(ctx,
		`UPDATE journey_enrollments SET node = $2, status = 'waiting', updated_at = now() WHERE id = $1`,
		enrollment, waitNode); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO journey_timers (enrollment_id, node, due_at, wait_node) VALUES ($1, $2, $3, $4)
		ON CONFLICT (enrollment_id) DO UPDATE SET node = EXCLUDED.node, due_at = EXCLUDED.due_at, wait_node = EXCLUDED.wait_node`,
		enrollment, resumeNode, due, waitNode)
	return err
}

// WaitForEvent parks the enrollment until the contact does eventName (resume at yesNode) or the
// timeout passes (resume at timeoutNode), whichever comes first.
// messageID, when set, limits it to that message's event (a message decision).
func (s *Store) WaitForEvent(ctx context.Context, en model.Enrollment, waitNode, eventName, messageID, yesNode, timeoutNode string, due time.Time) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := parkTx(ctx, tx, en.ID, waitNode, timeoutNode, due); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO journey_event_waits (enrollment_id, tenant_id, contact_id, event, wait_node, yes_node, message_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (enrollment_id) DO UPDATE SET event = EXCLUDED.event, wait_node = EXCLUDED.wait_node, yes_node = EXCLUDED.yes_node,
				message_id = EXCLUDED.message_id`,
			en.ID, en.TenantID, int64(en.ContactID), eventName, waitNode, yesNode, messageID)
		return err
	})
}

// resumeGrace is how long a resumed enrollment has to reach its next wait or exit before the
// safety timer retries it (a worker crashed mid-run).
const resumeGrace = 2 * time.Minute

// Resume moves a waiting enrollment from waitNode to resumeNode, only if it is still parked there:
// the compare-and-set makes a timeout and its awaited event race safely (exactly one wins). The
// winner drops the event wait and leaves a safety timer at resumeNode, which the next wait replaces
// and an exit removes; if the run dies in between, the timer retries it (sends are idempotent).
func (s *Store) Resume(ctx context.Context, enrollment int64, waitNode, resumeNode string) (bool, error) {
	won := false
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE journey_enrollments SET node = $3, status = 'active', updated_at = now()
			WHERE id = $1 AND node = $2 AND status = 'waiting'`, enrollment, waitNode, resumeNode)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		won = true
		if _, err := tx.Exec(ctx, `DELETE FROM journey_event_waits WHERE enrollment_id = $1`, enrollment); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO journey_timers (enrollment_id, node, due_at, wait_node) VALUES ($1, $2, now() + $3::interval, NULL)
			ON CONFLICT (enrollment_id) DO UPDATE SET node = EXCLUDED.node, due_at = EXCLUDED.due_at, wait_node = NULL`,
			enrollment, resumeNode, resumeGrace)
		return err
	})
	return won, err
}

// EnrollmentStatus returns an enrollment's status (active, waiting, completed, converted).
func (s *Store) EnrollmentStatus(ctx context.Context, enrollment int64) (string, error) {
	var st string
	err := s.db.QueryRow(ctx, `SELECT status FROM journey_enrollments WHERE id = $1`, enrollment).Scan(&st)
	return st, notFound(err)
}

// EventWait is an enrollment waiting for the event a contact just did.
type EventWait struct {
	Enrollment model.Enrollment
	WaitNode   string
	YesNode    string
	MessageID  string // only this message's event counts ("" = any)
}

func (s *Store) EventWaits(ctx context.Context, tenant int64, contact uint32, eventName string) ([]EventWait, error) {
	rows, err := s.db.Query(ctx, `
		SELECT w.enrollment_id, e.journey_id, e.jumps, w.wait_node, w.yes_node, w.message_id FROM journey_event_waits w
		JOIN journey_enrollments e ON e.id = w.enrollment_id
		WHERE w.tenant_id = $1 AND w.contact_id = $2 AND w.event = $3`, tenant, int64(contact), eventName)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (EventWait, error) {
		w := EventWait{Enrollment: model.Enrollment{TenantID: tenant, ContactID: contact}}
		err := r.Scan(&w.Enrollment.ID, &w.Enrollment.JourneyID, &w.Enrollment.Jumps, &w.WaitNode, &w.YesNode, &w.MessageID)
		return w, err
	})
}

// Convert ends a contact's live enrollments in these journeys as converted (they reached the goal).
func (s *Store) Convert(ctx context.Context, tenant int64, contact uint32, journeys []int64) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE journey_enrollments SET status = 'converted', updated_at = now()
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

// timerLease is how long a claimed timer stays hidden from other pollers. A worker that crashes
// mid-step loses its lease and the step is retried (at-least-once; sends are idempotent).
const timerLease = time.Minute

// ClaimDueTimers leases up to limit due timers, calls fn for each outside any transaction, and deletes
// those fn handled. The claim is one short statement (SKIP LOCKED, so pollers never share a timer)
// rather than a transaction held open during fn: fn may schedule the enrollment's next wait, which
// upserts the same row and would otherwise block on our lock.
func (s *Store) ClaimDueTimers(ctx context.Context, limit int, fn func(model.Timer) error) (int, error) {
	rows, err := s.db.Query(ctx, `
		WITH due AS (
			SELECT enrollment_id FROM journey_timers
			WHERE due_at <= now() ORDER BY due_at LIMIT $1
			FOR UPDATE SKIP LOCKED)
		UPDATE journey_timers t SET due_at = now() + $2::interval
		FROM due, journey_enrollments e
		WHERE t.enrollment_id = due.enrollment_id AND e.id = t.enrollment_id
		RETURNING t.enrollment_id, t.node, t.due_at, coalesce(t.wait_node, ''), e.tenant_id, e.journey_id, e.contact_id, e.jumps`, limit, timerLease)
	if err != nil {
		return 0, err
	}
	timers, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Timer, error) {
		var t model.Timer
		var contact int64
		err := r.Scan(&t.Enrollment.ID, &t.Node, &t.DueAt, &t.WaitNode, &t.Enrollment.TenantID, &t.Enrollment.JourneyID, &contact, &t.Enrollment.Jumps)
		t.Enrollment.ContactID = uint32(contact)
		return t, err
	})
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, t := range timers {
		if err := fn(t); err != nil {
			continue // lease expires and the timer fires again
		}
		// Match on the lease time: if fn scheduled a new wait, the row has a new due_at and is kept.
		if _, err := s.db.Exec(ctx, `DELETE FROM journey_timers WHERE enrollment_id = $1 AND due_at = $2`,
			t.Enrollment.ID, t.DueAt); err != nil {
			return handled, err
		}
		handled++
	}
	return handled, nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
