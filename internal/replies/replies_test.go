package replies

import (
	"bytes"
	"mime/multipart"
	"strings"
	"testing"
)

func TestAddress(t *testing.T) {
	if a, ok := Address("replies@in.example.com", "abc"); !ok || a != "replies+rabc@in.example.com" {
		t.Errorf("%q %v", a, ok)
	}
	if _, ok := Address("not an address", "abc"); ok {
		t.Error("bad address accepted")
	}
	if c := NewCode(); len(c) != 16 || strings.ToLower(c) != c {
		t.Errorf("code %q", c)
	}
}

func TestParseProviders(t *testing.T) {
	code := "0123456789abcdef"
	// SendGrid Inbound Parse: multipart form.
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("to", "Replies <replies+r"+code+"@in.example.com>")
	_ = mw.WriteField("from", "Asha <asha@x.test>")
	_ = mw.WriteField("subject", "Re: Welcome")
	_ = mw.WriteField("text", "Yes please")
	mw.Close()
	r, err := Parse(mw.FormDataContentType(), b.Bytes())
	if err != nil || len(r.Codes) != 1 || r.Codes[0] != code || r.Text != "Yes please" || r.Subject != "Re: Welcome" {
		t.Errorf("sendgrid: %+v %v", r, err)
	}
	// Mailgun: urlencoded, the recipient upper-cased by a mail server.
	r, err = Parse("application/x-www-form-urlencoded", []byte("recipient=REPLIES%2BR"+strings.ToUpper(code)+"%40in.example.com&sender=a%40x.test&stripped-text=ok"))
	if err != nil || len(r.Codes) != 1 || r.Codes[0] != code || r.Text != "ok" {
		t.Errorf("mailgun: %+v %v", r, err)
	}
	// Postmark: JSON with MailboxHash.
	r, err = Parse("application/json", []byte(`{"From":"a@x.test","Subject":"Re","TextBody":"hi","MailboxHash":"r`+code+`","ToFull":[{"Email":"replies@in.example.com"}]}`))
	if err != nil || len(r.Codes) != 1 || r.Codes[0] != code {
		t.Errorf("postmark: %+v %v", r, err)
	}
	// No code: nothing to match.
	r, _ = Parse("application/json", []byte(`{"to":"hello@x.test","text":"hi"}`))
	if len(r.Codes) != 0 {
		t.Errorf("codes %v", r.Codes)
	}
}
