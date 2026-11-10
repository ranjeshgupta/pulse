//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
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

func mailpitGet(t *testing.T, path string, out any) {
	t.Helper()
	resp, err := http.Get(mailpitURL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(resp.Body).Decode(out)
}

func contactEvents(t *testing.T, c *http.Client, ext string) []string {
	var out struct {
		Events []struct{ Name string } `json:"events"`
	}
	b, _ := json.Marshal(jsonCall(t, c, "GET", "/v1/contacts/"+url.PathEscape(ext)+"/events", nil, -1))
	_ = json.Unmarshal(b, &out)
	var names []string
	for _, e := range out.Events {
		names = append(names, e.Name)
	}
	return names
}

// TestEmailTrackingAndUnsubscribe follows a real email through Mailpit: headers, tracked links, the
// open pixel, one-click unsubscribe, and suppression of the next send.
func TestEmailTrackingAndUnsubscribe(t *testing.T) {
	c, siteKey := workspace(t, "Tracking")
	run := fmt.Sprint(time.Now().UnixNano())
	ext, email := "p0-"+run, "p0-"+run+"@example.com"
	const dest = "https://shop.test/sale?x=1&y=2"
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "promo", "channel": "email", "subject": "Hi {{.Attrs.first_name}}",
		"html": `<html><body><p>Big sale</p><a href="` + html.EscapeString(dest) + `">Shop now</a></body></html>`}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "email": email, "attrs": map[string]any{"first_name": "Asha"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })

	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	var search struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	waitFor(t, 15*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+email), &search)
		return len(search.Messages) == 1
	})
	id := search.Messages[0].ID

	var headers map[string][]string
	mailpitGet(t, "/api/v1/message/"+id+"/headers", &headers)
	if !strings.HasPrefix(strings.Join(headers["List-Unsubscribe"], ""), "<"+apiURL+"/u/") || strings.Join(headers["List-Unsubscribe-Post"], "") != "List-Unsubscribe=One-Click" {
		t.Errorf("unsubscribe headers = %v / %v", headers["List-Unsubscribe"], headers["List-Unsubscribe-Post"])
	}
	var msg struct{ HTML string }
	mailpitGet(t, "/api/v1/message/"+id, &msg)
	link := func(prefix string) string {
		m := regexp.MustCompile(`(?:href|src)="(` + regexp.QuoteMeta(apiURL+prefix) + `[^"]+)"`).FindStringSubmatch(msg.HTML)
		if m == nil {
			t.Fatalf("no %s link in the email:\n%s", prefix, msg.HTML)
		}
		return html.UnescapeString(m[1])
	}
	click, open, unsub := link("/t/c/"), link("/t/o/"), link("/u/")

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(click)
	if err != nil || resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != dest {
		t.Fatalf("click redirect: %v %v -> %q, want %q", err, resp.StatusCode, resp.Header.Get("Location"), dest)
	}
	tampered := strings.Replace(click, url.QueryEscape(dest), url.QueryEscape("https://evil.test/"), 1)
	if resp, _ := noRedirect.Get(tampered); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("tampered destination: %d, want 400 (no open redirect)", resp.StatusCode)
	}
	if resp, _ := http.Get(open); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/gif" {
		t.Errorf("open pixel: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	waitFor(t, 10*time.Second, func() bool {
		evs := contactEvents(t, c, ext)
		return slices.Contains(evs, "email_opened") && slices.Contains(evs, "email_clicked")
	})

	// The unsubscribe page asks first (mail scanners open links); the one-click POST unsubscribes.
	page, _ := http.Get(unsub)
	body, _ := io.ReadAll(page.Body)
	if !strings.Contains(string(body), "Unsubscribe from") {
		t.Errorf("unsubscribe page: %s", body)
	}
	resp, err = http.Post(unsub, "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("one-click unsubscribe: %v %v", err, resp.StatusCode)
	}
	waitFor(t, 10*time.Second, func() bool {
		ct, _ := jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"].(map[string]any)
		attrs, _ := ct["attrs"].(map[string]any)
		return attrs["optout_email"] == true
	})

	// A browser can't opt them back in, but can still update ordinary attributes.
	webPost := func(body string) int {
		req, _ := http.NewRequest("POST", apiURL+"/v1/web/events", strings.NewReader(body))
		req.Header.Set("X-Pulse-Site-Key", siteKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if code := webPost(`{"events":[{"external_id":"` + ext + `","name":"resubscribed","props":{"channel":"email"}}]}`); code != 400 {
		t.Errorf("browser resubscribe: %d, want 400", code)
	}
	if code := webPost(`{"events":[{"external_id":"` + ext + `","name":"identify","attrs":{"optout_email":false,"city":"Goa"}}]}`); code != 202 {
		t.Fatalf("browser identify: %d", code)
	}
	waitFor(t, 10*time.Second, func() bool {
		ct, _ := jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"].(map[string]any)
		attrs, _ := ct["attrs"].(map[string]any)
		return attrs["city"] == "Goa"
	})
	ct := jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, 200)["contact"].(map[string]any)
	if ct["attrs"].(map[string]any)["optout_email"] != true {
		t.Error("a site-key request changed consent")
	}

	// The next email is suppressed, not sent.
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "email_suppressed") })
	mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+email), &search)
	if len(search.Messages) != 1 {
		t.Errorf("%d emails to an unsubscribed contact, want only the first", len(search.Messages))
	}
}

func TestCSVImportExport(t *testing.T) {
	c, _ := workspace(t, "Import")
	run := fmt.Sprint(time.Now().UnixNano())
	csv := "external_id,email,city,plan\nimp1-" + run + ",a-" + run + "@example.com,Pune,pro\n,b-" + run + "@example.com,Delhi,\n"
	req, _ := http.NewRequest("POST", apiURL+"/v1/contacts/import", bytes.NewBufferString(csv))
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("X-Pulse-CSRF", "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Rows, Imported int }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 || out.Imported != 2 {
		t.Fatalf("import: %d %+v", resp.StatusCode, out)
	}
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "pro", "rule": map[string]any{"attr": "plan", "op": "eq", "value": "pro"}}, 201)["id"]
	waitFor(t, 15*time.Second, func() bool {
		return jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%v", seg), nil, 200)["size"] == 1.0
	})
	exp, err := c.Get(apiURL + "/v1/contacts/export")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(exp.Body)
	if !strings.HasPrefix(string(data), "external_id,email,created_at,updated_at,city,email,first_seen,last_seen,plan\n") || !strings.Contains(string(data), "b-"+run+"@example.com") {
		t.Errorf("export header or rows wrong:\n%s", data)
	}
	if !strings.Contains(string(data), "imp1-"+run) || !strings.Contains(string(data), "Pune") {
		t.Errorf("export misses imported contacts:\n%s", data)
	}
}
