package providers

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"golang.org/x/oauth2"

	"pulse/internal/channel"
)

func testRBM(srv string, c *http.Client) *GoogleRBM {
	return &GoogleRBM{HTTP: c, AgentID: "acme_agent", baseURL: srv,
		tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.test"})}
}

func TestGoogleRBMTextRequest(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{"name":"phones/+919812345678/agentMessages/m1","contentMessage":{}}`)
	o := Outbound{MessageID: "m1", Channel: channel.RCS, To: "+919812345678",
		Rendered: channel.Rendered{Content: channel.Content{Text: "Your order shipped", Suggestions: []channel.Suggestion{
			{Type: "reply", Text: "Thanks"},
			{Type: "url", Text: "Track", Value: "https://acme.com/t/1"},
			{Type: "dial", Text: "Call us", Value: "+918000000000"},
		}}}}
	res, err := testRBM(srv.URL, srv.Client()).Send(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderMessageID != "m1" {
		t.Fatalf("id = %q", res.ProviderMessageID)
	}
	q, _ := url.ParseQuery(got.RawQuery)
	if got.Method != http.MethodPost || got.Path != "/v1/phones/+919812345678/agentMessages" ||
		q.Get("messageId") != "m1" || q.Get("agentId") != "acme_agent" || got.Header.Get("Authorization") != "Bearer ya29.test" {
		t.Fatalf("request %s %s?%s %q", got.Method, got.Path, got.RawQuery, got.Header.Get("Authorization"))
	}
	b := decodeJSON(t, got.Body)
	cm := jsonPath(b, "contentMessage")
	for path, want := range map[string]any{
		"text":        jsonPath(cm, "text") == "Your order shipped",
		"reply":       jsonPath(cm, "suggestions", 0, "reply", "text") == "Thanks",
		"reply pb":    jsonPath(cm, "suggestions", 0, "reply", "postbackData") != nil,
		"url":         jsonPath(cm, "suggestions", 1, "action", "openUrlAction", "url") == "https://acme.com/t/1",
		"url text":    jsonPath(cm, "suggestions", 1, "action", "text") == "Track",
		"dial":        jsonPath(cm, "suggestions", 2, "action", "dialAction", "phoneNumber") == "+918000000000",
		"no richCard": jsonPath(cm, "richCard") == nil,
	} {
		if want != true {
			t.Errorf("%s wrong in %s", path, got.Body)
		}
	}
}

func TestGoogleRBMCardRequest(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{}`)
	o := Outbound{MessageID: "m2", To: "+919812345678", Rendered: channel.Rendered{Content: channel.Content{
		Card:        &channel.Card{Title: "Sale", Description: "50% off", ImageURL: "https://cdn.acme.com/sale.jpg"},
		Suggestions: []channel.Suggestion{{Type: "url", Text: "Shop", Value: "https://acme.com"}},
	}}}
	if _, err := testRBM(srv.URL, srv.Client()).Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	card := jsonPath(decodeJSON(t, got.Body), "contentMessage", "richCard", "standaloneCard")
	cc := jsonPath(card, "cardContent")
	if jsonPath(card, "cardOrientation") != "VERTICAL" || jsonPath(cc, "title") != "Sale" ||
		jsonPath(cc, "description") != "50% off" || jsonPath(cc, "media", "height") != "MEDIUM" ||
		jsonPath(cc, "media", "contentInfo", "fileUrl") != "https://cdn.acme.com/sale.jpg" ||
		jsonPath(cc, "suggestions", 0, "action", "openUrlAction", "url") != "https://acme.com" {
		t.Fatalf("body %s", got.Body)
	}
}

func TestGoogleRBMErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"not rcs capable", 404, `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`, "unreachable"},
		{"duplicate message id", 409, `{"error":{"code":409,"status":"ALREADY_EXISTS"}}`, "ok"},
		{"bad request", 400, `{"error":{"code":400,"status":"INVALID_ARGUMENT"}}`, "permanent"},
		{"unauthenticated", 401, `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`, "permanent"},
		{"agent not launched", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED"}}`, "permanent"},
		{"quota", 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, "transient"},
		{"server error", 500, `{}`, "transient"},
		{"unavailable", 503, `{}`, "transient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tc.status, tc.body)
			_, err := testRBM(srv.URL, srv.Client()).Send(context.Background(),
				Outbound{MessageID: "m", To: "+919812345678", Rendered: channel.Rendered{Content: channel.Content{Text: "hi"}}})
			if c := errClass(err); c != tc.want {
				t.Fatalf("got %s (%v), want %s", c, err, tc.want)
			}
		})
	}
}
