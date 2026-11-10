package providers

import (
	"context"
	"net/http"
	"testing"

	"pulse/internal/channel"
)

func TestMSG91Request(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{"type":"success","message":"3463706a6b6e313233343536"}`)
	p := &MSG91{HTTP: srv.Client(), BaseURL: srv.URL, AuthKey: "key1", FlowTemplateID: "flow1", SenderID: "ACMEIN"}
	o := Outbound{MessageID: "m1", To: "+919812345678",
		Rendered: channel.Rendered{Content: channel.Content{Text: "Your OTP is 1234"}}}
	res, err := p.Send(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderMessageID != "3463706a6b6e313233343536" {
		t.Fatalf("id = %q", res.ProviderMessageID)
	}
	if got.Method != http.MethodPost || got.Path != "/api/v5/flow" || got.Header.Get("authkey") != "key1" {
		t.Fatalf("request %s %s authkey=%q", got.Method, got.Path, got.Header.Get("authkey"))
	}
	b := decodeJSON(t, got.Body)
	if b["template_id"] != "flow1" || b["short_url"] != "0" || b["sender"] != "ACMEIN" ||
		jsonPath(b, "recipients", 0, "mobiles") != "919812345678" ||
		jsonPath(b, "recipients", 0, "message") != "Your OTP is 1234" {
		t.Fatalf("body %s", got.Body)
	}

	o.Rendered.Content.SenderID = "ACMEOT" // the template's sender wins
	if _, err := p.Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if decodeJSON(t, got.Body)["sender"] != "ACMEOT" {
		t.Fatalf("body %s", got.Body)
	}
}

func TestMSG91Errors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"error type with 200", 200, `{"type":"error","message":"Authentication failure"}`, "permanent"},
		{"error type with 400", 400, `{"type":"error","message":"Invalid template"}`, "permanent"},
		{"401 plain", 401, `unauthorized`, "permanent"},
		{"rate limited", 429, `{"type":"error","message":"Too many requests"}`, "transient"},
		{"server error", 502, `{"type":"error","message":"bad gateway"}`, "transient"},
		{"unreadable 200", 200, `<html>`, "permanent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tc.status, tc.body)
			p := &MSG91{HTTP: srv.Client(), BaseURL: srv.URL, AuthKey: "k", FlowTemplateID: "f"}
			_, err := p.Send(context.Background(), Outbound{To: "+919812345678"})
			if c := errClass(err); c != tc.want {
				t.Fatalf("got %s (%v), want %s", c, err, tc.want)
			}
		})
	}
}
