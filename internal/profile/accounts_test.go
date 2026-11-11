package profile

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
)

func pts(n int) *int { return &n }

func TestStageChanged(t *testing.T) {
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	m, changed := Merge(map[string]any{"city": "Pune"}, &event.Event{Name: event.StageChanged, Ts: ts, Props: map[string]any{"stage": "MQL"}})
	if m["stage"] != "MQL" || m["stage_changed_at"] != "2026-11-10T09:00:00Z" || !reflect.DeepEqual(changed, []string{"stage", "stage_changed_at"}) {
		t.Errorf("stage_changed: %v %v", m, changed)
	}
	if _, changed := Merge(m, &event.Event{Name: event.StageChanged, Ts: ts.Add(time.Hour), Props: map[string]any{"stage": "MQL"}}); len(changed) != 0 {
		t.Errorf("moving to the current stage changed %v", changed)
	}
	if _, changed := Merge(m, &event.Event{Name: event.StageChanged, Ts: ts, Props: map[string]any{"stage": " bad"}}); len(changed) != 0 {
		t.Errorf("an invalid stage name changed %v", changed)
	}
	// A rename keeps the time, and leaves contacts who moved on alone.
	r, changed := Merge(m, &event.Event{Name: event.StageChanged, Ts: ts.Add(time.Hour), Props: map[string]any{"stage": "Marketing qualified", "from": "MQL", "rename": true}})
	if r["stage"] != "Marketing qualified" || r["stage_changed_at"] != "2026-11-10T09:00:00Z" || !reflect.DeepEqual(changed, []string{"stage"}) {
		t.Errorf("rename: %v %v", r, changed)
	}
	if _, changed := Merge(m, &event.Event{Name: event.StageChanged, Props: map[string]any{"stage": "X", "from": "Lead", "rename": true}}); len(changed) != 0 {
		t.Errorf("rename of another stage changed %v", changed)
	}
	// Attributes sent with an event can't set the stage or company copies, or merged_into.
	_, changed = Merge(m, &event.Event{Name: "identify", Attrs: map[string]any{"stage": "Customer", "stage_changed_at": "x", "company_id": 9, "company_size": 5, "merged_into": "x", "company": "Acme"}})
	if !reflect.DeepEqual(changed, []string{"company"}) {
		t.Errorf("event-only attributes were set from attrs: %v", changed)
	}
}

func TestStageForPoints(t *testing.T) {
	stages := []pg.Stage{{Name: "Lead"}, {Name: "MQL", MinPoints: pts(50)}, {Name: "SQL", MinPoints: pts(100)}, {Name: "Customer"}}
	for _, c := range []struct {
		cur           string
		before, after float64
		want          string
	}{
		{"Lead", 40, 60, "MQL"},
		{"", 40, 60, "MQL"},
		{"Lead", 40, 120, "SQL"},       // crosses both: the furthest reached
		{"MQL", 60, 70, ""},            // no threshold crossed
		{"Lead", 60, 70, ""},           // already past 50 (moved back by hand): no automatic move
		{"Customer", 40, 120, ""},      // never backwards
		{"SQL", 40, 60, ""},            // crossing MQL doesn't move an SQL back
		{"Lead", 60, 40, ""},           // losing points never moves
		{"Gone stage", 90, 100, "SQL"}, // a deleted stage counts as before every stage
	} {
		if got := StageForPoints(stages, c.cur, c.before, c.after); got != c.want {
			t.Errorf("%s %v -> %v: %q, want %q", c.cur, c.before, c.after, got, c.want)
		}
	}
}

func TestCompanyEvents(t *testing.T) {
	cur := map[string]any{"city": "Pune", "company_old": "x"}
	link := &event.Event{Name: event.CompanyLinked, Props: map[string]any{"company_id": 7.0, "name": "Acme",
		"attrs": map[string]any{"industry": "Retail", "size": 120.0, "id": "nope", "bad key": 1}}}
	m, changed := Merge(cur, link)
	want := map[string]any{"city": "Pune", "company_id": 7.0, "company_name": "Acme", "company_industry": "Retail", "company_size": 120.0}
	if !reflect.DeepEqual(m, want) || !reflect.DeepEqual(changed, []string{"company_id", "company_industry", "company_name", "company_old", "company_size"}) {
		t.Errorf("link: %v %v", m, changed)
	}
	// An update for the linked company refreshes the copies; one for another company does nothing.
	up, changed := Merge(m, &event.Event{Name: event.CompanyUpdated, Props: map[string]any{"company_id": 7, "name": "Acme Ltd", "attrs": map[string]any{"industry": "Retail"}}})
	if up["company_name"] != "Acme Ltd" || up["company_size"] != nil || !reflect.DeepEqual(changed, []string{"company_name", "company_size"}) {
		t.Errorf("update: %v %v", up, changed)
	}
	if _, changed := Merge(m, &event.Event{Name: event.CompanyUpdated, Props: map[string]any{"company_id": 8, "name": "Other"}}); len(changed) != 0 {
		t.Errorf("update of another company changed %v", changed)
	}
	if _, changed := Merge(m, &event.Event{Name: event.CompanyUnlinked, Props: map[string]any{"company_id": 8}}); len(changed) != 0 {
		t.Errorf("unlinking another company changed %v", changed)
	}
	un, changed := Merge(m, &event.Event{Name: event.CompanyUnlinked, Props: map[string]any{"company_id": 7}})
	if !reflect.DeepEqual(un, map[string]any{"city": "Pune"}) || len(changed) != 4 {
		t.Errorf("unlink: %v %v", un, changed)
	}
	if un, _ := Merge(m, &event.Event{Name: event.CompanyUnlinked, Props: map[string]any{"source": "manual"}}); len(un) != 1 {
		t.Errorf("unlink without an id: %v", un)
	}
}

