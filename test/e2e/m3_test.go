//go:build e2e

package e2e

// M3, campaigns: a one-time email made the way the wizard makes it (a hidden template, a broadcast
// to a segment with an exclusion) arrives, and the exact estimate equals what was sent; a provider's
// daily quota stops sends at the limit and a send deadline drops late messages; journey steps are
// copied and pasted between journeys; duplicates; saved test contacts.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// segmentSize polls until a segment holds n members.
func waitSegmentSize(t *testing.T, c *http.Client, id any, n float64) {
	t.Helper()
	waitFor(t, 30*time.Second, func() bool {
		return jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%v", id), nil, 200)["size"] == n
	})
}

// waitReachReady waits for the workspace's reachability bitmaps (built once per workspace).
func waitReachReady(t *testing.T, c *http.Client, seg any) {
	t.Helper()
	waitFor(t, 40*time.Second, func() bool {
		r := jsonCall(t, c, "POST", "/v1/segments/estimate", map[string]any{"include": []any{seg}, "channel": "sms", "transactional": true}, -1)
		return r["ready"] == true
	})
}

func TestOneTimeCampaignEstimateAndSend(t *testing.T) {
	c, _ := workspace(t, "Campaigns")
	run := fmt.Sprint(time.Now().UnixNano())
	plan := "m3-" + run
	addr := func(k string) string { return "m3-" + k + "-" + run + "@example.com" }
	// a, b, c are reachable; d is excluded (VIP); e unsubscribed from email; f has no email; g bounced.
	ev := []any{}
	for _, k := range []string{"a", "b", "c", "d", "e", "g"} {
		attrs := map[string]any{"plan": plan, "first_name": strings.ToUpper(k)}
		if k == "d" {
			attrs["vip"] = "yes"
		}
		if k == "g" {
			attrs["email_bounced"] = true
		}
		ev = append(ev, map[string]any{"external_id": "m3" + k + run, "name": "identify", "email": addr(k), "attrs": attrs})
	}
	ev = append(ev, map[string]any{"external_id": "m3f" + run, "name": "identify", "attrs": map[string]any{"plan": plan, "phone": "+919800001234"}})
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": ev}, 202)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": "m3e" + run, "name": "unsubscribed", "props": map[string]any{"channel": "email"}}}}, 202)

	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M3 audience", "rule": map[string]any{"attr": "plan", "op": "eq", "value": plan}}, 201)["id"]
	vip := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M3 VIPs", "rule": map[string]any{"attr": "vip", "op": "eq", "value": "yes"}}, 201)["id"]
	// Built only to know the segments role has seen the unsubscribe (reachability is set in the same pass).
	optedOut := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M3 opted out", "rule": map[string]any{"all": []any{
		map[string]any{"attr": "plan", "op": "eq", "value": plan}, map[string]any{"attr": "optout_email", "op": "eq", "value": "true"}}}}, 201)["id"]
	waitSegmentSize(t, c, seg, 7)
	waitSegmentSize(t, c, vip, 1)
	waitSegmentSize(t, c, optedOut, 1)
	waitReachReady(t, c, seg)
	time.Sleep(time.Second)

	// The exact estimate, step by step.
	est := jsonCall(t, c, "POST", "/v1/segments/estimate", map[string]any{"include": []any{seg}, "exclude": []any{vip}, "channel": "email"}, 200)
	want := map[string]float64{"audience": 7, "after_exclude": 6, "after_consent": 5, "reachable": 3, "will_send": 3}
	for k, v := range want {
		if est[k] != v {
			t.Errorf("estimate %s = %v, want %v (%v)", k, est[k], v, est)
		}
	}
	// Reach by channel for the segment builder.
	reach := jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%v/reach", seg), nil, 200)
	if !strings.Contains(fmt.Sprint(reach["channels"]), "channel:sms reachable:1") {
		t.Errorf("reach by channel: %v", reach["channels"])
	}

	// The wizard: the content becomes a hidden template, then a tagged broadcast that sends now.
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Diwali (Email)", "channel": "email", "hidden": true,
		"subject": `Diwali offer for {{contact.first_name | default:"you"}}`, "html": "<p>Lights, sweets and 20% off.</p>"}, 201)["id"]
	list := jsonCall(t, c, "GET", "/v1/templates", nil, 200)
	if strings.Contains(fmt.Sprint(list["templates"]), "Diwali (Email)") {
		t.Error("a campaign's own template shows in the template list")
	}
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Diwali " + run, "channel": "email", "template_id": tpl,
		"segment_id": seg, "exclude_segment_ids": []any{vip}, "repeat": "none", "start_at": time.Now().UTC().Format(time.RFC3339),
		"tags": []string{"Festive", " festive ", "Diwali 2026"}}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)

	waitFor(t, 30*time.Second, func() bool {
		return mailCount(t, addr("a")) == 1 && mailCount(t, addr("b")) == 1 && mailCount(t, addr("c")) == 1
	})
	var search struct{ Messages []struct{ Subject string } }
	mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+addr("b")), &search)
	if len(search.Messages) != 1 || search.Messages[0].Subject != "Diwali offer for B" {
		t.Errorf("email: %+v", search.Messages)
	}
	// What the estimate said is what was sent.
	waitFor(t, 30*time.Second, func() bool {
		rep := jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil, 200)
		tot, _ := rep["totals"].(map[string]any)
		return tot["sent"] == est["reachable"]
	})
	for _, k := range []string{"d", "e", "g"} {
		if n := mailCount(t, addr(k)); n != 0 {
			t.Errorf("%s got %d emails", k, n)
		}
	}

	// The Campaigns hub: one-time, tagged (tags normalised), filterable by tag.
	hub := jsonCall(t, c, "GET", "/v1/campaigns?category=one_time&tag=festive", nil, 200)
	rows, _ := hub["campaigns"].([]any)
	if len(rows) != 1 {
		t.Fatalf("hub rows = %v", hub)
	}
	row := rows[0].(map[string]any)
	if row["category"] != "one_time" || fmt.Sprint(row["tags"]) != "[Festive Diwali 2026]" || row["audience"] != 7.0 {
		t.Errorf("hub row = %v", row)
	}
	waitFor(t, 20*time.Second, func() bool {
		rows, _ := jsonCall(t, c, "GET", "/v1/campaigns?category=one_time", nil, 200)["campaigns"].([]any)
		return len(rows) == 1 && rows[0].(map[string]any)["sent"] == 3.0
	})
	if tags := jsonCall(t, c, "GET", "/v1/campaigns/tags", nil, 200)["tags"]; fmt.Sprint(tags) != "[Diwali 2026 Festive]" {
		t.Errorf("tag suggestions = %v", tags)
	}

	// Duplicates: the broadcast (and its own template), the template, the segment.
	dup := jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/duplicate", bc), nil, 201)["id"]
	d := jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v", dup), nil, 200)["broadcast"].(map[string]any)
	if d["status"] != "draft" || d["template_id"] == tpl || !strings.HasSuffix(d["name"].(string), "(copy)") {
		t.Errorf("duplicate broadcast = %v", d)
	}
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/templates/%v/duplicate", tpl), nil, 201)
	sd := jsonCall(t, c, "POST", fmt.Sprintf("/v1/segments/%v/duplicate", seg), nil, 201)["id"]
	waitSegmentSize(t, c, sd, 7)

	// A saved test contact with a bare address gets a test send.
	qa := "m3-qa-" + run + "@example.com"
	tc := jsonCall(t, c, "POST", "/v1/test-contacts", map[string]any{"name": "Quinn QA", "email": qa}, 200)["id"]
	jsonCall(t, c, "POST", "/v1/test-contacts", map[string]any{"name": "Bad", "phone": "98000"}, 400)
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "test_contact_id": tc}, 202)
	waitFor(t, 20*time.Second, func() bool { return mailCount(t, qa) == 1 })
	mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+qa), &search)
	if search.Messages[0].Subject != "Diwali offer for Quinn" {
		t.Errorf("test subject %q", search.Messages[0].Subject)
	}
}

