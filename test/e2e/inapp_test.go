//go:build e2e

package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
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
	secret := jsonCall(t, c, "GET", "/v1/settings/identity", nil, 200)["secret"].(string)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ext))
	hash := hex.EncodeToString(mac.Sum(nil))
	q := "/v1/web/inbox?id=" + url.QueryEscape(ext) + "&user_hash=" + hash
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
		_, inbox = webCall(t, "GET", q, siteKey, nil)
		return len(asList(inbox["messages"])) == 1
	})
	t.Logf("broadcast -> app inbox in %v", took)
	m := asList(inbox["messages"])[0].(map[string]any)
	if m["title"] != "Hi Mira" || m["category"] != "offers" || inbox["unread"] != 1.0 || m["read_at"] != nil {
		t.Fatalf("inbox = %v", inbox)
	}
	if code, _ := webCall(t, "POST", "/v1/web/inbox", siteKey, map[string]any{"id": ext, "action": "read"}); code != 403 {
		t.Errorf("reading a known user's inbox without user_hash: %d, want 403", code)
	}
	if code, _ := webCall(t, "POST", "/v1/web/inbox", siteKey, map[string]any{"id": ext, "user_hash": hash, "action": "read"}); code != 200 {
		t.Fatalf("mark read: %d", code)
	}
	webCall(t, "POST", "/v1/web/inbox", siteKey, map[string]any{"id": ext, "user_hash": hash, "action": "click", "message_ids": []any{m["id"]}})
	_, inbox = webCall(t, "GET", q, siteKey, nil)
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
	webCall(t, "POST", "/v1/web/inbox", siteKey, map[string]any{"id": ext, "user_hash": hash, "action": "delete"})
	if n := len(asList(jsonCall(t, c, "GET", "/v1/contacts/"+ext+"/inbox", nil, 200)["messages"])); n != 0 {
		t.Errorf("admin inbox after delete has %d messages", n)
	}

	// Even with identity verification off for events, a known user's inbox needs their hash.
	if code, _ := webCall(t, "GET", "/v1/web/inbox?id="+url.QueryEscape(ext), siteKey, nil); code != 403 {
		t.Errorf("inbox without user_hash: %d, want 403", code)
	}
}

// TestInAppCampaigns: the SDK gets only the active campaigns its visitor's segments and platform match.
func TestInAppCampaigns(t *testing.T) {
	c, siteKey := workspace(t, "InApp")
	run := fmt.Sprint(time.Now().UnixNano())
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "vips", "rule": map[string]any{"attr": "tier", "op": "eq", "value": "vip"}}, 201)["id"]
	time.Sleep(3 * time.Second)
	vip, other := "ia-vip-"+run, "ia-other-"+run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": vip, "name": "identify", "attrs": map[string]any{"tier": "vip"}},
		map[string]any{"external_id": other, "name": "identify", "attrs": map[string]any{"tier": "basic"}},
	}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", fmt.Sprint("/v1/segments/", seg), nil, 200)["size"] == 1.0 })
	campaign := map[string]any{"name": "VIP welcome", "layout": "modal", "status": "active",
		"content":   map[string]any{"title": "Welcome back", "body": "Your VIP gift", "buttons": []any{map[string]any{"text": "Open", "action": "url", "value": "myapp://gift"}}},
		"targeting": map[string]any{"segment_ids": []any{seg}, "platforms": []any{"android", "ios"}, "trigger_event": "app_open", "frequency": "once"}}
	id := jsonCall(t, c, "POST", "/v1/inapp", campaign, 200)["id"]
	campaign["name"], campaign["status"] = "Draft", "draft"
	jsonCall(t, c, "POST", "/v1/inapp", campaign, 200)
	jsonCall(t, c, "POST", "/v1/inapp", map[string]any{"name": "bad", "layout": "modal", "content": map[string]any{"title": "x"},
		"targeting": map[string]any{"trigger_event": "app_open", "frequency": "once", "segment_ids": []any{999999}}}, 400)

	secret := jsonCall(t, c, "GET", "/v1/settings/identity", nil, 200)["secret"].(string)
	hashOf := func(id string) string {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(id))
		return hex.EncodeToString(m.Sum(nil))
	}
	count := func(who, platform string) int {
		_, out := webCall(t, "POST", "/v1/web/inapp", siteKey, map[string]any{"id": who, "user_hash": hashOf(who), "platform": platform})
		return len(asList(out["messages"]))
	}
	// Without the user hash a signed-in user is treated as anonymous: segment campaigns stay hidden.
	if _, out := webCall(t, "POST", "/v1/web/inapp", siteKey, map[string]any{"id": vip, "platform": "android"}); len(asList(out["messages"])) != 0 {
		t.Error("segment campaigns were shown without the user hash")
	}
	if n := count(vip, "android"); n != 1 {
		t.Errorf("VIP on android sees %d campaigns, want 1", n)
	}
	if code, _ := webCall(t, "POST", "/v1/web/inapp", siteKey, map[string]any{"id": vip, "platform": "web"}); code != 400 {
		t.Errorf("web platform: %d, want 400 (websites use focus items)", code)
	}
	if n := count(other, "android"); n != 0 {
		t.Errorf("non-VIP sees %d, want 0", n)
	}
	// The admin check needs no user hash; a test send reaches one contact once, even as a draft.
	if n := len(asList(jsonCall(t, c, "GET", "/v1/inapp/check?platform=android&external_id="+other, nil, 200)["messages"])); n != 0 {
		t.Errorf("check for non-VIP = %d", n)
	}
	if n := len(asList(jsonCall(t, c, "GET", "/v1/inapp/check?platform=android&external_id="+vip, nil, 200)["messages"])); n != 1 {
		t.Errorf("check for VIP = %d", n)
	}
	drafts := asList(jsonCall(t, c, "GET", "/v1/inapp", nil, 200)["messages"])
	var draftID any
	for _, d := range drafts {
		if d.(map[string]any)["status"] == "draft" {
			draftID = d.(map[string]any)["id"]
		}
	}
	jsonCall(t, c, "POST", fmt.Sprint("/v1/inapp/", draftID, "/test"), map[string]any{"external_id": other}, 200)
	if n := count(other, "android"); n != 1 {
		t.Errorf("after a test send the contact gets %d campaigns, want the draft", n)
	}
	if n := count(other, "android"); n != 0 {
		t.Errorf("a test send shows once; got %d on the second fetch", n)
	}

	// The SDK reports what happened; the list shows it.
	webEvents(t, siteKey, map[string]any{"external_id": vip, "name": "inapp_shown", "props": map[string]any{"inapp_id": id}},
		map[string]any{"external_id": vip, "name": "inapp_clicked", "props": map[string]any{"inapp_id": id, "button": "Open"}})
	waitFor(t, 15*time.Second, func() bool {
		st, _ := jsonCall(t, c, "GET", "/v1/inapp", nil, 200)["stats"].(map[string]any)
		s, _ := st[fmt.Sprint(id)].(map[string]any)
		return s["shown"] == 1.0 && s["clicked"] == 1.0
	})
}

