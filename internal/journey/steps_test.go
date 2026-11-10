package journey

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/segment"
)

type fakeContacts map[uint32]model.Contact

func (f fakeContacts) ContactByID(_ context.Context, _ int64, id uint32) (model.Contact, error) {
	return f[id], nil
}

type fakeEvents struct{ evs []event.Event }

func (f *fakeEvents) PublishEvent(_ context.Context, e event.Event) error {
	f.evs = append(f.evs, e)
	return nil
}

type countMap map[string]int

func (c countMap) Count(_ context.Context, ev string, _ int) (int, error) { return c[ev], nil }

func executor(t *testing.T, counts countMap) (*Executor, *fakeStore, *fakeSender, *fakeEvents) {
	t.Helper()
	store, sender, evs := newFakeStore(), &fakeSender{}, &fakeEvents{}
	x := &Executor{
		Store: store, Membership: fakeMembers{}, Sender: sender, Events: evs,
		Contacts: fakeContacts{42: {ID: 42, ExternalID: "u42", Email: "u42@x.test", Attrs: map[string]any{"city": "Pune", "timezone": "Asia/Kolkata"}}},
		Counters: func(int64, uint32, time.Time) segment.Counter { return counts },
		HTTP:     http.DefaultClient,
		Now:      func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }, // 17:30 in Kolkata
	}
	return x, store, sender, evs
}

