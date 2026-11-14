//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestBroadcastExclusionsAndLocalTime: excluded segments get nothing; a local-time broadcast holds
// each message until the clock time in the contact's own zone.
func TestBroadcastExclusionsAndLocalTime(t *testing.T) {
	c, _ := workspace(t, "Exclusions")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	sms := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "sale", "channel": "sms", "content": map[string]any{"text": "Sale"}}, 201)["id"]
	pune := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "pune", "rule": map[string]any{"attr": "city", "op": "eq", "value": "Pune"}}, 201)["id"])
	vip := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "vip", "rule": map[string]any{"attr": "vip", "op": "eq", "value": "yes"}}, 201)["id"])
	run := time.Now().UnixNano() % 1000000
	phone := func(i int) string { return fmt.Sprintf("+9197%06d%02d", run, i) }
	var evs []any
	for i, a := range []map[string]any{
		{"city": "Pune"}, {"city": "Pune", "vip": "yes"}, {"city": "Pune", "timezone": "Pacific/Honolulu"},
	} {
		a["phone"] = phone(i)
		evs = append(evs, map[string]any{"external_id": fmt.Sprint("ex-", i), "name": "identify", "attrs": a})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	waitFor(t, 15*time.Second, func() bool {
		return jsonCall(t, c, "GET", fmt.Sprint("/v1/segments/", pune), nil, 200)["size"] == 3.0 &&
			jsonCall(t, c, "GET", fmt.Sprint("/v1/segments/", vip), nil, 200)["size"] == 1.0
	})
	sentTo := func(prefix string) []string {
		var out []string
		for _, m := range sandbox(t, c) {
			if strings.HasPrefix(m.MessageID, prefix) {
				out = append(out, m.Recipient)
			}
		}
		return out
	}

	// Excluding a segment that is the audience, or doesn't exist, is refused.
	base := map[string]any{"name": "pune sale", "channel": "sms", "template_id": sms, "segment_id": pune,
		"start_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "repeat": "none"}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	jsonCall(t, c, "POST", "/v1/broadcasts", with(map[string]any{"exclude_segment_ids": []int64{pune}}), 400)
	jsonCall(t, c, "POST", "/v1/broadcasts", with(map[string]any{"exclude_segment_ids": []int64{999999}}), 400)

	b := idOf(jsonCall(t, c, "POST", "/v1/broadcasts", with(map[string]any{"exclude_segment_ids": []int64{vip}}), 200)["id"])
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%d/schedule", b), nil, 200)
	prefix := fmt.Sprintf("bc%d:", b)
	waitFor(t, 15*time.Second, func() bool { return len(sentTo(prefix)) >= 2 })
	time.Sleep(time.Second)
	if got := sentTo(prefix); len(got) != 2 || slices.Contains(got, phone(1)) {
		t.Errorf("sent to %v, want everyone in Pune but the VIP %s", got, phone(1))
	}
	if runs := asList(jsonCall(t, c, "GET", fmt.Sprint("/v1/broadcasts/", b), nil, 200)["runs"]); len(runs) != 1 || runs[0].(map[string]any)["recipients"] != 2.0 {
		t.Errorf("runs = %v", runs)
	}

	// Local time: repeating broadcasts can't use it.
	jsonCall(t, c, "POST", "/v1/broadcasts", with(map[string]any{"local_time": true, "timezone": "UTC", "repeat": "daily"}), 400)
	// Scheduled for 9:00 the day after tomorrow (UTC), it starts when 9:00 comes in UTC+14 (the
	// day before at 19:00 UTC: always in the future, whatever the time of day the test runs).
	nine := time.Now().UTC().Truncate(24 * time.Hour).Add(57 * time.Hour)
	later := idOf(jsonCall(t, c, "POST", "/v1/broadcasts", with(map[string]any{"name": "tomorrow", "local_time": true, "timezone": "UTC",
		"start_at": nine.Format(time.RFC3339)}), 200)["id"])
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%d/schedule", later), nil, 200)
	lb := jsonCall(t, c, "GET", fmt.Sprint("/v1/broadcasts/", later), nil, 200)["broadcast"].(map[string]any)
	if next, _ := time.Parse(time.RFC3339, fmt.Sprint(lb["next_run_at"])); !next.Equal(nine.Add(-14 * time.Hour)) {
		t.Errorf("next run = %v, want %v", lb["next_run_at"], nine.Add(-14*time.Hour))
	}
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%d/cancel", later), nil, 200)

	// A minute ago in UTC: contacts without a zone get it now; the one in Honolulu (UTC-10) waits.
	local := idOf(jsonCall(t, c, "POST", "/v1/broadcasts", with(map[string]any{"name": "local", "local_time": true, "timezone": "UTC"}), 200)["id"])
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%d/schedule", local), nil, 200)
	lp := fmt.Sprintf("bc%d:", local)
	waitFor(t, 15*time.Second, func() bool { return len(sentTo(lp)) >= 2 })
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(contactEvents(t, c, "ex-2"), "sms_deferred") })
	if got := sentTo(lp); len(got) != 2 || slices.Contains(got, phone(2)) {
		t.Errorf("local-time sends = %v, want ex-0 and ex-1 now and ex-2 (Honolulu) later", got)
	}
}

