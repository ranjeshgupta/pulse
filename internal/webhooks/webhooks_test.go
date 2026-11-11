package webhooks

import (
	"encoding/json"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/model"
)

func TestSubscriptionFilters(t *testing.T) {
	h := Webhook{Events: []string{"segment_entered"}, SegmentID: 4}
	if !h.wants(Payload{Event: "segment_entered", SegmentID: 4}) || h.wants(Payload{Event: "segment_entered", SegmentID: 5}) {
		t.Error("segment filter")
	}
	f := Webhook{Events: []string{"form_submitted"}, FormID: 2}
	if !f.wants(Payload{Event: "form_submitted", Props: map[string]any{"form_id": 2.0}}) || f.wants(Payload{Event: "form_submitted", Props: map[string]any{"form_id": 3.0}}) {
		t.Error("form filter")
	}
	every := Webhook{Events: []string{"*"}}
	if every.wants(Payload{Event: ContactCreated}) || !every.wants(Payload{Event: "purchase"}) {
		t.Error("* is every recorded event, not contact_created")
	}
}

func TestContactCreated(t *testing.T) {
	enc := func(e event.Event) *kgo.Record {
		b, _ := json.Marshal(e)
		return &kgo.Record{Topic: event.TopicEnriched, Value: b}
	}
	ps := decode(enc(event.Event{ID: "e1", TenantID: 1, ContactID: 3, Name: "identify", ChangedAttrs: []string{"date_identified", "email"}}))
	if len(ps) != 2 || ps[1].Event != ContactCreated || ps[1].ID != "created:e1" {
		t.Fatalf("payloads = %+v", ps)
	}
	if ps := decode(enc(event.Event{ID: "e2", TenantID: 1, ContactID: 3, Name: "purchase", ChangedAttrs: []string{"last_seen"}})); len(ps) != 1 {
		t.Errorf("only an identification makes contact_created: %+v", ps)
	}
	b, _ := json.Marshal(model.MembershipChange{TenantID: 1, SegmentID: 2, ContactID: 3, Entered: true})
	if ps := decode(&kgo.Record{Topic: event.TopicMembership, Value: b}); len(ps) != 1 || ps[0].Event != "segment_entered" {
		t.Errorf("membership = %+v", ps)
	}
}
