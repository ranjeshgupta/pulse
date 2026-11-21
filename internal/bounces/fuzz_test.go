package bounces

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestClipKeepsWholeCharacters(t *testing.T) {
	s := clip("a" + strings.Repeat("é", maxReason))
	if len(s) > maxReason || !utf8.ValidString(s) {
		t.Errorf("clip: %d bytes, valid %v", len(s), utf8.ValidString(s))
	}
}

// FuzzParseReport reads arbitrary emails as bounce reports (DSN and ARF): no panics, and what it
// returns is well formed.
func FuzzParseReport(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.eml"))
	for _, p := range files {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte("Content-Type: multipart/report; report-type=delivery-status; boundary=b\r\n\r\n--b\r\nContent-Type: message/delivery-status\r\nContent-Transfer-Encoding: base64\r\n\r\nRmluYWwtUmVjaXBpZW50OiByZmM4MjI7IGFAYi5jbw0KQWN0aW9uOiBmYWlsZWQNClN0YXR1czogNS4xLjE=\r\n--b--\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		bs, err := ParseReport(raw)
		if err != nil {
			return
		}
		for _, b := range bs {
			if b.Email == "" || len(b.Reason) > maxReason {
				t.Fatalf("bad bounce %+v", b)
			}
			switch b.Kind {
			case Hard, Soft, Complaint:
			default:
				t.Fatalf("kind %q", b.Kind)
			}
		}
	})
}

// FuzzParse runs arbitrary webhook bodies through Detect, Parse and Verify: no panics, and with
// keys saved Verify refuses a body nothing signs.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		`[{"email":"a@b.co","event":"bounce","type":"bounce","reason":"550","sg_message_id":"x"}]`,
		`{"RecordType":"Bounce","Type":"HardBounce","Email":"a@b.co","Metadata":{"pulse_ref":"p1x.y"}}`,
		`{"event-data":{"event":"failed","severity":"permanent","recipient":"a@b.co","delivery-status":{"code":550}}}`,
		`{"Type":"Notification","Message":"{\"notificationType\":\"Bounce\",\"bounce\":{\"bounceType\":\"Permanent\",\"bouncedRecipients\":[{\"emailAddress\":\"a@b.co\"}]}}"}`,
		`{"Type":"SubscriptionConfirmation","SubscribeURL":"https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription"}`,
		`[{"msys":{"message_event":{"type":"bounce","bounce_class":"10","rcpt_to":"a@b.co"}}}]`,
		`{"email":"a@b.co","type":"hard","reason":"x","ref":"r"}`, `[]`, `{}`, `null`, `[1]`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		format := Detect(body)
		_, _, _ = Parse(body)
		h := http.Header{}
		noCerts := func(string) ([]byte, error) { return nil, errors.New("no network in tests") }
		keys := Keys{SendGridKey: "not a key", MailgunKey: "k", BasicUser: "u", BasicPassword: "p"}
		for _, fm := range []string{format, FormatSendGrid, FormatMailgun, FormatSES, FormatPostmark, FormatGeneric} {
			if err := Verify(fm, h, body, keys, noCerts, time.Now()); err == nil {
				t.Fatalf("Verify(%s) accepted an unsigned body", fm)
			}
			_ = Verify(fm, h, body, Keys{}, noCerts, time.Now())
		}
		MailgunToken(body)
	})
}
