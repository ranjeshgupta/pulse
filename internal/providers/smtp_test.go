package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"

	"pulse/internal/channel"
)

type part struct {
	contentType, disposition, body string
	params                         map[string]string
}

// readParts walks a multipart body (recursively) and returns its leaf parts, decoded.
func readParts(t *testing.T, contentType string, body io.Reader) (string, []part) {
	t.Helper()
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mt, "multipart/") {
		return mt, nil
	}
	var out []part
	mr := multipart.NewReader(body, params["boundary"])
	for {
		p, err := mr.NextRawPart() // raw: decode the transfer encodings ourselves
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		ct := p.Header.Get("Content-Type")
		if inner, _, _ := mime.ParseMediaType(ct); strings.HasPrefix(inner, "multipart/") {
			_, sub := readParts(t, ct, p)
			out = append(out, part{contentType: inner})
			out = append(out, sub...)
			continue
		}
		var r io.Reader = p
		switch p.Header.Get("Content-Transfer-Encoding") {
		case "quoted-printable":
			r = quotedprintable.NewReader(p)
		case "base64":
			r = base64.NewDecoder(base64.StdEncoding, p)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		mt, ps, _ := mime.ParseMediaType(ct)
		out = append(out, part{contentType: mt, params: ps, disposition: p.Header.Get("Content-Disposition"), body: string(b)})
	}
	return mt, out
}

func base() Mail {
	return Mail{ID: "42:send", From: "Acme <hello@acme.test>", To: "asha@example.com", Subject: "Hi Asha ✨",
		HTML: "<p>Hi Asha, " + strings.Repeat("long line ", 200) + "</p>", Text: "Hi Asha,\nsee https://shop.test/sale",
		Headers: map[string]string{"List-Unsubscribe": "<https://p.test/u/x>"}, Date: time.Unix(1_800_000_000, 0)}
}

func TestBuildMessageAlternative(t *testing.T) {
	raw, err := BuildMessage(base())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line longer than RFC 5322 allows: %d", len(line))
		}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if subj, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); subj != "Hi Asha ✨" || msg.Header.Get("List-Unsubscribe") != "<https://p.test/u/x>" {
		t.Errorf("headers: %v", msg.Header)
	}
	top, parts := readParts(t, msg.Header.Get("Content-Type"), msg.Body)
	if top != "multipart/alternative" || len(parts) != 2 {
		t.Fatalf("top %s, parts %+v", top, parts)
	}
	// Text first, then HTML: clients show the last part they understand.
	if parts[0].contentType != "text/plain" || parts[0].body != "Hi Asha,\r\nsee https://shop.test/sale" || parts[0].params["charset"] != "utf-8" {
		t.Errorf("text part = %+v", parts[0])
	}
	if parts[1].contentType != "text/html" || !strings.HasPrefix(parts[1].body, "<p>Hi Asha, long line") {
		t.Errorf("html part = %+v", parts[1].contentType)
	}
}

func TestBuildMessageMixedWithAttachments(t *testing.T) {
	m := base()
	pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0, 1, 2, 250}, 100)...)
	m.Attachments = []Attachment{
		{Filename: "Q3 report.pdf", ContentType: "application/pdf", Data: pdf},
		{Filename: "résumé.csv", ContentType: "text/csv; charset=utf-8", Data: []byte("a,b\n1,2\n")},
	}
	raw, err := BuildMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := mail.ReadMessage(bytes.NewReader(raw))
	top, parts := readParts(t, msg.Header.Get("Content-Type"), msg.Body)
	if top != "multipart/mixed" {
		t.Fatalf("top = %s", top)
	}
	var kinds []string
	for _, p := range parts {
		kinds = append(kinds, p.contentType)
	}
	if strings.Join(kinds, ",") != "multipart/alternative,text/plain,text/html,application/pdf,text/csv" {
		t.Fatalf("structure = %v", kinds)
	}
	if parts[3].body != string(pdf) || parts[3].params["name"] != "Q3 report.pdf" {
		t.Errorf("pdf part: name %q, %d bytes", parts[3].params["name"], len(parts[3].body))
	}
	_, dp, err := mime.ParseMediaType(parts[3].disposition)
	if err != nil || !strings.HasPrefix(parts[3].disposition, "attachment;") || dp["filename"] != "Q3 report.pdf" {
		t.Errorf("pdf disposition = %q", parts[3].disposition)
	}
	_, dp, _ = mime.ParseMediaType(parts[4].disposition)
	if dp["filename"] != "résumé.csv" || parts[4].body != "a,b\n1,2\n" || parts[4].params["charset"] != "utf-8" {
		t.Errorf("csv part: %+v (%v)", parts[4], dp)
	}
}

func TestBuildMessageHTMLOnlyAndInjection(t *testing.T) {
	m := base()
	m.Text = ""
	raw, err := BuildMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := mail.ReadMessage(bytes.NewReader(raw))
	if ct := msg.Header.Get("Content-Type"); ct != `text/html; charset="utf-8"` || msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Errorf("html-only content type = %s", ct)
	}
	m.Headers = map[string]string{"X-Evil": "a\r\nBcc: x@evil.test"}
	if _, err := BuildMessage(m); err == nil {
		t.Error("a header with a line break was accepted")
	}
	m = base()
	m.To = "asha@example.com\r\nBcc: x@evil.test"
	if _, err := BuildMessage(m); err == nil {
		t.Error("an address with a line break was accepted")
	}
}

func TestGatewaySendsTextAndAttachmentLinks(t *testing.T) {
	var got struct {
		Text        string `json:"text"`
		Attachments []struct {
			Filename, URL string
			Size          int
		} `json:"attachments"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	g := &Gateway{HTTP: srv.Client(), URL: srv.URL, Secret: "s"}
	o := Outbound{MessageID: "m1", Channel: "email", To: "a@b.test",
		Rendered:    channel.Rendered{Subject: "s", HTML: "<p>x</p>", Content: channel.Content{Text: "x"}},
		Attachments: []Attachment{{Filename: "a.pdf", ContentType: "application/pdf", Data: []byte("%PDF-"), URL: "https://p.test/a/k/download?t=1"}}}
	if _, err := g.Send(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got.Text != "x" || len(got.Attachments) != 1 || got.Attachments[0].URL != "https://p.test/a/k/download?t=1" || got.Attachments[0].Size != 5 {
		t.Errorf("gateway body = %+v", got)
	}
	o.Attachments[0].URL = "" // no secret key: no links, so the message can't go this way
	if _, err := g.Send(context.Background(), o); err == nil {
		t.Error("an attachment without a link was sent")
	}
}
