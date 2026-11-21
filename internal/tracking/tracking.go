// Package tracking instruments outgoing email (open pixel, click redirects, unsubscribe links) and
// verifies the requests those links make.
//
// A token identifies one message to one contact. It is sealed with AES-GCM, so it can't be forged
// or read (external ids are often email addresses). Click links also sign the destination URL, so
// the redirect endpoint can't be used as an open redirect to arbitrary sites.
package tracking

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
)

// Token is what a tracking link knows about its message.
type Token struct {
	TenantID   int64  `json:"t"`
	ExternalID string `json:"e"`
	MessageID  string `json:"m"`
	Channel    string `json:"ch"`
	TemplateID int64  `json:"tp,omitempty"`
	JourneyID  int64  `json:"j,omitempty"`

	BroadcastID int64  `json:"b,omitempty"`
	Run         int64  `json:"r,omitempty"`
	Variant     string `json:"v,omitempty"`
	// Topic: the message's subscription topic; its unsubscribe link leaves that topic only.
	Topic string `json:"tc,omitempty"`
}

// Props are the message's identifiers as event properties, so engagement events count in the
// same reports as the message's outcome events.
func (t Token) Props() map[string]any {
	p := map[string]any{"message_id": t.MessageID, "template_id": t.TemplateID, "journey_id": t.JourneyID}
	if t.BroadcastID != 0 {
		p["broadcast_id"], p["run"] = t.BroadcastID, t.Run
		if t.Variant != "" {
			p["variant"] = t.Variant
		}
	}
	return p
}

type Codec struct {
	box  *auth.Box
	mac  []byte
	base string // public base URL, e.g. https://pulse.example.com
}

// New derives a separate link-signing key from the server secret.
func New(box *auth.Box, secretHex, publicURL string) *Codec {
	sum := sha256.Sum256([]byte("pulse-click-links:" + secretHex))
	return &Codec{box: box, mac: sum[:], base: strings.TrimRight(publicURL, "/")}
}

func (c *Codec) Encode(t Token) string {
	b, _ := json.Marshal(t)
	return c.box.SealString(string(b))
}

func (c *Codec) Decode(s string) (Token, error) {
	plain, err := c.box.OpenString(s)
	if err != nil {
		return Token{}, errors.New("invalid tracking token")
	}
	var t Token
	return t, json.Unmarshal([]byte(plain), &t)
}

func (c *Codec) sign(token, dest string) string {
	m := hmac.New(sha256.New, c.mac)
	m.Write([]byte(token + "|" + dest))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12])
}

// VerifyClick checks that dest is the URL this token's link was created for.
func (c *Codec) VerifyClick(token, dest, sig string) bool {
	return hmac.Equal([]byte(c.sign(token, dest)), []byte(sig))
}

func (c *Codec) UnsubscribeURL(token string) string { return c.base + "/u/" + token }
func (c *Codec) PreferencesURL(token string) string {
	return c.base + "/u/" + token + "?view=preferences"
}

// PushTrackKey is the push payload data key holding the message's PushURL.
const PushTrackKey = "pulse_track"

// PushURL is where a push notification's service worker or app SDK reports a click or dismissal:
// POST {PushURL}?a=clicked or ?a=dismissed.
// SurveyURL is a survey's page for the message's contact: the token names the contact and
// message, and the signature binds it to this survey (a message's token can't answer another).
func (c *Codec) SurveyURL(survey int64, token string) string {
	id := strconv.FormatInt(survey, 10)
	return c.base + "/s/" + id + "/" + token + "/" + c.sign(token, "survey:"+id)
}

// VerifySurvey checks a survey link's signature.
func (c *Codec) VerifySurvey(survey int64, token, sig string) bool {
	want := c.sign(token, "survey:"+strconv.FormatInt(survey, 10))
	return hmac.Equal([]byte(want), []byte(sig))
}

func (c *Codec) PushURL(token string) string { return c.base + "/t/p/" + token }

func (c *Codec) OpenURL(token string) string { return c.base + "/t/o/" + token + ".gif" }

func (c *Codec) ClickURL(token, dest string) string {
	return c.base + "/t/c/" + token + "?u=" + url.QueryEscape(dest) + "&s=" + c.sign(token, dest)
}

var anchorHref = regexp.MustCompile(`(?i)(<a\b[^>]*?\bhref\s*=\s*)(["'])(https?://[^"']+)(["'])`)

// Instrument rewrites http(s) links in anchors to click redirects, adds the unsubscribe footer if the
// template didn't place {{.UnsubscribeURL}} itself, and adds the open pixel.
func (c *Codec) Instrument(htmlBody, token string) string {
	unsub := c.UnsubscribeURL(token)
	out := anchorHref.ReplaceAllStringFunc(htmlBody, func(m string) string {
		p := anchorHref.FindStringSubmatch(m)
		dest := html.UnescapeString(p[3])
		if strings.HasPrefix(dest, c.base+"/u/") { // the unsubscribe link stays direct
			return m
		}
		return p[1] + p[2] + html.EscapeString(c.ClickURL(token, dest)) + p[4]
	})
	if !strings.Contains(out, html.EscapeString(unsub)) && !strings.Contains(out, unsub) {
		out = insertBeforeBodyEnd(out, `<p style="font:12px Arial,sans-serif;color:#8a93a3;text-align:center;margin:28px 0 8px">`+
			`You're receiving this because you subscribed. <a href="`+html.EscapeString(unsub)+`" style="color:#8a93a3">Unsubscribe</a></p>`)
	}
	return insertBeforeBodyEnd(out, `<img src="`+html.EscapeString(c.OpenURL(token))+`" width="1" height="1" alt="" style="display:block;border:0;width:1px;height:1px">`)
}

