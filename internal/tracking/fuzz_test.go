package tracking

import (
	"encoding/base64"
	"strings"
	"testing"
)

// FuzzDecode feeds garbage to every token and reference decoder: all must refuse it without
// panicking. Garbage that is properly sealed or signed (as if the key had leaked or a bug had
// sealed bad JSON) must not panic either.
func FuzzDecode(f *testing.F) {
	c := refCodec(&testing.T{})
	f.Add(c.Encode(Token{TenantID: 1, ExternalID: "a@b.co", MessageID: "m"}), []byte(`{"t":1,"e":"x"}`))
	f.Add(c.Ref(Ref{TenantID: 7, ContactID: 9, BroadcastID: 1, Run: 2, Variant: "B"}), []byte{1, 2, 3})
	f.Add(c.EncodeAMP(AMPToken{Token: Token{TenantID: 1}, Block: "poll"}), []byte(ampPrefix+`{"t":1,"b":"x"}`))
	f.Add(c.EncodeOptin(OptinToken{TenantID: 1, FormID: 2, ExternalID: "x"}), []byte(optinPrefix+`{}`))
	f.Add("", []byte{})
	f.Fuzz(func(t *testing.T, s string, raw []byte) {
		if tok, err := c.Decode(s); err == nil && tok.Props() == nil {
			t.Fatal("no props")
		}
		_, _ = c.DecodeAMP(s)
		_, _ = c.DecodeOptin(s)
		if r, err := c.ParseRef(s); err == nil && r.Props() == nil {
			t.Fatal("no props")
		}
		c.VerifyClick(s, s, s)
		c.VerifySurvey(1, s, s)

		// Sealed garbage.
		sealed := c.box.SealString(string(raw))
		_, _ = c.Decode(sealed)
		_, _ = c.DecodeAMP(sealed)
		_, _ = c.DecodeOptin(sealed)
		// A correctly signed reference over garbage bytes.
		payload := refPrefix + base64.RawURLEncoding.EncodeToString(raw)
		if r, err := c.ParseRef(payload + "." + c.refMAC(payload)); err == nil {
			if r.TenantID <= 0 {
				t.Fatalf("accepted tenant %d", r.TenantID)
			}
			if back, err := c.ParseRef(c.Ref(r)); err != nil || back != r {
				t.Fatalf("round trip of %+v: %+v, %v", r, back, err)
			}
		}
	})
}

// FuzzShortenText checks short-link rewriting: no panics, at most maxShort links, and every
// recorded destination was in the text.
func FuzzShortenText(f *testing.F) {
	f.Add("Sale! https://x.y/a?b=1. And https://x.y/a?b=1 again, (https://z.co/p)")
	f.Add("https://pulse.example.com/t/s/abc https://pulse.example.com/u/x")
	f.Fuzz(func(t *testing.T, text string) {
		out, urls := ShortenText(text, "https://pulse.example.com", "abcdefghij", nil)
		if len(urls) > maxShort {
			t.Fatalf("%d links", len(urls))
		}
		for _, u := range urls {
			if !strings.Contains(text, u) {
				t.Fatalf("destination %q not in text", u)
			}
		}
		if len(urls) > 0 && !strings.Contains(out, "https://pulse.example.com/t/s/abcdefghij") {
			t.Fatalf("no short link in %q", out)
		}
	})
}
