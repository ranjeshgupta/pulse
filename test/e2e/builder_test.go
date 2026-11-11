//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestJourneyBuilderSteps runs the Mautic-style steps end to end: a decision (submits a form ->
// Yes), a condition (contact stage), Do Not Contact, owner, an email to a user that arrives in
// Mailpit, a marketing message that falls back to its second channel, and change campaigns into a
// journey that shows a focus item to that one contact (via the decide API) and loops with a jump
// until the jump cap ends it.
func TestJourneyBuilderSteps(t *testing.T) {
	c, siteKey := workspace(t, "Builder")
	run := fmt.Sprint(time.Now().UnixNano())
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	jsonCall(t, c, "POST", "/v1/stages", map[string]any{"name": "MQL"}, 201)
	uid := idOf(jsonCall(t, c, "GET", "/v1/auth/me", nil, 200)["user"].(map[string]any)["id"])

	formID := idOf(jsonCall(t, c, "POST", "/v1/forms", map[string]any{"name": "Demo request", "status": "active",
		"fields": []any{map[string]any{"key": "email", "label": "Email", "type": "email", "required": true}}}, 200)["id"])
	formKey := jsonCall(t, c, "GET", fmt.Sprint("/v1/forms/", formID), nil, 200)["form"].(map[string]any)["public_key"].(string)
	notifyTpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "alert", "channel": "email",
		"subject": "New lead {{.Attrs.first_name}} " + run, "html": "<p>{{.Contact.Email}} asked for a demo</p>"}, 201)["id"]
	offerTpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "offer", "channel": "email",
		"subject": "Your offer " + run, "html": "<p>Hi {{.Attrs.first_name}}</p>"}, 201)["id"]
	smsTpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "offer sms", "channel": "sms", "content": map[string]any{"text": "offer"}}, 201)["id"]
	// A draft focus item: no page shows it by targeting, only the journey step does.
	focus := idOf(jsonCall(t, c, "POST", "/v1/focus-items", map[string]any{"name": "VIP " + run, "kind": "bar", "status": "draft",
		"content": map[string]any{"title": "Just for you"}, "targeting": map[string]any{"trigger": "load", "frequency": "always"}}, 200)["id"])

	loop := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "VIP loop", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "never_sent"},
		"entry":   "f",
		"nodes": map[string]any{
			"f": map[string]any{"type": "show_focus", "focus_id": focus, "next": "p"},
			"p": map[string]any{"type": "adjust_points", "points": 1, "next": "j"},
			"j": map[string]any{"type": "jump", "target": "p"},
		}}}, 201)
	loopID := idOf(loop["id"])
	jsonCall(t, c, "POST", fmt.Sprint("/v1/journeys/", loopID, "/activate"), nil, 200)

	alert := "alert-" + run + "@team.example.com"
	main := map[string]any{"name": "Demo requests", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "trial_started"},
		"entry":   "form",
		"nodes": map[string]any{
			"form":  map[string]any{"type": "on_form", "form_id": formID, "seconds": 3600, "yes": "stage", "no": "x"},
			"stage": map[string]any{"type": "check", "preset": "stage", "rule": map[string]any{"attr": "stage", "op": "in", "value": []string{"MQL"}}, "yes": "dnc", "no": "x"},
			"dnc":   map[string]any{"type": "dnc_add", "channel": "sms", "reason": "prefers email", "next": "owner"},
			"owner": map[string]any{"type": "update_owner", "owner_id": uid, "next": "tell"},
			"tell":  map[string]any{"type": "notify_user", "template_id": notifyTpl, "emails": []string{alert}, "next": "msg"},
			"msg": map[string]any{"type": "send_message", "channels": []any{
				map[string]any{"channel": "sms", "template_id": smsTpl}, map[string]any{"channel": "email", "template_id": offerTpl}}, "next": "move"},
			"move": map[string]any{"type": "change_journey", "add_journeys": []int64{loopID}, "next": "x"},
			"x":    map[string]any{"type": "exit"},
		}}}
	// A journey can't move contacts into itself, and a delete step needs typing DELETE.
	bad := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "bad", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "x"}, "entry": "d",
		"nodes": map[string]any{"d": map[string]any{"type": "delete_contact"}}}}, 400)
	if !strings.Contains(fmt.Sprint(bad["error"]), "DELETE") {
		t.Errorf("delete without confirmation: %v", bad)
	}
	mainID := idOf(jsonCall(t, c, "POST", "/v1/journeys", main, 201)["id"])
	jsonCall(t, c, "POST", fmt.Sprint("/v1/journeys/", mainID, "/activate"), nil, 200)
	time.Sleep(3 * time.Second) // journey index refresh

	lead, idle := "lead-"+run, "idle-"+run
	leadMail := "lead-" + run + "@example.com"
	phone := "+9198" + run[len(run)-8:]
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": lead, "name": "identify", "email": leadMail, "attrs": map[string]any{"first_name": "Asha", "phone": phone}},
		map[string]any{"external_id": idle, "name": "identify", "email": "idle-" + run + "@example.com"},
	}}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, lead)["first_name"] == "Asha" })
	jsonCall(t, c, "PUT", "/v1/contacts/"+lead+"/stage", map[string]any{"stage": "MQL"}, 202)
	waitFor(t, 10*time.Second, func() bool { return stageOf(t, c, lead) == "MQL" })
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": lead, "name": "trial_started"}, map[string]any{"external_id": idle, "name": "trial_started"}}}, 202)
	time.Sleep(1500 * time.Millisecond) // both wait on the form decision

	if code, _ := postForm(t, formKey, url.Values{"email": {leadMail}}, 5*time.Second); code != 200 {
		t.Fatalf("form submission: %d", code)
	}
	took := waitFor(t, 20*time.Second, func() bool { return mailCount(t, alert) == 1 && mailCount(t, leadMail) == 1 })
	t.Logf("form decision -> stage condition -> actions in %v", took)
	if got := searchMail(t, "to:"+alert); len(got.Messages) != 1 || got.Messages[0].Subject != "New lead Asha "+run {
		t.Errorf("email to user: %+v", got)
	}
	if got := searchMail(t, "to:"+leadMail); len(got.Messages) != 1 || got.Messages[0].Subject != "Your offer "+run {
		t.Errorf("marketing message: %+v (want the email: SMS is marked Do Not Contact)", got)
	}
	if got := smsTo(t, c, phone); len(got) != 0 {
		t.Errorf("SMS sent despite Do Not Contact: %v", got)
	}
	a := attrsOf(t, c, lead)
	if a["optout_sms"] != true || idOf(a["owner_id"]) != uid {
		t.Errorf("attrs after DNC and owner steps: %v", a)
	}

	// Change campaigns put the lead in the loop journey: 1 point a pass, 25 jumps, then the cap.
	waitFor(t, 20*time.Second, func() bool {
		p, _ := attrsOf(t, c, lead)["points"].(float64)
		return p == 26 && slices.Contains(contactEvents(t, c, lead), "journey_jump_limit")
	})
	if st := jsonCall(t, c, "GET", fmt.Sprint("/v1/journeys/", loopID, "/stats"), nil, 200)["status"].(map[string]any); st["completed"] != 1.0 {
		t.Errorf("loop journey stats %v", st)
	}
	time.Sleep(time.Second)
	if p, _ := attrsOf(t, c, lead)["points"].(float64); p != 26 {
		t.Errorf("points %v after the cap: the loop must stop", p)
	}

	// The focus item is shown to that contact on their next page view, once, and to nobody else.
	decide := func(ext string) []string {
		b, _ := json.Marshal(map[string]any{"external_id": ext, "url": "https://shop.test/", "slots": []string{}})
		req, _ := http.NewRequest("POST", apiURL+"/v1/web/decide", bytes.NewReader(b))
		req.Header.Set("X-Pulse-Site-Key", siteKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var d struct{ Focus []struct{ Name string } }
		_ = json.NewDecoder(resp.Body).Decode(&d)
		var names []string
		for _, f := range d.Focus {
			names = append(names, f.Name)
		}
		return names
	}
	if got := decide(idle); len(got) != 0 {
		t.Errorf("another contact sees %v", got)
	}
	if got := decide(lead); !slices.Equal(got, []string{"VIP " + run}) {
		t.Errorf("lead sees %v, want the journey's focus item", got)
	}
	if got := decide(lead); len(got) != 0 {
		t.Errorf("shown again: %v", got)
	}

	// Per-step counts for the canvas: both entered the decision, one said yes; the idle contact waits.
	rep := jsonCall(t, c, "GET", fmt.Sprint("/v1/journeys/", mainID, "/report"), nil, 200)
	steps, _ := rep["steps"].(map[string]any)
	form, _ := steps["form"].(map[string]any)
	stage, _ := steps["stage"].(map[string]any)
	if form["entered"] != 2.0 || form["yes"] != 1.0 || stage["yes"] != 1.0 {
		t.Errorf("step stats %v", steps)
	}
	if st := jsonCall(t, c, "GET", fmt.Sprint("/v1/journeys/", mainID, "/stats"), nil, 200)["status"].(map[string]any); st["completed"] != 1.0 || st["waiting"] != 1.0 {
		t.Errorf("journey stats %v", st)
	}
}

