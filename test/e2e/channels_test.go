//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

// workspace signs up a fresh workspace and returns a browser-like client and its site key.
func workspace(t *testing.T, name string) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	run := fmt.Sprint(time.Now().UnixNano())
	out := jsonCall(t, c, "POST", "/v1/auth/signup", map[string]any{"workspace": name + " " + run, "email": "o-" + run + "@example.com", "password": "owner password 1"}, 201)
	return c, out["workspace"].(map[string]any)["site_key"].(string)
}

type sandboxMsg struct {
	Channel, Recipient, MessageID string `json:",omitempty"`
}

func sandbox(t *testing.T, c *http.Client) []sandboxMsg {
	t.Helper()
	var out struct {
		Messages []struct {
			Channel   string `json:"channel"`
			Recipient string `json:"recipient"`
			MessageID string `json:"message_id"`
		} `json:"messages"`
	}
	b, _ := json.Marshal(jsonCall(t, c, "GET", "/v1/sandbox/messages", nil, 200))
	_ = json.Unmarshal(b, &out)
	msgs := make([]sandboxMsg, len(out.Messages))
	for i, m := range out.Messages {
		msgs[i] = sandboxMsg{m.Channel, m.Recipient, m.MessageID}
	}
	return msgs
}

// TestWhatsAppFallsBackToSMS runs a live journey through sandbox providers: a reachable contact gets
// WhatsApp, an unreachable one (number ending 0000) gets the SMS fallback instead, exactly once.
func TestWhatsAppFallsBackToSMS(t *testing.T) {
	c, _ := workspace(t, "Fallback")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "whatsapp", "provider": "sandbox_whatsapp",
		"config": map[string]string{"unreachable_suffix": "0000"}, "is_default": true}, 200)
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	wa := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "wa", "channel": "whatsapp",
		"content": map[string]any{"template_name": "promo", "body_params": []string{"{{.Attrs.first_name}}"}}}, 201)["id"]
	sms := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "sms", "channel": "sms",
		"content": map[string]any{"text": "Hi {{.Attrs.first_name}}, see https://x.test"}}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "promo", "rule": map[string]any{"attr": "promo", "op": "eq", "value": "yes"}}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "promo", "graph": map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": seg},
		"entry":   "wa",
		"nodes": map[string]any{
			"wa":   map[string]any{"type": "send_whatsapp", "template_id": wa, "fallback_template_id": sms, "next": "done"},
			"done": map[string]any{"type": "exit"},
		},
	}}, 201)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j["id"]), nil, 200)
	time.Sleep(3 * time.Second) // journey index refresh

	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": "on-wa", "name": "identify", "attrs": map[string]any{"promo": "yes", "first_name": "Asha", "phone": "+919811112222"}},
		map[string]any{"external_id": "no-wa", "name": "identify", "attrs": map[string]any{"promo": "yes", "first_name": "Ravi", "phone": "+919833330000"}},
	}}, 202)

	var msgs []sandboxMsg
	waitFor(t, 15*time.Second, func() bool { msgs = sandbox(t, c); return len(msgs) >= 2 })
	time.Sleep(2 * time.Second) // anything extra (a duplicate) would show up by now
	msgs = sandbox(t, c)
	got := map[string]string{}
	for _, m := range msgs {
		got[m.Recipient] += m.Channel + ","
	}
	if got["+919811112222"] != "whatsapp," {
		t.Errorf("reachable contact got %q, want only WhatsApp", got["+919811112222"])
	}
	if got["+919833330000"] != "sms," {
		t.Errorf("unreachable contact got %q, want only the SMS fallback", got["+919833330000"])
	}
	for _, m := range msgs {
		if m.Channel == "sms" && !strings.HasSuffix(m.MessageID, ":sms") {
			t.Errorf("fallback SMS id %q should derive from the WhatsApp message id", m.MessageID)
		}
	}
}

// TestOnsiteDecide checks what the web SDK receives for a known member and an anonymous visitor.
func TestOnsiteDecide(t *testing.T) {
	c, siteKey := workspace(t, "Onsite")
	vip := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "vip", "rule": map[string]any{"attr": "tier", "op": "eq", "value": "gold"}}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/dwc", map[string]any{"slot": "hero", "name": "Hero", "status": "active", "default_html": "<p>hello</p>",
		"variants": []any{map[string]any{"segment_id": vip, "html": "<p>gold</p>"}}}, 200)
	jsonCall(t, c, "POST", "/v1/focus-items", map[string]any{"name": "offer", "kind": "bar", "status": "active",
		"content":   map[string]any{"title": "10% off"},
		"targeting": map[string]any{"exclude_segment_ids": []any{vip}, "url_contains": []string{"/shop"}, "trigger": "load", "frequency": "session"}}, 200)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "goldie", "name": "identify", "attrs": map[string]any{"tier": "gold"}}, 202)
	waitFor(t, 10*time.Second, func() bool {
		segs, _ := jsonCall(t, c, "GET", "/v1/contacts/goldie", nil, -1)["segments"].([]any)
		return len(segs) == 1
	})

	decide := func(body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", apiURL+"/v1/web/decide", bytes.NewReader(b))
		req.Header.Set("X-Pulse-Site-Key", siteKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Error("decide must allow cross-origin calls from customer sites")
		}
		return resp.StatusCode, out
	}
	_, member := decide(map[string]any{"external_id": "goldie", "url": "https://shop.test/shop/1", "slots": []string{"hero"}})
	if member["content"].(map[string]any)["hero"] != "<p>gold</p>" || len(member["focus"].([]any)) != 0 {
		t.Errorf("member decision = %v, want gold hero and no (excluded) focus item", member)
	}
	_, anon := decide(map[string]any{"url": "https://shop.test/shop/1", "slots": []string{"hero"}})
	if anon["content"].(map[string]any)["hero"] != "<p>hello</p>" || len(anon["focus"].([]any)) != 1 {
		t.Errorf("anonymous decision = %v, want default hero and the offer", anon)
	}
	_, elsewhere := decide(map[string]any{"url": "https://shop.test/about", "slots": []string{}})
	if len(elsewhere["focus"].([]any)) != 0 {
		t.Errorf("offer shown off /shop: %v", elsewhere)
	}
	req, _ := http.NewRequest("POST", apiURL+"/v1/web/decide", strings.NewReader(`{}`))
	req.Header.Set("X-Pulse-Site-Key", "sk_wrong")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Errorf("wrong site key: %v %v", resp.StatusCode, err)
	}
}