// TestJourneyFormTrigger: "submits a form" starts a journey for that form's submissions only, from
// the moment the journey is activated.
func TestJourneyFormTrigger(t *testing.T) {
	c, _ := workspace(t, "FormTrigger")
	run := fmt.Sprint(time.Now().UnixNano())
	newForm := func(name string) (int64, string) {
		id := idOf(jsonCall(t, c, "POST", "/v1/forms", map[string]any{"name": name, "status": "active",
			"fields": []any{map[string]any{"key": "email", "label": "Email", "type": "email", "required": true}}}, 200)["id"])
		key := jsonCall(t, c, "GET", fmt.Sprint("/v1/forms/", id), nil, 200)["form"].(map[string]any)["public_key"].(string)
		return id, key
	}
	formA, keyA := newForm("Webinar")
	_, keyB := newForm("Contact us")
	graph := func(form int64) map[string]any {
		return map[string]any{"trigger": map[string]any{"type": "form_submitted", "form_id": form}, "entry": "tag",
			"nodes": map[string]any{"tag": map[string]any{"type": "update_tags", "add_tags": []any{"webinar_lead"}, "next": "x"}, "x": map[string]any{"type": "exit"}}}
	}
	jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "bad", "graph": graph(999999)}, 400)
	j := idOf(jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Webinar follow-up", "graph": graph(formA)}, 201)["id"])
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/activate", j), nil, 200)

	// Submitted straight after activation: the engine reloads on the change, not 2 s later.
	a, b := "a-"+run+"@example.com", "b-"+run+"@example.com"
	if code, _ := postForm(t, keyA, url.Values{"email": {a}}, 5*time.Second); code != 200 {
		t.Fatalf("form A: %d", code)
	}
	if code, _ := postForm(t, keyB, url.Values{"email": {b}}, 5*time.Second); code != 200 {
		t.Fatalf("form B: %d", code)
	}
	took := waitFor(t, 15*time.Second, func() bool { return fmt.Sprint(attrsOf(t, c, a)["tags"]) == "[webinar_lead]" })
	t.Logf("form submission -> journey step in %v", took)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, b) != nil })
	time.Sleep(2 * time.Second)
	if tags := attrsOf(t, c, b)["tags"]; tags != nil {
		t.Errorf("a submission of another form entered the journey: tags %v", tags)
	}
}

// TestJourneyArchiveAndDelete: a journey that never ran can be deleted; one that ran is archived
// (hidden, kept, can't go live) and restored.
func TestJourneyArchiveAndDelete(t *testing.T) {
	c, _ := workspace(t, "Archive")
	graph := map[string]any{"trigger": map[string]any{"type": "event", "event": "signup"}, "entry": "x", "nodes": map[string]any{"x": map[string]any{"type": "exit"}}}
	draft := idOf(jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "never ran", "graph": graph}, 201)["id"])
	ran := idOf(jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "ran", "graph": graph}, 201)["id"])
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/activate", ran), nil, 200)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "arch-1", "name": "signup"}, 202)
	listed := func() map[int64]map[string]any {
		out := map[int64]map[string]any{}
		for _, j := range asList(jsonCall(t, c, "GET", "/v1/journeys", nil, 200)["journeys"]) {
			m := j.(map[string]any)
			out[idOf(m["id"])] = m
		}
		return out
	}
	waitFor(t, 15*time.Second, func() bool { return listed()[ran]["has_run"] == true })
	if listed()[draft]["has_run"] != false {
		t.Errorf("draft has_run = %v", listed()[draft]["has_run"])
	}

	jsonCall(t, c, "DELETE", fmt.Sprint("/v1/journeys/", draft), nil, 204)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/archive", ran), nil, 409) // live: pause first
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/pause", ran), nil, 200)
	if e := jsonCall(t, c, "DELETE", fmt.Sprint("/v1/journeys/", ran), nil, 409)["error"]; !strings.Contains(fmt.Sprint(e), "archive") {
		t.Errorf("delete of a journey that ran: %v", e)
	}
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/archive", ran), nil, 200)
	if l := listed(); l[ran]["archived_at"] == nil || l[draft] != nil {
		t.Errorf("after archive: %v", l)
	}
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/activate", ran), nil, 409)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/unarchive", ran), nil, 200)
	if l := listed(); l[ran]["archived_at"] != nil {
		t.Errorf("after restore: %v", l[ran])
	}
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%d/activate", ran), nil, 200)
	if contactEvents(t, c, "arch-1") == nil {
		t.Error("the enrollment's contact is gone")
	}
}

// TestWebhookTestDeliveryListed: a test send that fails is listed with the deliveries as a test.
func TestWebhookTestDeliveryListed(t *testing.T) {
	c, _ := workspace(t, "HookTest")
	id := idOf(jsonCall(t, c, "POST", "/v1/settings/webhooks", map[string]any{"name": "Nowhere", "url": "http://127.0.0.1:9/hook",
		"active": true, "events": []any{"purchase"}}, 200)["id"])
	if r := jsonCall(t, c, "POST", fmt.Sprintf("/v1/settings/webhooks/%d/test", id), nil, 200); r["ok"] != false {
		t.Fatalf("test to a closed port = %v", r)
	}
	ds := asList(jsonCall(t, c, "GET", fmt.Sprint("/v1/settings/webhooks/", id), nil, 200)["deliveries"])
	if len(ds) != 1 {
		t.Fatalf("deliveries = %v", ds)
	}
	d := ds[0].(map[string]any)
	if d["event_name"] != "webhook_test" || d["test"] != true || d["final"] != true || d["error"] == nil {
		t.Errorf("test delivery = %v", d)
	}
}
