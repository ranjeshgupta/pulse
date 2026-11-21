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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "smtp", Channel: channel.Email, Name: "SMTP",
		Description: "Any SMTP server: Amazon SES, SendGrid, Mailgun, Postmark, Brevo, Microsoft 365, your own mail server.",
		Presets:     smtpPresets,
		Config: []Field{
			{Key: "host", Label: "Host", Placeholder: "email-smtp.ap-south-1.amazonaws.com", Required: true, Hint: "The SMTP server's address."},
			{Key: "port", Label: "Port", Placeholder: "587", Required: true, Type: "number", Hint: "Usually 587 (STARTTLS) or 465 (SSL/TLS)."},
			{Key: "tls", Label: "Encryption", Options: []string{"auto", "starttls", "ssl", "none"}, Default: "auto",
				OptionLabels: map[string]string{"auto": "Automatic", "starttls": "STARTTLS", "ssl": "SSL/TLS", "none": "None"},
				Hint:         "Automatic: SSL/TLS on port 465, otherwise STARTTLS when the server offers it."},
			{Key: "auth_protocol", Label: "Login type", Options: []string{"plain", "login", "cram-md5", "none"}, Default: "plain",
				OptionLabels: map[string]string{"plain": "PLAIN", "login": "LOGIN", "cram-md5": "CRAM-MD5", "none": "No login"},
				Hint:         "PLAIN works with most servers; Microsoft 365 also takes LOGIN. No login: the server allows your IP address."},
			{Key: "from", Label: "From address", Placeholder: "Acme <hello@acme.com>", Required: true, Hint: "An address the server lets you send from."},
			{Key: "max_connections", Label: "Max connections", Placeholder: "4", Type: "number", Advanced: true, Hint: "Connections open to the server at once (1 to 50)."},
			{Key: "messages_per_connection", Label: "Messages per connection", Placeholder: "100", Type: "number", Advanced: true, Hint: "Pulse reconnects after this many."},
			{Key: "helo", Label: "HELO hostname", Placeholder: "localhost", Advanced: true, Hint: "Some servers want your domain here, e.g. acme.com."},
			{Key: "skip_tls_verify", Label: "Skip TLS certificate check", Type: "bool", Advanced: true,
				Hint: "Only for a server with a self-signed certificate on your own network: anyone in between could read the mail."},
			{Key: "service", Label: "Sending service (for delivery reports)", Advanced: true,
				Options: []string{"", ServiceSES, ServiceSendGrid, ServiceMailgun, ServicePostmark, ServiceSparkPost, ServiceOther},
				OptionLabels: map[string]string{"": "Detect from the host", ServiceSES: "Amazon SES", ServiceSendGrid: "SendGrid", ServiceMailgun: "Mailgun",
					ServicePostmark: "Postmark", ServiceSparkPost: "SparkPost", ServiceOther: "Other"}},
			{Key: "headers", Label: "Custom headers", Type: "textarea", Advanced: true, Placeholder: "X-SES-CONFIGURATION-SET: marketing",
				Hint: "Added to every email, one per line as Name: value."},
		},
		Secrets: []Field{
			{Key: "username", Label: "Username", ShowIf: &ShowIf{Key: "auth_protocol", Values: []string{"", "plain", "login", "cram-md5"}}},
			{Key: "password", Label: "Password", ShowIf: &ShowIf{Key: "auth_protocol", Values: []string{"", "plain", "login", "cram-md5"}}},
		},
		New: newSMTP,
	})
}

