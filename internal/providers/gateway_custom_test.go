package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"pulse/internal/channel"
	"pulse/internal/httpauth"
	"pulse/internal/model"
	"pulse/internal/personalize"
)

func customGateway(t *testing.T, cr CustomRequest, auth *httpauth.Config, secrets map[string]string) *Gateway {
	t.Helper()
	b, _ := json.Marshal(cr)
	cfg := map[string]string{"mode": GatewayCustom, "request": string(b)}
	if auth != nil {
		a, _ := json.Marshal(auth)
		cfg["auth"] = string(a)
	}
	p, err := New("http_sms", Deps{AllowPrivate: true, Tokens: &httpauth.MemoryCache{}, Scope: "3:9"}, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Gateway)
}

func smsOutbound() Outbound {
	return Outbound{MessageID: "m-1", Channel: channel.SMS, To: "+919812345678",
		Rendered: channel.Rendered{Content: channel.Content{Text: `Hi "Asha" & co`, SenderID: "ACMEIN", DLTTemplateID: "1107"}},
		Vars: &personalize.Vars{Contact: model.Contact{ExternalID: "u-1", Attrs: map[string]any{"first_name": "Asha", "city": "Pune"}},
			Event: map[string]any{"order_id": "A-9"}}}
}

func TestCustomRequestRendering(t *testing.T) {
	var got struct {
		method, path, query, ct, body string
		hdr                           http.Header
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query, got.ct, got.body, got.hdr = r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Content-Type"), string(b), r.Header
		fmt.Fprint(w, `{"result":{"ok":true,"msg":{"id":"P-77"}}}`)
	}))
	defer srv.Close()
	g := customGateway(t, CustomRequest{Method: "POST", URL: srv.URL + "/v2/{{contact.city}}/send?route=tx&sender={{message.sender_id}}",
		Headers: []httpauth.Header{{Name: "X-Customer", Value: "{{contact.external_id}}"}},
		Body:    `{"to":"{{message.to}}","text":"{{message.text}}","name":"{{contact.first_name}}","order":"{{event.order_id}}","dlt":"{{message.dlt_template_id}}"}`,
		Success: SuccessRule{JSONPath: "result.ok", JSONValue: "true"}, IDPath: "result.msg.id"}, nil, nil)
	res, err := g.Send(context.Background(), smsOutbound())
	if err != nil || res.ProviderMessageID != "P-77" {
		t.Fatalf("send %+v %v", res, err)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body isn't JSON: %s", got.body)
	}
	if body["text"] != `Hi "Asha" & co` || body["name"] != "Asha" || body["order"] != "A-9" || body["dlt"] != "1107" || body["to"] != "+919812345678" {
		t.Errorf("body %v", body)
	}
	if got.path != "/v2/Pune/send" || got.query != "route=tx&sender=ACMEIN" || got.hdr.Get("X-Customer") != "u-1" || got.ct != "application/json" {
		t.Errorf("request %s %s %s %v", got.path, got.query, got.ct, got.hdr)
	}

	// Form bodies are percent-encoded per value; a value can't add a field.
	g = customGateway(t, CustomRequest{Method: "POST", URL: srv.URL + "/f", ContentType: "application/x-www-form-urlencoded",
		Body: "to={{message.to}}&msg={{message.text}}&fixed=a b"}, nil, nil)
	o := smsOutbound()
	o.Rendered.Content.Text = "x&admin=1"
	if _, err := g.Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	form, _ := url.ParseQuery(got.body)
	if form.Get("msg") != "x&admin=1" || form.Get("admin") != "" || form.Get("to") != "+919812345678" || form.Get("fixed") != "a b" {
		t.Errorf("form %v", form)
	}
	// A JSON body can't be broken out of; a header can't be split.
	g = customGateway(t, CustomRequest{Method: "POST", URL: srv.URL + "/h", Headers: []httpauth.Header{{Name: "X-Name", Value: "{{contact.first_name}}"}}}, nil, nil)
	o.Vars.Contact.Attrs["first_name"] = "a\r\nX-Evil: 1"
	if _, err := g.Send(context.Background(), o); !errors.Is(err, ErrPermanent) {
		t.Errorf("header injection: %v", err)
	}
}

