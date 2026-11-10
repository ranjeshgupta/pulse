// Package chsink bulk-loads events.enriched into ClickHouse: every event into `events`, and a new
// profile version into `contacts` whenever a contact is created or its attributes change. It also
// mirrors segment.membership into `segment_members`, so batch queries can use "in segment" rules,
// and copies a visitor's history to the known contact on an identity merge.
package chsink

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/store/ch"
)

type Writer interface {
	InsertEvents(ctx context.Context, evs []event.Event) error
	InsertContacts(ctx context.Context, cs []ch.ContactSnapshot) error
	InsertMembers(ctx context.Context, rows []ch.MemberRow) error
	CopyEvents(ctx context.Context, tenant int64, from, to uint32) error
}

// Batch is one poll's worth of rows.
type Batch struct {
	Events  []event.Event
	Snaps   []ch.ContactSnapshot
	Members []ch.MemberRow
	Merges  []Merge
}

// Merge copies contact From's events to contact To.
type Merge struct {
	TenantID int64
	From, To uint32
}

type Sink struct {
	w   Writer
	log *slog.Logger
	now func() time.Time
}

func New(w Writer, log *slog.Logger) *Sink { return &Sink{w: w, log: log, now: time.Now} }

// Split decodes a batch into event rows and profile snapshots. Undecodable records are skipped
// (they could never be inserted, and retrying would block the partition forever).
func (s *Sink) Split(recs []*kgo.Record) Batch {
	var b Batch
	version := uint64(s.now().UnixNano())
	for _, r := range recs {
		if r.Topic == event.TopicMembership {
			var c model.MembershipChange
			if err := json.Unmarshal(r.Value, &c); err != nil {
				s.log.Error("skipping undecodable record", "topic", r.Topic, "offset", r.Offset, "err", err)
				continue
			}
			// Changes of one contact are ordered on their partition; At breaks ties across sources.
			b.Members = append(b.Members, ch.MemberRow{TenantID: c.TenantID, SegmentID: c.SegmentID, ContactID: c.ContactID,
				Member: c.Entered, Version: uint64(c.At.UnixNano())})
			continue
		}
		var e event.Event
		if err := json.Unmarshal(r.Value, &e); err != nil {
			s.log.Error("skipping undecodable record", "topic", r.Topic, "offset", r.Offset, "err", err)
			continue
		}
		b.Events = append(b.Events, e)
		if e.NewContact || len(e.ChangedAttrs) > 0 {
			// Records of one contact arrive in order, so a later record gets a higher version.
			version++
			b.Snaps = append(b.Snaps, ch.ContactSnapshot{
				TenantID: e.TenantID, ID: e.ContactID, ExternalID: e.ExternalID, Attrs: e.Profile, Version: version,
			})
		}
		if e.Name == event.ContactMerged {
			if from, ok := attr.Number(attr.String(e.Props["from_contact_id"])); ok && from > 0 {
				b.Merges = append(b.Merges, Merge{TenantID: e.TenantID, From: uint32(from), To: e.ContactID})
			}
		}
	}
	return b
}

// Handle writes a batch. Every step is safe to repeat: rows collapse on their keys, and copied
// events keep their ids, which segment counts deduplicate.
func (s *Sink) Handle(ctx context.Context, recs []*kgo.Record) error {
	b := s.Split(recs)
	if err := s.w.InsertContacts(ctx, b.Snaps); err != nil {
		return err
	}
	if err := s.w.InsertMembers(ctx, b.Members); err != nil {
		return err
	}
	if err := s.w.InsertEvents(ctx, b.Events); err != nil {
		return err
	}
	for _, m := range b.Merges {
		if err := s.w.CopyEvents(ctx, m.TenantID, m.From, m.To); err != nil {
			return err
		}
	}
	return nil
}