// TestBroadcastScheduleAndPreview covers scheduled sends, journey schedule windows, provider tests and previews.
func TestBroadcastScheduleAndPreview(t *testing.T) {
	c, _ := workspace(t, "Broadcast")
	prov := jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)["id"]
	sms := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "sale", "channel": "sms", "content": map[string]any{"text": "Sale for {{.Attrs.first_name}} in {{.Attrs.city}}"}}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "pune", "rule": map[string]any{"attr": "city", "op": "eq", "value": "Pune"}}, 201)["id"]

	// A journey that only starts tomorrow must not enroll anyone today.
	tomorrow := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "later", "start_at": tomorrow, "graph": map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": seg}, "entry": "s",
		"nodes": map[string]any{"s": map[string]any{"type": "send_sms", "template_id": sms, "next": "x"}, "x": map[string]any{"type": "exit"}},
	}}, 201)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j["id"]), nil, 200)
	jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "bad", "start_at": tomorrow, "end_at": time.Now().UTC().Format(time.RFC3339),
		"graph": map[string]any{"trigger": map[string]any{"type": "event", "event": "x"}, "entry": "x", "nodes": map[string]any{"x": map[string]any{"type": "exit"}}}}, 400)
	time.Sleep(3 * time.Second)

	var evs []any
	for i, city := range []string{"Pune", "Pune", "Delhi"} {
		evs = append(evs, map[string]any{"external_id": fmt.Sprint("bc-", i), "name": "identify",
			"attrs": map[string]any{"city": city, "first_name": fmt.Sprint("N", i), "phone": fmt.Sprintf("+91980000100%d", i)}})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	waitFor(t, 10*time.Second, func() bool {
		s := jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%v", seg), nil, 200)
		return s["size"] == 2.0
	})

	// Preview with a real contact's attributes.
	pv := jsonCall(t, c, "POST", "/v1/templates/preview", map[string]any{"external_id": "bc-0",
		"template": map[string]any{"name": "x", "channel": "sms", "content": map[string]any{"text": "Hi {{.Attrs.first_name}} from {{.Attrs.city}}"}}}, 200)
	if pv["content"].(map[string]any)["text"] != "Hi N0 from Pune" {
		t.Errorf("preview = %v", pv)
	}

	// The provider's own connection test.
	if r := jsonCall(t, c, "POST", fmt.Sprintf("/v1/channels/providers/%v/test", prov), map[string]any{"to": "+919899990000"}, 200); r["ok"] != true {
		t.Errorf("provider test = %v", r)
	}

	// A one-time broadcast whose time has passed sends right away, to the segment's 2 members.
	b := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "pune sale", "channel": "sms", "template_id": sms,
		"segment_id": seg, "start_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "repeat": "none"}, 200)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", b["id"]), nil, 200)
	var got []sandboxMsg
	waitFor(t, 15*time.Second, func() bool {
		got = nil
		for _, m := range sandbox(t, c) {
			if strings.HasPrefix(m.MessageID, "bc") {
				got = append(got, m)
			}
		}
		return len(got) >= 2
	})
	time.Sleep(time.Second)
	bd := jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v", b["id"]), nil, 200)
	if bd["broadcast"].(map[string]any)["status"] != "completed" {
		t.Errorf("one-time broadcast status = %v, want completed", bd["broadcast"])
	}
	if runs := bd["runs"].([]any); len(runs) != 1 || runs[0].(map[string]any)["recipients"] != 2.0 {
		t.Errorf("runs = %v, want one run to 2 recipients", runs)
	}
	// Nothing came from the not-yet-started journey (only the provider test and the broadcast).
	for _, m := range sandbox(t, c) {
		if !strings.HasPrefix(m.MessageID, "bc") && !strings.HasPrefix(m.MessageID, "test-") {
			t.Errorf("unexpected message %+v: the journey starts tomorrow", m)
		}
	}
}
