//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

func smsTo(t *testing.T, c *http.Client, phone string) []string {
	var out struct {
		Messages []struct {
			Channel   string `json:"channel"`
			Recipient string `json:"recipient"`
			Payload   struct {
				Content struct{ Text string } `json:"content"`
			} `json:"payload"`
		} `json:"messages"`
	}
	b, _ := json.Marshal(jsonCall(t, c, "GET", "/v1/sandbox/messages?channel=sms", nil, 200))
	_ = json.Unmarshal(b, &out)
	var texts []string
	for _, m := range out.Messages {
		if m.Recipient == phone {
			texts = append(texts, m.Payload.Content.Text)
		}
	}
	return texts
}

// TestJourneyPowerSteps: wait for an event with a timeout, a goal that converts, a profile update, a
// check on the updated profile, and a webhook that the SSRF guard blocks without stalling the journey.
func TestJourneyPowerSteps(t *testing.T) {
	c, _ := workspace(t, "Power")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	tpl := func(text string) any {
		return jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": text, "channel": "sms", "content": map[string]any{"text": text}}, 201)["id"]
	}
	thanks, nudge := tpl("thanks for looking"), tpl("still thinking?")
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "power", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "signup"},
		"goal":    map[string]any{"event": "purchase"},
		"entry":   "tag",
		"nodes": map[string]any{
			"tag":  map[string]any{"type": "update_attr", "attrs": map[string]any{"stage": "lead"}, "next": "hook"},
			"hook": map[string]any{"type": "webhook", "url": "http://127.0.0.1:9/internal", "next": "wait"},
			"wait": map[string]any{"type": "wait_for_event", "event": "cart_view", "seconds": 4, "yes": "chk", "no": "late"},
			"chk":  map[string]any{"type": "check", "rule": map[string]any{"attr": "stage", "op": "eq", "value": "lead"}, "yes": "yes", "no": "x"},
			"yes":  map[string]any{"type": "send_sms", "template_id": thanks, "next": "x"},
			"late": map[string]any{"type": "send_sms", "template_id": nudge, "next": "x"},
			"x":    map[string]any{"type": "exit"},
		},
	}}, 201)
	jpath := fmt.Sprintf("/v1/journeys/%v", j["id"])
	jsonCall(t, c, "POST", jpath+"/activate", nil, 200)
	time.Sleep(3 * time.Second)

	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	people := map[string]string{"looker": "+9198100" + run + "1", "idle": "+9198100" + run + "2", "buyer": "+9198100" + run + "3"}
	var evs []any
	for who, phone := range people {
		evs = append(evs, map[string]any{"external_id": who + run, "name": "identify", "attrs": map[string]any{"phone": phone}},
			map[string]any{"external_id": who + run, "name": "signup"})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	time.Sleep(1500 * time.Millisecond) // everyone is now parked on "wait"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": "looker" + run, "name": "cart_view"},
		map[string]any{"external_id": "buyer" + run, "name": "purchase"},
	}}, 202)

	waitFor(t, 15*time.Second, func() bool { return len(smsTo(t, c, people["looker"])) == 1 && len(smsTo(t, c, people["idle"])) == 1 })
	time.Sleep(5 * time.Second) // the buyer's timeout has long passed
	if got := smsTo(t, c, people["looker"]); !slices.Equal(got, []string{"thanks for looking"}) {
		t.Errorf("looker got %v: the event should resume the wait at once, via the check", got)
	}
	if got := smsTo(t, c, people["idle"]); !slices.Equal(got, []string{"still thinking?"}) {
		t.Errorf("idle contact got %v: the timeout path", got)
	}
	if got := smsTo(t, c, people["buyer"]); len(got) != 0 {
		t.Errorf("buyer got %v: reaching the goal must end the journey", got)
	}
	stats := jsonCall(t, c, "GET", jpath+"/stats", nil, 200)["status"].(map[string]any)
	if stats["converted"] != 1.0 || stats["completed"] != 2.0 {
		t.Errorf("stats = %v, want 1 converted and 2 completed", stats)
	}
	ct := jsonCall(t, c, "GET", "/v1/contacts/idle"+run, nil, 200)["contact"].(map[string]any)
	if ct["attrs"].(map[string]any)["stage"] != "lead" {
		t.Errorf("update step: attrs = %v", ct["attrs"])
	}
	if !slices.Contains(contactEvents(t, c, "idle"+run), "webhook_failed") {
		t.Error("webhook to 127.0.0.1 should be blocked and recorded as webhook_failed")
	}
}

func TestReentryAndFrequencyCap(t *testing.T) {
	c, _ := workspace(t, "Rules")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	promo := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "promo", "channel": "sms", "content": map[string]any{"text": "promo"}}, 201)["id"]
	otp := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "otp", "channel": "sms", "transactional": true, "content": map[string]any{"text": "code 1234"}}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "again", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "ping", "reentry": "always"}, "entry": "x",
		"nodes": map[string]any{"x": map[string]any{"type": "exit"}},
	}}, 201)
	jpath := fmt.Sprintf("/v1/journeys/%v", j["id"])
	jsonCall(t, c, "POST", jpath+"/activate", nil, 200)
	jsonCall(t, c, "PUT", "/v1/settings/delivery", map[string]any{"caps": map[string]any{"sms": map[string]any{"max": 1, "hours": 24}},
		"quiet": map[string]any{"enabled": false, "start": "21:00", "end": "09:00", "timezone": "Asia/Kolkata", "channels": []string{"sms"}}}, 200)
	time.Sleep(3 * time.Second)

	ext, phone := fmt.Sprint("rc-", time.Now().UnixNano()), fmt.Sprintf("+9198%08d", time.Now().UnixNano()%100000000)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": phone}}, 202)
	for i := 0; i < 2; i++ {
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "ping"}, 202)
		time.Sleep(time.Second)
	}
	waitFor(t, 10*time.Second, func() bool {
		st := jsonCall(t, c, "GET", jpath+"/stats", nil, 200)["status"].(map[string]any)
		return st["completed"] == 2.0
	})

	for _, tid := range []any{promo, promo, otp} {
		jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tid, "external_id": ext}, 202)
		time.Sleep(700 * time.Millisecond)
	}
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "sms_capped") })
	time.Sleep(time.Second)
	if got := smsTo(t, c, phone); len(got) != 2 || !slices.Contains(got, "promo") || !slices.Contains(got, "code 1234") {
		t.Errorf("sent %v: want one promo (the second is capped) and the transactional code", got)
	}
}
