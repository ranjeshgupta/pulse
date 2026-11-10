//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// segmentPreview counts the contacts a rule matches right now.
func segmentPreview(t *testing.T, c *http.Client, rule map[string]any) float64 {
	t.Helper()
	n, _ := jsonCall(t, c, "POST", "/v1/segments/preview", map[string]any{"rule": rule}, 200)["count"].(float64)
	return n
}

// newSegment creates a segment and waits until its backfill is done and the streaming index has it.
func newSegment(t *testing.T, c *http.Client, name string, rule map[string]any) int64 {
	t.Helper()
	id := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": name, "rule": rule}, 201)["id"])
	waitFor(t, 15*time.Second, func() bool {
		return jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%d", id), nil, 200)["status"] == "ready"
	})
	time.Sleep(2500 * time.Millisecond) // the streaming index refresh
	return id
}

// TestDeviceSessionsAndDerivedAttributes: browser events carry the device and locale (as the web SDK
// sends them); Pulse keeps the last-seen device, the email domain and the date attributes, ignores
// attempts to set them directly, and segments filter on all of it, including browsing sessions.
func TestDeviceSessionsAndDerivedAttributes(t *testing.T) {
	c, site := workspace(t, "Devices")
	run := fmt.Sprint(time.Now().UnixNano())
	ext := "dev-" + run
	desktop := map[string]any{"platform": "web", "device_type": "desktop", "os": "Windows", "os_version": "11", "browser": "Chrome", "locale": "en-IN"}
	mobile := map[string]any{"platform": "web", "device_type": "mobile", "os": "Android", "os_version": "14", "browser": "Chrome",
		"device_brand": "Samsung", "device_model": "SM-S918B", "locale": "en-IN"}
	with := func(base map[string]any, extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	if code := webEvents(t, site,
		map[string]any{"external_id": ext, "name": "identify", "email": "Asha@Example.COM", "props": desktop,
			"attrs": map[string]any{"device_type": "hacked", "email_domain": "evil.test", "date_added": "1999-01-01", "first_name": "Asha"}},
		map[string]any{"external_id": ext, "name": "session_start", "props": with(desktop, map[string]any{"session_id": "s1"})},
		map[string]any{"external_id": ext, "name": "session_start", "props": with(mobile, map[string]any{"session_id": "s2"})},
		map[string]any{"external_id": ext, "name": "page_view", "props": with(mobile, map[string]any{"session_id": "s2", "url": "https://shop.test/pricing"})},
	); code != 202 {
		t.Fatalf("web events: %d", code)
	}
	var a map[string]any
	waitFor(t, 10*time.Second, func() bool { a = attrsOf(t, c, ext); return a["device_model"] == "SM-S918B" })
	for k, want := range map[string]any{"device_type": "mobile", "device_os": "Android", "device_os_version": "14", "device_brand": "Samsung",
		"browser": "Chrome", "locale": "en-IN", "email_domain": "example.com", "first_name": "Asha"} {
		if a[k] != want {
			t.Errorf("%s = %v, want %v", k, a[k], want)
		}
	}
	day := time.Now().UTC().Format(time.DateOnly)
	if s, _ := a["date_added"].(string); !strings.HasPrefix(s, day[:4]) {
		t.Errorf("date_added = %v (a browser can't set it)", a["date_added"])
	}
	if a["date_identified"] == nil || a["date_modified"] != day {
		t.Errorf("date_identified = %v, date_modified = %v", a["date_identified"], a["date_modified"])
	}

	// A desktop visit replaces the whole device: no Samsung model sticks to it.
	webEvents(t, site, map[string]any{"external_id": ext, "name": "page_view", "props": with(desktop, map[string]any{"url": "https://shop.test/"})})
	waitFor(t, 10*time.Second, func() bool { a = attrsOf(t, c, ext); return a["device_type"] == "desktop" })
	if a["device_model"] != nil || a["device_brand"] != nil || a["device_os"] != "Windows" {
		t.Errorf("after a desktop visit: model %v, brand %v, os %v", a["device_model"], a["device_brand"], a["device_os"])
	}

	// Segments on the new attributes and on sessions, live.
	seg := newSegment(t, c, "Mobile example.com regulars", map[string]any{"all": []any{
		map[string]any{"attr": "device_type", "op": "eq", "value": "mobile"},
		map[string]any{"attr": "email_domain", "op": "in", "value": []any{"example.com", "example.org"}},
		map[string]any{"event": "session_start", "op": "gte", "count": 3, "days": 30},
	}})
	if slices.Contains(segmentsOf(t, c, ext), seg) {
		t.Fatal("desktop with two sessions must not match yet")
	}
	webEvents(t, site, map[string]any{"external_id": ext, "name": "session_start", "props": with(mobile, map[string]any{"session_id": "s3"})})
	took := waitFor(t, 10*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, ext), seg) })
	t.Logf("third session on mobile entered the segment in %v", took)
	if n := segmentPreview(t, c, map[string]any{"attr": "date_added", "op": "within_days", "value": 1}); n != 1 {
		t.Errorf("date added in the last day = %v, want 1", n)
	}
	if n := segmentPreview(t, c, map[string]any{"attr": "email", "op": "ends_with", "value": "@Example.COM"}); n != 1 {
		t.Errorf("email ends with = %v, want 1", n)
	}
}

