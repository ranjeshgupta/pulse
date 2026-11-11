package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"sort"
	"strconv"
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
			{Key: "max_connections", Label: "Max connections", Placeholder: "4"},
			{Key: "messages_per_connection", Label: "Messages per connection", Placeholder: "100"},
		},
		Secrets: []Field{{Key: "username", Label: "Username"}, {Key: "password", Label: "Password"}},
		New: func(_ Deps, c, s map[string]string) (Provider, error) {
			maxConns, err := intSetting(c, "max_connections", "Max connections", defaultSMTPMaxConns, 1, 50)
			if err != nil {
				return nil, err
			}
			perConn, err := intSetting(c, "messages_per_connection", "Messages per connection", defaultSMTPMessagesPerConn, 1, 10000)
			if err != nil {
				return nil, err
			}
			p := &SMTP{Addr: net.JoinHostPort(c["host"], c["port"]), From: c["from"], MaxConns: maxConns, MessagesPerConn: perConn}
			if s["username"] != "" {
				p.Auth = smtp.PlainAuth("", s["username"], s["password"], c["host"])
				sum := sha256.Sum256([]byte(s["password"]))
				p.username, p.passSum = s["username"], hex.EncodeToString(sum[:])
			}
			return p, nil
		},
	})
}

// intSetting reads an optional whole-number setting within [lo, hi]; empty means def.
func intSetting(c map[string]string, key, label string, def, lo, hi int) (int, error) {
	v := strings.TrimSpace(c[key])
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be a whole number from %d to %d", label, lo, hi)
	}
	return n, nil
}

// SMTP sends through a relay over pooled connections (see smtppool.go): up to MaxConns per relay
// and login, each reused for up to MessagesPerConn messages. STARTTLS is used whenever the server
// offers it, implicit TLS on port 465; plain auth is only used over TLS (net/smtp refuses it
// otherwise, except to localhost).
type SMTP struct {
	Addr string
	Auth smtp.Auth
	From string // overrides Outbound.From when set
	// Pool limits; 0 means the defaults (4 connections, 100 messages each).
	MaxConns, MessagesPerConn int

	username, passSum string                                                            // identify the login in the pool key (passSum: SHA-256 of the password)
	tlsConfig         *tls.Config                                                       // tests: trust a test CA (production uses the system roots)
	dial              func(ctx context.Context, network, addr string) (net.Conn, error) // tests
}

func (s *SMTP) Send(ctx context.Context, o Outbound) (Result, error) {
	from := o.From
	if s.From != "" {
		from = s.From
	}
	msg, err := BuildMessage(Mail{ID: o.MessageID, From: from, To: o.To, Subject: o.Rendered.Subject, HTML: o.Rendered.HTML,
		Text: o.Rendered.Content.Text, Headers: o.Headers, Attachments: o.Attachments, Date: time.Now()})
	if err != nil {
		return Result{}, Permanent(err.Error())
	}
	fa, _ := mail.ParseAddress(from)
	ta, _ := mail.ParseAddress(o.To)
	p, err := s.acquirePool()
	if err != nil {
		return Result{}, Permanent(err.Error())
	}
	defer p.release()
	if err := p.send(ctx, fa.Address, ta.Address, msg); err != nil {
		var te *textproto.Error
		if errors.As(err, &te) && te.Code >= 500 { // 5xx reply: rejected, retrying won't help
			return Result{}, Permanent(err.Error())
		}
		return Result{}, err
	}
	return Result{ProviderMessageID: o.MessageID}, nil
}

// Mail is one email to build: the HTML, its plain-text alternative and any attachments.
type Mail struct {
	ID, From, To, Subject string
	HTML, Text            string
	Headers               map[string]string // extra headers (List-Unsubscribe)
	Attachments           []Attachment
	Date                  time.Time
}