// TestMessageDecision: a journey branches on whether its own email was opened.
func TestMessageDecision(t *testing.T) {
	c, _ := workspace(t, "Decision")
	run := fmt.Sprint(time.Now().UnixNano())
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "hello", "channel": "email", "subject": "Decision " + run, "html": "<p>Hi</p>"}, 201)["id"]
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "opened?", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "signup"}, "entry": "mail",
		"nodes": map[string]any{
			"mail":   map[string]any{"type": "send_email", "template_id": tpl, "next": "opened"},
			"opened": map[string]any{"type": "message_decision", "step": "mail", "outcome": "opened", "seconds": 86400, "yes": "tag", "no": "x"},
			"tag":    map[string]any{"type": "update_tags", "add_tags": []any{"engaged"}, "next": "x"},
			"x":      map[string]any{"type": "exit"},
		}}}, 201)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j), nil, 200)
	time.Sleep(3 * time.Second) // the journey index refreshes every 2 seconds
	ext, other := "md-"+run, "md2-"+run
	for _, e := range []string{ext, other} {
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
			map[string]any{"external_id": e, "name": "identify", "email": e + "@example.com"},
			map[string]any{"external_id": e, "name": "signup"},
		}}, 202)
	}
	var search struct {
		Messages []struct {
			ID string
			To []struct{ Address string }
		} `json:"messages"`
	}
	waitFor(t, 15*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("subject:\"Decision "+run+"\""), &search)
		return len(search.Messages) == 2
	})
	// The other contact opens an unrelated email event: it must not count for this journey.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": other, "name": "email_opened", "props": map[string]any{"message_id": "999:mail"}}, 202)
	for _, m := range search.Messages {
		if m.To[0].Address != ext+"@example.com" {
			continue
		}
		var msg struct{ HTML string }
		mailpitGet(t, "/api/v1/message/"+m.ID, &msg)
		px := regexp.MustCompile(`src="(` + regexp.QuoteMeta(apiURL) + `/t/o/[^"]+)"`).FindStringSubmatch(msg.HTML)
		if px == nil {
			t.Fatal("no open pixel")
		}
		resp, err := http.Get(html.UnescapeString(px[1]))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	took := waitFor(t, 15*time.Second, func() bool { return fmt.Sprint(attrsOf(t, c, ext)["tags"]) == "[engaged]" })
	t.Logf("email opened -> yes branch in %v", took)
	time.Sleep(2 * time.Second)
	if attrsOf(t, c, other)["tags"] != nil {
		t.Error("an open of a different message took the yes branch")
	}
}