// TestReplyTracking: with a reply tracking address, emails carry a Reply-To with a code; the
// provider's inbound webhook posts the reply, Pulse records email_replied, and a "replies to
// email" decision takes Yes.
func TestReplyTracking(t *testing.T) {
	c, _ := workspace(t, "Replies")
	run := fmt.Sprint(time.Now().UnixNano())
	rules := jsonCall(t, c, "GET", "/v1/settings/delivery", nil, 200)
	rules["reply_to"] = "replies@in.example.com"
	jsonCall(t, c, "PUT", "/v1/settings/delivery", rules, 200)
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	mail := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "ask", "channel": "email", "subject": "Quick question " + run, "html": "<p>Reply?</p>"}, 201)["id"]
	thanks := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "thanks", "channel": "sms", "content": map[string]any{"text": "thanks for replying"}}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "ask", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "asked"}, "entry": "m",
		"nodes": map[string]any{
			"m": map[string]any{"type": "send_email", "template_id": mail, "next": "d"},
			"d": map[string]any{"type": "message_decision", "step": "m", "outcome": "replied", "seconds": 3600, "yes": "t", "no": "x"},
			"t": map[string]any{"type": "send_sms", "template_id": thanks, "next": "x"},
			"x": map[string]any{"type": "exit"},
		}}}, 201)
	jsonCall(t, c, "POST", fmt.Sprint("/v1/journeys/", j["id"], "/activate"), nil, 200)
	time.Sleep(3 * time.Second)

	ext, email, phone := "r-"+run, "r-"+run+"@example.com", "+9197"+run[len(run)-8:]
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext, "name": "identify", "email": email, "attrs": map[string]any{"phone": phone}},
		map[string]any{"external_id": ext, "name": "asked"}}}, 202)
	waitFor(t, 15*time.Second, func() bool { return mailCount(t, email) == 1 })
	msg := searchMail(t, "to:"+email).Messages[0]
	var headers map[string][]string
	mailpitGet(t, "/api/v1/message/"+msg.ID+"/headers", &headers)
	replyTo := strings.Join(headers["Reply-To"], "")
	if !strings.HasPrefix(replyTo, "replies+r") || !strings.HasSuffix(replyTo, "@in.example.com") {
		t.Fatalf("Reply-To %q", replyTo)
	}

	hook := jsonCall(t, c, "GET", "/v1/settings/bounces", nil, 200)["reply_url"].(string)
	u, _ := url.Parse(hook)
	form := url.Values{"recipient": {replyTo}, "sender": {email}, "subject": {"Re: Quick question"}, "stripped-text": {"Yes!"}}
	resp, err := http.PostForm(apiURL+u.Path, form) // Mailgun-style inbound route
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("reply webhook: %v %v", err, resp)
	}
	resp.Body.Close()
	waitFor(t, 15*time.Second, func() bool { return len(smsTo(t, c, phone)) == 1 })
	// The timeline is read from ClickHouse, which the sink fills a moment after the journey acts.
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "email_replied") })
	if resp, _ := http.PostForm(apiURL+"/v1/replies/wrong-token", form); resp.StatusCode != 404 {
		t.Errorf("unknown token: %d", resp.StatusCode)
	}
}
