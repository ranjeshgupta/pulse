//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestSendingSpeed (O6): a paced one-time campaign sends its first message at once and spreads the
// rest (720 an hour: one every 5 seconds), without counting them as held for quiet hours.
func TestSendingSpeed(t *testing.T) {
	c, _ := workspace(t, "Speed")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	var evs []any
	for i := range 3 {
		evs = append(evs, map[string]any{"external_id": fmt.Sprintf("sp%d-%s", i, run), "name": "identify",
			"attrs": map[string]any{"speed": run, "phone": fmt.Sprintf("+91981330%04d", i)}})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "speed", "rule": map[string]any{"attr": "speed", "op": "eq", "value": run}}, 201)["id"]
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "speed", "channel": "sms", "content": map[string]any{"text": "Hi"}}, 201)["id"]
	time.Sleep(3 * time.Second)
	base := map[string]any{"name": "Paced", "channel": "sms", "template_id": tpl, "segment_id": seg, "repeat": "none", "start_at": time.Now().UTC().Format(time.RFC3339)}
	base["send_per_hour"] = 10
	jsonCall(t, c, "POST", "/v1/broadcasts", base, 400)
	base["send_per_hour"] = 720
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", base, 200)["id"]
	start := time.Now()
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)
	sentAt := map[int]time.Duration{}
	waitFor(t, 60*time.Second, func() bool {
		for i := range 3 {
			if _, ok := sentAt[i]; !ok && len(smsTo(t, c, fmt.Sprintf("+91981330%04d", i))) > 0 {
				sentAt[i] = time.Since(start)
			}
		}
		return len(sentAt) == 3
	})
	var last time.Duration
	for _, d := range sentAt {
		last = max(last, d)
	}
	if last < 8*time.Second {
		t.Errorf("all three went out within %v: the speed wasn't applied (%v)", last, sentAt)
	}
	rep := jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil, 200)
	if d := rep["totals"].(map[string]any)["deferred"]; d != 0.0 {
		t.Errorf("paced messages counted as held: %v", d)
	}
}
