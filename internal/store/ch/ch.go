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

// DeleteEventsBefore removes a workspace's events (and message outcomes) older than days.
func (s *Store) DeleteEventsBefore(ctx context.Context, tenant int64, days int) error {
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, table := range []string{"events", "message_events"} {
		if err := s.conn.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = ? AND ts < ?", uint64(tenant), cutoff); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	return nil
}
