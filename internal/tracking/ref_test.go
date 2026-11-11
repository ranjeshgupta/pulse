package tracking

import (
	"regexp"
	"strings"
	"testing"

	"pulse/internal/auth"
)

func refCodec(t *testing.T) *Codec {
	box, err := auth.NewBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	return New(box, strings.Repeat("ab", 32), "https://pulse.example.com")
}

func TestRefRoundTrip(t *testing.T) {
	c := refCodec(t)
	for _, r := range []Ref{
		{TenantID: 7, ContactID: 123456, MessageID: "bc12:1731000000:123456", TemplateID: 34, BroadcastID: 12, Run: 1731000000, Variant: "B"},
		{TenantID: 99999, ContactID: 4000000000, MessageID: "98765432:send_3:j2", TemplateID: 3, JourneyID: 77},
		{TenantID: 1, ContactID: 1, MessageID: "bc1:5:1:sms", BroadcastID: 1, Run: 5},
	} {
		s := c.Ref(r)
		if len(s) > 80 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(s) {
			t.Errorf("ref %q: too long or bad characters", s)
		}
		got, err := c.ParseRef("<" + s + "@mail.example.com>")
		if err != nil || got != r {
			t.Errorf("round trip: %+v %v, want %+v", got, err, r)
		}
		if got.Props()["message_id"] != r.MessageID {
			t.Error("props")
		}
	}
}

func TestRefRefusesForgeries(t *testing.T) {
	c := refCodec(t)
	s := c.Ref(Ref{TenantID: 7, ContactID: 1, MessageID: "x"})
	for _, bad := range []string{"", "p1", s[:len(s)-1] + "A", "p1AAAA." + s[strings.IndexByte(s, '.')+1:], strings.Replace(s, "p1", "p2", 1)} {
		if _, err := c.ParseRef(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	other := refCodec(t)
	other.mac = []byte("another server")
	if _, err := other.ParseRef(s); err == nil {
		t.Error("a reference signed by another server key was accepted")
	}
}
