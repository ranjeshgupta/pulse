package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "smtp", Channel: channel.Email, Name: "SMTP",
		Description: "Any SMTP relay: Amazon SES SMTP, SendGrid, Postmark, your own MTA.",
		Config: []Field{
			{Key: "host", Label: "Host", Placeholder: "email-smtp.ap-south-1.amazonaws.com", Required: true},
			{Key: "port", Label: "Port", Placeholder: "587", Required: true},
			{Key: "from", Label: "From address", Placeholder: "Acme <hello@acme.com>", Required: true},
		},
		Secrets: []Field{{Key: "username", Label: "Username"}, {Key: "password", Label: "Password"}},
		New: func(_ Deps, c, s map[string]string) (Provider, error) {
			p := &SMTP{Addr: net.JoinHostPort(c["host"], c["port"]), From: c["from"]}
			if s["username"] != "" {
				p.Auth = smtp.PlainAuth("", s["username"], s["password"], c["host"])
			}
			return p, nil
		},
	})
}

// SMTP sends through a relay. Plain auth is only used over TLS (net/smtp refuses it otherwise,
// except to localhost).
type SMTP struct {
	Addr string
	Auth smtp.Auth
	From string // overrides Outbound.From when set
}

func (s *SMTP) Send(_ context.Context, o Outbound) (Result, error) {
	from := o.From
	if s.From != "" {
		from = s.From
	}
	msg, err := BuildMailWithHeaders(o.MessageID, from, o.To, o.Rendered.Subject, o.Rendered.HTML, o.Headers, time.Now())
	if err != nil {
		return Result{}, Permanent(err.Error())
	}
	fa, _ := mail.ParseAddress(from)
	ta, _ := mail.ParseAddress(o.To)
	if err := smtp.SendMail(s.Addr, s.Auth, fa.Address, []string{ta.Address}, msg); err != nil {
		var te *textproto.Error
		if errors.As(err, &te) && te.Code >= 500 { // 5xx reply: rejected, retrying won't help
			return Result{}, Permanent(err.Error())
		}
		return Result{}, err
	}
	return Result{ProviderMessageID: o.MessageID}, nil
}

// BuildMail produces the RFC 5322 message bytes. Addresses are parsed, so header injection through
// them is impossible; the subject is Q-encoded.
func BuildMail(id, fromAddr, toAddr, subject, html string, now time.Time) ([]byte, error) {
	return BuildMailWithHeaders(id, fromAddr, toAddr, subject, html, nil, now)
}

// BuildMailWithHeaders also writes extra headers (sorted, so output is stable). Header names and
// values must not contain line breaks.
func BuildMailWithHeaders(id, fromAddr, toAddr, subject, html string, extra map[string]string, now time.Time) ([]byte, error) {
	from, err := mail.ParseAddress(fromAddr)
	if err != nil {
		return nil, fmt.Errorf("from address: %w", err)
	}
	to, err := mail.ParseAddress(toAddr)
	if err != nil {
		return nil, fmt.Errorf("to address: %w", err)
	}
	if strings.ContainsAny(id, "\r\n<>") {
		return nil, errors.New("invalid message id")
	}
	var b bytes.Buffer
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+id+"@pulse>")
	header("X-Pulse-Message-Id", id)
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.ContainsAny(k+extra[k], "\r\n") {
			return nil, errors.New("header contains a line break")
		}
		header(k, extra[k])
	}
	header("MIME-Version", "1.0")
	header("Content-Type", `text/html; charset="utf-8"`)
	header("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(html, "\r\n", "\n"), "\n", "\r\n"))
	return b.Bytes(), nil
}
