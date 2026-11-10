package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"pulse/internal/channel"
)

func twilioOutbound() Outbound {
	return Outbound{MessageID: "m1", Channel: channel.SMS, To: "+919812345678",
		Rendered: channel.Rendered{Content: channel.Content{Text: "Hello Asha"}}}
}

func TestTwilioRequest(t *testing.T) {
	srv, got := fakeAPI(t, 201, `{"sid":"SM123","status":"queued"}`)
	p := &Twilio{HTTP: srv.Client(), BaseURL: srv.URL, AccountSID: "AC1", AuthToken: "tok", From: "+14155550123",
		StatusCallback: "https://pulse.example.com/v1/webhooks/providers/7"}
	res, err := p.Send(context.Background(), twilioOutbound())
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderMessageID != "SM123" {
		t.Fatalf("id = %q", res.ProviderMessageID)
	}
	if got.Method != http.MethodPost || got.Path != "/2010-04-01/Accounts/AC1/Messages.json" {
		t.Fatalf("request %s %s", got.Method, got.Path)
	}
	if !got.HasBasicAuth || got.User != "AC1" || got.Pass != "tok" {
		t.Fatalf("basic auth %q:%q", got.User, got.Pass)
	}
	form, _ := url.ParseQuery(string(got.Body))
	if form.Get("To") != "+919812345678" || form.Get("Body") != "Hello Asha" || form.Get("From") != "+14155550123" ||
		form.Has("MessagingServiceSid") || form.Get("StatusCallback") != "https://pulse.example.com/v1/webhooks/providers/7" {
		t.Fatalf("form %v", form)
	}

	p.MessagingServiceSID = "MG1"
	if _, err := p.Send(context.Background(), twilioOutbound()); err != nil {
		t.Fatal(err)
	}
	form, _ = url.ParseQuery(string(got.Body))
	if form.Get("MessagingServiceSid") != "MG1" || form.Has("From") {
		t.Fatalf("messaging service form %v", form)
	}
}

func TestTwilioErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"invalid number", 400, `{"code":21211,"message":"The 'To' number is not valid."}`, "permanent"},
		{"not mobile", 400, `{"code":21614,"message":"not a mobile number"}`, "permanent"},
		{"other 4xx", 400, `{"code":21602,"message":"Message body is required."}`, "permanent"},
		{"bad credentials", 401, `{"code":20003,"message":"Authenticate"}`, "permanent"},
		{"rate limited", 429, `{"code":20429,"message":"Too Many Requests"}`, "transient"},
		{"server error", 503, `oops`, "transient"},
		{"2xx without sid", 200, `{}`, "permanent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tc.status, tc.body)
			p := &Twilio{HTTP: srv.Client(), BaseURL: srv.URL, AccountSID: "AC1", AuthToken: "tok", From: "+1"}
			_, err := p.Send(context.Background(), twilioOutbound())
			if c := errClass(err); c != tc.want {
				t.Fatalf("got %s (%v), want %s", c, err, tc.want)
			}
		})
	}
}

func twilioSign(token, u string, form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := u
	for _, k := range keys {
		s += k + form.Get(k)
	}
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte(s))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func twilioCallback(form url.Values, sig string) *http.Request {
	// The app runs behind a proxy: it sees http://pulse:7070, Twilio called https://pulse.example.com.
	r := httptest.NewRequest(http.MethodPost, "http://pulse:7070/v1/webhooks/providers/7?t=1", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "pulse.example.com")
	r.Header.Set("X-Twilio-Signature", sig)
	return r
}

func TestTwilioReceipts(t *testing.T) {
	p := &Twilio{AuthToken: "tok"}
	const public = "https://pulse.example.com/v1/webhooks/providers/7?t=1"
	for _, tc := range []struct {
		name   string
		form   url.Values
		want   []Receipt
		reason string
	}{
		{"delivered", url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"delivered"}, "AccountSid": {"AC1"}},
			[]Receipt{{ProviderMessageID: "SM1", Status: "delivered"}}, ""},
		{"undelivered", url.Values{"MessageSid": {"SM2"}, "MessageStatus": {"undelivered"}, "ErrorCode": {"30003"}},
			[]Receipt{{ProviderMessageID: "SM2", Status: "failed", Reason: "twilio error 30003", Unreachable: true}}, ""},
		{"failed", url.Values{"MessageSid": {"SM3"}, "MessageStatus": {"failed"}, "ErrorCode": {"30008"}},
			[]Receipt{{ProviderMessageID: "SM3", Status: "failed", Reason: "twilio error 30008"}}, ""},
		{"sent is not final", url.Values{"MessageSid": {"SM4"}, "MessageStatus": {"sent"}}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.ParseReceipts(twilioCallback(tc.form, twilioSign("tok", public, tc.form)))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %+v, want %+v", got[i], tc.want[i])
				}
			}
		})
	}

	form := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"delivered"}}
	good := twilioSign("tok", public, form)
	tampered := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"failed"}}
	for name, r := range map[string]*http.Request{
		"tampered body":  twilioCallback(tampered, good),
		"wrong key":      twilioCallback(form, twilioSign("other", public, form)),
		"internal url":   twilioCallback(form, twilioSign("tok", "http://pulse:7070/v1/webhooks/providers/7?t=1", form)),
		"missing header": twilioCallback(form, ""),
	} {
		if _, err := p.ParseReceipts(r); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: err = %v, want ErrUnauthorized", name, err)
		}
	}
}
