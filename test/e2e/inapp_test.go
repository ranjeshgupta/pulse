//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

func webCall(t *testing.T, method, path, siteKey string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, apiURL+path, rd)
	req.Header.Set("X-Pulse-Site-Key", siteKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestAppInbox: a broadcast to the inbox channel stores a message per contact; the SDK API lists
// it and marks it read, and the read counts in the broadcast's report.
func TestAppInbox(t *testing.T) {
	c, siteKey := workspace(t, "Inbox")
	run := fmt.Sprint(time.Now().UnixNano())
	ext := "inb-" + run
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "welcome", "channel": "inbox",
		"content": map[string]any{"title": "Hi {{.Attrs.first_name}}", "body": "Your welcome offer is here", "url": "app://offers", "category": "offers", "expires_days": 7}}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "bad", "channel": "inbox", "content": map[string]any{"title": "x"}}, 400)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "cohort", "rule": map[string]any{"attr": "cohort", "op": "eq", "value": run}}, 201)["id"]
	time.Sleep(3 * time.Second)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"first_name": "Mira", "cohort": run}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", fmt.Sprint("/v1/segments/", seg), nil, 200)["size"] == 1.0 })

	b := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "inbox hello", "channel": "inbox", "template_id": tpl, "segment_id": seg,
		"start_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "repeat": "none"}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprint("/v1/broadcasts/", b, "/schedule"), nil, 200)

	var inbox map[string]any
	took := waitFor(t, 15*time.Second, func() bool {
		_, inbox = webCall(t, "GET", "/v1/web/inbox?id="+url.QueryEscape(ext), siteKey, nil)
		return len(asList(inbox["messages"])) == 1
	})
	t.Logf("broadcast -> app inbox in %v", took)
	m := asList(inbox["messages"])[0].(map[string]any)
	if m["title"] != "Hi Mira" || m["category"] != "offers" || inbox["unread"] != 1.0 || m["read_at"] != nil {
		t.Fatalf("inbox = %v", inbox)
	}
	if code, _ := webCall(t, "POST", "/v1/web/inbox", siteKey, map[string]any{"id": ext, "action": "read"}); code != 200 {
		t.Fatalf("mark read: %d", code)
	}
	webCall(t, "POST", "/v1/web/inbox", siteKey, map[string]any{"id": ext, "action": "click", "message_ids": []any{m["id"]}})
	_, inbox = webCall(t, "GET", "/v1/web/inbox?id="+url.QueryEscape(ext), siteKey, nil)
	if inbox["unread"] != 0.0 {
		t.Errorf("unread after reading = %v", inbox["unread"])
	}
	waitFor(t, 15*time.Second, func() bool {
		tot, _ := jsonCall(t, c, "GET", fmt.Sprint("/v1/broadcasts/", b, "/report"), nil, 200)["totals"].(map[string]any)
		return tot["sent"] == 1.0 && tot["opened"] == 1.0 && tot["clicked"] == 1.0
	})
	if evs := contactEvents(t, c, ext); !slices.Contains(evs, "inbox_opened") || !slices.Contains(evs, "inbox_clicked") {
		t.Errorf("events = %v", evs)
	}
	// Deleting empties it; the admin view agrees.
	webCall(t, "POST", "/v1/web/inbox", siteKey, map[string]any{"id": ext, "action": "delete"})
	if n := len(asList(jsonCall(t, c, "GET", "/v1/contacts/"+ext+"/inbox", nil, 200)["messages"])); n != 0 {
		t.Errorf("admin inbox after delete has %d messages", n)
	}

	// With identity verification on, a known user's inbox needs their hash.
	jsonCall(t, c, "PUT", "/v1/settings/identity", map[string]any{"required": true}, 200)
	if code, _ := webCall(t, "GET", "/v1/web/inbox?id="+url.QueryEscape(ext), siteKey, nil); code != 403 {
		t.Errorf("inbox without user_hash: %d, want 403", code)
	}
}
