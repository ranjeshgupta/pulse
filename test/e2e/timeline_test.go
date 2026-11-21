//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestCampaignTimeline (batch O5): a journey's timeline has entries and finishes, each step's
// Yes / No per day, the messages of one step, and the trigger segment's joins.
func TestCampaignTimeline(t *testing.T) {
	c, _ := workspace(t, "Timeline")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "tl", "channel": "sms", "content": map[string]any{"text": "Hi"}}, 201)["id"]
	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Timeline " + run, "rule": map[string]any{"attr": "tl", "op": "eq", "value": run}}, 201)["id"])
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "timeline", "graph": map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": seg},
		"entry":   "chk",
		"nodes": map[string]any{
			"chk": map[string]any{"type": "check", "rule": map[string]any{"attr": "city", "op": "eq", "value": "Pune"}, "yes": "sms", "no": "x"},
			"sms": map[string]any{"type": "send_sms", "template_id": tpl, "next": "x"},
			"x":   map[string]any{"type": "exit"},
		},
	}}, 201)
	jpath := fmt.Sprintf("/v1/journeys/%v", j["id"])
	jsonCall(t, c, "POST", jpath+"/activate", nil, 200)
	time.Sleep(3 * time.Second)
	for i, city := range []string{"Pune", "Goa"} {
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": fmt.Sprintf("tl%d-%s", i, run), "name": "identify",
			"attrs": map[string]any{"tl": run, "city": city, "phone": fmt.Sprintf("+91981220%04d", i)}}, 202)
	}
	count := func(list []any, kind string) float64 {
		var n float64
		for _, b := range list {
			if m := b.(map[string]any); m["kind"] == kind {
				n += m["count"].(float64)
			}
		}
		return n
	}
	var tl map[string]any
	took := waitFor(t, 40*time.Second, func() bool {
		tl = jsonCall(t, c, "GET", jpath+"/timeline", nil, 200)
		steps, _ := tl["steps"].(map[string]any)
		chk, _ := steps["chk"].([]any)
		life, _ := tl["timeline"].(map[string]any)["lifecycle"].([]any)
		return count(chk, "yes") == 1 && count(chk, "no") == 1 && count(life, "entered") == 2
	})
	t.Logf("timeline complete after %v", took)
	if seg, _ := tl["timeline"].(map[string]any)["segment"].([]any); count(seg, "joined") != 2 {
		t.Errorf("segment joins %v", seg)
	}
	step := jsonCall(t, c, "GET", jpath+"/timeline?step=sms&bucket=hour&from="+time.Now().AddDate(0, 0, -1).Format("2006-01-02"), nil, 200)
	if msgs, _ := step["timeline"].(map[string]any)["messages"].([]any); count(msgs, "sent") != 1 {
		t.Errorf("step messages %v", msgs)
	}
	jsonCall(t, c, "GET", jpath+"/timeline?from=2025-01-01", nil, 400) // over 120 days
	jsonCall(t, c, "GET", jpath+"/timeline?step=bad%20id", nil, 400)
}
