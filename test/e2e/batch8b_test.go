//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestRCSReplies: an RCS reply (here a tapped suggestion chip, through the sandbox) is recorded as
// rcs_received and rcs_replied on the latest RCS message, so a journey's "replied" decision takes
// Yes; STOP on RCS unsubscribes the contact from RCS.
func TestRCSReplies(t *testing.T) {
	c, _ := workspace(t, "RCS replies")
	run := fmt.Sprint(time.Now().UnixNano())
	for _, ch := range []string{"rcs", "sms"} {
		jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": ch, "provider": "sandbox_" + ch, "is_default": true}, 200)
	}
	ask := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "ask", "channel": "rcs", "content": map[string]any{
		"text": "Want 10% off?", "suggestions": []any{map[string]any{"type": "reply", "text": "Yes please", "value": "yes"}}}}, 201)["id"]
	thanks := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "thanks", "channel": "sms", "content": map[string]any{"text": "code SAVE10 " + run}}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "rcs ask", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "rcs_asked"}, "entry": "m",
		"nodes": map[string]any{
			"m": map[string]any{"type": "send_rcs", "template_id": ask, "next": "d"},
			"d": map[string]any{"type": "message_decision", "step": "m", "outcome": "replied", "seconds": 3600, "yes": "t", "no": "x"},
			"t": map[string]any{"type": "send_sms", "template_id": thanks, "next": "x"},
			"x": map[string]any{"type": "exit"},
		}}}, 201)
	jsonCall(t, c, "POST", fmt.Sprint("/v1/journeys/", j["id"], "/activate"), nil, 200)
	time.Sleep(3 * time.Second)

	ext, phone := "rcs-"+run, "+9196"+run[len(run)-8:]
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": phone}},
		map[string]any{"external_id": ext, "name": "rcs_asked"}}}, 202)
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "rcs_sent") })

	// A suggestion with a channel other than RCS is refused.
	jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "sms", "from": phone, "text": "x", "suggestion": "reply"}, 400)
	jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "rcs", "from": phone, "text": "Yes please", "suggestion": "reply", "postback": "yes"}, 202)
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(smsTo(t, c, phone), "code SAVE10 "+run) })
	evs := contactEvents(t, c, ext)
	if !slices.Contains(evs, "rcs_received") || !slices.Contains(evs, "rcs_replied") {
		t.Errorf("events %v: want rcs_received and rcs_replied", evs)
	}

	r := jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "rcs", "from": phone, "text": "STOP"}, 202)
	if r["action"] != "opt_out" {
		t.Errorf("STOP on RCS = %v", r)
	}
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, ext)["optout_rcs"] == true })
	jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "rcs", "from": phone, "text": "START"}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, ext)["optout_rcs"] == false })
}

// TestPushClickTracking: a push carries a tracking link in its data; posting a click to it (as
// the service worker and app SDKs do) records push_clicked, which a journey's "clicked" decision
// waits for; a dismissal records push_dismissed; a forged token is refused.
func TestPushClickTracking(t *testing.T) {
	c, siteKey := workspace(t, "Push clicks")
	run := fmt.Sprint(time.Now().UnixNano())
	for _, ch := range []string{"push", "sms"} {
		jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": ch, "provider": "sandbox_" + ch, "is_default": true}, 200)
	}
	push := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "sale", "channel": "push", "content": map[string]any{
		"title": "Sale " + run, "body": "50% off today", "url": "https://shop.example.com/sale"}}, 201)["id"]
	thanks := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "clicked", "channel": "sms", "content": map[string]any{"text": "saw your click " + run}}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "push click", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "push_asked"}, "entry": "p",
		"nodes": map[string]any{
			"p": map[string]any{"type": "send_push", "template_id": push, "next": "d"},
			"d": map[string]any{"type": "message_decision", "step": "p", "outcome": "clicked", "seconds": 3600, "yes": "t", "no": "x"},
			"t": map[string]any{"type": "send_sms", "template_id": thanks, "next": "x"},
			"x": map[string]any{"type": "exit"},
		}}}, 201)
	jsonCall(t, c, "POST", fmt.Sprint("/v1/journeys/", j["id"], "/activate"), nil, 200)
	time.Sleep(3 * time.Second)

	ext, phone := "push-"+run, "+9195"+run[len(run)-8:]
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": phone}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	b, _ := json.Marshal(map[string]any{"external_id": ext, "token": "tok-android-" + run, "platform": "android"})
	req, _ := http.NewRequest("POST", apiURL+"/v1/web/devices", strings.NewReader(string(b)))
	req.Header.Set("X-Pulse-Site-Key", siteKey)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
		t.Fatalf("register device: %v %v", err, resp)
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "push_asked"}, 202)

	var track string
	waitFor(t, 15*time.Second, func() bool {
		var out struct {
			Messages []struct {
				Recipient string `json:"recipient"`
				Payload   struct {
					Content struct {
						Data map[string]string `json:"data"`
					} `json:"content"`
				} `json:"payload"`
			} `json:"messages"`
		}
		raw, _ := json.Marshal(jsonCall(t, c, "GET", "/v1/sandbox/messages?channel=push", nil, 200))
		_ = json.Unmarshal(raw, &out)
		for _, m := range out.Messages {
			if m.Recipient == "tok-android-"+run {
				track = m.Payload.Content.Data["pulse_track"]
			}
		}
		return track != ""
	})
	u, err := url.Parse(track)
	if err != nil || !strings.HasPrefix(u.Path, "/t/p/") {
		t.Fatalf("tracking link %q", track)
	}
	post := func(path string) int {
		resp, err := http.Post(apiURL+path, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("/t/p/forged-token?a=clicked"); code != 404 {
		t.Errorf("forged token: %d, want 404", code)
	}
	if code := post(u.Path + "?a=opened"); code != 400 {
		t.Errorf("unknown action: %d, want 400", code)
	}
	if code := post(u.Path + "?a=clicked&platform=android"); code != 204 {
		t.Fatalf("click: %d", code)
	}
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(smsTo(t, c, phone), "saw your click "+run) })
	if code := post(u.Path + "?a=dismissed"); code != 204 {
		t.Fatalf("dismiss: %d", code)
	}
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "push_dismissed") })
	if !slices.Contains(contactEvents(t, c, ext), "push_clicked") {
		t.Error("push_clicked not recorded")
	}
}