func graph(t *testing.T, js string) Graph {
	t.Helper()
	g, err := ParseGraph([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

var en = model.Enrollment{ID: 7, TenantID: 1, JourneyID: 3, ContactID: 42}

func TestWaitUntilUsesTheContactsTimeZone(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) // 17:30 IST
	// 09:00 has passed in Kolkata today, so the next 09:00 IST is tomorrow 03:30 UTC.
	got := WaitUntil(Node{TimeOfDay: "09:00", Timezone: "UTC"}, map[string]any{"timezone": "Asia/Kolkata"}, now)
	if want := time.Date(2026, 11, 11, 3, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("09:00 for a Kolkata contact = %v, want %v", got.UTC(), want)
	}
	// Without a contact zone, the step's zone applies: 18:00 UTC is later today.
	if got := WaitUntil(Node{TimeOfDay: "18:00", Timezone: "UTC"}, map[string]any{}, now); !got.Equal(time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("18:00 UTC = %v", got)
	}

	x, store, _, _ := executor(t, nil)
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"w","nodes":{
		"w":{"type":"wait_until","until":"2026-11-01T00:00:00Z","next":"x"},"x":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "w"); err != nil {
		t.Fatal(err)
	}
	if store.nodes[7] != [2]string{"x", StatusCompleted} {
		t.Errorf("a date in the past continues straight away: %v", store.nodes[7])
	}
}

func TestWaitForEventParksWithTimeout(t *testing.T) {
	x, store, _, _ := executor(t, nil)
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"w","nodes":{
		"w":{"type":"wait_for_event","event":"purchase","seconds":86400,"yes":"thanks","no":"nudge"},
		"thanks":{"type":"exit"},"nudge":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "w"); err != nil {
		t.Fatal(err)
	}
	if len(store.eventWaits) != 1 || store.eventWaits[0] != "purchase:thanks:nudge" || store.timers[7].node != "nudge" {
		t.Errorf("waits %v, timer %+v", store.eventWaits, store.timers[7])
	}
}

func TestCheckStepEvaluatesRules(t *testing.T) {
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"c","nodes":{
		"c":{"type":"check","rule":{"all":[{"attr":"city","op":"eq","value":"Pune"},{"event":"email_opened","op":"gte","count":1,"days":7}]},"yes":"a","no":"b"},
		"a":{"type":"exit"},"b":{"type":"exit"}}}`)
	for counts, want := range map[int]string{1: "a", 0: "b"} {
		x, store, _, _ := executor(t, countMap{"email_opened": counts})
		if err := x.Run(context.Background(), g, en, "c"); err != nil {
			t.Fatal(err)
		}
		if store.nodes[7][0] != want {
			t.Errorf("opened %d times: went to %s, want %s", counts, store.nodes[7][0], want)
		}
	}
}

func TestSplitIsStableAndWeighted(t *testing.T) {
	vs := []Variant{{Weight: 1, Next: "a"}, {Weight: 3, Next: "b"}}
	if pickVariant(vs, 99, "s") != pickVariant(vs, 99, "s") {
		t.Fatal("the same enrollment must always take the same path")
	}
	got := map[string]int{}
	for i := int64(0); i < 4000; i++ {
		got[pickVariant(vs, i, "s")]++
	}
	if got["a"] < 850 || got["a"] > 1150 {
		t.Errorf("1:3 split gave %v", got)
	}
}

func TestUpdateAndWebhookSteps(t *testing.T) {
	var body map[string]any
	var key string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("X-Pulse-Idempotency-Key")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(204)
	}))
	defer hook.Close()
	x, store, _, evs := executor(t, nil)
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"u","nodes":{
		"u":{"type":"update_attr","attrs":{"stage":"lead"},"next":"h"},
		"h":{"type":"webhook","url":"`+hook.URL+`","next":"x"},"x":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "u"); err != nil {
		t.Fatal(err)
	}
	if len(evs.evs) != 2 || evs.evs[0].Name != "identify" || evs.evs[0].Attrs["stage"] != "lead" || evs.evs[0].ExternalID != "u42" ||
		evs.evs[1].Name != "journey_completed" {
		t.Fatalf("update published %+v", evs.evs)
	}
	c, _ := body["contact"].(map[string]any)
	attrs, _ := c["attrs"].(map[string]any)
	if c["external_id"] != "u42" || attrs["stage"] != "lead" || key != "7:h" {
		t.Errorf("webhook got %v (key %q): want the updated contact", body, key)
	}
	if store.nodes[7] != [2]string{"x", StatusCompleted} {
		t.Errorf("journey did not finish: %v", store.nodes[7])
	}

	// A failing endpoint is recorded and the journey carries on.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) }))
	defer bad.Close()
	x, store, _, evs = executor(t, nil)
	g = graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"h","nodes":{
		"h":{"type":"webhook","url":"`+bad.URL+`","next":"x"},"x":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "h"); err != nil {
		t.Fatal(err)
	}
	if len(evs.evs) != 2 || evs.evs[0].Name != "webhook_failed" || store.nodes[7][1] != StatusCompleted {
		t.Errorf("events %+v, enrollment %v", evs.evs, store.nodes[7])
	}
}

func TestNewStepValidation(t *testing.T) {
	base := `{"trigger":{"type":"event","event":"signup"%s},%s"entry":"s","nodes":{"s":%s,"x":{"type":"exit"}}}`
	cases := map[string][3]string{
		"time of day":  {"", "", `{"type":"wait_until","time_of_day":"25:00","next":"x"}`},
		"not both":     {"", "", `{"type":"wait_until","time_of_day":"09:00","until":"2026-01-01T00:00:00Z","next":"x"}`},
		"time zone":    {"", "", `{"type":"wait_until","time_of_day":"09:00","timezone":"Mars/Base","next":"x"}`},
		"timeout":      {"", "", `{"type":"wait_for_event","event":"buy","yes":"x","no":"x"}`},
		"2 to 5 paths": {"", "", `{"type":"split","variants":[{"weight":1,"next":"x"}]}`},
		"weight":       {"", "", `{"type":"split","variants":[{"weight":0,"next":"x"},{"weight":1,"next":"x"}]}`},
		"consent":      {"", "", `{"type":"update_attr","attrs":{"optout_email":false},"next":"x"}`},
		"http(s) URL":  {"", "", `{"type":"webhook","url":"ftp://x","next":"x"}`},
		"invalid":      {"", "", `{"type":"check","rule":{"attr":"bad-key","op":"exists"},"yes":"x","no":"x"}`},
		"re-entry":     {`,"reentry":"sometimes"`, "", `{"type":"exit"}`},
		"1-3650 days":  {`,"reentry":"after_days"`, "", `{"type":"exit"}`},
		"goal":         {"", `"goal":{"event":"bad name"},`, `{"type":"exit"}`},
	}
	for want, c := range cases {
		js := strings.Replace(base, "%s", c[0], 1)
		js = strings.Replace(js, "%s", c[1], 1)
		js = strings.Replace(js, "%s", c[2], 1)
		if _, err := ParseGraph([]byte(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v\n%s", want, err, js)
		}
	}
	ok := `{"trigger":{"type":"event","event":"signup","reentry":"after_days","days":30},"goal":{"event":"purchase"},"entry":"s","nodes":{
		"s":{"type":"split","variants":[{"weight":50,"next":"a"},{"weight":50,"next":"x"}]},
		"a":{"type":"wait_for_event","event":"purchase","seconds":3600,"yes":"x","no":"x"},"x":{"type":"exit"}}}`
	if _, err := ParseGraph([]byte(ok)); err != nil {
		t.Errorf("valid journey rejected: %v", err)
	}
}

func TestAudienceSteps(t *testing.T) {
	x, store, _, evs := executor(t, nil)
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"t","nodes":{
		"t":{"type":"update_tags","add_tags":["lead"],"remove_tags":["cold"],"next":"p"},
		"p":{"type":"adjust_points","points":15,"next":"l"},
		"l":{"type":"update_list","list_id":9,"list_action":"add","next":"c"},
		"c":{"type":"check","rule":{"all":[{"tag":"lead","op":"has"},{"attr":"points","op":"gte","value":15}]},"yes":"x","no":"n"},
		"n":{"type":"exit"},"x":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "t"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range evs.evs {
		names = append(names, e.Name)
		if e.ExternalID != "u42" || e.Props["journey_id"] != int64(3) {
			t.Errorf("event %+v: want it for u42 from journey 3", e)
		}
	}
	if strings.Join(names, ",") != "tag_added,tag_removed,points_changed,list_joined,journey_completed" {
		t.Errorf("events %v", names)
	}
	if store.nodes[7][0] != "x" {
		t.Errorf("the check after the steps should see the new tag and points; ended at %v", store.nodes[7])
	}
	for js, want := range map[string]string{
		`{"type":"update_tags","next":"x"}`:                               "at least one tag",
		`{"type":"update_tags","add_tags":["a b"],"next":"x"}`:            "valid tag",
		`{"type":"adjust_points","next":"x"}`:                             "non-zero",
		`{"type":"update_list","list_id":3,"list_action":"x","next":"x"}`: "choose a list",
	} {
		_, err := ParseGraph([]byte(`{"trigger":{"type":"event","event":"signup"},"entry":"s","nodes":{"s":` + js + `,"x":{"type":"exit"}}}`))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", js, err, want)
		}
	}
}

func TestMessageDecision(t *testing.T) {
	g := Graph{Trigger: Trigger{Type: TriggerEvent, Event: "signup"}, Entry: "mail", Nodes: map[string]Node{
		"mail":   {Type: NodeSendEmail, TemplateID: 1, Next: "opened"},
		"opened": {Type: NodeMessageDecision, Step: "mail", Outcome: "opened", Seconds: 86400, Yes: "x", No: "x"},
		"x":      {Type: NodeExit},
	}}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	name, id := g.MessageEvent(42, g.Nodes["opened"])
	if name != "email_opened" || id != "42:mail" {
		t.Errorf("waits for %s %s", name, id)
	}
	bad := g
	bad.Nodes = map[string]Node{"mail": g.Nodes["mail"], "x": g.Nodes["x"],
		"opened": {Type: NodeMessageDecision, Step: "mail", Outcome: "read", Seconds: 86400, Yes: "x", No: "x"}}
	if err := bad.Validate(); err == nil {
		t.Error("an email can't be read (only opened, clicked, delivered)")
	}
	bad.Nodes["opened"] = Node{Type: NodeMessageDecision, Step: "x", Outcome: "opened", Seconds: 86400, Yes: "x", No: "x"}
	if err := bad.Validate(); err == nil {
		t.Error("the step must be a send step")
	}
}