// smtpPresets fill the SMTP form for well-known services (host names as each service documents them).
var smtpPresets = []Preset{
	{Name: "Amazon SES", Values: map[string]string{"host": "email-smtp.ap-south-1.amazonaws.com", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServiceSES},
		Note: "Change ap-south-1 to your SES region. The username and password are SES SMTP credentials (SES console → SMTP settings), not your AWS keys."},
	{Name: "SendGrid", Values: map[string]string{"host": "smtp.sendgrid.net", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServiceSendGrid},
		Note: "The username is literally apikey; the password is your SendGrid API key."},
	{Name: "Mailgun", Values: map[string]string{"host": "smtp.mailgun.org", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServiceMailgun},
		Note: "Use smtp.eu.mailgun.org for an EU domain. The username is the domain's SMTP login, e.g. postmaster@mg.acme.com."},
	{Name: "Postmark", Values: map[string]string{"host": "smtp.postmarkapp.com", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServicePostmark},
		Note: "The username and password are both your Postmark server API token."},
	{Name: "Mailjet", Values: map[string]string{"host": "in-v3.mailjet.com", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServiceOther},
		Note: "The username is your Mailjet API key and the password its secret key."},
	{Name: "Brevo", Values: map[string]string{"host": "smtp-relay.brevo.com", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServiceOther},
		Note: "The username is your Brevo login and the password an SMTP key (Brevo → SMTP & API)."},
	{Name: "SparkPost", Values: map[string]string{"host": "smtp.sparkpostmail.com", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServiceSparkPost},
		Note: "The username is SMTP_Injection and the password an API key with sending rights."},
	{Name: "Microsoft 365", Values: map[string]string{"host": "smtp.office365.com", "port": "587", "tls": "starttls", "auth_protocol": "login", "service": ServiceOther},
		Note: "The mailbox needs SMTP AUTH turned on in the Microsoft 365 admin centre."},
	{Name: "Zoho Mail", Values: map[string]string{"host": "smtp.zoho.in", "port": "465", "tls": "ssl", "auth_protocol": "plain", "service": ServiceOther},
		Note: "Use smtp.zoho.com outside India; with two-factor sign-in on, use an app-specific password."},
	{Name: "Gmail", Values: map[string]string{"host": "smtp.gmail.com", "port": "587", "tls": "starttls", "auth_protocol": "plain", "service": ServiceOther},
		Note: "For a Gmail or Google Workspace account, the Gmail provider is simpler and sets Google's daily limit for you."},
}

// newSMTP builds the generic SMTP adapter from the form's settings.
func newSMTP(d Deps, c, s map[string]string) (Provider, error) {
	host, port := strings.TrimSpace(c["host"]), strings.TrimSpace(c["port"])
	if host == "" || strings.ContainsAny(host, " /:") {
		return nil, errors.New("enter the SMTP server's host name, e.g. smtp.example.com")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, errors.New("the port must be a number from 1 to 65535, usually 587 or 465")
	}
	if !d.AllowPrivate && !smtpPorts[port] {
		return nil, errors.New("the port must be 25, 465, 587 or 2525")
	}
	maxConns, err := intSetting(c, "max_connections", "Max connections", defaultSMTPMaxConns, 1, 50)
	if err != nil {
		return nil, err
	}
	perConn, err := intSetting(c, "messages_per_connection", "Messages per connection", defaultSMTPMessagesPerConn, 1, 10000)
	if err != nil {
		return nil, err
	}
	svc, err := smtpService(c["service"], host)
	if err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(c["tls"])
	switch mode {
	case "", "auto", "starttls", "ssl", "none":
	default:
		return nil, errors.New("encryption must be automatic, STARTTLS, SSL/TLS or none")
	}
	helo := strings.TrimSpace(c["helo"])
	if strings.ContainsAny(helo, " \r\n") {
		return nil, errors.New("the HELO hostname can't contain spaces")
	}
	headers, err := parseHeaderLines(c["headers"])
	if err != nil {
		return nil, err
	}
	p := &SMTP{Addr: net.JoinHostPort(host, port), From: c["from"], MaxConns: maxConns, MessagesPerConn: perConn, Service: svc,
		TLSMode: mode, SkipVerify: c["skip_tls_verify"] == "true", Helo: helo, Headers: headers, restricted: !d.AllowPrivate}
	proto := strings.TrimSpace(c["auth_protocol"])
	if s["username"] != "" && proto != "none" {
		switch proto {
		case "", "plain":
			p.Auth = smtp.PlainAuth("", s["username"], s["password"], host)
		case "login":
			p.Auth = &loginAuth{user: s["username"], pass: s["password"], host: host}
		case "cram-md5":
			p.Auth = smtp.CRAMMD5Auth(s["username"], s["password"])
		default:
			return nil, errors.New("the login type must be PLAIN, LOGIN, CRAM-MD5 or no login")
		}
		sum := sha256.Sum256([]byte(s["password"]))
		p.username, p.passSum, p.authProto = s["username"], hex.EncodeToString(sum[:]), proto
	}
	return p, nil
}

// smtpPorts are the ports a workspace-entered SMTP server may use (unless the operator allows
// private addresses, for local development): anything else would make the connection test a
// scanner for other services.
var smtpPorts = map[string]bool{"25": true, "465": true, "587": true, "2525": true}

// reservedHeaders are set by Pulse on every email; custom headers can't replace them.
var reservedHeaders = map[string]bool{"From": true, "To": true, "Subject": true, "Date": true, "Message-Id": true, "Mime-Version": true,
	"Content-Type": true, "Content-Transfer-Encoding": true, "X-Pulse-Ref": true, "List-Unsubscribe": true, "List-Unsubscribe-Post": true}

