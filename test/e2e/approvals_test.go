//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestApprovals (O6): with four-eyes on, a user can't launch directly, can't approve their own
// request, and an approval by someone else (here the workspace's API key) launches the campaign;
// a campaign changed after the request isn't launched by approving it.
func TestApprovals(t *testing.T) {
	c, _ := workspace(t, "Approvals")
	key := jsonCall(t, c, "POST", "/v1/settings/api-key", nil, 200)["api_key"].(string)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "ap", "channel": "sms", "content": map[string]any{"text": "Hi"}}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "ap", "rule": map[string]any{"attr": "ap", "op": "eq", "value": "x"}}, 201)["id"]
	b := map[string]any{"name": "Launch me", "channel": "sms", "template_id": tpl, "segment_id": seg, "repeat": "none",
		"start_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", b, 200)["id"]
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"approvals": map[string]any{"four_eyes": true}}, 200)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 409)

	req := idOf(jsonCall(t, c, "POST", "/v1/approvals", map[string]any{"kind": "broadcast", "campaign_id": bc, "note": "Diwali send"}, 200)["id"])
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/approvals/%d/approve", req), nil, 403) // not your own
	list := jsonCall(t, c, "GET", "/v1/approvals?status=pending", nil, 200)["approvals"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["campaign_name"] != "Launch me" {
		t.Fatalf("pending: %v", list)
	}
	// A change after the request: approving refuses to launch it.
	b["name"] = "Launch me (v2)"
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/broadcasts/%v", bc), b, 200)
	call(t, "POST", fmt.Sprintf("/v1/approvals/%d/approve", req), key, nil, 409)

	req = idOf(jsonCall(t, c, "POST", "/v1/approvals", map[string]any{"kind": "broadcast", "campaign_id": bc}, 200)["id"])
	if r := call(t, "POST", fmt.Sprintf("/v1/approvals/%d/approve", req), key, nil, 200); r["status"] != "scheduled" {
		t.Errorf("approve: %v", r)
	}
	latest := jsonCall(t, c, "GET", fmt.Sprintf("/v1/approvals/latest?kind=broadcast&id=%v", bc), nil, 200)
	if a := latest["approval"].(map[string]any); a["status"] != "approved" || a["decider"] != "API key" {
		t.Errorf("latest: %v", latest)
	}
}
