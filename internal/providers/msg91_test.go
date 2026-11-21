package providers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"pulse/internal/channel"
)

func TestMSG91Request(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{"type":"success","message":"3463706a6b6e313233343536"}`)
	p := &MSG91{HTTP: srv.Client(), BaseURL: srv.URL, AuthKey: "key1", FlowTemplateID: "flow1", SenderID: "ACMEIN"}
	o := Outbound{MessageID: "m1", To: "+919812345678", Ref: "p1AQIDBAUGBwgJ.abc-DEF_123",
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
		jsonPath(b, "recipients", 0, "message") != "Your OTP is 1234" ||
		jsonPath(b, "recipients", 0, "CRQID") != "p1AQIDBAUGBwgJ.abc-DEF_123" {
		t.Fatalf("body %s", got.Body)
	}

	o.Rendered.Content.SenderID = "ACMEOT" // the template's sender wins
	if _, err := p.Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if decodeJSON(t, got.Body)["sender"] != "ACMEOT" {
		t.Fatalf("body %s", got.Body)
	}

	// No reference, or one MSG91 would refuse: no CRQID.
	for _, ref := range []string{"", "has space", strings.Repeat("a", 81)} {
		o.Ref = ref
		if _, err := p.Send(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		if _, ok := jsonPath(decodeJSON(t, got.Body), "recipients", 0).(map[string]any)["CRQID"]; ok {
			t.Fatalf("ref %q: body %s", ref, got.Body)
		}
	}
}

func TestMSG91Receipts(t *testing.T) {
	p := &MSG91{WebhookToken: "tok3n"}
	one := `{"pluginsource":"","status":"1","telNum":"919812345678","CRQID":"p1AQIDBAUGBwgJ.abc-DEF_123",
	 "requestId":"3463706a6b6e313233343536","eventName":"delivered","upperCaseEventName":"DELIVERED"}`
	got, err := p.ParseReceipts(webhookReq(http.MethodPost, "/v1/webhooks/providers/7?token=tok3n", one, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := Receipt{ProviderMessageID: "3463706a6b6e313233343536", Status: "delivered", Ref: "p1AQIDBAUGBwgJ.abc-DEF_123"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v", got)
	}

	list := `[
	 {"status":"2","requestId":"r2","CRQID":"0","failureReason":"DLT template id not found"},
	 {"status":"9","requestId":"r3","CRQID":"p1x.y"},
	 {"status":"17","requestId":"r4"},
	 {"status":"0","requestId":"r5","eventName":"sent"},
	 {"status":"","requestId":"r6","eventName":"rejected"},
	 {"status":"1","requestId":""}
	]`
	got, err = p.ParseReceipts(webhookReq(http.MethodPost, "/v1/webhooks/providers/7", list, "tok3n"))
	if err != nil {
		t.Fatal(err)
	}
	wants := []Receipt{
		{ProviderMessageID: "r2", Status: "failed", Reason: "msg91: failed: DLT template id not found", Code: "provider_rejected"},
		{ProviderMessageID: "r3", Status: "failed", Reason: "msg91: blocked as NDNC", Code: "consent", Ref: "p1x.y"},
		{ProviderMessageID: "r4", Status: "failed", Reason: "msg91: number blocked", Code: "blocked"},
		{ProviderMessageID: "r6", Status: "failed", Reason: "msg91: rejected", Code: "provider_rejected"},
	}
	if len(got) != len(wants) {
		t.Fatalf("got %+v", got)
	}
	for i := range wants {
		if got[i] != wants[i] {
			t.Errorf("receipt %d = %+v, want %+v", i, got[i], wants[i])
		}
	}

	// A wrong or missing token, or no saved token, is refused.
	for _, tc := range []struct {
		saved, query, header string
	}{{"tok3n", "", ""}, {"tok3n", "nope", ""}, {"", "", ""}} {
		p := &MSG91{WebhookToken: tc.saved}
		_, err := p.ParseReceipts(webhookReq(http.MethodPost, "/x?token="+tc.query, one, tc.header))
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%+v: err = %v", tc, err)
		}
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
