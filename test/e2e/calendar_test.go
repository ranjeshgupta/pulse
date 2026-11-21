//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestCalendar (O6): a weekly campaign shows each of its runs in the period; drafts show at their
// planned time; a too-long period is refused.
func TestCalendar(t *testing.T) {
	c, _ := workspace(t, "Calendar")
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "cal", "channel": "sms", "content": map[string]any{"text": "Hi"}}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "cal", "rule": map[string]any{"attr": "cal", "op": "eq", "value": "x"}}, 201)["id"]
	start := time.Now().AddDate(0, 0, 1).UTC().Truncate(time.Hour)
	weekly := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Weekly digest", "channel": "sms", "template_id": tpl, "segment_id": seg,
		"repeat": "weekly", "start_at": start.Format(time.RFC3339)}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", weekly), nil, 200)
	jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Planned", "channel": "sms", "template_id": tpl, "segment_id": seg,
		"repeat": "none", "start_at": start.AddDate(0, 0, 2).Format(time.RFC3339)}, 200)
	from, to := start.Format("2006-01-02"), start.AddDate(0, 0, 27).Format("2006-01-02")
	items := jsonCall(t, c, "GET", "/v1/campaigns/calendar?from="+from+"&to="+to, nil, 200)["items"].([]any)
	runs := map[string]int{}
	for _, it := range items {
		m := it.(map[string]any)
		runs[m["name"].(string)] = len(m["sends"].([]any))
	}
	if runs["Weekly digest"] != 4 || runs["Planned"] != 1 {
		t.Errorf("calendar runs %v", runs)
	}
	jsonCall(t, c, "GET", "/v1/campaigns/calendar?from=2026-01-01&to=2026-12-31", nil, 400)
}
