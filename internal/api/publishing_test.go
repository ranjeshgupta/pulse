package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/store/pg"
)

// pubStore adds segment publishing and journeys to memStore. UIStore is embedded for the methods
// these tests never call.
type pubStore struct {
	*memStore
	UIStore
	journeys map[int64]model.Journey
	statuses map[int64]string
	rebuilt  []int64
}

func (p *pubStore) SetSegmentPublishing(_ context.Context, tenant, id int64, published bool, start time.Time, end *time.Time, rebuild bool) error {
	for i, s := range p.segments {
		if s.TenantID == tenant && s.ID == id {
			p.segments[i].Unpublished, p.segments[i].StartAt, p.segments[i].EndAt = !published, start, end
			if rebuild {
				p.segments[i].Status = "new"
				p.rebuilt = append(p.rebuilt, id)
			}
			return nil
		}
	}
	return pg.ErrNotFound
}

func (p *pubStore) GetJourney(_ context.Context, tenant, id int64) (model.Journey, error) {
	j, ok := p.journeys[id]
	if !ok || j.TenantID != tenant {
		return model.Journey{}, pg.ErrNotFound
	}
	return j, nil
}

func (p *pubStore) ListJourneys(_ context.Context, tenant int64) ([]model.Journey, error) {
	var out []model.Journey
	for _, j := range p.journeys {
		if j.TenantID == tenant {
			out = append(out, j)
		}
	}
	return out, nil
}

func (p *pubStore) SetJourneyStatus(_ context.Context, _, id int64, status string) error {
	p.statuses[id] = status
	return nil
}

func (p *pubStore) SetJourneyWindow(_ context.Context, tenant, id int64, w schedule.Window) error {
	j, ok := p.journeys[id]
	if !ok || j.TenantID != tenant {
		return pg.ErrNotFound
	}
	j.StartAt, j.EndAt = w.Start, w.End
	p.journeys[id] = j
	return nil
}

var pubNow = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func newPubServer(t *testing.T) (*Server, *pubStore) {
	t.Helper()
	s, mem, _ := newServer()
	ps := &pubStore{memStore: mem, journeys: map[int64]model.Journey{}, statuses: map[int64]string{}}
	s.Store = ps
	s.now = func() time.Time { return pubNow }
	mem.segments = []model.Segment{
		{ID: 1, TenantID: 1, Name: "Buyers", Kind: "rule", Status: "ready", StartAt: pubNow.Add(-48 * time.Hour)},
		{ID: 2, TenantID: 1, Name: "VIP list", Kind: "static", Status: "ready", StartAt: pubNow.Add(-48 * time.Hour)},
		{ID: 3, TenantID: 2, Name: "Other workspace", Kind: "rule", Status: "ready", StartAt: pubNow.Add(-48 * time.Hour)},
	}
	return s, ps
}

func TestPublishState(t *testing.T) {
	end := pubNow.Add(time.Hour)
	past := pubNow.Add(-time.Hour)
	for _, c := range []struct {
		seg  model.Segment
		want string
	}{
		{model.Segment{StartAt: pubNow}, model.SegmentLive}, // the start itself is live
		{model.Segment{StartAt: past, EndAt: &end}, model.SegmentLive},
		{model.Segment{Unpublished: true, StartAt: past}, model.SegmentUnpublished},
		{model.Segment{StartAt: end}, model.SegmentUpcoming},
		{model.Segment{StartAt: past.Add(-time.Hour), EndAt: &past}, model.SegmentEnded},
		{model.Segment{Unpublished: true, StartAt: end}, model.SegmentUnpublished}, // unpublished wins
	} {
		if got := c.seg.PublishState(pubNow); got != c.want {
			t.Errorf("%+v: %s, want %s", c.seg, got, c.want)
		}
	}
}

