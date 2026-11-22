//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func segmentOf(t *testing.T, c *http.Client, id any) map[string]any {
	t.Helper()
	return jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%v", id), nil, 200)
}

// TestSegmentPublishing: an unpublished segment is frozen (members stay as they were while profiles
// change), refuses new sends, says what uses it, and catches up when published again. A segment
// whose window starts later isn't built until then; one whose window ended reports so.
func TestSegmentPublishing(t *testing.T) {
	c, _ := workspace(t, "Publishing")
	run := fmt.Sprint(time.Now().UnixNano())
	a, b := "pa-"+run, "pb-"+run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": a, "name": "identify", "attrs": map[string]any{"city": "Pune"}}, 202)
	seg := newSegment(t, c, "Pune", map[string]any{"attr": "city", "op": "eq", "value": "Pune"})
	waitFor(t, 15*time.Second, func() bool { return segmentOf(t, c, seg)["size"] == 1.0 })
	s := segmentOf(t, c, seg)
	if s["published"] != true || s["state"] != "live" || s["start_at"] == nil {
		t.Fatalf("a new segment is published and live: %v", s)
	}
	start := s["start_at"]

	// It's the trigger of a live journey: the usage list names it.
	sms := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "hi", "channel": "sms", "content": map[string]any{"text": "Hi"}}, 201)["id"]
	graph := map[string]any{"trigger": map[string]any{"type": "segment_entered", "segment_id": seg}, "entry": "s",
		"nodes": map[string]any{"s": map[string]any{"type": "send_sms", "template_id": sms, "next": "x"}, "x": map[string]any{"type": "exit"}}}
	live := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Pune welcome", "graph": graph}, 201)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", live), nil, 200)
	uses := jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%v/usage", seg), nil, 200)["uses"].([]any)
	if len(uses) != 1 || uses[0].(map[string]any)["name"] != "Pune welcome" {
		t.Errorf("usage = %v, want the live journey", uses)
	}

	// Unpublish: members stay as they are while profiles change.
	u := jsonCall(t, c, "PUT", fmt.Sprintf("/v1/segments/%v/publishing", seg), map[string]any{"published": false, "start_at": start}, 200)
	if u["state"] != "unpublished" || u["published"] != false {
		t.Fatalf("unpublish: %v", u)
	}
	time.Sleep(3 * time.Second) // the streaming index refresh
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": b, "name": "identify", "attrs": map[string]any{"city": "Pune"}},
		map[string]any{"external_id": a, "name": "identify", "attrs": map[string]any{"city": "Delhi"}},
	}}, 202)
	time.Sleep(4 * time.Second)
	if in := segmentsOf(t, c, a); !containsID(in, seg) {
		t.Errorf("a frozen segment lost a member whose city changed: %v", in)
	}
	if in := segmentsOf(t, c, b); containsID(in, seg) {
		t.Errorf("a frozen segment gained a member: %v", in)
	}

	// New sends can't use it.
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "pune sale", "channel": "sms", "template_id": sms,
		"segment_id": seg, "start_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "repeat": "none"}, 200)["id"]
	if r := jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 409); !strings.Contains(fmt.Sprint(r["error"]), "unpublished") {
		t.Errorf("schedule to an unpublished segment: %v", r)
	}
	draft := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Pune later", "graph": graph}, 201)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", draft), nil, 409)

	// Publish again: rebuilt from history, so it catches up (b in, a out).
	p := jsonCall(t, c, "PUT", fmt.Sprintf("/v1/segments/%v/publishing", seg), map[string]any{"published": true, "start_at": start}, 200)
	if p["state"] != "live" || p["status"] != "new" {
		t.Fatalf("publish again: %v", p)
	}
	waitFor(t, 20*time.Second, func() bool {
		return segmentOf(t, c, seg)["status"] == "ready" && containsID(segmentsOf(t, c, b), seg) && !containsID(segmentsOf(t, c, a), seg)
	})
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)

	// A window that starts tomorrow: created, but not built until then.
	tomorrow := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	later := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Later", "rule": map[string]any{"attr": "city", "op": "eq", "value": "Pune"}, "start_at": tomorrow}, 201)["id"]
	time.Sleep(4 * time.Second)
	if l := segmentOf(t, c, later); l["state"] != "upcoming" || l["status"] != "new" || l["size"] != 0.0 {
		t.Errorf("upcoming segment: %v", l)
	}
	// A window that has ended.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	e := jsonCall(t, c, "PUT", fmt.Sprintf("/v1/segments/%v/publishing", later), map[string]any{"published": true,
		"start_at": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339), "end_at": past}, 200)
	if e["state"] != "ended" {
		t.Errorf("ended segment: %v", e)
	}
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/segments/%v/publishing", later), map[string]any{"published": true, "start_at": tomorrow, "end_at": past}, 400)
}

func containsID(ids []int64, id int64) bool { return slices.Contains(ids, id) }

// TestJourneyUnpublish: unpublishing with "finish" keeps people inside going (they get their
// message after the wait) while nobody new enters; new dates reopen it; "hold" pauses it.
func TestJourneyUnpublish(t *testing.T) {
	c, _ := workspace(t, "Unpublish")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	sms := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "nudge", "channel": "sms", "content": map[string]any{"text": "Still there?"}}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Visit nudge", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "visit"}, "entry": "w",
		"nodes": map[string]any{
			"w": map[string]any{"type": "wait", "seconds": 5, "next": "s"},
			"s": map[string]any{"type": "send_sms", "template_id": sms, "next": "x"},
			"x": map[string]any{"type": "exit"},
		}}}, 201)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j), nil, 200)
	time.Sleep(3 * time.Second)

	run := fmt.Sprint(time.Now().UnixNano())
	visit := func(ext, phone string) {
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "visit", "attrs": map[string]any{"phone": phone}}, 202)
	}
	inside, late := "in-"+run, "late-"+run
	visit(inside, "+919800002001")
	waitFor(t, 10*time.Second, func() bool {
		st, _ := jsonCall(t, c, "GET", fmt.Sprintf("/v1/journeys/%v/stats", j), nil, 200)["status"].(map[string]any)
		return st["waiting"] == 1.0
	})

	r := jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/unpublish", j), map[string]any{"people": "finish"}, 200)
	if r["status"] != "active" || r["end_at"] == nil {
		t.Fatalf("unpublish finish: %v", r)
	}
	time.Sleep(3 * time.Second) // the journey workers reload
	visit(late, "+919800002002")

	got := func(phone string) bool {
		for _, m := range sandbox(t, c) {
			if m.Recipient == phone {
				return true
			}
		}
		return false
	}
	waitFor(t, 20*time.Second, func() bool { return got("+919800002001") }) // the one inside finished
	time.Sleep(2 * time.Second)
	if got("+919800002002") {
		t.Error("someone entered after the campaign was unpublished")
	}

	// New dates while live reopen it.
	jr := jsonCall(t, c, "GET", fmt.Sprintf("/v1/journeys/%v", j), nil, 200)
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/journeys/%v/schedule", j), map[string]any{"start_at": jr["start_at"], "end_at": nil}, 200)
	if jr = jsonCall(t, c, "GET", fmt.Sprintf("/v1/journeys/%v", j), nil, 200); jr["end_at"] != nil || jr["status"] != "active" {
		t.Errorf("reopened: %v", jr)
	}

	// Hold: paused.
	if r = jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/unpublish", j), map[string]any{"people": "hold"}, 200); r["status"] != "paused" {
		t.Errorf("unpublish hold: %v", r)
	}
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/unpublish", j), map[string]any{"people": "finish"}, 409) // not live any more
}