func TestCustomRequestValidation(t *testing.T) {
	for _, cr := range []CustomRequest{
		{Method: "POST", URL: "https://{{contact.city}}.example.com/x"},
		{Method: "POST", URL: "{{message.to}}"},
		{Method: "DELETE", URL: "https://api.example.com"},
		{Method: "POST", URL: "ftp://api.example.com"},
		{Method: "POST", URL: "https://api.example.com", Body: "{{nope.x}}"},
		{Method: "POST", URL: "https://api.example.com", Success: SuccessRule{StatusMin: 300, StatusMax: 200}},
		{Method: "POST", URL: "https://api.example.com", Receipt: &ReceiptMapping{IDPath: "id", StatusPath: "s", Statuses: map[string]string{"x": "sent"}, Verify: "query", VerifyName: "t"}},
		{Method: "POST", URL: "https://api.example.com", Receipt: &ReceiptMapping{IDPath: "id", StatusPath: "s", Statuses: map[string]string{"x": "read"}}},
		{Method: "POST", URL: "https://api.example.com", Receipt: &ReceiptMapping{IDPath: "id", StatusPath: "s", Statuses: map[string]string{"ok": "delivered", "OK": "failed"}, Verify: "query", VerifyName: "t"}},
	} {
		if err := cr.Validate(); err == nil {
			t.Errorf("accepted %+v", cr)
		}
	}
	ok := CustomRequest{Method: "get", URL: "https://api.example.com/{{message.to}}?a={{contact.x}}"}
	if err := ok.Validate(); err != nil {
		t.Error(err)
	}
}

// A status matches exactly first, then ignoring case, the same way every time.
func TestReceiptStatusMatching(t *testing.T) {
	m := &ReceiptMapping{IDPath: "id", StatusPath: "s", Statuses: map[string]string{"ok": "delivered", "OK": "failed", "Dlvr": "read"}}
	for range 20 {
		for st, want := range map[string]string{"ok": "delivered", "OK": "failed", "Ok": "failed", "DLVR": "read", "x": ""} {
			if got := m.status(st); got != want {
				t.Fatalf("status(%q) = %q, want %q", st, got, want)
			}
		}
	}
}

func TestCustomRequestSuccessRules(t *testing.T) {
	cr := CustomRequest{Success: SuccessRule{StatusMin: 200, StatusMax: 202, JSONPath: "status", JSONValue: "queued"}, IDPath: "ids[0]"}
	if r, err := cr.Result(202, []byte(`{"status":"QUEUED","ids":["x1"]}`), nil); err != nil || r.ProviderMessageID != "x1" {
		t.Errorf("ok: %+v %v", r, err)
	}
	if _, err := cr.Result(200, []byte(`{"status":"rejected"}`), nil); !errors.Is(err, ErrPermanent) {
		t.Errorf("json value mismatch: %v", err)
	}
	if _, err := cr.Result(204, nil, nil); !errors.Is(err, ErrPermanent) {
		t.Errorf("2xx outside range: %v", err)
	}
	if _, err := cr.Result(503, nil, nil); err == nil || !Transient(err) {
		t.Errorf("503: %v", err)
	}
	if _, err := cr.Result(410, nil, nil); !errors.Is(err, ErrUnreachable) {
		t.Errorf("410: %v", err)
	}
}