func TestRebuildOnPublish(t *testing.T) {
	for _, c := range []struct {
		before, after string
		want          bool
	}{
		{model.SegmentUnpublished, model.SegmentLive, true}, // woken up: catch up on what it missed
		{model.SegmentEnded, model.SegmentLive, true},
		{model.SegmentLive, model.SegmentLive, false}, // only the end moved
		{model.SegmentLive, model.SegmentUnpublished, false},
		{model.SegmentLive, model.SegmentEnded, false},
		{model.SegmentLive, model.SegmentUpcoming, true}, // rebuilt at its new start
		{model.SegmentUnpublished, model.SegmentUnpublished, false},
	} {
		if got := rebuildOnPublish(c.before, c.after); got != c.want {
			t.Errorf("%s -> %s: %v, want %v", c.before, c.after, got, c.want)
		}
	}
}

func TestSegmentPublishingAPI(t *testing.T) {
	s, ps := newPubServer(t)
	h := s.Handler()
	start := pubNow.Add(-48 * time.Hour).Format(time.RFC3339)

	w := do(t, h, "PUT", "/v1/segments/1/publishing", "key-1", `{"published":false,"start_at":"`+start+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"unpublished"`) {
		t.Fatalf("unpublish: %d %s", w.Code, w.Body)
	}
	if len(ps.rebuilt) != 0 {
		t.Errorf("unpublishing rebuilt %v: members must stay as they were", ps.rebuilt)
	}
	w = do(t, h, "GET", "/v1/segments", "key-1", "")
	if !strings.Contains(w.Body.String(), `"state":"unpublished"`) || !strings.Contains(w.Body.String(), `"state":"live"`) {
		t.Errorf("list states: %s", w.Body)
	}

	w = do(t, h, "PUT", "/v1/segments/1/publishing", "key-1", `{"published":true,"start_at":"`+start+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"live"`) || !strings.Contains(w.Body.String(), `"status":"new"`) {
		t.Fatalf("publish again: %d %s", w.Code, w.Body)
	}
	if len(ps.rebuilt) != 1 {
		t.Errorf("publishing again must rebuild the segment: %v", ps.rebuilt)
	}

	// A static list is never rebuilt: its members are added by hand.
	future := pubNow.Add(24 * time.Hour).Format(time.RFC3339)
	w = do(t, h, "PUT", "/v1/segments/2/publishing", "key-1", `{"published":true,"start_at":"`+future+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"upcoming"`) || len(ps.rebuilt) != 1 {
		t.Errorf("static upcoming: %d %s %v", w.Code, w.Body, ps.rebuilt)
	}

	for _, c := range []struct{ body, want string }{
		{`{"published":true}`, "published and start_at are required"},
		{`{"start_at":"` + start + `"}`, "published and start_at are required"},
		{`{"published":true,"start_at":"` + future + `","end_at":"` + start + `"}`, "the end must be after the start"},
	} {
		w = do(t, h, "PUT", "/v1/segments/1/publishing", "key-1", c.body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.body, w.Code, w.Body)
		}
	}
	// Another workspace's segment is not found.
	if w = do(t, h, "PUT", "/v1/segments/3/publishing", "key-1", `{"published":false,"start_at":"`+start+`"}`); w.Code != http.StatusNotFound {
		t.Errorf("other workspace: %d %s", w.Code, w.Body)
	}
}

func TestSegmentsLiveFor(t *testing.T) {
	s, ps := newPubServer(t)
	ctx := context.Background()
	if err := s.segmentsLiveFor(ctx, 1, []int64{1, 2}, pubNow); err != nil {
		t.Fatalf("live segments refused: %v", err)
	}
	ps.segments[0].Unpublished = true
	if err := s.segmentsLiveFor(ctx, 1, []int64{1}, pubNow); !errors.Is(err, errSegmentNotLive) || !strings.Contains(err.Error(), `"Buyers" is unpublished`) {
		t.Errorf("unpublished: %v", err)
	}
	ps.segments[0].Unpublished = false
	ended := pubNow.Add(-time.Hour)
	ps.segments[0].EndAt = &ended
	if err := s.segmentsLiveFor(ctx, 1, []int64{1}, pubNow); !errors.Is(err, errSegmentNotLive) || !strings.Contains(err.Error(), "has ended") {
		t.Errorf("ended: %v", err)
	}
	ps.segments[0].EndAt = nil
	ps.segments[0].StartAt = pubNow.Add(2 * time.Hour)
	if err := s.segmentsLiveFor(ctx, 1, []int64{1}, pubNow.Add(3*time.Hour)); err != nil {
		t.Errorf("a send after the segment starts is fine: %v", err)
	}
	if err := s.segmentsLiveFor(ctx, 1, []int64{1}, pubNow); !errors.Is(err, errSegmentNotLive) || !strings.Contains(err.Error(), "only starts") {
		t.Errorf("a send before the segment starts: %v", err)
	}
}

func TestCreateSegmentUnpublished(t *testing.T) {
	s, ps := newPubServer(t)
	h := s.Handler()
	w := do(t, h, "POST", "/v1/segments", "key-1", `{"name":"Later","rule":{"attr":"city","op":"eq","value":"Pune"},"published":false}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	got := ps.segments[len(ps.segments)-1]
	if !got.Unpublished || got.Name != "Later" {
		t.Errorf("created %+v, want unpublished", got)
	}
	w = do(t, h, "POST", "/v1/segments", "key-1", `{"name":"Bad","rule":{"attr":"city","op":"eq","value":"Pune"},"start_at":"2026-12-02T00:00:00Z","end_at":"2026-12-01T00:00:00Z"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("end before start: %d %s", w.Code, w.Body)
	}
}

func TestUnpublishJourney(t *testing.T) {
	s, ps := newPubServer(t)
	h := s.Handler()
	ps.journeys[7] = model.Journey{ID: 7, TenantID: 1, Name: "Welcome", Status: "active", StartAt: pubNow.Add(-time.Hour), Graph: json.RawMessage(`{}`)}

	w := do(t, h, "POST", "/v1/journeys/7/unpublish", "key-1", `{"people":"finish"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", w.Code, w.Body)
	}
	if j := ps.journeys[7]; j.EndAt == nil || !j.EndAt.Equal(pubNow) || ps.statuses[7] != "" {
		t.Errorf("finish must end the window now and keep it live: %+v %q", j, ps.statuses[7])
	}
	// An end already in the past stays.
	if w = do(t, h, "POST", "/v1/journeys/7/unpublish", "key-1", `{"people":"finish"}`); w.Code != http.StatusOK || !ps.journeys[7].EndAt.Equal(pubNow) {
		t.Errorf("again: %d %s", w.Code, w.Body)
	}

	if w = do(t, h, "POST", "/v1/journeys/7/unpublish", "key-1", `{"people":"hold"}`); w.Code != http.StatusOK || ps.statuses[7] != "paused" {
		t.Errorf("hold: %d %s %q", w.Code, w.Body, ps.statuses[7])
	}
	if w = do(t, h, "POST", "/v1/journeys/7/unpublish", "key-1", `{"people":"maybe"}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad choice: %d", w.Code)
	}

	// Not started yet: nobody is inside, so it's paused.
	ps.journeys[8] = model.Journey{ID: 8, TenantID: 1, Name: "Later", Status: "active", StartAt: pubNow.Add(time.Hour), Graph: json.RawMessage(`{}`)}
	if w = do(t, h, "POST", "/v1/journeys/8/unpublish", "key-1", `{"people":"finish"}`); w.Code != http.StatusOK || ps.statuses[8] != "paused" {
		t.Errorf("upcoming: %d %s %q", w.Code, w.Body, ps.statuses[8])
	}
	// A draft can't be unpublished.
	ps.journeys[9] = model.Journey{ID: 9, TenantID: 1, Name: "Draft", Status: "draft", StartAt: pubNow.Add(-time.Hour), Graph: json.RawMessage(`{}`)}
	if w = do(t, h, "POST", "/v1/journeys/9/unpublish", "key-1", `{"people":"finish"}`); w.Code != http.StatusConflict {
		t.Errorf("draft: %d %s", w.Code, w.Body)
	}
}

func TestJourneyScheduleWhileLive(t *testing.T) {
	s, ps := newPubServer(t)
	h := s.Handler()
	end := pubNow.Add(-time.Minute)
	ps.journeys[7] = model.Journey{ID: 7, TenantID: 1, Name: "Welcome", Status: "active", StartAt: pubNow.Add(-time.Hour), EndAt: &end}
	start := pubNow.Add(-time.Hour).Format(time.RFC3339)
	w := do(t, h, "PUT", "/v1/journeys/7/schedule", "key-1", `{"start_at":"`+start+`","end_at":null}`)
	if w.Code != http.StatusOK || ps.journeys[7].EndAt != nil {
		t.Fatalf("reopen: %d %s %+v", w.Code, w.Body, ps.journeys[7])
	}
	if w = do(t, h, "PUT", "/v1/journeys/7/schedule", "key-1", `{"end_at":"`+start+`"}`); w.Code != http.StatusBadRequest {
		t.Errorf("no start: %d", w.Code)
	}
	if w = do(t, h, "PUT", "/v1/journeys/7/schedule", "key-1", `{"start_at":"`+start+`","end_at":"`+start+`"}`); w.Code != http.StatusBadRequest {
		t.Errorf("end = start: %d", w.Code)
	}
	if w = do(t, h, "PUT", "/v1/journeys/99/schedule", "key-1", `{"start_at":"`+start+`"}`); w.Code != http.StatusNotFound {
		t.Errorf("missing: %d", w.Code)
	}

	// Reopening a live journey is a launch: its trigger segment must be live.
	ps.segments[0].Unpublished = true
	ps.journeys[7] = model.Journey{ID: 7, TenantID: 1, Name: "Welcome", Status: "active", StartAt: pubNow.Add(-time.Hour), EndAt: &end,
		Graph: json.RawMessage(`{"trigger":{"type":"segment_entered","segment_id":1},"entry":"a","nodes":{"a":{"type":"exit"}}}`)}
	if w = do(t, h, "PUT", "/v1/journeys/7/schedule", "key-1", `{"start_at":"`+start+`"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "unpublished") {
		t.Errorf("reopen with an unpublished trigger segment: %d %s", w.Code, w.Body)
	}
	// Ending it sooner is always fine.
	sooner := pubNow.Add(-30 * time.Minute).Format(time.RFC3339)
	if w = do(t, h, "PUT", "/v1/journeys/7/schedule", "key-1", `{"start_at":"`+start+`","end_at":"`+sooner+`"}`); w.Code != http.StatusOK {
		t.Errorf("end sooner: %d %s", w.Code, w.Body)
	}
	// An archived journey keeps its dates.
	archived := pubNow
	ps.journeys[8] = model.Journey{ID: 8, TenantID: 1, Name: "Old", Status: "paused", StartAt: pubNow.Add(-time.Hour), ArchivedAt: &archived, Graph: json.RawMessage(`{}`)}
	if w = do(t, h, "PUT", "/v1/journeys/8/schedule", "key-1", `{"start_at":"`+start+`"}`); w.Code != http.StatusConflict {
		t.Errorf("archived: %d %s", w.Code, w.Body)
	}
}

func TestSegmentUsage(t *testing.T) {
	s, ps := newPubServer(t)
	h := s.Handler()
	ps.segments = append(ps.segments, model.Segment{ID: 4, TenantID: 1, Name: "Buyers in Pune", Kind: "rule",
		Rule: json.RawMessage(`{"segment":1,"op":"in"}`), StartAt: pubNow})
	ps.journeys[7] = model.Journey{ID: 7, TenantID: 1, Name: "Welcome", Status: "active",
		Graph: json.RawMessage(`{"trigger":{"type":"segment_entered","segment_id":1},"entry":"a","nodes":{"a":{"type":"exit"}}}`)}
	ps.journeys[8] = model.Journey{ID: 8, TenantID: 1, Name: "Old draft", Status: "draft",
		Graph: json.RawMessage(`{"trigger":{"type":"segment_entered","segment_id":1},"entry":"a","nodes":{"a":{"type":"exit"}}}`)}
	w := do(t, h, "GET", "/v1/segments/1/usage", "key-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("usage: %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"Buyers in Pune"`) || !strings.Contains(body, `"Welcome"`) || strings.Contains(body, "Old draft") {
		t.Errorf("usage: %s", body)
	}
	if w = do(t, h, "GET", "/v1/segments/3/usage", "key-1", ""); w.Code != http.StatusNotFound {
		t.Errorf("other workspace: %d", w.Code)
	}
}
