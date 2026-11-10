package tracking

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"pulse/internal/auth"
)

func codec(t *testing.T) *Codec {
	box, err := auth.NewBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	return New(box, strings.Repeat("ab", 32), "https://pulse.test/")
}

func TestTokenRoundTripAndPrivacy(t *testing.T) {
	c := codec(t)
	tok := c.Encode(Token{TenantID: 3, ExternalID: "asha@example.com", MessageID: "9:send", Channel: "email", TemplateID: 2})
	if strings.Contains(tok, "asha") {
		t.Fatal("external id readable in the token")
	}
	got, err := c.Decode(tok)
	if err != nil || got.ExternalID != "asha@example.com" || got.MessageID != "9:send" {
		t.Fatalf("decode = %+v %v", got, err)
	}
	if _, err := c.Decode(tok[:len(tok)-2] + "xx"); err == nil {
		t.Error("tampered token accepted")
	}
}

func TestInstrument(t *testing.T) {
	c := codec(t)
	tok := c.Encode(Token{TenantID: 1, ExternalID: "u1", MessageID: "m1", Channel: "email"})
	in := `<html><body><a href="https://shop.test/p?a=1&amp;b=2">Shop</a> <a href='mailto:x@y.z'>mail</a></body></html>`
	out := c.Instrument(in, tok)

	hrefs := regexp.MustCompile(`href="([^"]+)"`).FindAllStringSubmatch(out, -1)
	click, _ := url.Parse(html.UnescapeString(hrefs[0][1]))
	if !strings.HasPrefix(click.Path, "/t/c/") || click.Query().Get("u") != "https://shop.test/p?a=1&b=2" {
		t.Errorf("click link = %s", hrefs[0][1])
	}
	if !c.VerifyClick(strings.TrimPrefix(click.Path, "/t/c/"), click.Query().Get("u"), click.Query().Get("s")) {
		t.Error("click signature doesn't verify")
	}
	if c.VerifyClick(strings.TrimPrefix(click.Path, "/t/c/"), "https://evil.test", click.Query().Get("s")) {
		t.Error("signature accepted for a different destination (open redirect)")
	}
	if !strings.Contains(out, `href='mailto:x@y.z'`) {
		t.Error("non-http links must stay untouched")
	}
	if !strings.Contains(out, ">Unsubscribe</a>") || !strings.Contains(out, "/t/o/"+tok+".gif") {
		t.Error("footer or pixel missing")
	}
	if i, j := strings.Index(out, "/t/o/"), strings.Index(out, "</body>"); i > j {
		t.Error("pixel must sit inside the body")
	}

	// A template that places the unsubscribe link itself gets no second one, and that link isn't wrapped.
	own := `<p><a href="` + c.UnsubscribeURL(tok) + `">Leave</a></p>`
	out = c.Instrument(own, tok)
	if strings.Count(out, "/u/") != 1 || strings.Contains(out, "/t/c/") {
		t.Errorf("own unsubscribe link handled wrongly: %s", out)
	}
	if h := c.Headers(tok); h["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" || !strings.HasPrefix(h["List-Unsubscribe"], "<https://pulse.test/u/") {
		t.Errorf("headers = %v", h)
	}
}