func TestCustomReceipts(t *testing.T) {
	mapping := &ReceiptMapping{ItemsPath: "events", IDPath: "msg_id", StatusPath: "state", ReasonPath: "err.text", CodePath: "err.code",
		Statuses: map[string]string{"DLVRD": "delivered", "SEEN": "read", "FAILED": "failed", "NOT_ON_WA": "undeliverable"},
		Verify:   "hmac", VerifyName: "X-Signature"}
	g := customGateway(t, CustomRequest{Method: "POST", URL: "https://api.example.com/send", Receipt: mapping}, nil, map[string]string{"receipt_token": "rt-secret"})
	body := `{"events":[{"msg_id":"P1","state":"dlvrd"},{"msg_id":"P2","state":"FAILED","err":{"code":17,"text":"DND"}},
		{"msg_id":"P3","state":"queued"},{"msg_id":"P4","state":"NOT_ON_WA"}]}`
	sign := func(b string) string {
		m := hmac.New(sha256.New, []byte("rt-secret"))
		m.Write([]byte(b))
		return "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/providers/9", strings.NewReader(body))
	req.Header.Set("X-Signature", sign(body))
	rs, err := g.ParseReceipts(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{P1 delivered false   } {P2 failed false code 17: DND  } {P4 failed true the provider reported a failure (NOT_ON_WA)  }]`
	var parts []string
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("{%s %s %v %s %s %s}", r.ProviderMessageID, r.Status, r.Unreachable, r.Reason, r.Code, r.Kind))
	}
	if "["+strings.Join(parts, " ")+"]" != want {
		t.Errorf("receipts\n got [%s]\nwant %s", strings.Join(parts, " "), want)
	}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Signature", sign(body+" "))
	if _, err := g.ParseReceipts(req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("bad signature: %v", err)
	}
	// Fail closed: no receipt token saved, no receipts.
	g = customGateway(t, CustomRequest{Method: "POST", URL: "https://api.example.com/send",
		Receipt: &ReceiptMapping{IDPath: "id", StatusPath: "s", Statuses: map[string]string{"d": "delivered"}, Verify: "query", VerifyName: "token"}}, nil, nil)
	req = httptest.NewRequest(http.MethodPost, "/?token=", strings.NewReader(`{"id":"1","s":"d"}`))
	if _, err := g.ParseReceipts(req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("no token saved: %v", err)
	}
	g.ReceiptToken = "tok"
	req = httptest.NewRequest(http.MethodPost, "/?token=tok", strings.NewReader(`id=1&s=d`))
	if rs, err := g.ParseReceipts(req); err != nil || len(rs) != 1 || rs[0].Status != "delivered" {
		t.Errorf("form receipt: %v %v", rs, err)
	}
}

// The login flow on the gateway: one login, the token reused, refreshed once after a 401.
func TestGatewayLoginFlowAndPreview(t *testing.T) {
	var logins atomic.Int32
	var revoked atomic.Value
	revoked.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["user"] != "acme" || in["key"] != "k-secret-value" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(w, `{"session":"S%d","valid_for":600}`, logins.Add(1))
		case "/send":
			tok := r.Header.Get("X-Session")
			if tok == "" || tok == revoked.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(w, `{"id":"%s-ok"}`, tok)
		}
	}))
	defer srv.Close()
	auth := &httpauth.Config{Type: httpauth.Login, Login: &httpauth.LoginRequest{URL: srv.URL + "/auth",
		Body: `{"user":"{{secrets.user}}","key":"{{secrets.key}}"}`, TokenFrom: "json", TokenPath: "session",
		ExpiryFrom: "json", ExpiryPath: "valid_for"}, Inject: &httpauth.Inject{In: "header", Name: "X-Session"}}
	secrets := map[string]string{"user": "acme", "key": "k-secret-value"}
	g := customGateway(t, CustomRequest{Method: "POST", URL: srv.URL + "/send", Body: `{"to":"{{message.to}}"}`, IDPath: "id"}, auth, secrets)
	for i, want := range []string{"S1-ok", "S1-ok"} {
		if r, err := g.Send(context.Background(), smsOutbound()); err != nil || r.ProviderMessageID != want {
			t.Fatalf("send %d: %+v %v", i, r, err)
		}
	}
	revoked.Store("S1")
	if r, err := g.Send(context.Background(), smsOutbound()); err != nil || r.ProviderMessageID != "S2-ok" || logins.Load() != 2 {
		t.Errorf("after 401: %+v %v, %d logins", r, err, logins.Load())
	}
	res, err := g.TestAuth(context.Background())
	if err != nil || res.Token != "••••" || res.Expires.IsZero() {
		t.Errorf("test auth %+v %v", res, err)
	}

	method, target, headers, body := g.PreviewRequest(smsOutbound())
	if method != "POST" || target != srv.URL+"/send" || headers["X-Session"] != "{{token}}" || body != `{"to":"+919812345678"}` {
		t.Errorf("preview %s %s %v %s", method, target, headers, body)
	}
	// Wrong login credentials: permanent (provider_auth), not retried for hours.
	bad := customGateway(t, CustomRequest{Method: "POST", URL: srv.URL + "/send"}, auth, map[string]string{"user": "acme", "key": "nope"})
	if _, err := bad.Send(context.Background(), smsOutbound()); !errors.Is(err, ErrPermanent) || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("bad login: %v", err)
	}
}

func TestStandardGatewayWithAPIKeyAndPreviewMasks(t *testing.T) {
	a, _ := json.Marshal(httpauth.Config{Type: httpauth.APIKey, KeyName: "X-Api-Key"})
	p, err := New("http_sms", Deps{AllowPrivate: true}, map[string]string{"url": "https://gw.example.com/pulse", "auth": string(a)},
		map[string]string{"signing_secret": "sig-secret", httpauth.SecretAPIKey: "key-12345"})
	if err != nil {
		t.Fatal(err)
	}
	_, target, headers, body := p.(*Gateway).PreviewRequest(smsOutbound())
	if target != "https://gw.example.com/pulse" || headers["X-Api-Key"] != "••••" || !strings.Contains(headers["X-Pulse-Signature"], "v1=") ||
		!strings.Contains(body, `"to":"+919812345678"`) {
		t.Errorf("preview %s %v %s", target, headers, body)
	}
	if _, err := New("http_sms", Deps{}, map[string]string{"url": "https://gw.example.com"}, nil); err == nil {
		t.Error("standard mode without a signing secret accepted")
	}
}
