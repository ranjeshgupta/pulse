//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestBestTime (O6): best time can't be combined with local time or a sending speed; with no
// engagement history anywhere in the workspace, messages go at the start.
func TestBestTime(t *testing.T) {
	c, _ := workspace(t, "Best time")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	phone := "+9198155" + fmt.Sprintf("%05d", time.Now().UnixNano()%100000)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "bt-" + run, "name": "identify", "attrs": map[string]any{"bt": run, "phone": phone}}, 202)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "bt", "rule": map[string]any{"attr": "bt", "op": "eq", "value": run}}, 201)["id"]
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "bt", "channel": "sms", "content": map[string]any{"text": "Hi"}}, 201)["id"]
	time.Sleep(3 * time.Second)
	b := map[string]any{"name": "Best", "channel": "sms", "template_id": tpl, "segment_id": seg, "repeat": "none",
		"start_at": time.Now().UTC().Format(time.RFC3339), "best_time": true, "local_time": true}
	jsonCall(t, c, "POST", "/v1/broadcasts", b, 400)
	b["local_time"] = false
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", b, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)
	waitFor(t, 30*time.Second, func() bool { return len(smsTo(t, c, phone)) == 1 })
}