func TestMergeIdentityKnownContacts(t *testing.T) {
	loser := map[string]any{"city": "Goa", "phone": "+91", "tags": []any{"a"}, "points": 10.0, "first_seen": "2026-01-01T00:00:00Z", "last_seen": "2026-11-09",
		"company_id": 3.0, "company_name": "Old", "company_size": 9.0}
	winner := map[string]any{"city": "Pune", "tags": []any{"b"}, "points": 5.0, "first_seen": "2026-05-01T00:00:00Z", "last_seen": "2026-10-01",
		"company_id": 4.0, "company_name": "New"}
	m := MergeIdentity(loser, winner)
	if m["city"] != "Pune" || m["phone"] != "+91" || m["points"] != 15.0 || !reflect.DeepEqual(m["tags"], []any{"a", "b"}) {
		t.Errorf("merged = %v", m)
	}
	if m["first_seen"] != "2026-01-01T00:00:00Z" || m["last_seen"] != "2026-11-09" {
		t.Errorf("first/last seen = %v / %v: want the earlier and the later", m["first_seen"], m["last_seen"])
	}
	if m["company_id"] != 4.0 || m["company_size"] != nil {
		t.Errorf("a winner with a company keeps only its own company's attributes: %v", m)
	}
}

func decodeAll(recs []*kgo.Record) []event.Event {
	var out []event.Event
	for _, r := range recs {
		var e event.Event
		_ = json.Unmarshal(r.Value, &e)
		out = append(out, e)
	}
	return out
}