// TestOwnerAndManualUnsubscribe: a contact is assigned to a teammate and segments filter on the owner;
// an unsubscribe by the team is told apart from the contact's own.
func TestOwnerAndManualUnsubscribe(t *testing.T) {
	c, _ := workspace(t, "Owners")
	run := fmt.Sprint(time.Now().UnixNano())
	me := jsonCall(t, c, "GET", "/v1/auth/me", nil, 200)["user"].(map[string]any)
	uid := idOf(me["id"])
	owned, other := "own-"+run, "other-"+run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": owned, "name": "identify", "attrs": map[string]any{"owner_id": 999}},
		map[string]any{"external_id": other, "name": "identify", "attrs": map[string]any{"plan": "pro"}},
	}}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, other)["plan"] == "pro" })
	if attrsOf(t, c, owned)["owner_id"] != nil {
		t.Fatal("owner_id can't be set by attributes")
	}
	seg := newSegment(t, c, "Mine", map[string]any{"attr": "owner_id", "op": "eq", "value": fmt.Sprint(uid)})

	jsonCall(t, c, "PUT", "/v1/contacts/"+owned+"/owner", map[string]any{"user_id": 424242}, 400)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": owned, "name": "owner_changed", "props": map[string]any{"owner_id": uid}}, 400)
	jsonCall(t, c, "PUT", "/v1/contacts/"+owned+"/owner", map[string]any{"user_id": uid}, 202)
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, owned), seg) })
	if a := attrsOf(t, c, owned); a["owner_name"] == nil || a["owner_name"] == "" {
		t.Errorf("owner_name = %v", a["owner_name"])
	}
	if slices.Contains(segmentsOf(t, c, other), seg) {
		t.Error("an unowned contact is in the owner's segment")
	}
	jsonCall(t, c, "PUT", "/v1/contacts/"+owned+"/owner", map[string]any{"user_id": 0}, 202)
	waitFor(t, 10*time.Second, func() bool { return !slices.Contains(segmentsOf(t, c, owned), seg) })

	// The team unsubscribes one contact from SMS; the other replies STOP-like via a preference link
	// (simulated with its source). Only the first is "unsubscribed manually".
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": owned, "name": "unsubscribed", "props": map[string]any{"channel": "sms", "source": "pulse_app"}},
		map[string]any{"external_id": other, "name": "unsubscribed", "props": map[string]any{"channel": "sms", "source": "sms_keyword"}},
	}}, 202)
	manual := map[string]any{"all": []any{
		map[string]any{"attr": "optout_sms", "op": "eq", "value": true},
		map[string]any{"event": "unsubscribed", "op": "gte", "count": 1, "days": 365, "where": []any{
			map[string]any{"prop": "channel", "op": "eq", "value": "sms"}, map[string]any{"prop": "method", "op": "eq", "value": "manual"}}},
	}}
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, other)["optout_sms"] == true })
	waitFor(t, 10*time.Second, func() bool { return segmentPreview(t, c, manual) == 1 })
	seg2 := newSegment(t, c, "Unsubscribed manually: SMS", manual)
	if !slices.Contains(segmentsOf(t, c, owned), seg2) || slices.Contains(segmentsOf(t, c, other), seg2) {
		t.Errorf("manual unsubscribe segment: owned in %v, other in %v", segmentsOf(t, c, owned), segmentsOf(t, c, other))
	}
}