// InstrumentAMP does for an email's AMP part what Instrument does for its HTML: click redirects,
// the unsubscribe footer when missing, and an open pixel (as <amp-img>: AMP has no <img>).
func (c *Codec) InstrumentAMP(doc, token string) string {
	if doc == "" {
		return doc
	}
	unsub := c.UnsubscribeURL(token)
	out := anchorHref.ReplaceAllStringFunc(doc, func(m string) string {
		p := anchorHref.FindStringSubmatch(m)
		dest := html.UnescapeString(p[3])
		if strings.HasPrefix(dest, c.base+"/u/") || strings.HasPrefix(dest, c.base+"/v1/amp/") {
			return m
		}
		return p[1] + p[2] + html.EscapeString(c.ClickURL(token, dest)) + p[4]
	})
	if !strings.Contains(out, html.EscapeString(unsub)) && !strings.Contains(out, unsub) {
		out = insertBeforeBodyEnd(out, `<p style="font:12px Arial,sans-serif;color:#8a93a3;text-align:center;margin:28px 0 8px">`+
			`You're receiving this because you subscribed. <a href="`+html.EscapeString(unsub)+`" style="color:#8a93a3">Unsubscribe</a></p>`)
	}
	return insertBeforeBodyEnd(out, `<amp-img src="`+html.EscapeString(c.OpenURL(token))+`" width="1" height="1" alt="" layout="fixed"></amp-img>`)
}

// AMPToken names one form or poll block of one message to one contact: the token in the AMP
// part's post-back links (POST /v1/amp/{token}). Sealed like tracking tokens, with its own prefix
// so neither kind of token is accepted as the other.
type AMPToken struct {
	Token
	ContactID uint32 `json:"c,omitempty"`
	Block     string `json:"bk"`
	Exp       int64  `json:"x"`            // Unix seconds; refused after
	Test      bool   `json:"ts,omitempty"` // a test send: answers aren't recorded
}

const ampPrefix = "amp1:"

// AMPTTL is how long post-back links work after the email is sent (Gmail shows the AMP part for 30 days).
const AMPTTL = 60 * 24 * time.Hour

func (c *Codec) EncodeAMP(t AMPToken) string {
	b, _ := json.Marshal(t)
	return c.box.SealString(ampPrefix + string(b))
}

// DecodeAMP opens an AMP token (expiry is the caller's check).
func (c *Codec) DecodeAMP(s string) (AMPToken, error) {
	if len(s) > 2048 {
		return AMPToken{}, errors.New("invalid AMP token")
	}
	plain, err := c.box.OpenString(s)
	if err != nil || !strings.HasPrefix(plain, ampPrefix) {
		return AMPToken{}, errors.New("invalid AMP token")
	}
	var t AMPToken
	if err := json.Unmarshal([]byte(plain[len(ampPrefix):]), &t); err != nil || t.TenantID == 0 || t.Block == "" {
		return AMPToken{}, errors.New("invalid AMP token")
	}
	return t, nil
}

// AMPURL is a block's post-back link ("submit") or its results list ("results").
func (c *Codec) AMPURL(token, kind string) string {
	if kind == "results" {
		return c.base + "/v1/amp/" + token + "/results"
	}
	return c.base + "/v1/amp/" + token
}

// InstrumentText adds the unsubscribe link to an email's plain-text part when the text doesn't
// carry it already. Links in the text part stay direct: no click redirects and no open pixel.
func (c *Codec) InstrumentText(text, token string) string {
	unsub := c.UnsubscribeURL(token)
	if text == "" || strings.Contains(text, unsub) {
		return text
	}
	return strings.TrimRight(text, "\n ") + "\n\n--\nUnsubscribe: " + unsub + "\n"
}

// Base is the public base URL links point to.
func (c *Codec) Base() string { return c.base }

func insertBeforeBodyEnd(doc, fragment string) string {
	if i := strings.LastIndex(strings.ToLower(doc), "</body>"); i >= 0 {
		return doc[:i] + fragment + doc[i:]
	}
	return doc + fragment
}

// Headers are the RFC 8058 one-click unsubscribe headers mailbox providers expect from bulk senders.
func (c *Codec) Headers(token string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + c.UnsubscribeURL(token) + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

// Pixel is a 1x1 transparent GIF.
var Pixel = []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01,
	0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b}

// OptinToken is a double opt-in confirmation link's payload (sealed: it names a person).
type OptinToken struct {
	TenantID   int64  `json:"t"`
	FormID     int64  `json:"f"`
	ExternalID string `json:"e"`
	Email      string `json:"m"`
	Issued     int64  `json:"i"` // unix seconds
}

const optinPrefix = "optin:"

// EncodeOptin seals a confirmation link's token.
func (c *Codec) EncodeOptin(t OptinToken) string {
	b, _ := json.Marshal(t)
	return c.box.SealString(optinPrefix + string(b))
}

// DecodeOptin opens a confirmation token (expiry is the caller's check).
func (c *Codec) DecodeOptin(s string) (OptinToken, error) {
	if len(s) > 2048 {
		return OptinToken{}, errors.New("invalid confirmation token")
	}
	plain, err := c.box.OpenString(s)
	if err != nil || !strings.HasPrefix(plain, optinPrefix) {
		return OptinToken{}, errors.New("invalid confirmation token")
	}
	var t OptinToken
	if err := json.Unmarshal([]byte(plain[len(optinPrefix):]), &t); err != nil || t.TenantID == 0 || t.FormID == 0 || t.ExternalID == "" {
		return OptinToken{}, errors.New("invalid confirmation token")
	}
	return t, nil
}
