package profile

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/eventschema"
	"pulse/internal/model"
)

func TestPhoneCountry(t *testing.T) {
	for in, want := range map[string]string{
		"+919812345678": "IN", "+91 98123 45678": "IN", "0044 20 7946 0958": "GB", "+14155550123": "US",
		"+16135550123": "CA", "9812345678": "", "": "", "+": "", "+999123": "",
	} {
		if got := PhoneCountry(in); got != want {
			t.Errorf("PhoneCountry(%q) = %q, want %q", in, got, want)
		}
	}
	merged, changed := Merge(map[string]any{}, &event.Event{Attrs: map[string]any{"phone": "+919812345678"}})
	if merged["phone_country"] != "IN" || !contains(changed, "phone_country") {
		t.Errorf("merged %v changed %v", merged, changed)
	}
	merged, _ = Merge(merged, &event.Event{Attrs: map[string]any{"phone": "98123"}})
	if _, ok := merged["phone_country"]; ok {
		t.Errorf("a national number keeps the old country: %v", merged)
	}
	// A contact whose phone predates the attribute gets it at its next event.
	merged, changed = Merge(map[string]any{"phone": "+447946095800"}, &event.Event{Name: "page_view"})
	if merged["phone_country"] != "GB" || !contains(changed, "phone_country") {
		t.Errorf("backfill: %v %v", merged, changed)
	}
	if _, changed := Merge(map[string]any{}, &event.Event{Attrs: map[string]any{"phone_country": "US"}}); len(changed) != 0 {
		t.Errorf("phone_country can't be set directly: %v", changed)
	}
}

type schemaStore map[int64][]eventschema.Schema

func (s schemaStore) AllEventSchemas(context.Context) (map[int64][]eventschema.Schema, error) {
	return s, nil
}

func TestSchemaChecksAndListAdds(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, pub := &memStore{contacts: map[string]model.Contact{}}, &memPub{}
	svc := New(store, rdb, pub)
	var purchase eventschema.Schema
	for _, s := range eventschema.Suggested() {
		if s.Name == "purchase" {
			purchase = s
		}
	}
	svc.Schemas = eventschema.NewCache(schemaStore{1: {purchase}}, rdb, nil)
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return ts.Add(48 * time.Hour) } // checks count on the day they happen
	mk := func(id, name, props string) *kgo.Record {
		var p map[string]any
		_ = json.Unmarshal([]byte(props), &p)
		b, _ := json.Marshal(event.Event{ID: id, TenantID: 1, ExternalID: "u1", Name: name, Ts: ts, Props: p})
		return &kgo.Record{Topic: event.TopicRaw, Value: b}
	}
	err := svc.HandleBatch(context.Background(), []*kgo.Record{
		mk("1", "purchase", `{"order_id":"o1","value":10,"product_code":"SKU1","items":[{"product_code":"SKU2"}]}`),
		mk("2", "purchase", `{"value":"ten","product_code":"SKU1"}`),
		mk("3", "page_view", `{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(store.contacts["u1"].Attrs["products"])
	if string(got) != `["SKU1","SKU2"]` {
		t.Errorf("products = %s", got)
	}
	sum, err := eventschema.Summarize(context.Background(), rdb, 1, "purchase", 1, ts.Add(48*time.Hour))
	if err != nil || sum.Checked != 2 || sum.Bad != 1 || len(sum.Samples) != 1 {
		t.Errorf("summary %+v %v", sum, err)
	}
}
