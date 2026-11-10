package chsink

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
)

func rec(t *testing.T, e event.Event) *kgo.Record {
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Value: b}
}

func TestSplit(t *testing.T) {
	s := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return time.Unix(100, 0) }
	recs := []*kgo.Record{
		rec(t, event.Event{ID: "a", ContactID: 1, Name: "signup", NewContact: true, Profile: map[string]any{"city": "Pune"}}),
		rec(t, event.Event{ID: "b", ContactID: 1, Name: "view"}),
		{Value: []byte("not json")},
		rec(t, event.Event{ID: "c", ContactID: 1, Name: "identify", ChangedAttrs: []string{"city"}, Profile: map[string]any{"city": "Delhi"}}),
		rec(t, event.Event{ID: "d", TenantID: 3, ContactID: 1, Name: event.ContactMerged, Props: map[string]any{"from_contact_id": 9}}),
		{Topic: event.TopicMembership, Value: []byte(`{"tenant_id":3,"segment_id":4,"contact_id":1,"entered":true,"at":"2026-11-10T09:00:00Z"}`)},
	}
	b := s.Split(recs)
	evs, snaps := b.Events, b.Snaps
	if len(b.Merges) != 1 || b.Merges[0] != (Merge{TenantID: 3, From: 9, To: 1}) {
		t.Errorf("merges = %+v", b.Merges)
	}
	if len(b.Members) != 1 || !b.Members[0].Member || b.Members[0].SegmentID != 4 || b.Members[0].Version == 0 {
		t.Errorf("members = %+v", b.Members)
	}
	if len(evs) != 4 {
		t.Fatalf("%d events, want 4 (bad record skipped)", len(evs))
	}
	if len(snaps) != 2 {
		t.Fatalf("%d snapshots, want 2 (new contact + attribute change)", len(snaps))
	}
	if snaps[1].Version <= snaps[0].Version || snaps[1].Attrs["city"] != "Delhi" {
		t.Errorf("later snapshot must win: %+v", snaps)
	}
}