// parseHeaderLines reads custom headers written one per line as "Name: value".
func parseHeaderLines(text string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name = textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if !ok || !headerToken.MatchString(name) {
			return nil, fmt.Errorf("custom headers, line %d: write it as Name: value", i+1)
		}
		if reservedHeaders[name] {
			return nil, fmt.Errorf("custom headers: %s is set by Pulse", name)
		}
		out[name] = strings.TrimSpace(value)
		if len(out) > 20 {
			return nil, errors.New("custom headers: at most 20")
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

var headerToken = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~-]+$`)

// loginAuth is the LOGIN mechanism (Microsoft 365 and some older servers). Like net/smtp's
// PlainAuth, it refuses to send the password over an unencrypted connection except to localhost.
type loginAuth struct{ user, pass, host string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && server.Name != "localhost" && server.Name != "127.0.0.1" && server.Name != "::1" {
		return "", nil, errors.New("smtp: won't send the password over an unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("smtp: wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch prompt := strings.ToLower(strings.TrimSpace(string(fromServer))); {
	case strings.Contains(prompt, "user"):
		return []byte(a.user), nil
	case strings.Contains(prompt, "pass"):
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("smtp: unexpected LOGIN prompt %q", fromServer)
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
	// Service is the sending service behind the relay (EmailServices), which decides the metadata
	// header that carries the message reference into its webhooks.
	Service string
	// Helo is the name given in EHLO (default localhost); Google's SMTP relay wants the sending
	// domain.
	Helo string
	// TLSMode: "" or "auto" (SSL/TLS on port 465, else STARTTLS when offered), "ssl", "starttls"
	// (required: fail when the server doesn't offer it) or "none".
	TLSMode string
	// SkipVerify accepts any server certificate (a self-signed one on a private network).
	SkipVerify bool
	// Headers are added to every email (custom headers from the form).
	Headers map[string]string

	// restricted: a workspace entered this server, so connections go through safehttp's dialer
	// (public addresses only). Adapters built in code (the operator's default relay) aren't.
	restricted bool

	username, passSum, authProto string                                                            // identify the login in the pool key (passSum: SHA-256 of the password)
	tlsConfig                    *tls.Config                                                       // tests: trust a test CA (production uses the system roots)
	dial                         func(ctx context.Context, network, addr string) (net.Conn, error) // tests
}

func (s *SMTP) Send(ctx context.Context, o Outbound) (Result, error) {
	from := o.From
	if s.From != "" {
		from = s.From
	}
	ampPart := o.Rendered.Content.AMP
	if s.Service == ServicePostmark {
		ampPart = "" // Postmark doesn't send AMP (SupportsAMP)
	}
	headers := o.Headers
	if len(s.Headers) > 0 {
		headers = make(map[string]string, len(s.Headers)+len(o.Headers))
		for k, v := range s.Headers {
			headers[k] = v
		}
		for k, v := range o.Headers { // the message's own (List-Unsubscribe…) win
			headers[k] = v
		}
	}
	msg, err := BuildMessage(Mail{ID: o.MessageID, From: from, To: o.To, Subject: o.Rendered.Subject, HTML: o.Rendered.HTML,
		AMP: ampPart, Text: o.Rendered.Content.Text, Headers: headers, Attachments: o.Attachments, Date: time.Now(), Ref: o.Ref, Service: s.Service})
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
	// AMP is the optional text/x-amp-html part, placed between the text and HTML parts (clients
	// that can't show AMP pick the last part they understand, the HTML).
	AMP         string
	Headers     map[string]string // extra headers (List-Unsubscribe)
	Attachments []Attachment
	Date        time.Time
	// Ref is the message reference (X-Pulse-Ref and the Message-ID); Service adds the sending
	// service's own metadata header with it (RefHeaders).
	Ref, Service string
}

// Sending services whose webhooks Pulse reads (package bounces).
const (
	ServiceSendGrid  = "sendgrid"
	ServiceMailgun   = "mailgun"
	ServicePostmark  = "postmark"
	ServiceSES       = "ses"
	ServiceSparkPost = "sparkpost"
	ServiceOther     = "other"
)

// EmailServices lists the services RefHeaders knows.
var EmailServices = []string{ServiceSendGrid, ServiceMailgun, ServicePostmark, ServiceSES, ServiceSparkPost, ServiceOther}

// DetectService names the sending service of an SMTP relay host ("other" when unknown).
func DetectService(host string) string {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	switch {
	case h == "smtp.sendgrid.net":
		return ServiceSendGrid
	case strings.HasSuffix(h, ".mailgun.org"):
		return ServiceMailgun
	case strings.HasSuffix(h, ".postmarkapp.com"):
		return ServicePostmark
	case strings.HasPrefix(h, "email-smtp.") && strings.HasSuffix(h, ".amazonaws.com"):
		return ServiceSES
	case strings.HasSuffix(h, ".sparkpostmail.com"):
		return ServiceSparkPost
	}
	return ServiceOther
}

// smtpService is the configured service, or the one the host belongs to.
func smtpService(configured, host string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(configured))
	if v == "" || v == "auto" {
		return DetectService(host), nil
	}
	for _, s := range EmailServices {
		if v == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("sending service must be auto, %s", strings.Join(EmailServices, ", "))
}

// RefHeaders are the headers that carry a message reference: X-Pulse-Ref always (bounce emails
// quote the original headers), plus the service's own metadata header, which its webhooks echo:
// SendGrid unique_args (X-SMTPAPI), Mailgun variables, Postmark metadata (80 characters a value, so
// longer references are split over pulse_ref and pulse_ref2), SES message tags, SparkPost metadata.
func RefHeaders(service, ref string) map[string]string {
	if ref == "" {
		return nil
	}
	h := map[string]string{"X-Pulse-Ref": ref}
	switch service {
	case ServiceSendGrid:
		h["X-SMTPAPI"] = `{"unique_args":{"pulse_ref":"` + ref + `"}}`
	case ServiceMailgun:
		h["X-Mailgun-Variables"] = `{"pulse_ref":"` + ref + `"}`
	case ServicePostmark:
		if len(ref) <= 80 {
			h["X-PM-Metadata-pulse_ref"] = ref
		} else {
			h["X-PM-Metadata-pulse_ref"], h["X-PM-Metadata-pulse_ref2"] = ref[:80], ref[80:]
		}
	case ServiceSES:
		h["X-SES-MESSAGE-TAGS"] = "pulse_ref=" + ref
	case ServiceSparkPost:
		h["X-MSYS-API"] = `{"metadata":{"pulse_ref":"` + ref + `"}}`
	}
	return h
}

// refChars are the only characters a reference has; anything else is refused (no header injection).
func refOK(ref string) bool {
	for _, c := range ref {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return len(ref) <= 300
}

// BuildMessage produces the RFC 5322 message bytes. Addresses are parsed, so header injection
// through them is impossible; the subject is Q-encoded. The body is multipart/alternative (text,
// then AMP when there is one, then HTML) when there is a text or AMP part, wrapped in multipart/mixed when there are attachments.
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
	extra := m.Headers
	if m.Ref != "" {
		if !refOK(m.Ref) {
			return nil, errors.New("invalid message reference")
		}
		// The reference names the message for bounces and complaints that quote only its Message-ID.
		domain := "pulse"
		if _, d, ok := strings.Cut(from.Address, "@"); ok && d != "" && !strings.ContainsAny(d, "<>[] ") {
			domain = d
		}
		header("Message-ID", "<"+m.Ref+"@"+domain+">")
		extra = map[string]string{}
		for k, v := range m.Headers {
			extra[k] = v
		}
		for k, v := range RefHeaders(m.Service, m.Ref) {
			extra[k] = v
		}
	} else {
		header("Message-ID", "<"+m.ID+"@pulse>")
	}
	header("X-Pulse-Message-Id", m.ID)
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

	// body writes the message's content (text and HTML) as one entity, created by w.
	body := func(w func(textproto.MIMEHeader) (io.Writer, error)) error {
		if m.Text == "" && m.AMP == "" {
			return textPart(w, "text/html", m.HTML)
		}
		alt := multipart.NewWriter(io.Discard)
		pw, err := w(textproto.MIMEHeader{"Content-Type": {"multipart/alternative; boundary=" + strconv.Quote(alt.Boundary())}})
		if err != nil {
			return err
		}
		alt = newWriterWithBoundary(pw, alt.Boundary())
		create := func(h textproto.MIMEHeader) (io.Writer, error) { return alt.CreatePart(h) }
		if m.Text != "" {
			if err := textPart(create, "text/plain", m.Text); err != nil {
				return err
			}
		}
		if m.AMP != "" {
			if err := textPart(create, "text/x-amp-html", m.AMP); err != nil {
				return err
			}
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
