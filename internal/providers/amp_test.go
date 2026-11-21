package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"

	"pulse/internal/channel"
)

const ampDoc = "<!doctype html><html ⚡4email><head><meta charset=\"utf-8\"></head><body><p>AMP hi</p></body></html>"

func partTypes(t *testing.T, raw []byte) (string, []part) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return readParts(t, msg.Header.Get("Content-Type"), msg.Body)
}

func partKinds(ps []part) string {
	var k []string
	for _, p := range ps {
		k = append(k, p.contentType)
	}
	return strings.Join(k, ",")
}

// The AMP part sits between the text and HTML parts (clients show the last one they understand).
func TestBuildMessageWithAMP(t *testing.T) {
	m := base()
	m.AMP = ampDoc
	raw, err := BuildMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	top, parts := partTypes(t, raw)
	if top != "multipart/alternative" || partKinds(parts) != "text/plain,text/x-amp-html,text/html" {
		t.Fatalf("%s: %s", top, partKinds(parts))
	}
	if parts[1].body != ampDoc || parts[1].params["charset"] != "utf-8" {
		t.Errorf("amp part = %+v", parts[1])
	}
	// With attachments: mixed(alternative(text, amp, html), files).
	m.Attachments = []Attachment{{Filename: "a.txt", ContentType: "text/plain", Data: []byte("x")}}
	raw, _ = BuildMessage(m)
	if _, parts := partTypes(t, raw); partKinds(parts) != "multipart/alternative,text/plain,text/x-amp-html,text/html,text/plain" {
		t.Errorf("mixed: %s", partKinds(parts))
	}
	// Without a text part the AMP part still goes in an alternative.
	m = base()
	m.Text, m.AMP = "", ampDoc
	raw, _ = BuildMessage(m)
	if top, parts := partTypes(t, raw); top != "multipart/alternative" || partKinds(parts) != "text/x-amp-html,text/html" {
		t.Errorf("no text: %s %s", top, partKinds(parts))
	}
}

// SMTP sends the AMP part for every relay but Postmark (which doesn't support AMP); the gateway
// and sandbox get it in their JSON.
func TestProvidersSendAMP(t *testing.T) {
	o := outbound(1)
	o.Rendered.Content = channel.Content{Text: "plain", AMP: ampDoc}
	for _, c := range []struct {
		host    string
		service string
		want    string
	}{
		{"", "", "text/plain,text/x-amp-html,text/html"},
		{"", "ses", "text/plain,text/x-amp-html,text/html"},
		{"", "sendgrid", "text/plain,text/x-amp-html,text/html"},
		{"", "sparkpost", "text/plain,text/x-amp-html,text/html"},
		{"", "mailgun", "text/plain,text/x-amp-html,text/html"},
		{"", "postmark", "text/plain,text/html"},
	} {
		f := newFakeSMTP(t)
		f.start(t)
		host, port, _ := net.SplitHostPort(f.addr())
		p, err := New("smtp", Deps{AllowPrivate: true}, map[string]string{"host": host, "port": port, "from": "Acme <hello@acme.test>", "service": c.service}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Send(context.Background(), o); err != nil {
			t.Fatalf("%s: %v", c.service, err)
		}
		_, _, msgs := f.stats()
		if len(msgs) != 1 {
			t.Fatalf("%s: %d messages", c.service, len(msgs))
		}
		if _, parts := partTypes(t, []byte(msgs[0])); partKinds(parts) != c.want {
			t.Errorf("%s: %s, want %s", c.service, partKinds(parts), c.want)
		}
		ok, why := SupportsAMP("smtp", map[string]string{"service": c.service})
		if ok != strings.Contains(c.want, "amp") || (!ok && why == "") {
			t.Errorf("%s: SupportsAMP = %v %q", c.service, ok, why)
		}
	}
	for _, k := range []string{"gmail_smtp", "gworkspace_smtp", "gworkspace_relay", "sandbox_email", "http_email"} {
		if ok, _ := SupportsAMP(k, nil); !ok {
			t.Errorf("%s: no AMP", k)
		}
	}
	if ok, why := SupportsAMP("twilio", nil); ok || why == "" {
		t.Error("twilio sends AMP?")
	}

	var got struct {
		Content struct {
			AMP string `json:"amp"`
		} `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewDecoder(r.Body).Decode(&got) }))
	defer srv.Close()
	g := &Gateway{HTTP: srv.Client(), URL: srv.URL, Secret: "s"}
	if _, err := g.Send(context.Background(), Outbound{MessageID: "m", Channel: "email", To: "a@b.test", Rendered: o.Rendered}); err != nil {
		t.Fatal(err)
	}
	if got.Content.AMP != ampDoc {
		t.Errorf("gateway content.amp = %q", got.Content.AMP)
	}
	if v := MessageVars(Outbound{Rendered: o.Rendered})["amp"]; v != ampDoc {
		t.Errorf("message.amp = %v", v)
	}
}
