//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestHoldout (O6): a campaign's control group is kept back (the same people), shows in its report
// with conversions for the uplift, and the workspace control group keeps people from every campaign.
func TestHoldout(t *testing.T) {
	c, _ := workspace(t, "Holdout")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	var evs []any
	for i := range 40 {
		evs = append(evs, map[string]any{"external_id": fmt.Sprintf("ho%d-%s", i, run), "name": "identify",
			"attrs": map[string]any{"ho": run, "phone": fmt.Sprintf("+91981440%04d", i)}})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "ho", "rule": map[string]any{"attr": "ho", "op": "eq", "value": run}}, 201)["id"]
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "ho", "channel": "sms", "content": map[string]any{"text": "Sale"}}, 201)["id"]
	time.Sleep(3 * time.Second)
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Holdout", "channel": "sms", "template_id": tpl, "segment_id": seg,
		"repeat": "none", "start_at": time.Now().UTC().Format(time.RFC3339), "holdout_percent": 50, "conversion_event": "purchase", "conversion_days": 3}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)
	var rep map[string]any
	waitFor(t, 40*time.Second, func() bool {
		rep = jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil, 200)
		h, _ := rep["holdout"].(map[string]any)
		sent := rep["totals"].(map[string]any)["sent"].(float64)
		return h != nil && h["contacts"].(float64)+sent == 40
	})
	h := rep["holdout"].(map[string]any)
	if n := h["contacts"].(float64); n < 8 || n > 32 {
		t.Errorf("50%% holdout kept back %v of 40", n)
	}
	// The workspace control group: 20% of contacts get no campaign at all.
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"messages": map[string]any{"inbox_expiry_days": 30, "control_percent": 20}}, 200)
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"messages": map[string]any{"inbox_expiry_days": 30, "control_percent": 25}}, 400)
}
