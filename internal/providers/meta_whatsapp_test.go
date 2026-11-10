package providers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"pulse/internal/channel"
)

func waOutbound() Outbound {
	return Outbound{MessageID: "m1", Channel: channel.WhatsApp, To: "+919812345678",
		Rendered: channel.Rendered{Content: channel.Content{TemplateName: "order_shipped", Language: "en_US",
			BodyParams: []string{"Asha", "#1042"}, HeaderImageURL: "https://cdn.acme.com/box.png"}}}
}

func TestMetaWhatsAppRequest(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{"messaging_product":"whatsapp","contacts":[{"input":"919812345678","wa_id":"919812345678"}],"messages":[{"id":"wamid.HBgM"}]}`)
	p := &MetaWhatsApp{HTTP: srv.Client(), BaseURL: srv.URL, PhoneNumberID: "1065", AccessToken: "EAAG"}
	res, err := p.Send(context.Background(), waOutbound())
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderMessageID != "wamid.HBgM" {
		t.Fatalf("id = %q", res.ProviderMessageID)
	}
	if got.Method != http.MethodPost || got.Path != "/v21.0/1065/messages" || got.Header.Get("Authorization") != "Bearer EAAG" {
		t.Fatalf("request %s %s %q", got.Method, got.Path, got.Header.Get("Authorization"))
	}
	b := decodeJSON(t, got.Body)
	checks := map[string]any{
		"messaging_product": b["messaging_product"], "to": b["to"], "type": b["type"],
		"name": jsonPath(b, "template", "name"), "lang": jsonPath(b, "template", "language", "code"),
		"header": jsonPath(b, "template", "components", 0, "type"),
		"image":  jsonPath(b, "template", "components", 0, "parameters", 0, "image", "link"),
		"body":   jsonPath(b, "template", "components", 1, "type"),
		"p0":     jsonPath(b, "template", "components", 1, "parameters", 0, "text"),
		"p1":     jsonPath(b, "template", "components", 1, "parameters", 1, "text"),
	}
	want := map[string]any{"messaging_product": "whatsapp", "to": "919812345678", "type": "template",
		"name": "order_shipped", "lang": "en_US", "header": "header", "image": "https://cdn.acme.com/box.png",
		"body": "body", "p0": "Asha", "p1": "#1042"}
	for k, v := range want {
		if checks[k] != v {
			t.Errorf("%s = %v, want %v (body %s)", k, checks[k], v, got.Body)
		}
	}

	// No params, no header: no components at all, and the language defaults to en.
	o := waOutbound()
	o.Rendered.Content = channel.Content{TemplateName: "hello_world"}
	if _, err := p.Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	b = decodeJSON(t, got.Body)
	if _, ok := jsonPath(b, "template").(map[string]any)["components"]; ok || jsonPath(b, "template", "language", "code") != "en" {
		t.Fatalf("body %s", got.Body)
	}
}

func TestMetaWhatsAppErrors(t *testing.T) {
	metaErr := func(code int) string {
		return `{"error":{"message":"err","type":"OAuthException","code":` + strconv.Itoa(code) + `,"fbtrace_id":"x"}}`
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"not a whatsapp user", 400, metaErr(131026), "unreachable"},
		{"template not found", 400, metaErr(132001), "permanent"},
		{"template params mismatch", 400, metaErr(132000), "permanent"},
		{"re-engagement window", 400, metaErr(131047), "permanent"},
		{"invalid token", 401, metaErr(190), "permanent"},
		{"invalid parameter", 400, metaErr(100), "permanent"},
		{"throughput limit", 400, metaErr(130429), "transient"},
		{"spam rate limit", 400, metaErr(131048), "transient"},
		{"pair rate limit", 400, metaErr(131056), "transient"},
		{"app rate limit", 400, metaErr(4), "transient"},
		{"unknown 4xx", 400, metaErr(999999), "permanent"},
		{"5xx", 500, metaErr(1), "transient"},
		{"5xx no body", 503, ``, "transient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tc.status, tc.body)
			p := &MetaWhatsApp{HTTP: srv.Client(), BaseURL: srv.URL, PhoneNumberID: "1", AccessToken: "t"}
			_, err := p.Send(context.Background(), waOutbound())
			if c := errClass(err); c != tc.want {
				t.Fatalf("got %s (%v), want %s", c, err, tc.want)
			}
		})
	}
}

const waStatuses = `{"object":"whatsapp_business_account","entry":[{"id":"1022","changes":[{"field":"messages","value":{
 "messaging_product":"whatsapp","metadata":{"phone_number_id":"1065"},
 "statuses":[
  {"id":"wamid.A","status":"sent","timestamp":"1","recipient_id":"919812345678"},
  {"id":"wamid.A","status":"delivered","timestamp":"2","recipient_id":"919812345678"},
  {"id":"wamid.B","status":"read","timestamp":"3","recipient_id":"919812345679"},
  {"id":"wamid.C","status":"failed","timestamp":"4","recipient_id":"919812345670","errors":[{"code":131026,"title":"Message undeliverable"}]},
  {"id":"wamid.D","status":"failed","timestamp":"5","recipient_id":"919812345671","errors":[{"code":131049,"title":"Ecosystem limit"}]}
 ]}}]}]}`

func waWebhook(body, secret string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/providers/9", bytes.NewBufferString(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return r
}

func TestMetaWhatsAppReceipts(t *testing.T) {
	p := &MetaWhatsApp{AppSecret: "s3cret"}
	got, err := p.ParseReceipts(waWebhook(waStatuses, "s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Receipt{
		{ProviderMessageID: "wamid.A", Status: "delivered"},
		{ProviderMessageID: "wamid.B", Status: "read"},
		{ProviderMessageID: "wamid.C", Status: "failed", Unreachable: true, Reason: "meta whatsapp 131026: Message undeliverable"},
		{ProviderMessageID: "wamid.D", Status: "failed", Reason: "meta whatsapp 131049: Ecosystem limit"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("receipt %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	tampered := waWebhook(waStatuses, "s3cret")
	tampered.Body = io.NopCloser(strings.NewReader(strings.Replace(waStatuses, "wamid.C", "wamid.X", 1)))
	noHeader := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(waStatuses))
	for name, r := range map[string]*http.Request{
		"tampered body": tampered,
		"wrong secret":  waWebhook(waStatuses, "other"),
		"no header":     noHeader,
	} {
		if _, err := p.ParseReceipts(r); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: err = %v, want ErrUnauthorized", name, err)
		}
	}
	if _, err := (&MetaWhatsApp{}).ParseReceipts(waWebhook(waStatuses, "")); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("no app secret configured: err = %v, want ErrUnauthorized", err)
	}
}

func TestMetaWhatsAppChallenge(t *testing.T) {
	p := &MetaWhatsApp{VerifyToken: "pulse-verify"}
	req := func(q string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/v1/webhooks/providers/9?"+q, nil)
	}
	if resp, ok := p.Challenge(req("hub.mode=subscribe&hub.verify_token=pulse-verify&hub.challenge=1158201444")); !ok || resp != "1158201444" {
		t.Fatalf("valid challenge: %q %v", resp, ok)
	}
	for _, q := range []string{
		"hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=1",
		"hub.mode=unsubscribe&hub.verify_token=pulse-verify&hub.challenge=1",
		"hub.challenge=1",
	} {
		if _, ok := p.Challenge(req(q)); ok {
			t.Errorf("%s: ok = true", q)
		}
	}
	if _, ok := (&MetaWhatsApp{}).Challenge(req("hub.mode=subscribe&hub.verify_token=&hub.challenge=1")); ok {
		t.Error("empty verify token accepted")
	}
}
