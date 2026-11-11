package providers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pulse/internal/channel"
)

// newGupshup builds a Gupshup adapter through its Kind against a fake API (AllowPrivate so the
// SSRF-safe client may reach httptest).
func newGupshup(t *testing.T, kind, base string, config map[string]string) Provider {
	t.Helper()
	c := map[string]string{"base_url": base}
	for k, v := range config {
		c[k] = v
	}
	s := map[string]string{"userid": "2000123456", "password": "pw-secret", "api_key": "key-secret", "webhook_token": "tok3n"}
	p, err := New(kind, Deps{AllowPrivate: true}, c, s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func formOf(t *testing.T, got *captured) url.Values {
	t.Helper()
	if ct := got.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Fatalf("content type %q", ct)
	}
	f, err := url.ParseQuery(string(got.Body))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func smsOut(c channel.Content) Outbound {
	return Outbound{MessageID: "m1", Channel: channel.SMS, To: "+919812345678", Rendered: channel.Rendered{Content: c}}
}

func TestGupshupSMSRequest(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{"response":{"id":"3562707498794989059-328736121207676738","phone":"919812345678","details":"","status":"success"}}`)
	p := newGupshup(t, "gupshup_sms", srv.URL+"/GatewayAPI/rest", map[string]string{"mask": "ACMEIN",
		"principal_entity_id": "1201000000000000001", "dlt_template_id": "1207000000000000009"}).(*GupshupSMS)

	res, err := p.Send(context.Background(), smsOut(channel.Content{Text: "Your OTP is 1234 [ok] €5"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderMessageID != "3562707498794989059-328736121207676738" {
		t.Fatalf("id %q", res.ProviderMessageID)
	}
	if got.Method != http.MethodPost || got.Path != "/GatewayAPI/rest" {
		t.Fatalf("request %s %s", got.Method, got.Path)
	}
	f := formOf(t, got)
	want := map[string]string{"method": "SendMessage", "send_to": "919812345678", "msg": "Your OTP is 1234 [ok] €5",
		"msg_type": "TEXT", "userid": "2000123456", "password": "pw-secret", "auth_scheme": "plain", "v": "1.1",
		"format": "json", "mask": "ACMEIN", "principalEntityId": "1201000000000000001", "dltTemplateId": "1207000000000000009"}
	for k, v := range want {
		if f.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, f.Get(k), v)
		}
	}

	// The template's own sender, entity and DLT template win; non-GSM text goes as Unicode.
	_, err = p.Send(context.Background(), smsOut(channel.Content{Text: "आपका OTP 1234 है", SenderID: "ACMEOT",
		DLTEntityID: "1201999", DLTTemplateID: "1207888"}))
	if err != nil {
		t.Fatal(err)
	}
	f = formOf(t, got)
	if f.Get("msg_type") != "UNICODE_TEXT" || f.Get("mask") != "ACMEOT" || f.Get("principalEntityId") != "1201999" ||
		f.Get("dltTemplateId") != "1207888" || f.Get("msg") != "आपका OTP 1234 है" {
		t.Fatalf("form %v", f)
	}

	// No defaults and no template values: the DLT fields are left out.
	bare := newGupshup(t, "gupshup_sms", srv.URL, nil).(*GupshupSMS)
	if _, err := bare.Send(context.Background(), smsOut(channel.Content{Text: "hi"})); err != nil {
		t.Fatal(err)
	}
	f = formOf(t, got)
	for _, k := range []string{"mask", "principalEntityId", "dltTemplateId"} {
		if f.Has(k) {
			t.Errorf("%s sent without a value", k)
		}
	}

	// The text response format works too.
	srv2, _ := fakeAPI(t, 200, "success | 919812345678 | 728014710863298817-1234567890")
	res, err = newGupshup(t, "gupshup_sms", srv2.URL, nil).Send(context.Background(), smsOut(channel.Content{Text: "hi"}))
	if err != nil || res.ProviderMessageID != "728014710863298817-1234567890" {
		t.Fatalf("text response: %+v %v", res, err)
	}
}

func TestGupshupGSM7(t *testing.T) {
	for text, want := range map[string]bool{"Hello @ £5 {x} ~ €": true, "Ünïcode? no: ï": false, "😀": false, "": true, "line\nbreak": true} {
		if isGSM7(text) != want {
			t.Errorf("isGSM7(%q) = %v", text, !want)
		}
	}
}

func TestGupshupSMSErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
		bad        bool
		contains   string
	}{
		{"bad credentials", `{"response":{"id":"102","phone":"","details":"Authentication failed due to invalid userId or password.","status":"error"}}`, 200, "permanent", false, "credentials"},
		{"bad credentials text", "error | 102 | Authentication failed due to invalid userId or password.", 200, "permanent", false, "credentials"},
		{"invalid number", `{"response":{"id":"103","phone":"12","details":"The phone number is invalid","status":"error"}}`, 200, "permanent", true, ""},
		{"international off", `{"response":{"id":"175","phone":"","details":"The \"INTERNATIONAL_PHONE\" service is disabled for you.","status":"error"}}`, 200, "permanent", false, "175"},
		{"401", `nope`, 401, "permanent", false, "credentials"},
		{"429", `slow down`, 429, "transient", false, ""},
		{"5xx", `{"response":{"status":"error","id":"500","details":"x"}}`, 503, "transient", false, ""},
		{"unreadable 200", `<html>`, 200, "permanent", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tc.status, tc.body)
			_, err := newGupshup(t, "gupshup_sms", srv.URL, nil).Send(context.Background(), smsOut(channel.Content{Text: "hi"}))
			if c := errClass(err); c != tc.want {
				t.Fatalf("got %s (%v), want %s", c, err, tc.want)
			}
			if errors.Is(err, ErrBadNumber) != tc.bad {
				t.Errorf("bad number = %v (%v)", !tc.bad, err)
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error %q lacks %q", err, tc.contains)
			}
		})
	}
}

func webhookReq(method, target, body, header string) *http.Request {
	r := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if header != "" {
		r.Header.Set("X-Pulse-Token", header)
	}
	return r
}

func TestGupshupSMSReceipts(t *testing.T) {
	p := newGupshup(t, "gupshup_sms", "https://enterprise.smsgupshup.com/GatewayAPI/rest", nil).(*GupshupSMS)
	body := `[
	 {"externalId":"111-1","deliveredTS":1718007189549,"status":"SUCCESS","phoneNo":"919812345678","cause":"SUCCESS","errCode":"000"},
	 {"externalId":"111-2","eventType":"FAILED","phoneNo":"919812345679","cause":"UNKNOWN_SUBSCRIBER","errCode":"003"},
	 {"externalId":"111-3","status":"FAILURE","cause":"DND_FAIL","errCode":6},
	 {"externalId":"111-4","status":"FAILURE","cause":"","errCode":"038"},
	 {"externalId":"111-5","status":"UNKNOWN","cause":"UNKNOWN","errCode":"024"},
	 {"externalId":"111-6","eventType":"DELIVERED","cause":"SUCCESS","errCode":"000"},
	 {"externalId":"111-7","status":"FAILURE","cause":"ABSENT_SUBSCRIBER","errCode":"001"},
	 {"externalId":"111-8","status":"FAILURE","cause":"SOMETHING_NEW","errCode":"099"}
	]`
	got, err := p.ParseReceipts(webhookReq(http.MethodPost, "/v1/webhooks/providers/7?token=tok3n", body, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := []Receipt{
		{ProviderMessageID: "111-1", Status: "delivered"},
		{ProviderMessageID: "111-2", Status: "failed", Unreachable: true, Reason: "gupshup sms 003: UNKNOWN_SUBSCRIBER", Code: "invalid_number"},
		{ProviderMessageID: "111-3", Status: "failed", Reason: "gupshup sms 6: DND_FAIL", Code: "consent"},
		{ProviderMessageID: "111-4", Status: "failed", Reason: "gupshup sms 038: MSG_DOES_NOT_MATCH_TEMPLATE", Code: "template"},
		{ProviderMessageID: "111-6", Status: "delivered"},
		{ProviderMessageID: "111-7", Status: "failed", Reason: "gupshup sms 001: ABSENT_SUBSCRIBER", Code: "not_on_channel"},
		{ProviderMessageID: "111-8", Status: "failed", Reason: "gupshup sms 099: SOMETHING_NEW", Code: "provider_rejected"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("receipt %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// GET-style query parameters (token in the header) and a form body.
	got, err = p.ParseReceipts(webhookReq(http.MethodGet,
		"/v1/webhooks/providers/7?externalId=9-9&deliveredTS=1&status=FAILURE&cause=BLOCKED&phoneNo=91981&errCode=007", "", "tok3n"))
	if err != nil || len(got) != 1 || got[0].Code != "blocked" || got[0].ProviderMessageID != "9-9" {
		t.Fatalf("query: %+v %v", got, err)
	}
	got, err = p.ParseReceipts(webhookReq(http.MethodPost, "/x?token=tok3n", "externalId=8-8&status=SUCCESS&cause=SUCCESS", ""))
	if err != nil || len(got) != 1 || got[0].Status != "delivered" {
		t.Fatalf("form: %+v %v", got, err)
	}
}

func TestGupshupWebhookToken(t *testing.T) {
	sms := newGupshup(t, "gupshup_sms", "https://e.example.com", nil)
	wa := newGupshup(t, "gupshup_whatsapp", "https://api.example.com", map[string]string{"source": "9180", "app_name": "a"})
	rcs := newGupshup(t, "gupshup_rcs", "https://e.example.com", nil)
	body := `[{"externalId":"1-1","status":"SUCCESS"}]`
	waBody := `{"app":"a","timestamp":1,"version":2,"type":"message-event","payload":{"id":"w1","gsId":"g1","type":"delivered","destination":"9198","payload":{"ts":1}}}`
	for name, p := range map[string]Provider{"sms": sms, "whatsapp": wa, "rcs": rcs} {
		rp := p.(ReceiptParser)
		b := body
		if name == "whatsapp" {
			b = waBody
		}
		for label, r := range map[string]*http.Request{
			"missing":      webhookReq(http.MethodPost, "/w", b, ""),
			"wrong query":  webhookReq(http.MethodPost, "/w?token=nope", b, ""),
			"wrong header": webhookReq(http.MethodPost, "/w", b, "nope"),
		} {
			if _, err := rp.ParseReceipts(r); !errors.Is(err, ErrUnauthorized) {
				t.Errorf("%s %s: err = %v, want ErrUnauthorized", name, label, err)
			}
		}
		for label, r := range map[string]*http.Request{
			"query":  webhookReq(http.MethodPost, "/w?token=tok3n", b, ""),
			"header": webhookReq(http.MethodPost, "/w", b, "tok3n"),
		} {
			if got, err := rp.ParseReceipts(r); err != nil || len(got) != 1 {
				t.Errorf("%s %s: %+v %v", name, label, got, err)
			}
		}
	}
	// No saved token: everything is refused, even an empty token.
	for _, p := range []ReceiptParser{&GupshupSMS{}, &GupshupWhatsApp{}, &GupshupRCS{}} {
		if _, err := p.ParseReceipts(webhookReq(http.MethodPost, "/w?token=", body, "")); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%T without a token: err = %v", p, err)
		}
	}
	// The token is required in the form.
	for _, id := range []string{"gupshup_sms", "gupshup_whatsapp", "gupshup_rcs"} {
		k, _ := Lookup(id)
		found := false
		for _, f := range k.Secrets {
			found = found || (f.Key == "webhook_token" && f.Required)
		}
		if !found || !ReportsDelivery(id, nil) {
			t.Errorf("%s: webhook_token required %v, reports delivery %v", id, found, ReportsDelivery(id, nil))
		}
	}
}

func TestGupshupWhatsAppTemplate(t *testing.T) {
	srv, got := fakeAPI(t, 202, `{"messageId":"ee4a68a0-1203-4c85-8dc3-49d0b3226a35","status":"success"}`)
	p := newGupshup(t, "gupshup_whatsapp", srv.URL, map[string]string{"source": "+917834811114", "app_name": "AcmeApp"})
	o := waOutbound()
	o.Rendered.Content.TemplateName = "c0e7f3b2-8a5d-4b41-9a7e-1d2f3a4b5c6d"
	res, err := p.Send(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderMessageID != "ee4a68a0-1203-4c85-8dc3-49d0b3226a35" {
		t.Fatalf("id %q", res.ProviderMessageID)
	}
	if got.Path != "/wa/api/v1/template/msg" || got.Header.Get("apikey") != "key-secret" {
		t.Fatalf("request %s apikey=%q", got.Path, got.Header.Get("apikey"))
	}
	f := formOf(t, got)
	if f.Get("channel") != "whatsapp" || f.Get("source") != "917834811114" || f.Get("destination") != "919812345678" ||
		f.Get("src.name") != "AcmeApp" {
		t.Fatalf("form %v", f)
	}
	tpl := decodeJSON(t, []byte(f.Get("template")))
	if tpl["id"] != "c0e7f3b2-8a5d-4b41-9a7e-1d2f3a4b5c6d" || jsonPath(tpl, "params", 0) != "Asha" || jsonPath(tpl, "params", 1) != "#1042" {
		t.Fatalf("template %s", f.Get("template"))
	}
	msg := decodeJSON(t, []byte(f.Get("message")))
	if msg["type"] != "image" || jsonPath(msg, "image", "link") != "https://cdn.acme.com/box.png" {
		t.Fatalf("message %s", f.Get("message"))
	}

	// Without params or header: params is an empty list, no message field.
	o.Rendered.Content = channel.Content{TemplateName: "t1"}
	if _, err := p.Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	f = formOf(t, got)
	if f.Get("template") != `{"id":"t1","params":[]}` || f.Has("message") {
		t.Fatalf("form %v", f)
	}
}

func TestGupshupWhatsAppSession(t *testing.T) {
	srv, got := fakeAPI(t, 202, `{"status":"submitted","messageId":"gs-1"}`)
	p := newGupshup(t, "gupshup_whatsapp", srv.URL, map[string]string{"source": "917834811114", "app_name": "AcmeApp"})
	o := Outbound{MessageID: "m1", Channel: channel.WhatsApp, To: "+919812345678",
		Rendered: channel.Rendered{Content: channel.Content{Text: "Thanks, we got it"}}}
	res, err := p.Send(context.Background(), o)
	if err != nil || res.ProviderMessageID != "gs-1" {
		t.Fatalf("%+v %v", res, err)
	}
	f := formOf(t, got)
	if got.Path != "/wa/api/v1/msg" || f.Has("template") || f.Get("message") != `{"text":"Thanks, we got it","type":"text"}` {
		t.Fatalf("%s %v", got.Path, f)
	}
	o.Rendered.Content = channel.Content{}
	if _, err := p.Send(context.Background(), o); errClass(err) != "permanent" {
		t.Fatalf("empty message: %v", err)
	}
}

func TestGupshupWhatsAppErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
		bad        bool
	}{
		{"auth", `{"message":"Authentication Failed","status":"error"}`, 401, "permanent", false},
		{"invalid destination", `{"message":"Invalid Destination","status":"error"}`, 400, "permanent", true},
		{"other 400", `{"message":"Template Not Found","status":"error"}`, 400, "permanent", false},
		{"429", `{"message":"Too many requests","status":"error"}`, 429, "transient", false},
		{"5xx", ``, 502, "transient", false},
		{"2xx without id", `{"status":"submitted"}`, 202, "permanent", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tc.status, tc.body)
			p := newGupshup(t, "gupshup_whatsapp", srv.URL, map[string]string{"source": "9180", "app_name": "a"})
			_, err := p.Send(context.Background(), waOutbound())
			if c := errClass(err); c != tc.want {
				t.Fatalf("got %s (%v), want %s", c, err, tc.want)
			}
			if errors.Is(err, ErrBadNumber) != tc.bad {
				t.Errorf("bad number = %v", !tc.bad)
			}
			if tc.name == "auth" && !strings.Contains(err.Error(), "credentials") {
				t.Errorf("auth error %q", err)
			}
		})
	}
}

func TestGupshupWhatsAppReceipts(t *testing.T) {
	p := newGupshup(t, "gupshup_whatsapp", "https://api.gupshup.io", map[string]string{"source": "9180", "app_name": "a"}).(*GupshupWhatsApp)
	ev := func(typ, id, gsID, inner string) string {
		gs := ""
		if gsID != "" {
			gs = `"gsId":"` + gsID + `",`
		}
		return `{"app":"AcmeApp","timestamp":1718007189549,"version":2,"type":"message-event","payload":{"id":"` + id + `",` + gs +
			`"type":"` + typ + `","destination":"919812345678","payload":` + inner + `}}`
	}
	cases := []struct {
		body string
		want []Receipt
	}{
		{ev("enqueued", "gs-1", "", `{"whatsappMessageId":"wamid.1","type":"session"}`), nil},
		{ev("sent", "wamid.1", "gs-1", `{"ts":1}`), nil},
		{ev("delivered", "wamid.1", "gs-1", `{"ts":2}`), []Receipt{{ProviderMessageID: "gs-1", Status: "delivered"}}},
		{ev("read", "wamid.1", "gs-1", `{"ts":3}`), []Receipt{{ProviderMessageID: "gs-1", Status: "read"}}},
		{ev("failed", "gs-2", "", `{"code":1002,"reason":"Number Does Not Exists On WhatsApp"}`),
			[]Receipt{{ProviderMessageID: "gs-2", Status: "failed", Unreachable: true, Reason: "gupshup whatsapp 1002: Number Does Not Exists On WhatsApp", Code: "not_on_channel"}}},
		{ev("failed", "wamid.3", "gs-3", `{"code":131026,"reason":"Message undeliverable"}`),
			[]Receipt{{ProviderMessageID: "gs-3", Status: "failed", Unreachable: true, Reason: "meta whatsapp 131026: Message undeliverable", Code: "blocked"}}},
		{ev("failed", "gs-4", "", `{"code":1012,"reason":"User is Opted Out"}`),
			[]Receipt{{ProviderMessageID: "gs-4", Status: "failed", Reason: "gupshup whatsapp 1012: User is Opted Out", Code: "consent"}}},
		{ev("failed", "gs-5", "", `{"code":4001,"reason":"API rate limited"}`),
			[]Receipt{{ProviderMessageID: "gs-5", Status: "failed", Reason: "gupshup whatsapp 4001: API rate limited", Code: "rate_limited"}}},
		{ev("failed", "wamid.6", "gs-6", `{"code":131049,"reason":"Ecosystem limit"}`),
			[]Receipt{{ProviderMessageID: "gs-6", Status: "failed", Reason: "meta whatsapp 131049: Ecosystem limit", Code: "spam_policy"}}},
		{`{"app":"AcmeApp","timestamp":1,"version":2,"type":"message","payload":{"id":"ABEG","source":"919812345678","type":"text","payload":{"text":"STOP"},"sender":{"phone":"919812345678","name":"Asha"}}}`,
			[]Receipt{{ProviderMessageID: "ABEG", Status: Inbound, From: "+919812345678", Text: "STOP"}}},
		{`{"app":"AcmeApp","timestamp":1,"version":2,"type":"message","payload":{"id":"ABEH","source":"919812345678","type":"button_reply","payload":{"title":"Stop promotions","id":"b1"},"sender":{"phone":"919812345678"}}}`,
			[]Receipt{{ProviderMessageID: "ABEH", Status: Inbound, From: "+919812345678", Text: "Stop promotions"}}},
		{`{"app":"AcmeApp","timestamp":1,"version":2,"type":"user-event","payload":{"phone":"919812345678","type":"sandbox-start"}}`, nil},
		{``, nil},
	}
	for i, tc := range cases {
		got, err := p.ParseReceipts(webhookReq(http.MethodPost, "/w?token=tok3n", tc.body, ""))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("case %d: got %+v, want %+v", i, got, tc.want)
		}
		for j := range got {
			if got[j] != tc.want[j] {
				t.Errorf("case %d: %+v, want %+v", i, got[j], tc.want[j])
			}
		}
	}
}

func TestGupshupRCSRequest(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{"response":{"id":"4843671605020377110-242097152723471982","phone":"919812345678","details":"","status":"success"}}`)
	p := newGupshup(t, "gupshup_rcs", srv.URL, nil)
	o := Outbound{MessageID: "m1", Channel: channel.RCS, To: "+919812345678",
		Rendered: channel.Rendered{Content: channel.Content{Text: "Your order shipped", Suggestions: []channel.Suggestion{
			{Type: "reply", Text: "Thanks"},
			{Type: "url", Text: "Track", Value: "https://acme.com/t/1"},
			{Type: "dial", Text: "Call us", Value: "+918000000000"},
		}}}}
	res, err := p.Send(context.Background(), o)
	if err != nil || res.ProviderMessageID != "4843671605020377110-242097152723471982" {
		t.Fatalf("%+v %v", res, err)
	}
	f := formOf(t, got)
	if f.Get("method") != "SendMessage" || f.Get("msg_type") != "TEXT" || f.Get("userid") != "2000123456" ||
		f.Get("password") != "pw-secret" || f.Get("send_to") != "919812345678" || f.Get("auth_scheme") != "plain" {
		t.Fatalf("form %v", f)
	}
	cm := jsonPath(decodeJSON(t, []byte(f.Get("msg"))), "contentMessage")
	for path, ok := range map[string]bool{
		"text":     jsonPath(cm, "text") == "Your order shipped",
		"reply":    jsonPath(cm, "suggestions", 0, "reply", "text") == "Thanks",
		"url":      jsonPath(cm, "suggestions", 1, "action", "openUrlAction", "url") == "https://acme.com/t/1",
		"dial":     jsonPath(cm, "suggestions", 2, "action", "dialAction", "phoneNumber") == "+918000000000",
		"no card":  jsonPath(cm, "richCard") == nil,
		"reply pb": jsonPath(cm, "suggestions", 0, "reply", "postbackData") != nil,
	} {
		if !ok {
			t.Errorf("%s wrong in %s", path, f.Get("msg"))
		}
	}

	// A card: title, description, image and suggestions inside the card; no text.
	o.Rendered.Content.Card = &channel.Card{Title: "Shipped", Description: "Arrives Friday", ImageURL: "https://cdn.acme.com/box.png"}
	if _, err := p.Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	cm = jsonPath(decodeJSON(t, []byte(formOf(t, got).Get("msg"))), "contentMessage")
	card := jsonPath(cm, "richCard", "standaloneCard", "cardContent")
	if jsonPath(cm, "text") != nil || jsonPath(card, "title") != "Shipped" || jsonPath(card, "description") != "Arrives Friday" ||
		jsonPath(card, "media", "contentInfo", "fileUrl") != "https://cdn.acme.com/box.png" ||
		jsonPath(card, "suggestions", 1, "action", "openUrlAction", "url") != "https://acme.com/t/1" {
		t.Fatalf("card %v", cm)
	}
}

func TestGupshupRCSErrors(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"response":{"id":"102","details":"Authentication failed due to invalid userId or password.","status":"error"}}`, "permanent"},
		{`{"response":{"id":"301","details":"Recipient is not RCS capable","status":"error"}}`, "unreachable"},
	} {
		srv, _ := fakeAPI(t, 200, tc.body)
		_, err := newGupshup(t, "gupshup_rcs", srv.URL, nil).Send(context.Background(),
			Outbound{To: "+919812345678", Rendered: channel.Rendered{Content: channel.Content{Text: "hi"}}})
		if c := errClass(err); c != tc.want {
			t.Errorf("%s: got %s (%v)", tc.body, c, err)
		}
	}
}

func TestGupshupRCSReceipts(t *testing.T) {
	p := newGupshup(t, "gupshup_rcs", "https://e.example.com", nil).(*GupshupRCS)
	body := `[
	 {"externalId":"r-1","eventType":"DELIVERED","cause":"SUCCESS","errCode":"000"},
	 {"externalId":"r-2","eventType":"READ"},
	 {"externalId":"r-3","eventType":"FAILED","cause":"RCS_NOT_CAPABLE","errCode":"501"},
	 {"externalId":"r-4","eventType":"FAILED","cause":"UNKNOWN_SUBSCRIBER","errCode":"003"},
	 {"externalId":"r-5","eventType":"FAILED","cause":"SYSTEM_FAILURE","errCode":"005"},
	 {"senderPhoneNumber":"919812345678","messageId":"u-1","text":"Hello"},
	 {"senderPhoneNumber":"+919812345678","messageId":"u-2","suggestionResponse":{"postbackData":"reply:Thanks","text":"Thanks","type":"REPLY"}},
	 {"senderPhoneNumber":"+919812345678","messageId":"u-3","suggestionResponse":{"postbackData":"url:Track","text":"Track","type":"ACTION"}}
	]`
	got, err := p.ParseReceipts(webhookReq(http.MethodPost, "/w?token=tok3n", body, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := []Receipt{
		{ProviderMessageID: "r-1", Status: "delivered"},
		{ProviderMessageID: "r-2", Status: "read"},
		{ProviderMessageID: "r-3", Status: "failed", Unreachable: true, Reason: "gupshup rcs 501: RCS_NOT_CAPABLE", Code: "not_on_channel"},
		{ProviderMessageID: "r-4", Status: "failed", Unreachable: true, Reason: "gupshup rcs 003: UNKNOWN_SUBSCRIBER", Code: "not_on_channel"},
		{ProviderMessageID: "r-5", Status: "failed", Reason: "gupshup rcs 005: SYSTEM_FAILURE", Code: "provider_error"},
		{ProviderMessageID: "u-1", Status: Inbound, From: "+919812345678", Text: "Hello"},
		{ProviderMessageID: "u-2", Status: Inbound, From: "+919812345678", Text: "Thanks", Suggestion: "reply", Postback: "reply:Thanks"},
		{ProviderMessageID: "u-3", Status: Inbound, From: "+919812345678", Text: "Track", Suggestion: "action", Postback: "url:Track"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("receipt %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

type previewer interface {
	PreviewRequest(o Outbound) (method, url string, headers map[string]string, body string)
}

func TestGupshupPreviewMasksSecrets(t *testing.T) {
	sms := newGupshup(t, "gupshup_sms", "https://enterprise.smsgupshup.com/GatewayAPI/rest", map[string]string{"mask": "ACMEIN"})
	wa := newGupshup(t, "gupshup_whatsapp", "https://api.gupshup.io", map[string]string{"source": "9180", "app_name": "AcmeApp"})
	rcs := newGupshup(t, "gupshup_rcs", "https://enterprise.smsgupshup.com/GatewayAPI/rest", nil)
	for name, tc := range map[string]struct {
		p      Provider
		o      Outbound
		url    string
		secret string
		inBody string
		masked func(h map[string]string, body string) bool
	}{
		"sms": {sms, smsOut(channel.Content{Text: "Hi Asha"}), "https://enterprise.smsgupshup.com/GatewayAPI/rest", "pw-secret",
			"mask=ACMEIN", func(h map[string]string, b string) bool { return strings.Contains(b, "password=••••") }},
		"whatsapp": {wa, waOutbound(), "https://api.gupshup.io/wa/api/v1/template/msg", "key-secret",
			"src.name=AcmeApp", func(h map[string]string, b string) bool { return h["apikey"] == "••••" }},
		"rcs": {rcs, Outbound{To: "+919812345678", Rendered: channel.Rendered{Content: channel.Content{Text: "Hi"}}},
			"https://enterprise.smsgupshup.com/GatewayAPI/rest", "pw-secret",
			"msg_type=TEXT", func(h map[string]string, b string) bool { return strings.Contains(b, "password=••••") }},
	} {
		method, u, h, body := tc.p.(previewer).PreviewRequest(tc.o)
		all := body
		for k, v := range h {
			all += k + v
		}
		if method != http.MethodPost || u != tc.url || strings.Contains(all, tc.secret) || !strings.Contains(body, tc.inBody) ||
			!tc.masked(h, body) || h["Content-Type"] != "application/x-www-form-urlencoded" {
			t.Errorf("%s: %s %s %v %s", name, method, u, h, body)
		}
	}
}

func TestGupshupBaseURL(t *testing.T) {
	if _, err := New("gupshup_sms", Deps{}, map[string]string{"base_url": "ftp://x"},
		map[string]string{"userid": "1", "password": "p", "webhook_token": "t"}); err == nil {
		t.Error("ftp base URL accepted")
	}
	p, err := New("gupshup_whatsapp", Deps{}, map[string]string{"source": "1", "app_name": "a"},
		map[string]string{"api_key": "k", "webhook_token": "t"})
	if err != nil || p.(*GupshupWhatsApp).BaseURL != "https://api.gupshup.io" {
		t.Fatalf("default base URL: %v", err)
	}
	// The SSRF-safe client refuses private addresses unless allowed.
	srv, _ := fakeAPI(t, 200, `{"response":{"id":"1","status":"success"}}`)
	p, _ = New("gupshup_sms", Deps{}, map[string]string{"base_url": srv.URL},
		map[string]string{"userid": "1", "password": "p", "webhook_token": "t"})
	if _, err := p.Send(context.Background(), smsOut(channel.Content{Text: "hi"})); err == nil {
		t.Error("private address reached without AllowPrivate")
	}
}