// BuildMessage produces the RFC 5322 message bytes. Addresses are parsed, so header injection
// through them is impossible; the subject is Q-encoded. The body is multipart/alternative (text,
// then HTML) when there is a text part, wrapped in multipart/mixed when there are attachments.
// Text parts are quoted-printable (no line-length limits to break), attachments base64.
func BuildMessage(m Mail) ([]byte, error) {
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return nil, fmt.Errorf("from address: %w", err)
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return nil, fmt.Errorf("to address: %w", err)
	}
	if strings.ContainsAny(m.ID, "\r\n<>") {
		return nil, errors.New("invalid message id")
	}
	var b bytes.Buffer
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", m.Date.Format(time.RFC1123Z))
	header("Message-ID", "<"+m.ID+"@pulse>")
	header("X-Pulse-Message-Id", m.ID)
	keys := make([]string, 0, len(m.Headers))
	for k := range m.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.ContainsAny(k+m.Headers[k], "\r\n") {
			return nil, errors.New("header contains a line break")
		}
		header(k, m.Headers[k])
	}
	header("MIME-Version", "1.0")

	// body writes the message's content (text and HTML) as one entity, created by w.
	body := func(w func(textproto.MIMEHeader) (io.Writer, error)) error {
		if m.Text == "" {
			return textPart(w, "text/html", m.HTML)
		}
		alt := multipart.NewWriter(io.Discard)
		pw, err := w(textproto.MIMEHeader{"Content-Type": {"multipart/alternative; boundary=" + strconv.Quote(alt.Boundary())}})
		if err != nil {
			return err
		}
		alt = newWriterWithBoundary(pw, alt.Boundary())
		create := func(h textproto.MIMEHeader) (io.Writer, error) { return alt.CreatePart(h) }
		if err := textPart(create, "text/plain", m.Text); err != nil {
			return err
		}
		if err := textPart(create, "text/html", m.HTML); err != nil {
			return err
		}
		return alt.Close()
	}
	// The top-level entity's headers go straight into the message header.
	top := func(h textproto.MIMEHeader) (io.Writer, error) {
		for _, k := range []string{"Content-Type", "Content-Transfer-Encoding"} {
			if v := h.Get(k); v != "" {
				header(k, v)
			}
		}
		b.WriteString("\r\n")
		return &b, nil
	}
	if len(m.Attachments) == 0 {
		if err := body(top); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	mixed := multipart.NewWriter(io.Discard)
	boundary := mixed.Boundary()
	if _, err := top(textproto.MIMEHeader{"Content-Type": {"multipart/mixed; boundary=" + strconv.Quote(boundary)}}); err != nil {
		return nil, err
	}
	mixed = newWriterWithBoundary(&b, boundary)
	if err := body(func(h textproto.MIMEHeader) (io.Writer, error) { return mixed.CreatePart(h) }); err != nil {
		return nil, err
	}
	for _, a := range m.Attachments {
		if err := attachmentPart(mixed, a); err != nil {
			return nil, err
		}
	}
	if err := mixed.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func newWriterWithBoundary(w io.Writer, boundary string) *multipart.Writer {
	mw := multipart.NewWriter(w)
	_ = mw.SetBoundary(boundary) // generated by multipart itself, so always valid
	return mw
}

// textPart writes a quoted-printable UTF-8 text entity (CRLF line breaks).
func textPart(create func(textproto.MIMEHeader) (io.Writer, error), contentType, s string) error {
	w, err := create(textproto.MIMEHeader{
		"Content-Type":              {contentType + `; charset="utf-8"`},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(qp, strings.ReplaceAll(s, "\r\n", "\n")); err != nil {
		return err
	}
	return qp.Close()
}

// attachmentPart writes one file, base64 in 76-character lines, named in both the Content-Type
// (older clients) and the Content-Disposition (RFC 2231-encoded when not ASCII).
func attachmentPart(mw *multipart.Writer, a Attachment) error {
	ct, params, err := mime.ParseMediaType(a.ContentType)
	if err != nil {
		ct, params = "application/octet-stream", map[string]string{}
	}
	params["name"] = a.Filename
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename})
	typ := mime.FormatMediaType(ct, params)
	if disp == "" || typ == "" {
		return fmt.Errorf("attachment %q: invalid name or type", a.Filename)
	}
	w, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {typ},
		"Content-Disposition":       {disp},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString(a.Data)
	for len(enc) > 76 {
		if _, err := io.WriteString(w, enc[:76]+"\r\n"); err != nil {
			return err
		}
		enc = enc[76:]
	}
	_, err = io.WriteString(w, enc+"\r\n")
	return err
}
