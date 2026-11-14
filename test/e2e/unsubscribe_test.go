//go:build e2e

package e2e

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestUnsubscribeTopicsAndPolicy: a workspace with topics, the one-click link mode and the
// "segment" email policy. A topic's one-click header leaves the topic only; its next email is
// skipped while other email still goes; an email unsubscribe doesn't stop email by itself; the
// Contacts list finds both.
func TestUnsubscribeTopicsAndPolicy(t *testing.T) {
	c, _ := workspace(t, "Topics")
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"unsubscribe": map[string]any{
		"link_mode": "one_click", "email_policy": "segment",
		"topics": []map[string]any{{"key": "offers", "name": "Offers", "description": "Deals and coupons"}, {"key": "email", "name": "x"}},
	}}, 400) // a topic can't be named after a channel
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"unsubscribe": map[string]any{
		"link_mode": "one_click", "email_policy": "segment",
		"topics": []map[string]any{{"key": "offers", "name": "Offers", "description": "Deals and coupons"}},
	}}, 200)
	if got := jsonCall(t, c, "GET", "/v1/unsubscribe-topics", nil, 200); got["email_policy"] != "segment" || len(asList(got["topics"])) != 1 {
		t.Fatalf("topics: %v", got)
	}

	run := fmt.Sprint(time.Now().UnixNano())
	ext, email := "topic-"+run, "topic-"+run+"@example.com"
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "deal", "channel": "email", "subject": "Deal " + run,
		"html": `<html><body><p>Deal</p></body></html>`}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "email": email}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	attrs := func() map[string]any {
		ct, _ := jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"].(map[string]any)
		a, _ := ct["attrs"].(map[string]any)
		return a
	}
	var search struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	mails := func() int {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+email), &search)
		return len(search.Messages)
	}
	send := func(topic string) {
		jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext, "topic": topic}, 202)
	}
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext, "topic": "nope"}, 400)

	send("offers")
	waitFor(t, 15*time.Second, func() bool { return mails() == 1 })
	var msg struct{ HTML string }
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID, &msg)
	m := regexp.MustCompile(`href="(` + regexp.QuoteMeta(apiURL+"/u/") + `[^"]+)"`).FindStringSubmatch(msg.HTML)
	if m == nil {
		t.Fatalf("no unsubscribe link:\n%s", msg.HTML)
	}
	unsub := html.UnescapeString(m[1])

	// One-click mode: the page posts itself (a script allowed by its nonce); fetching it changes nothing.
	page, err := http.Get(unsub)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	csp := page.Header.Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`nonce="([^"]+)"`).FindStringSubmatch(string(body))
	if !strings.Contains(string(body), "Offers emails") || nonce == nil || !strings.Contains(csp, "'nonce-"+nonce[1]+"'") {
		t.Errorf("one-click page: %s\nCSP %s", body, csp)
	}
	time.Sleep(2 * time.Second)
	if attrs()["optout_topic_offers"] == true {
		t.Fatal("opening the link unsubscribed them")
	}

	// The mailbox provider's one-click POST leaves the topic only.
	resp, err := http.Post(unsub, "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("one-click: %v %v", err, resp.StatusCode)
	}
	waitFor(t, 10*time.Second, func() bool { return attrs()["optout_topic_offers"] == true })
	if a := attrs(); a["optout_email"] == true || a["optout_all"] == true {
		t.Errorf("a topic unsubscribe touched email consent: %v", a)
	}

	// The topic's next email is skipped; email without the topic still goes.
	send("offers")
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "email_suppressed") })
	send("")
	waitFor(t, 15*time.Second, func() bool { return mails() == 2 })

	// Unsubscribing from email doesn't stop email in "segment" mode: segments leave them out.
	resp, err = http.PostForm(unsub, url.Values{"action": {"email"}})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("email unsubscribe: %v %v", err, resp.StatusCode)
	}
	waitFor(t, 10*time.Second, func() bool { return attrs()["optout_email"] == true })
	send("")
	waitFor(t, 15*time.Second, func() bool { return mails() == 3 })

	for _, q := range []string{"unsubscribed:offers", "unsubscribed:email"} {
		waitFor(t, 15*time.Second, func() bool { ids, _ := viewIDs(t, c, q); return slices.Contains(ids, ext) })
	}
	if ids, _ := viewIDs(t, c, "unsubscribed:all"); slices.Contains(ids, ext) {
		t.Errorf("unsubscribed:all lists %v", ids)
	}

	// The preference center lists the topic, off.
	pref, _ := http.Get(unsub + "?view=preferences")
	pb, _ := io.ReadAll(pref.Body)
	if !strings.Contains(string(pb), `name="topic_offers">`) || strings.Contains(string(pb), `name="topic_offers" checked`) {
		t.Errorf("preference center: %s", pb)
	}
}