// TestQuotaAndDeadline: a provider with a daily quota of 2 sends 2 of 4 messages and holds the rest
// for tomorrow; a broadcast with a 1-hour deadline then has nothing it can send in time, so its
// messages are dropped as late instead.
func TestQuotaAndDeadline(t *testing.T) {
	c, _ := workspace(t, "Quota")
	run := fmt.Sprint(time.Now().UnixNano())
	prov := jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)["id"]
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/channels/providers/%v/limits", prov), map[string]any{"daily_quota": -1}, 400)
	// The quota day must not end within the deadline's hour while the test runs.
	zone := "UTC"
	if time.Now().UTC().Hour() >= 20 {
		zone = "America/Los_Angeles"
	}
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/channels/providers/%v/limits", prov), map[string]any{"daily_quota": 2, "rate_per_second": 50, "timezone": zone}, 200)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Sale SMS", "channel": "sms", "content": map[string]any{"text": "Sale today"}}, 201)["id"]
	ev := []any{}
	for i := range 6 {
		group := "first"
		if i >= 4 {
			group = "late"
		}
		ev = append(ev, map[string]any{"external_id": fmt.Sprintf("q%d-%s", i, run), "name": "identify",
			"attrs": map[string]any{"quota_group": group + run, "phone": fmt.Sprintf("+9198%08d", i+int(time.Now().Unix()%1e6)*10)}})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": ev}, 202)
	first := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Quota first", "rule": map[string]any{"attr": "quota_group", "op": "eq", "value": "first" + run}}, 201)["id"]
	late := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Quota late", "rule": map[string]any{"attr": "quota_group", "op": "eq", "value": "late" + run}}, 201)["id"]
	waitSegmentSize(t, c, first, 4)
	waitSegmentSize(t, c, late, 2)

	now := time.Now().UTC().Format(time.RFC3339)
	b1 := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Quota run", "channel": "sms", "template_id": tpl, "segment_id": first, "repeat": "none", "start_at": now}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", b1), nil, 200)
	smsCount := func() int {
		n := 0
		for _, m := range sandbox(t, c) {
			if m.Channel == "sms" {
				n++
			}
		}
		return n
	}
	waitFor(t, 30*time.Second, func() bool {
		rep := jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", b1), nil, 200)
		tot, _ := rep["totals"].(map[string]any)
		return tot["sent"] == 2.0 && tot["deferred"] == 2.0
	})
	time.Sleep(2 * time.Second)
	if n := smsCount(); n != 2 {
		t.Fatalf("%d SMS sent, want the quota of 2", n)
	}
	ps, _ := jsonCall(t, c, "GET", "/v1/channels/providers", nil, 200)["providers"].([]any)
	if p := ps[0].(map[string]any); p["usage_today"] != 2.0 {
		t.Errorf("usage today = %v", p["usage_today"])
	}

	// The quota is used up until tomorrow, after this broadcast's deadline: dropped as late.
	b2 := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Deadline run", "channel": "sms", "template_id": tpl, "segment_id": late,
		"repeat": "none", "start_at": now, "deadline": map[string]any{"hours": 1}}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", b2), nil, 200)
	waitFor(t, 30*time.Second, func() bool {
		rep := jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", b2), nil, 200)
		tot, _ := rep["totals"].(map[string]any)
		return tot["failed"] == 2.0 && rep["late"] == 2.0
	})
	evs := contactEvents(t, c, fmt.Sprintf("q4-%s", run))
	if !strings.Contains(strings.Join(evs, ","), "sms_failed") {
		t.Errorf("events of a late contact: %v", evs)
	}
	if n := smsCount(); n != 2 {
		t.Errorf("%d SMS sent; late messages must not go out", n)
	}
	// The hub shows both, with their channel.
	rows, _ := jsonCall(t, c, "GET", "/v1/campaigns?channel=sms", nil, 200)["campaigns"].([]any)
	if len(rows) != 2 {
		t.Errorf("hub sms rows: %v", rows)
	}
}

