//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestRSSArchive (O6): an RSS-to-email campaign sends what's new in a feed as {{block.feed}} (the
// feed is served from the test container), and its send shows on the workspace's public archive
// page, its own page and the archive's RSS feed.
func TestRSSArchive(t *testing.T) {
	host := env("PULSE_HOOK_HOST", "")
	if host == "" {
		t.Skip("set PULSE_HOOK_HOST: run inside the compose network (make e2e)")
	}
	run := fmt.Sprint(time.Now().UnixNano())
	pub := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC1123Z)
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC1123Z)
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Blog %s</title>
<item><title>Fresh post %s</title><link>https://blog.example.com/fresh</link><description>New things</description><pubDate>%s</pubDate></item>
<item><title>Stale post %s</title><link>https://blog.example.com/stale</link><pubDate>%s</pubDate></item></channel></rss>`, run, run, pub, run, old)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	feedURL := fmt.Sprintf("http://%s:%d/feed.xml", host, ln.Addr().(*net.TCPAddr).Port)

	c, _ := workspace(t, "Archive")
	email := "rss-" + run + "@example.com"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "rss-" + run, "name": "identify", "email": email, "attrs": map[string]any{"rss": run}}, 202)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "rss", "rule": map[string]any{"attr": "rss", "op": "eq", "value": run}}, 201)["id"]
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "digest", "channel": "email", "subject": "Digest " + run,
		"html": `<html><body><h1>This week</h1>{{block.feed}}</body></html>`}, 201)["id"]
	prev := jsonCall(t, c, "POST", "/v1/broadcasts/feed-preview", map[string]any{"url": feedURL, "repeat": "weekly"}, 200)
	if prev["total"].(float64) != 2 || len(asList(prev["items"])) != 1 {
		t.Fatalf("preview: %v", prev)
	}
	jsonCall(t, c, "POST", "/v1/broadcasts/feed-preview", map[string]any{"url": "ftp://x"}, 400)
	time.Sleep(3 * time.Second)
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Weekly digest", "channel": "email", "template_id": tpl, "segment_id": seg,
		"repeat": "weekly", "start_at": time.Now().Add(10 * time.Second).UTC().Format(time.RFC3339), "feed_url": feedURL, "archive_public": true}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)

	var search struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	waitFor(t, 60*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+email), &search)
		return len(search.Messages) == 1
	})
	var msg struct{ HTML string }
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID, &msg)
	if !strings.Contains(msg.HTML, "Fresh post "+run) || strings.Contains(msg.HTML, "Stale post") {
		t.Errorf("email feed content:\n%s", msg.HTML)
	}

	pages, _ := url.Parse(jsonCall(t, c, "GET", "/v1/landing-pages", nil, 200)["base_url"].(string))
	base := apiURL + pages.Path // the public address may not resolve inside the test container
	get := func(u string) (int, string, http.Header) {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	code, index, h := get(base + "archive")
	if code != 200 || !strings.Contains(index, "Digest "+run) || !strings.Contains(h.Get("Content-Security-Policy"), "nonce-") {
		t.Fatalf("archive index %d:\n%s", code, index)
	}
	i := strings.Index(index, `href="archive/`)
	id := index[i+len(`href="archive/`):]
	id = id[:strings.IndexByte(id, '"')]
	if code, page, _ := get(base + "archive/" + id); code != 200 || !strings.Contains(page, "Fresh post "+run) {
		t.Errorf("archive page %d:\n%s", code, page)
	}
	if code, rss, h := get(base + "archive.rss"); code != 200 || !strings.Contains(rss, "<title>Digest "+run+"</title>") || !strings.Contains(h.Get("Content-Type"), "rss") {
		t.Errorf("archive rss %d:\n%s", code, rss)
	}
	if code, _, _ := get(base + "archive/999999999"); code != 404 {
		t.Errorf("unknown archive page: %d", code)
	}
}