// Points that cross a stage threshold move the contact, with a stage_changed event right after.
func TestPointsMoveStage(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := &memStore{contacts: map[string]model.Contact{}, points: map[int64]map[string]int{1: {"demo": 40}},
		stages: map[int64][]pg.Stage{1: {{Name: "Lead"}, {Name: "MQL", MinPoints: pts(50)}, {Name: "SQL", MinPoints: pts(100)}}}}
	pub := &memPub{}
	svc := New(store, rdb, pub)
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	var recs []*kgo.Record
	for i, e := range []event.Event{
		{ID: "1", Name: event.StageChanged, Props: map[string]any{"stage": "Lead"}},
		{ID: "2", Name: "demo"},
		{ID: "3", Name: "demo"}, // 80: crosses MQL
		{ID: "4", Name: "page_view"},
	} {
		e.TenantID, e.ExternalID, e.Ts = 1, "u1", ts.Add(time.Duration(i)*time.Second)
		b, _ := json.Marshal(e)
		recs = append(recs, &kgo.Record{Value: b})
	}
	if err := svc.HandleBatch(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	out := decodeAll(pub.recs)
	var names []string
	for _, e := range out {
		names = append(names, e.Name)
	}
	if !reflect.DeepEqual(names, []string{"stage_changed", "demo", "demo", "stage_changed", "page_view"}) {
		t.Fatalf("published %v", names)
	}
	move := out[3]
	if move.Props["stage"] != "MQL" || move.Props["from"] != "Lead" || move.Props["source"] != "points" || move.ContactID != out[2].ContactID {
		t.Errorf("stage move = %+v", move)
	}
	if out[2].Profile["stage"] != "MQL" || !reflect.DeepEqual(out[2].ChangedAttrs, []string{"points", "stage", "stage_changed_at"}) {
		t.Errorf("the scoring event should carry the stage change: %v %v", out[2].Profile, out[2].ChangedAttrs)
	}
	if store.contacts["u1"].Attrs["stage"] != "MQL" {
		t.Errorf("stored stage = %v", store.contacts["u1"].Attrs["stage"])
	}
	n, _ := segment.NewRedisCounter(rdb, 1, out[0].ContactID, ts).Count(context.Background(), event.StageChanged, segment.Window{Days: 1})
	if n != 2 {
		t.Errorf("stage_changed counted %d times, want 2", n)
	}
}

// merge_requested merges a known contact into another: values, counters, history pointer, and later
// events follow, including events of a visitor merged into the loser earlier.
func TestDuplicateMerge(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, pub := &memStore{contacts: map[string]model.Contact{}}, &memPub{}
	svc := New(store, rdb, pub)
	ctx := context.Background()
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	send := func(evs ...event.Event) []event.Event {
		var recs []*kgo.Record
		for _, e := range evs {
			e.TenantID, e.Ts = 1, ts
			b, _ := json.Marshal(e)
			recs = append(recs, &kgo.Record{Value: b})
		}
		before := len(pub.recs)
		if err := svc.HandleBatch(ctx, recs); err != nil {
			t.Fatal(err)
		}
		return decodeAll(pub.recs[before:])
	}
	send(event.Event{ID: "v1", ExternalID: "anon-v", Name: "view"},
		event.Event{ID: "l1", ExternalID: "dup", Name: "identify", Email: "a@x.co", Attrs: map[string]any{"city": "Goa", "phone": "+91"}, Props: map[string]any{"anonymous_id": "anon-v"}},
		event.Event{ID: "l2", ExternalID: "dup", Name: "purchase"},
		event.Event{ID: "w1", ExternalID: "main", Name: "identify", Email: "a@x.co", Attrs: map[string]any{"city": "Pune"}})
	out := send(event.Event{ID: "m1", ExternalID: "main", Name: event.MergeRequested, Props: map[string]any{"loser": "dup"}})
	if len(out) != 3 || out[1].Name != event.ContactMerged || out[2].Name != event.ContactMergedInto || out[1].Props["from_external_id"] != "dup" {
		t.Fatalf("published %+v", out)
	}
	main, dup := store.contacts["main"], store.contacts["dup"]
	if main.Attrs["city"] != "Pune" || main.Attrs["phone"] != "+91" || dup.Attrs["merged_into"] != "main" {
		t.Errorf("winner %v, loser %v", main.Attrs, dup.Attrs)
	}
	if n, _ := segment.NewRedisCounter(rdb, 1, main.ID, ts).Count(ctx, "purchase", segment.Window{Days: 1}); n != 1 {
		t.Errorf("winner's purchase count = %d, want the loser's 1", n)
	}
	// Later events of the loser, and of the visitor merged into the loser before, go to the winner.
	late := send(event.Event{ID: "l3", ExternalID: "dup", Name: "view"}, event.Event{ID: "v2", ExternalID: "anon-v", Name: "view"})
	if late[0].ExternalID != "main" || late[1].ExternalID != "main" || late[1].ContactID != main.ID {
		t.Errorf("late events went to %s and %s, want main", late[0].ExternalID, late[1].ExternalID)
	}
	// Merging again, or merging the winner into the merged contact, does nothing.
	if again := send(event.Event{ID: "m2", ExternalID: "main", Name: event.MergeRequested, Props: map[string]any{"loser": "dup"}}); len(again) != 1 {
		t.Errorf("a second merge published %+v", again)
	}
	if back := send(event.Event{ID: "m3", ExternalID: "dup", Name: event.MergeRequested, Props: map[string]any{"loser": "main"}}); len(back) != 1 || store.contacts["main"].Attrs["merged_into"] != nil {
		t.Errorf("merging the winner into its own loser: %+v, winner %v", back, store.contacts["main"].Attrs)
	}
}

func TestMergeKeepsOptOuts(t *testing.T) {
	loser := map[string]any{"optout_email": true, "email_bounced": true, "city": "Pune"}
	winner := map[string]any{"optout_email": false, "optout_sms": true}
	out := MergeIdentity(loser, winner)
	if out["optout_email"] != true || out["email_bounced"] != true || out["optout_sms"] != true || out["city"] != "Pune" {
		t.Errorf("merged = %v: opt-outs and bounces must survive a merge", out)
	}
	if _, set := out["optout_whatsapp"]; set {
		t.Error("merge must not invent opt-outs")
	}
}

// First-touch attribution keeps the first value; last touch follows the latest.
func TestFirstTouchIsSetOnce(t *testing.T) {
	cur := map[string]any{}
	for _, c := range []string{"spring", "autumn"} {
		cur, _ = Merge(cur, &event.Event{Name: event.Identify, Attrs: map[string]any{
			"first_utm_campaign": c, "first_referrer": c + ".test", "last_utm_campaign": c,
		}})
	}
	if cur["first_utm_campaign"] != "spring" || cur["first_referrer"] != "spring.test" || cur["last_utm_campaign"] != "autumn" {
		t.Errorf("attrs = %v", cur)
	}
}
