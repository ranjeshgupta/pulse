// Package ch is the ClickHouse store: the event log, profile snapshots and segment queries.
package ch

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"pulse/internal/attr"
	"pulse/internal/automation"
	"pulse/internal/event"
	"pulse/internal/segment"
)

type Store struct{ conn driver.Conn }

func Open(ctx context.Context, addr, db, user, password string) (*Store, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:        []string{addr},
		Auth:        clickhouse.Auth{Database: db, Username: user, Password: password},
		DialTimeout: 5 * time.Second,
		Compression: &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
	})
	if err != nil {
		return nil, err
	}
	for i := 0; ; i++ {
		if err = conn.Ping(ctx); err == nil || i == 30 {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("clickhouse: %w", err)
	}
	return &Store{conn: conn}, nil
}

func (s *Store) Close() error { return s.conn.Close() }

// Migrate runs every statement of every *.sql file in dir. Statements must be idempotent (IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context, files fs.FS, dir string) error {
	names, err := fs.Glob(files, dir+"/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		for _, stmt := range strings.Split(stripComments(string(b)), ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if err := s.conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}

func stripComments(sql string) string {
	var out []string
	for _, line := range strings.Split(sql, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// InsertEvents writes enriched events in one batch.
func (s *Store) InsertEvents(ctx context.Context, evs []event.Event) error {
	if len(evs) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO events (tenant_id, id, name, contact_id, ts, props)")
	if err != nil {
		return err
	}
	for _, e := range evs {
		props := "{}"
		if len(e.Props) > 0 {
			b, _ := json.Marshal(e.Props)
			props = string(b)
		}
		if err := batch.Append(uint64(e.TenantID), e.ID, e.Name, e.ContactID, e.Ts, props); err != nil {
			return err
		}
	}
	return batch.Send()
}

// ContactSnapshot is one profile version for the contacts table.
type ContactSnapshot struct {
	TenantID   int64
	ID         uint32
	ExternalID string
	Attrs      map[string]any
	Version    uint64
}

func (s *Store) InsertContacts(ctx context.Context, cs []ContactSnapshot) error {
	if len(cs) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO contacts (tenant_id, id, external_id, attrs, version)")
	if err != nil {
		return err
	}
	for _, c := range cs {
		if err := batch.Append(uint64(c.TenantID), c.ID, c.ExternalID, attr.Strings(c.Attrs), c.Version); err != nil {
			return err
		}
	}
	return batch.Send()
}

// MemberRow is one membership state for segment_members. The highest version wins.
type MemberRow struct {
	TenantID  int64
	SegmentID int64
	ContactID uint32
	Member    bool
	Version   uint64
}

// InsertMembers mirrors membership changes, so batch queries can evaluate "in segment" rules.
func (s *Store) InsertMembers(ctx context.Context, rows []MemberRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO segment_members (tenant_id, segment_id, contact_id, member, version)")
	if err != nil {
		return err
	}
	for _, r := range rows {
		var m uint8
		if r.Member {
			m = 1
		}
		if err := batch.Append(uint64(r.TenantID), uint64(r.SegmentID), r.ContactID, m, r.Version); err != nil {
			return err
		}
	}
	return batch.Send()
}

// ContactDayCount is one (contact, UTC day) event count.
type ContactDayCount struct {
	ContactID uint32
	Day       string
	Count     int64
}

// DayCounts runs segment.FilterCountsSQL.
func (s *Store) DayCounts(ctx context.Context, q segment.Query) ([]ContactDayCount, error) {
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactDayCount
	for rows.Next() {
		var d ContactDayCount
		if err := rows.Scan(&d.ContactID, &d.Day, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CopyEvents gives contact to a copy of contact from's events (an identity merge). Ids are kept,
// so a repeated copy adds nothing that counts.
func (s *Store) CopyEvents(ctx context.Context, tenant int64, from, to uint32) error {
	return s.conn.Exec(ctx, `
		INSERT INTO events (tenant_id, id, name, contact_id, ts, props)
		SELECT tenant_id, id, name, ?, ts, props FROM events FINAL
		WHERE tenant_id = ? AND contact_id = ? AND name NOT IN ('contact_merged', 'contact_merged_into')`,
		to, uint64(tenant), from)
}

// SegmentIDs runs a compiled segment query and returns the matching contact ids.
func (s *Store) SegmentIDs(ctx context.Context, q segment.Query) ([]uint32, error) {
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Exec runs a statement (used by tests to clean up).
func (s *Store) Exec(ctx context.Context, sql string, args ...any) error {
	return s.conn.Exec(ctx, sql, args...)
}

// DeleteContacts removes contacts' events, profile snapshots and membership rows (lightweight deletes).
func (s *Store) DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error {
	for _, table := range []string{"events", "message_events", "segment_members", "first_active"} {
		if err := s.conn.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = ? AND contact_id IN ?", uint64(tenant), ids); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	return s.conn.Exec(ctx, "DELETE FROM contacts WHERE tenant_id = ? AND id IN ?", uint64(tenant), ids)
}

// retentionWhere is the WHERE clause of a retention delete (automation.Delete): the workspace,
// the delete's name predicate, and ts before the cutoff (partition pruning keeps it to old months).
func retentionWhere(tenant int64, d automation.Delete, cutoff time.Time) (string, []any, error) {
	if d.Table != automation.TableEvents && d.Table != automation.TableMessageEvents {
		return "", nil, fmt.Errorf("retention: unknown table %q", d.Table)
	}
	q := "FROM " + d.Table + " WHERE tenant_id = ? AND ts < ?"
	args := []any{uint64(tenant), cutoff}
	if len(d.Names) > 0 {
		if d.Table != automation.TableEvents {
			return "", nil, fmt.Errorf("retention: names apply to events only")
		}
		if d.NotIn {
			q += " AND name NOT IN ?"
		} else {
			q += " AND name IN ?"
		}
		args = append(args, d.Names)
	}
	return q, args, nil
}

// HasOlder reports whether a retention delete would remove anything: one row is enough, so it
// reads only the old partitions and stops at the first match.
func (s *Store) HasOlder(ctx context.Context, tenant int64, d automation.Delete, cutoff time.Time) (bool, error) {
	where, args, err := retentionWhere(tenant, d, cutoff)
	if err != nil {
		return false, err
	}
	rows, err := s.conn.Query(ctx, "SELECT 1 "+where+" LIMIT 1", args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// CountOlder counts what a retention delete would remove now (the settings preview).
func (s *Store) CountOlder(ctx context.Context, tenant int64, d automation.Delete, cutoff time.Time) (uint64, error) {
	where, args, err := retentionWhere(tenant, d, cutoff)
	if err != nil {
		return 0, err
	}
	var n uint64
	err = s.conn.QueryRow(ctx, "SELECT count() "+where, args...).Scan(&n)
	return n, err
}

// DeleteOlder runs a retention delete (a lightweight DELETE: rows vanish from queries at once and
// are dropped from disk by later merges).
func (s *Store) DeleteOlder(ctx context.Context, tenant int64, d automation.Delete, cutoff time.Time) error {
	where, args, err := retentionWhere(tenant, d, cutoff)
	if err != nil {
		return err
	}
	if err := s.conn.Exec(ctx, "DELETE "+where, args...); err != nil {
		return fmt.Errorf("%s: %w", d.Table, err)
	}
	return nil
}
