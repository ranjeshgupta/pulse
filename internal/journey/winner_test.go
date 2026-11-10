package journey

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/model"
)

const abJourney = `{"trigger":{"type":"event","event":"signup"},"goal":{"event":"purchase"},"entry":"ab","nodes":{
	"ab":{"type":"split","variants":[{"weight":1,"next":"ma","label":"A"},{"weight":1,"next":"mb","label":"B"}],
	      "winner":{"metric":"clicked","after_seconds":60,"min_per_variant":5}},
	"ma":{"type":"send_email","template_id":1,"next":"x"},
	"mb":{"type":"send_email","template_id":2,"next":"w"},
	"w":{"type":"wait","seconds":60,"next":"ms"},
	"ms":{"type":"send_sms","template_id":3,"next":"x"},
	"x":{"type":"exit"}}}`

func TestSplitWinnerValidation(t *testing.T) {
	g := graph(t, abJourney)
	if got := g.TrackedSendsAfter("ab"); strings.Join(got, ",") != "ma,mb" {
		t.Errorf("tracked sends after the split = %v (SMS can't be opened)", got)
	}
	for winner, want := range map[string]string{
		`{"metric":"replied","after_seconds":60,"min_per_variant":5}`:       "opened, clicked or converted",
		`{"metric":"clicked","after_seconds":0,"min_per_variant":5}`:        "1 second to 90 days",
		`{"metric":"clicked","after_seconds":60,"min_per_variant":0}`:       "1 to 1,000,000",
		`{"metric":"clicked","after_seconds":60,"min_per_variant":5,"x":1}`: "unknown field",
	} {
		js := strings.Replace(abJourney, `{"metric":"clicked","after_seconds":60,"min_per_variant":5}`, winner, 1)
		if _, err := ParseGraph([]byte(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", winner, err, want)
		}
	}
	noGoal := strings.Replace(strings.Replace(abJourney, `"goal":{"event":"purchase"},`, "", 1), `"metric":"clicked"`, `"metric":"converted"`, 1)
	if _, err := ParseGraph([]byte(noGoal)); err == nil || !strings.Contains(err.Error(), "goal") {
		t.Errorf("converted without a goal: %v", err)
	}
	smsOnly := `{"trigger":{"type":"event","event":"signup"},"entry":"ab","nodes":{
		"ab":{"type":"split","variants":[{"weight":1,"next":"s"},{"weight":1,"next":"x"}],"winner":{"metric":"opened","after_seconds":60,"min_per_variant":5}},
		"s":{"type":"send_sms","template_id":3,"next":"x"},"x":{"type":"exit"}}}`
	if _, err := ParseGraph([]byte(smsOnly)); err == nil || !strings.Contains(err.Error(), "email or app inbox") {
		t.Errorf("opens with SMS only: %v", err)
	}
}

func TestSplitRecordsPathAndFollowsWinner(t *testing.T) {
	x, _, sender, evs := executor(t, nil)
	g := graph(t, abJourney)
	if err := x.Run(context.Background(), g, en, "ab"); err != nil {
		t.Fatal(err)
	}
	if len(evs.evs) == 0 || evs.evs[0].Name != "journey_split" {
		t.Fatalf("events %+v", evs.evs)
	}
	e := evs.evs[0]
	path := e.Props["path"].(int)
	if e.ID != "jsplit:7:ab" || e.Props["step"] != "ab" || e.Props["decided"] != false || e.Props["journey_id"] != int64(3) || e.ExternalID != "u42" {
		t.Errorf("split event %+v", e)
	}
	if want := []string{"ma", "mb"}[path]; !strings.HasSuffix(sender.sent[0].ID, ":"+want) {
		t.Errorf("path %d sent %s", path, sender.sent[0].ID)
	}

	// Once decided, every enrollment takes the winner, whatever its hash.
	other := 1 - path
	g.ApplyDecisions(map[string]SplitDecision{"ab": {Path: other, Next: []string{"ma", "mb"}[other], Metric: "clicked"}})
	for i := int64(100); i < 110; i++ {
		x, _, sender, evs := executor(t, nil)
		e := en
		e.ID = i
		if err := x.Run(context.Background(), g, e, "ab"); err != nil {
			t.Fatal(err)
		}
		if evs.evs[0].Props["path"] != other || evs.evs[0].Props["decided"] != true || !strings.HasSuffix(sender.sent[0].ID, ":"+[]string{"ma", "mb"}[other]) {
			t.Fatalf("enrollment %d: %+v, sent %s", i, evs.evs[0].Props, sender.sent[0].ID)
		}
	}

	// A decision that no longer fits the graph (path now leads elsewhere) is ignored.
	g2 := graph(t, abJourney)
	g2.ApplyDecisions(map[string]SplitDecision{"ab": {Path: 1, Next: "somewhere-else"}, "ma": {Path: 0, Next: "x"}})
	if g2.Nodes["ab"].Decision() != nil || g2.Nodes["ma"].Decision() != nil {
		t.Error("stale decisions must not apply")
	}
}

type fakeWinnerStore struct {
	js       []model.Journey
	recorded map[string]SplitDecision
}

func (f *fakeWinnerStore) ActiveJourneys(context.Context) ([]model.Journey, error) { return f.js, nil }
func (f *fakeWinnerStore) RecordSplitWinner(_ context.Context, _, _ int64, step string, b []byte) (bool, error) {
	if _, ok := f.recorded[step]; ok {
		return false, nil
	}
	var d SplitDecision
	_ = json.Unmarshal(b, &d)
	f.recorded[step] = d
	return true, nil
}

type fakeSplitStats struct {
	stats []analytics.PathStat
	got   analytics.SplitQuery
}

func (f *fakeSplitStats) SplitStats(_ context.Context, q analytics.SplitQuery) ([]analytics.PathStat, error) {
	f.got = q
	return f.stats, nil
}

func TestWinnerPicker(t *testing.T) {
	start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	store := &fakeWinnerStore{js: []model.Journey{{ID: 3, TenantID: 1, Graph: json.RawMessage(abJourney), StartAt: start}}, recorded: map[string]SplitDecision{}}
	stats := &fakeSplitStats{stats: []analytics.PathStat{
		{Path: 0, Entrants: 6, Hits: 1, FirstAt: start.Add(time.Minute)},
		{Path: 1, Entrants: 5, Hits: 3, FirstAt: start.Add(2 * time.Minute)},
	}}
	now := start.Add(time.Minute + 30*time.Second) // 30 s into a 60 s test
	p := &WinnerPicker{Store: store, Stats: stats, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	if err := p.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.recorded) != 0 {
		t.Fatal("decided before the test window passed")
	}
	if stats.got.Metric != "clicked" || strings.Join(stats.got.Nodes, ",") != "ma,mb" || stats.got.Step != "ab" {
		t.Errorf("stats query %+v", stats.got)
	}
	now = start.Add(5 * time.Minute)
	if err := p.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, ok := store.recorded["ab"]
	if !ok || d.Path != 1 || d.Next != "mb" || d.Label != "B" || d.Stats[1].Rate != 0.6 {
		t.Fatalf("decision %+v", d)
	}
	// Recorded decisions are what the engine applies.
	store.js[0].SplitWinners, _ = json.Marshal(store.recorded)
	ds, err := Decisions(store.js[0].SplitWinners)
	if err != nil || ds["ab"].Path != 1 {
		t.Errorf("decisions %v %v", ds, err)
	}

	// Not enough contacts on a path: no decision, however long it runs.
	store.recorded = map[string]SplitDecision{}
	store.js[0].SplitWinners = nil
	stats.stats[1].Entrants = 4
	if err := p.Check(context.Background()); err != nil || len(store.recorded) != 0 {
		t.Errorf("decided with 4 of 5 contacts: %v %v", store.recorded, err)
	}
}