var shortLink = regexp.MustCompile(`https?://[^\s]+/t/s/[0-9A-Za-z]{11}`)

// TestSMSClickTrackingAndBounce: an SMS template with link tracking sends short links; following one
// redirects to the page and records sms_clicked, which a segment picks up. A number the provider
// rejects bounces: sms_failed with bounce, sms_bounced on the contact, and the next SMS is suppressed.
func TestSMSClickTrackingAndBounce(t *testing.T) {
	c, _ := workspace(t, "SMS clicks")
	run := fmt.Sprint(time.Now().UnixNano())
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms",
		"config": map[string]string{"unreachable_suffix": "0009"}, "is_default": true}, 200)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "sale", "channel": "sms",
		"content": map[string]any{"text": "Hi {{.Attrs.first_name}}, sale: https://shop.test/sale?c=1.", "track_links": true}}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "sms", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "go", "reentry": "always"}, "entry": "s",
		"nodes": map[string]any{"s": map[string]any{"type": "send_sms", "template_id": tpl, "next": "x"}, "x": map[string]any{"type": "exit"}},
	}}, 201)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j["id"]), nil, 200)
	clicker := newSegment(t, c, "Clicked an SMS", map[string]any{"event": "sms_clicked", "op": "gte", "count": 1, "days": 1})
	time.Sleep(time.Second)

	ok, bad := "ok-"+run, "bad-"+run
	okPhone, badPhone := "+9198"+run[len(run)-8:], "+91977"+run[len(run)-6:len(run)-1]+"0009"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ok, "name": "identify", "attrs": map[string]any{"first_name": "Asha", "phone": okPhone}},
		map[string]any{"external_id": bad, "name": "identify", "attrs": map[string]any{"first_name": "Ravi", "phone": badPhone}},
		map[string]any{"external_id": ok, "name": "go"},
		map[string]any{"external_id": bad, "name": "go"},
	}}, 202)

	var texts []string
	waitFor(t, 15*time.Second, func() bool { texts = smsTo(t, c, okPhone); return len(texts) > 0 })
	link := shortLink.FindString(texts[0])
	if link == "" || strings.Contains(texts[0], "shop.test") || !strings.HasSuffix(texts[0], link+".") {
		t.Fatalf("sms text %q: want the link replaced by a short link (punctuation kept)", texts[0])
	}
	noFollow := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(strings.Replace(link, "http://localhost:7080", apiURL, 1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://shop.test/sale?c=1" {
		t.Fatalf("short link: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if r2, err := noFollow.Get(apiURL + "/t/s/AAAAAAAAAA0"); err == nil {
		r2.Body.Close()
		if r2.StatusCode != http.StatusNotFound {
			t.Errorf("unknown short link: %d", r2.StatusCode)
		}
	}
	took := waitFor(t, 10*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, ok), clicker) })
	t.Logf("SMS click to segment in %v", took)

	// The rejected number bounced.
	waitFor(t, 15*time.Second, func() bool { return attrsOf(t, c, bad)["sms_bounced"] == true })
	// The event log and the ClickHouse profile mirror follow a moment later.
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(contactEvents(t, c, bad), "sms_failed") })
	waitFor(t, 10*time.Second, func() bool {
		return segmentPreview(t, c, map[string]any{"attr": "sms_bounced", "op": "eq", "value": true}) == 1
	})
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": bad, "name": "go"}, 202)
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(contactEvents(t, c, bad), "sms_suppressed") })
	// A new number clears the bounce.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": bad, "name": "identify", "attrs": map[string]any{"phone": okPhone[:len(okPhone)-1] + "7"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, bad)["sms_bounced"] == false })
}
