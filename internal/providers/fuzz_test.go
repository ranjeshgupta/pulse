package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// FuzzParseReceipts sends arbitrary bodies, correctly authenticated, to every adapter's delivery
// webhook parser, and the same bodies unauthenticated: nothing may panic, unauthenticated
// requests are refused, and every receipt has a status.
func FuzzParseReceipts(f *testing.F) {
	for _, s := range []string{
		waStatuses,
		`[{"externalId":"111-1","status":"SUCCESS","cause":"SUCCESS","errCode":"000"},{"externalId":"r-3","eventType":"FAILED","cause":"RCS_NOT_CAPABLE","errCode":501}]`,
		`{"app":"AcmeApp","timestamp":1,"version":2,"type":"message-event","payload":{"id":"a","gsId":"g","type":"failed","payload":{"code":131049,"reason":"x"}}}`,
		`{"senderPhoneNumber":"+919812345678","messageId":"MxB","suggestionResponse":{"postbackData":"yes","text":"Yes","type":"REPLY"}}`,
		`{"status":"1","requestId":"r","CRQID":"p1x.y","eventName":"delivered"}`,
		`MessageSid=SM1&MessageStatus=undelivered&ErrorCode=30003&From=%2B919812345678&Body=STOP`,
		`{"events":[{"msg_id":"P1","state":"dlvrd"},{"msg_id":"P2","state":"FAILED","err":{"code":17,"text":"DND"}}]}`,
		`{"id":"x","status":"delivered","reason":"r"}`,
		`externalId=8-8&status=SUCCESS&cause=SUCCESS`, ``, `[]`, `{}`, `null`,
	} {
		f.Add(s, uint8(0))
	}
	custom := func(t *testing.T) *Gateway {
		return customGateway(t, CustomRequest{Method: "POST", URL: "https://api.example.com/send", Receipt: &ReceiptMapping{
			ItemsPath: "events", IDPath: "msg_id", StatusPath: "state", ReasonPath: "err.text", CodePath: "err.code",
			Statuses: map[string]string{"dlvrd": "delivered", "failed": "failed"}, Verify: "query", VerifyName: "token"}},
			nil, map[string]string{"receipt_token": "tok3n"})
	}
	f.Fuzz(func(t *testing.T, body string, which uint8) {
		type target struct {
			name     string
			p        ReceiptParser
			signed   func() *http.Request
			unsigned func() *http.Request
		}
		tokenReq := func(tok string) func() *http.Request {
			return func() *http.Request { return webhookReq(http.MethodPost, "/w?token="+tok, body, "") }
		}
		form, _ := url.ParseQuery(body)
		const public = "https://pulse.example.com/v1/webhooks/providers/7?t=1"
		targets := []target{
			{"msg91", &MSG91{WebhookToken: "tok3n"}, tokenReq("tok3n"), tokenReq("nope")},
			{"gupshup_sms", newGupshup(t, "gupshup_sms", "https://e.example.com", nil).(ReceiptParser), tokenReq("tok3n"), tokenReq("")},
			{"gupshup_rcs", newGupshup(t, "gupshup_rcs", "https://e.example.com", nil).(ReceiptParser), tokenReq("tok3n"), tokenReq("x")},
			{"gupshup_whatsapp", newGupshup(t, "gupshup_whatsapp", "https://e.example.com", nil).(ReceiptParser), tokenReq("tok3n"), tokenReq("x")},
			{"meta_whatsapp", &MetaWhatsApp{AppSecret: "s3cret"}, func() *http.Request { return waWebhook(body, "s3cret") }, func() *http.Request { return waWebhook(body, "other") }},
			{"google_rbm", &GoogleRBM{ClientToken: "ct-123"}, func() *http.Request { return rbmWebhook(body, "ct-123") }, func() *http.Request { return rbmWebhook(body, "x") }},
			{"twilio", &Twilio{AuthToken: "tok"},
				func() *http.Request { return twilioRaw(body, twilioSign("tok", public, form)) },
				func() *http.Request { return twilioRaw(body, twilioSign("other", public, form)) }},
			{"gateway", &Gateway{Secret: "sec"},
				func() *http.Request { return gatewayReq(body, "sec") }, func() *http.Request { return gatewayReq(body, "bad") }},
			{"custom", custom(t), tokenReq("tok3n"), tokenReq("x")},
		}
		for i, tg := range targets {
			if which != 0 && int(which)%len(targets) != i {
				continue
			}
			rs, err := tg.p.ParseReceipts(tg.signed())
			if err == nil {
				for _, r := range rs {
					if r.Status == "" {
						t.Fatalf("%s: receipt without a status: %+v", tg.name, r)
					}
				}
			}
			if rs, err := tg.p.ParseReceipts(tg.unsigned()); err == nil && len(rs) > 0 {
				t.Fatalf("%s: unauthenticated request gave receipts %+v", tg.name, rs)
			}
		}
	})
}

func twilioRaw(body, sig string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://pulse:7070/v1/webhooks/providers/7?t=1", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "pulse.example.com")
	r.Header.Set("X-Twilio-Signature", sig)
	return r
}

func gatewayReq(body, secret string) *http.Request {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "." + body))
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/providers/9", strings.NewReader(body))
	r.Header.Set("X-Pulse-Signature", "t="+ts+",v1="+hex.EncodeToString(m.Sum(nil)))
	return r
}

// FuzzCustomGateway decodes arbitrary custom-gateway setups (the wizard's request template) and
// reads arbitrary answers and reports with them: nothing may panic.
func FuzzCustomGateway(f *testing.F) {
	f.Add(`{"method":"POST","url":"https://api.example.com/send","content_type":"application/json","body":"{\"to\":\"{{message.to}}\",\"text\":\"{{message.text | json}}\"}","success":{"status_min":200,"status_max":299,"id_path":"data.id","error_path":"error.message"},"receipt":{"items_path":"events","id_path":"msg_id","status_path":"state","statuses":{"dlvrd":"delivered"},"verify":"query","verify_name":"token"}}`,
		200, `{"data":{"id":"P1"},"events":[{"msg_id":"P1","state":"DLVRD"}]}`)
	f.Add(`{"method":"GET","url":"https://api.example.com/{{message.to}}","receipt":{"id_path":"id","status_path":"s","statuses":{"d":"bounced"},"verify":"hmac","verify_name":"X-Sig"}}`,
		500, `id=1&s=d`)
	f.Fuzz(func(t *testing.T, raw string, code int, body string) {
		c, err := ParseCustomRequest(raw)
		if err != nil {
			return
		}
		_ = c.Validate()
		g := &Gateway{Custom: &c}
		_, _ = g.CheckAnswer(code, []byte(body))
		if rs, err := g.CheckReport([]byte(body)); err == nil {
			for _, r := range rs {
				if r.ProviderMessageID == "" || r.Status == "" {
					t.Fatalf("receipt %+v", r)
				}
			}
		}
	})
}
