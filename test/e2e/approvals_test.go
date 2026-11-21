//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
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

	// Changing the message's template after asking blocks it too.
	req = idOf(jsonCall(t, c, "POST", "/v1/approvals", map[string]any{"kind": "broadcast", "campaign_id": bc}, 200)["id"])
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/templates/", tpl), map[string]any{"name": "ap", "channel": "sms", "content": map[string]any{"text": "Hi, changed"}}, 200)
	call(t, "POST", fmt.Sprintf("/v1/approvals/%d/approve", req), key, nil, 409)

	req = idOf(jsonCall(t, c, "POST", "/v1/approvals", map[string]any{"kind": "broadcast", "campaign_id": bc}, 200)["id"])
	if r := call(t, "POST", fmt.Sprintf("/v1/approvals/%d/approve", req), key, nil, 200); r["status"] != "scheduled" {
		t.Errorf("approve: %v", r)
	}
	latest := jsonCall(t, c, "GET", fmt.Sprintf("/v1/approvals/latest?kind=broadcast&id=%v", bc), nil, 200)
	if a := latest["approval"].(map[string]any); a["status"] != "approved" || a["decider"] != "API key" {
		t.Errorf("latest: %v", latest)
	}
	// Approved and scheduled: its content can't change behind the approval, through the template
	// or a content block it uses; a template no live campaign sends can.
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/templates/", tpl), map[string]any{"name": "ap", "channel": "sms", "content": map[string]any{"text": "Hi, sneaky"}}, 409)
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/templates/", tpl), map[string]any{"name": "ap", "channel": "sms", "content": map[string]any{"text": "Hi {{block.ap_sig}}"}}, 409)
	other := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "free", "channel": "sms", "content": map[string]any{"text": "x"}}, 201)["id"]
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/templates/", other), map[string]any{"name": "free", "channel": "sms", "content": map[string]any{"text": "y"}}, 200)
}

// TestApprovalsBlockKeys: a live template that names a block not yet created can't be filled by
// creating the block, or by renaming another block to that key, behind the approval.
func TestApprovalsBlockKeys(t *testing.T) {
	c, _ := workspace(t, "Approval blocks")
	key := jsonCall(t, c, "POST", "/v1/settings/api-key", nil, 200)["api_key"].(string)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "ab", "channel": "sms", "content": map[string]any{"text": "Hi {{block.promo}}"}}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "ab", "rule": map[string]any{"attr": "ab", "op": "eq", "value": "x"}}, 201)["id"]
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Promo", "channel": "sms", "template_id": tpl, "segment_id": seg, "repeat": "none",
		"start_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}, 200)["id"]
	other := jsonCall(t, c, "POST", "/v1/content-blocks", map[string]any{"key": "spare", "name": "Spare", "html": "<p>x</p>", "text": "x"}, -1)["id"]
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"approvals": map[string]any{"four_eyes": true}}, 200)
	req := idOf(jsonCall(t, c, "POST", "/v1/approvals", map[string]any{"kind": "broadcast", "campaign_id": bc}, 200)["id"])
	call(t, "POST", fmt.Sprintf("/v1/approvals/%d/approve", req), key, nil, 200)

	jsonCall(t, c, "POST", "/v1/content-blocks", map[string]any{"key": "promo", "name": "Promo", "html": "<p>50% off</p>", "text": "50% off"}, 409)
	jsonCall(t, c, "POST", "/v1/content-blocks", map[string]any{"key": " promo ", "name": "Promo", "html": "<p>50% off</p>", "text": "50% off"}, 409) // saved trimmed
	// Text after the JSON: the guard reads the body exactly as saving does, so it's still refused.
	raw, _ := http.NewRequest("POST", apiURL+"/v1/content-blocks", strings.NewReader(`{"key":"promo","name":"Promo","html":"<p>x</p>","text":"x"} trailing`))
	raw.Header.Set("Content-Type", "application/json")
	raw.Header.Set("X-Pulse-CSRF", "1")
	if resp, err := c.Do(raw); err != nil || (resp.StatusCode != 409 && resp.StatusCode != 400) {
		t.Errorf("block with trailing data: %v %v", err, resp.StatusCode)
	}
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/content-blocks/", other), map[string]any{"key": "promo", "name": "Spare", "html": "<p>x</p>", "text": "x"}, 409)
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/content-blocks/", other), map[string]any{"key": "spare", "name": "Spare 2", "html": "<p>y</p>", "text": "y"}, 200)
}