// TestPasteJourneySteps copies steps from one journey to another (and to another workspace), the
// way the editor does: the server checks the clipboard, the app inserts the steps.
func TestPasteJourneySteps(t *testing.T) {
	c, _ := workspace(t, "Paste")
	other, _ := workspace(t, "Paste other")
	email := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Welcome", "channel": "email", "subject": "Hi", "html": "<p>Hi</p>"}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Everyone", "rule": map[string]any{"attr": "plan", "op": "exists"}}, 201)["id"]
	steps := map[string]any{
		"w1": map[string]any{"type": "wait", "seconds": 3600, "next": "e1"},
		"e1": map[string]any{"type": "send_email", "template_id": email, "next": "d1", "deadline": map[string]any{"at": "21:00"}},
		"d1": map[string]any{"type": "message_decision", "step": "e1", "outcome": "opened", "seconds": 86400, "yes": "x1", "no": "x1"},
		"x1": map[string]any{"type": "exit"},
	}
	src := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Source", "graph": map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": seg}, "entry": "w1", "nodes": steps}}, 201)["id"]
	// Copy e1 and d1 (the editor's selection); x1 wasn't copied, so d1's paths are free.
	clip := map[string]any{"pulse": "journey-steps", "version": 1, "entry": "e1", "from": src, "nodes": map[string]any{
		"e1": map[string]any{"type": "send_email", "template_id": email, "next": "d1", "deadline": map[string]any{"at": "21:00"}},
		"d1": map[string]any{"type": "message_decision", "step": "e1", "outcome": "opened", "seconds": 86400},
	}}
	res := jsonCall(t, c, "POST", "/v1/journey-steps/check", map[string]any{"clipboard": clip}, 200)
	if notes, _ := res["notes"].([]any); len(notes) != 0 {
		t.Errorf("same workspace: notes %v", notes)
	}
	// Another workspace doesn't have the template: it is dropped with a note.
	res2 := jsonCall(t, other, "POST", "/v1/journey-steps/check", map[string]any{"clipboard": clip}, 200)
	if notes, _ := res2["notes"].([]any); len(notes) != 1 || !strings.Contains(fmt.Sprint(notes[0]), "doesn't exist in this workspace") {
		t.Errorf("other workspace: notes %v", res2["notes"])
	}
	if n := res2["clipboard"].(map[string]any)["nodes"].(map[string]any)["e1"].(map[string]any); n["template_id"] != nil {
		t.Errorf("foreign template kept: %v", n)
	}
	// Bad clipboards are refused.
	jsonCall(t, c, "POST", "/v1/journey-steps/check", map[string]any{"clipboard": map[string]any{"pulse": "journey-steps", "version": 1, "entry": "a",
		"nodes": map[string]any{"a": map[string]any{"type": "wait", "seconds": 0}}}}, 400)

	// Paste into a new journey (as the app does: new ids, linked in after Start) and take it live.
	var cleaned struct {
		Nodes map[string]map[string]any `json:"nodes"`
	}
	b, _ := json.Marshal(res["clipboard"])
	_ = json.Unmarshal(b, &cleaned)
	nodes := map[string]any{"email2": cleaned.Nodes["e1"], "opened2": cleaned.Nodes["d1"], "exit": map[string]any{"type": "exit"}}
	cleaned.Nodes["e1"]["next"] = "opened2"
	cleaned.Nodes["d1"]["step"], cleaned.Nodes["d1"]["yes"], cleaned.Nodes["d1"]["no"] = "email2", "exit", "exit"
	dst := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Pasted", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "signup"}, "entry": "email2", "nodes": nodes}}, 201)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", dst), nil, 200)
	g := jsonCall(t, c, "GET", fmt.Sprintf("/v1/journeys/%v", dst), nil, 200)["graph"].(map[string]any)
	if fmt.Sprint(g["nodes"].(map[string]any)["email2"].(map[string]any)["deadline"]) != "map[at:21:00]" {
		t.Errorf("pasted step lost its deadline: %v", g["nodes"])
	}

	// Duplicate and tag journeys; the hub sorts them into automation and trigger based.
	dup := jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/duplicate", src), nil, 201)["id"]
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/journeys/%v/tags", dup), map[string]any{"tags": []string{"Onboarding"}}, 200)
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/journeys/%v/tags", dup), map[string]any{"tags": []string{"a,b"}}, 400)
	hub := jsonCall(t, c, "GET", "/v1/campaigns", nil, 200)
	counts := hub["counts"].(map[string]any)
	if counts["automation"] != 2.0 || counts["trigger"] != 1.0 {
		t.Errorf("hub counts = %v", counts)
	}
	rows, _ := jsonCall(t, c, "GET", "/v1/campaigns?tag=onboarding", nil, 200)["campaigns"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["status"] != "draft" {
		t.Errorf("tagged duplicate: %v", rows)
	}
}
