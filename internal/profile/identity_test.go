package profile

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/automation"
	"pulse/internal/event"
	"pulse/internal/identity"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// idStore is memStore plus automation settings and contact_keys.
type idStore struct {
	*memStore
	set  automation.Settings
	keys map[pg.KeyValue]map[uint32]bool
}

func (s *idStore) AutomationSettings(context.Context, int64) (automation.Settings, error) {
	return s.set, nil
}

func (s *idStore) MatchIdentityKeys(_ context.Context, _ int64, kvs []pg.KeyValue) (map[pg.KeyValue][]model.Contact, error) {
	out := map[pg.KeyValue][]model.Contact{}
	for _, kv := range kvs {
		for _, c := range s.contacts {
			if s.keys[kv][c.ID] && c.Attrs[event.AttrMergedInto] == nil {
				out[kv] = append(out[kv], c)
			}
		}
	}
	return out, nil
}

func (s *idStore) ReplaceContactKeys(_ context.Context, _ int64, fields []string, cs []pg.ContactKeys) error {
	for _, c := range cs {
		for kv, ids := range s.keys {
			for _, f := range fields {
				if kv.Field == f {
					delete(ids, c.ContactID)
				}
			}
		}
		for f, v := range c.Values {
			kv := pg.KeyValue{Field: f, Value: v}
			if s.keys[kv] == nil {
				s.keys[kv] = map[uint32]bool{}
			}
			s.keys[kv][c.ContactID] = true
		}
	}
	return nil
}

func TestIdentityKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	set := automation.Defaults()
	set.Identity.Keys = []identity.Key{{Field: "external_id"}, {Field: "email", Mode: identity.Merge}, {Field: "phone", Mode: identity.Merge}, {Field: "username", Mode: identity.Suggest}}
	store := &idStore{memStore: &memStore{contacts: map[string]model.Contact{}}, set: set, keys: map[pg.KeyValue]map[uint32]bool{}}
	pub := &memPub{}
	svc := New(store, rdb, pub)
	ctx := context.Background()
	ts := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	n := 0
	// send runs a batch and returns what went to events.enriched and what went back to events.raw.
	send := func(evs ...event.Event) (enriched, raw []event.Event) {
		var recs []*kgo.Record
		for _, e := range evs {
			if e.ID == "" {
				n++
				e.ID = "e" + string(rune('a'+n))
			}
			e.TenantID, e.Ts = 1, ts
			b, _ := json.Marshal(e)
			recs = append(recs, &kgo.Record{Value: b})
		}
		before := len(pub.recs)
		if err := svc.HandleBatch(ctx, recs); err != nil {
			t.Fatal(err)
		}
		for _, r := range pub.recs[before:] {
			var e event.Event
			_ = json.Unmarshal(r.Value, &e)
			if r.Topic == event.TopicRaw {
				raw = append(raw, e)
			} else {
				enriched = append(enriched, e)
			}
		}
		return enriched, raw
	}

	send(event.Event{ExternalID: "crm-1", Name: "identify", Email: " Asha@X.co ", Attrs: map[string]any{"username": "asha"}})
	if !store.keys[pg.KeyValue{Field: "email", Value: "asha@x.co"}][store.contacts["crm-1"].ID] {
		t.Fatalf("crm-1's email key not stored: %v", store.keys)
	}

	// A new id with a known email goes to the contact that has it, with a merge of the new one.
	enriched, raw := send(event.Event{ExternalID: "app-9", Name: "login", Attrs: map[string]any{"email": "asha@x.co", "city": "Pune"}})
	if len(enriched) != 0 || len(raw) != 2 || raw[0].Name != event.MergeRequested || raw[0].ExternalID != "crm-1" ||
		raw[0].Props["loser"] != "app-9" || raw[1].Name != "login" || raw[1].ExternalID != "crm-1" || !raw[1].Rerouted {
		t.Fatalf("enriched %+v, raw %+v", enriched, raw)
	}
	_, raw = send(raw...)
	if len(raw) != 0 || store.contacts["app-9"].Attrs[event.AttrMergedInto] != "crm-1" || store.contacts["crm-1"].Attrs["city"] != "Pune" {
		t.Fatalf("after reroute: raw %+v, app-9 %v, crm-1 %v", raw, store.contacts["app-9"].Attrs, store.contacts["crm-1"].Attrs)
	}
	// Its later events follow it.
	if enriched, _ = send(event.Event{ExternalID: "app-9", Name: "view"}); enriched[0].ExternalID != "crm-1" {
		t.Errorf("app-9's later event went to %s", enriched[0].ExternalID)
	}

	// A known contact that gets another contact's phone takes that contact in.
	send(event.Event{ExternalID: "crm-2", Name: "identify", Attrs: map[string]any{"phone": "+91 98765 43210", "plan": "pro"}})
	enriched, raw = send(event.Event{ExternalID: "crm-1", Name: "identify", Attrs: map[string]any{"phone": "+919876543210"}})
	if len(raw) != 0 || len(enriched) < 2 || enriched[0].Name != event.MergeRequested || enriched[0].Props["loser"] != "crm-2" || enriched[0].Props["key"] != "phone" {
		t.Fatalf("merge by phone: enriched %+v raw %+v", enriched, raw)
	}
	if store.contacts["crm-2"].Attrs[event.AttrMergedInto] != "crm-1" || store.contacts["crm-1"].Attrs["plan"] != "pro" {
		t.Errorf("crm-2 %v, crm-1 %v", store.contacts["crm-2"].Attrs, store.contacts["crm-1"].Attrs)
	}
	if store.keys[pg.KeyValue{Field: "phone", Value: "+919876543210"}][store.contacts["crm-2"].ID] {
		t.Error("a merged contact keeps its key values")
	}

	// A key that only suggests never merges.
	if enriched, raw = send(event.Event{ExternalID: "crm-3", Name: "identify", Attrs: map[string]any{"username": "ASHA"}}); len(raw) != 0 || enriched[0].ExternalID != "crm-3" {
		t.Errorf("suggest key merged: %+v %+v", enriched, raw)
	}

	// Browser events and anonymous visitors never drive a merge, even with a known email.
	if enriched, raw = send(event.Event{ExternalID: "web-1", Name: "lead", Email: "asha@x.co", FromBrowser: true}); len(raw) != 0 || enriched[0].ExternalID != "web-1" {
		t.Errorf("browser event merged: %+v %+v", enriched, raw)
	}
	send(event.Event{ExternalID: "anon-77", Name: "view", Attrs: map[string]any{"phone": "+447700900123"}})
	if enriched, raw = send(event.Event{ExternalID: "crm-9", Name: "identify", Attrs: map[string]any{"phone": "+447700900123"}}); len(raw) != 0 || len(enriched) != 1 {
		t.Errorf("an anonymous visitor was matched: %+v %+v", enriched, raw)
	}

	// An external id that is an email address fills the email (and then matches by it).
	send(event.Event{ExternalID: "ravi@y.co", Name: "signup"})
	if c := store.contacts["ravi@y.co"]; c.Attrs["email"] != "ravi@y.co" || c.Email != "ravi@y.co" {
		t.Errorf("email from id: %+v", c)
	}
	if _, raw = send(event.Event{ExternalID: "web-7", Name: "lead", Email: "RAVI@y.co"}); len(raw) != 2 || raw[1].ExternalID != "ravi@y.co" {
		t.Errorf("match on an email that came from an id: %+v", raw)
	}
}

// Without merging keys nothing changes: no lookups, events stay where they are.
func TestIdentityDefaultsChangeNothing(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := &idStore{memStore: &memStore{contacts: map[string]model.Contact{}}, set: automation.Defaults(), keys: map[pg.KeyValue]map[uint32]bool{}}
	pub := &memPub{}
	svc := New(store, rdb, pub)
	for i, ext := range []string{"a", "b"} {
		b, _ := json.Marshal(event.Event{ID: ext, TenantID: 1, ExternalID: ext, Name: "identify", Email: "same@x.co", Ts: time.Unix(int64(i), 0)})
		if err := svc.HandleBatch(context.Background(), []*kgo.Record{{Value: b}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(pub.recs) != 2 || store.contacts["b"].Attrs[event.AttrMergedInto] != nil || len(store.keys) != 0 {
		t.Errorf("defaults merged or stored keys: %d records, keys %v", len(pub.recs), store.keys)
	}
}
