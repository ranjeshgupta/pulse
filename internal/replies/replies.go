// Package replies captures replies to emails, for the journey decision "replies to email".
//
// When a workspace sets a reply tracking address (delivery rules reply_to, e.g.
// replies@inbound.example.com), every marketing email gets the header
// Reply-To: replies+r<code>@inbound.example.com, where code is a random 16-character lower-case
// token that names the message (kept in Redis for 30 days with the message's tracking token). The
// domain's MX points at the email provider's inbound parse (SendGrid Inbound Parse, Mailgun routes,
// Postmark inbound), whose webhook posts the reply to /v1/replies/{workspace token}. Pulse finds the
// code among the recipients and records email_replied {message_id, template_id, journey_id, ...}
// on the contact who got the message.
package replies

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	codeLen = 16
	TTL     = 30 * 24 * time.Hour
	alpha   = "0123456789abcdefghijklmnopqrstuvwxyz"
	maxText = 2000
)

// NewCode returns a random reply code.
func NewCode() string {
	b := make([]byte, codeLen)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alpha[int(b[i])%len(alpha)]
	}
	return string(b)
}

func key(code string) string { return "rtok:" + code }

// Save stores the tracking token the code stands for.
func Save(ctx context.Context, rdb redis.Cmdable, code, token string) error {
	return rdb.Set(ctx, key(code), token, TTL).Err()
}

// Lookup returns the tracking token of a code ("" when unknown or expired).
func Lookup(ctx context.Context, rdb redis.Cmdable, code string) (string, error) {
	v, err := rdb.Get(ctx, key(code)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

// Address is the Reply-To address for a code: local+r<code>@domain of the workspace's address.
func Address(replyTo, code string) (string, bool) {
	a, err := mail.ParseAddress(replyTo)
	if err != nil {
		return "", false
	}
	local, domain, ok := strings.Cut(a.Address, "@")
	if !ok || local == "" || domain == "" {
		return "", false
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	return local + "+r" + code + "@" + domain, true
}

var codeRe = regexp.MustCompile(`(?i)\+r([0-9a-z]{16})@`)

// Reply is one inbound email.
type Reply struct {
	Codes   []string // reply codes found among the recipients
	From    string
	Subject string
	Text    string
}

// Parse reads an inbound-parse webhook: SendGrid (multipart form: to, envelope, from, subject,
// text), Mailgun (form: recipient, sender, subject, stripped-text / body-plain), Postmark (JSON:
// ToFull, OriginalRecipient, MailboxHash, From, Subject, StrippedTextReply / TextBody), or a
// generic JSON {to, from, subject, text}.
func Parse(contentType string, body []byte) (Reply, error) {
	mt, params, _ := mime.ParseMediaType(contentType)
	fields := map[string]string{}
	switch {
	case mt == "multipart/form-data":
		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			p, err := r.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return Reply{}, errors.New("unreadable form")
			}
			if p.FileName() != "" {
				continue // attachments
			}
			b, _ := io.ReadAll(io.LimitReader(p, 1<<20))
			fields[p.FormName()] = string(b)
		}
	case mt == "application/x-www-form-urlencoded":
		vs, err := url.ParseQuery(string(body))
		if err != nil {
			return Reply{}, errors.New("unreadable form")
		}
		for k := range vs {
			fields[k] = vs.Get(k)
		}
	default:
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return Reply{}, errors.New("send JSON or form data")
		}
		for k, v := range m {
			switch x := v.(type) {
			case string:
				fields[k] = x
			default:
				b, _ := json.Marshal(x)
				fields[k] = string(b)
			}
		}
	}
	var rcpt strings.Builder
	for _, k := range []string{"to", "To", "cc", "Cc", "envelope", "recipient", "ToFull", "CcFull", "OriginalRecipient"} {
		rcpt.WriteString(fields[k] + " ")
	}
	if h := fields["MailboxHash"]; strings.HasPrefix(strings.ToLower(h), "r") {
		rcpt.WriteString("+" + h + "@ ")
	}
	rep := Reply{From: first(fields, "from", "From", "sender"), Subject: first(fields, "subject", "Subject")}
	rep.Text = first(fields, "stripped-text", "StrippedTextReply", "text", "TextBody", "body-plain")
	if len(rep.Text) > maxText {
		rep.Text = rep.Text[:maxText]
	}
	seen := map[string]bool{}
	for _, m := range codeRe.FindAllStringSubmatch(rcpt.String(), -1) {
		c := strings.ToLower(m[1])
		if !seen[c] {
			seen[c] = true
			rep.Codes = append(rep.Codes, c)
		}
	}
	return rep, nil
}

func first(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}
