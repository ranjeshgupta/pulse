//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDoubleOptIn (O6): a form with double opt-in sends its confirmation email and joins the list
// only after the link is clicked; a second click changes nothing; a tampered link is refused.
func TestDoubleOptIn(t *testing.T) {
	c, _ := workspace(t, "Double opt-in")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "email", "provider": "sandbox_email", "is_default": true}, 200)
	run := fmt.Sprint(time.Now().UnixNano())
	email := "doi-" + run + "@example.com"
	list := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Newsletter"}, 201)["id"])
	noLink := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "no link", "channel": "email", "subject": "Hi", "html": "<p>Hi</p>"}, 201)["id"]
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Confirm", "channel": "email", "subject": "Please confirm",
		"html": `<p><a href="{{event.confirm_url}}">Yes</a></p>`, "content": map[string]any{"text": "Confirm: {{event.confirm_url}}"}, "transactional": true}, 201)["id"]
	form := func(tid any) map[string]any {
		return map[string]any{"name": "Newsletter", "status": "active",
			"fields":   []any{map[string]any{"key": "email", "label": "Email", "type": "email", "required": true}},
			"settings": map[string]any{"list_ids": []any{list}, "tags": []any{"newsletter"}, "double_optin": true, "confirm_template_id": tid, "confirmed_message": "Welcome aboard"}}
	}
	jsonCall(t, c, "POST", "/v1/forms", form(noLink), 400) // the email must carry the link
	phoneOnly := form(tpl)
	phoneOnly["fields"] = []any{map[string]any{"key": "phone", "label": "Phone", "type": "tel", "required": true}}
	jsonCall(t, c, "POST", "/v1/forms", phoneOnly, 400) // the confirmation needs an email field
	id := idOf(jsonCall(t, c, "POST", "/v1/forms", form(tpl), 200)["id"])
	key := jsonCall(t, c, "GET", fmt.Sprint("/v1/forms/", id), nil, 200)["form"].(map[string]any)["public_key"].(string)
	if code, body := postForm(t, key, url.Values{"email": {email}}, 5*time.Second); code >= 400 {
		t.Fatalf("submit: %d %s", code, body)
	}

	// The confirmation email arrives; the person isn't on the list yet.
	var link string
	re := regexp.MustCompile(`https?://\S+/f/confirm/\S+`)
	waitFor(t, 30*time.Second, func() bool {
		var out struct {
			Messages []struct {
				Recipient string `json:"recipient"`
				Payload   struct {
					Content struct{ Text string } `json:"content"`
				} `json:"payload"`
			} `json:"messages"`
		}
		b, _ := json.Marshal(jsonCall(t, c, "GET", "/v1/sandbox/messages?channel=email", nil, 200))
		_ = json.Unmarshal(b, &out)
		for _, m := range out.Messages {
			if m.Recipient == email {
				link = re.FindString(m.Payload.Content.Text)
			}
		}
		return link != ""
	})
	ext := email
	contacts := jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape(email), nil, 200)
	if cs, _ := contacts["contacts"].([]any); len(cs) == 1 {
		ext = cs[0].(map[string]any)["external_id"].(string)
	}
	if slices.Contains(segmentsOf(t, c, ext), list) {
		t.Fatal("joined the list before confirming")
	}
	get := func(u string) (int, string) {
		resp, err := http.Post(u, "application/x-www-form-urlencoded", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	// Links point at the public address; tests reach the server at apiURL.
	path := link[strings.Index(link, "/f/confirm/"):]
	// Opening the link (or a mail scanner opening it) only asks.
	if resp, err := http.Get(apiURL + path); err != nil || resp.StatusCode != 200 {
		t.Fatalf("open link: %v %v", resp, err)
	}
	time.Sleep(2 * time.Second)
	if slices.Contains(segmentsOf(t, c, ext), list) {
		t.Fatal("opening the link confirmed without the button")
	}
	if code, body := get(apiURL + path); code != 200 || !strings.Contains(body, "Welcome aboard") {
		t.Fatalf("confirm: %d %s", code, body)
	}
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, ext), list) })
	if code, _ := get(apiURL + path); code != 200 {
		t.Errorf("second click: %d", code)
	}
	if code, _ := get(apiURL + path[:len(path)-4] + "AAAA"); code != 404 {
		t.Errorf("tampered link: %d", code)
	}
}
