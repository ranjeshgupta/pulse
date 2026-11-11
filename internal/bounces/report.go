package bounces

// Bounce and complaint reports that arrive as email, read from a bounce mailbox (mailbox.go):
//
//   - delivery status notifications (RFC 3464): multipart/report; report-type=delivery-status,
//     with a message/delivery-status part (or RFC 6533's message/global-delivery-status) holding one
//     block of fields per recipient: Final-Recipient, Action, Status, Diagnostic-Code;
//   - feedback-loop complaints in the Abuse Reporting Format (RFC 5965): multipart/report;
//     report-type=feedback-report, with a message/feedback-report part (Feedback-Type,
//     Original-Rcpt-To) and the original message or its headers.
//
// Anything else (auto-replies, out-of-office notes, non-standard bounces written as plain text)
// yields no bounces.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
)

const (
	maxReportBytes = 10 << 20 // a report bigger than this is not read
	maxPartBytes   = 1 << 20
	maxDepth       = 5
	maxReason      = 500
)

// ErrNotReport: the message isn't a delivery status notification or a feedback report.
var ErrNotReport = errors.New("not a delivery status or feedback report")

// ParseReport reads one email (RFC 5322 bytes). It returns the bounces of a delivery status
// notification (failed recipients: a 5.x.x status is hard, 4.x.x and delayed are soft; delivered,
// relayed and expanded recipients are skipped) or the complaint of a feedback report
// (Feedback-Type not-spam is skipped). Source is "dsn" or "arf".
func ParseReport(raw []byte) ([]Bounce, error) {
	if len(raw) > maxReportBytes {
		return nil, errors.New("message too large")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrNotReport
	}
	return walk(textproto.MIMEHeader(msg.Header), msg.Body, 0)
}

// walk finds a multipart/report in the message (at the top, or nested in multipart/mixed, as
// some servers forward it) and parses it.
func walk(h textproto.MIMEHeader, body io.Reader, depth int) ([]Bounce, error) {
	if depth > maxDepth {
		return nil, ErrNotReport
	}
	mt, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
		return nil, ErrNotReport
	}
	if mt == "multipart/report" {
		switch strings.ToLower(params["report-type"]) {
		case "delivery-status", "global-delivery-status":
			return parseDSN(multipart.NewReader(body, params["boundary"]))
		case "feedback-report":
			return parseARF(multipart.NewReader(body, params["boundary"]))
		}
		return nil, ErrNotReport
	}
	mr := multipart.NewReader(body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err != nil {
			return nil, ErrNotReport
		}
		if strings.HasPrefix(strings.ToLower(p.Header.Get("Content-Type")), "multipart/") {
			if bs, err := walk(p.Header, p, depth+1); err == nil {
				return bs, nil
			}
		}
	}
}

// partBody returns a part's decoded content (multipart decodes quoted-printable itself).
func partBody(p *multipart.Part) []byte {
	var r io.Reader = io.LimitReader(p, maxPartBytes)
	switch strings.ToLower(strings.TrimSpace(p.Header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, newlineStripper{r})
	case "quoted-printable": // when multipart left it (it removes the header when it decodes)
		r = quotedprintable.NewReader(r)
	}
	b, _ := io.ReadAll(r)
	return b
}

// newlineStripper drops CR and LF, which base64 bodies wrap with.
type newlineStripper struct{ r io.Reader }

func (n newlineStripper) Read(p []byte) (int, error) {
	k, err := n.r.Read(p)
	j := 0
	for _, c := range p[:k] {
		if c != '\r' && c != '\n' {
			p[j] = c
			j++
		}
	}
	return j, err
}

// fieldGroups reads blocks of header-style fields separated by blank lines.
func fieldGroups(b []byte) []textproto.MIMEHeader {
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(bytes.TrimLeft(b, "\r\n"))))
	var out []textproto.MIMEHeader
	for range 1000 {
		h, err := tp.ReadMIMEHeader()
		if len(h) > 0 {
			out = append(out, h)
		}
		if err != nil {
			break
		}
		// skip extra blank lines between groups
		for {
			line, err := tp.R.Peek(1)
			if err != nil || (line[0] != '\r' && line[0] != '\n') {
				break
			}
			_, _ = tp.R.ReadByte()
		}
	}
	return out
}

// address takes the address out of "rfc822; user@example.com" (or "utf-8; ...", or a bare one).
func address(v string) string {
	if _, a, ok := strings.Cut(v, ";"); ok {
		v = a
	}
	v = strings.TrimSpace(v)
	if a, err := mail.ParseAddress(v); err == nil {
		return strings.ToLower(a.Address)
	}
	v = strings.Trim(v, "<> ")
	if strings.Count(v, "@") != 1 || strings.ContainsAny(v, " \t,;") {
		return ""
	}
	return strings.ToLower(v)
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxReason {
		s = s[:maxReason]
	}
	return s
}

func parseDSN(mr *multipart.Reader) ([]Bounce, error) {
	for {
		p, err := mr.NextPart()
		if err != nil {
			return nil, ErrNotReport
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if ct != "message/delivery-status" && ct != "message/global-delivery-status" {
			continue
		}
		groups := fieldGroups(partBody(p))
		var out []Bounce
		for _, g := range groups {
			rcpt := g.Get("Final-Recipient")
			if rcpt == "" {
				rcpt = g.Get("Original-Recipient")
			}
			email := address(rcpt)
			if email == "" { // the per-message block, or a recipient we can't read
				continue
			}
			status := strings.TrimSpace(g.Get("Status"))
			b := Bounce{Email: email, Source: "dsn", Reason: clip(strings.TrimSpace(status + " " + g.Get("Diagnostic-Code")))}
			switch strings.ToLower(strings.TrimSpace(g.Get("Action"))) {
			case "failed":
				b.Kind = Hard
				if strings.HasPrefix(status, "4") {
					b.Kind = Soft
				}
			case "delayed":
				b.Kind = Soft
			default: // delivered, relayed, expanded
				continue
			}
			out = append(out, b)
		}
		return out, nil
	}
}

func parseARF(mr *multipart.Reader) ([]Bounce, error) {
	var report textproto.MIMEHeader
	var original []string // recipients from the original message's headers
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		switch ct {
		case "message/feedback-report":
			if gs := fieldGroups(partBody(p)); len(gs) > 0 {
				report = gs[0]
			}
		case "message/rfc822", "text/rfc822-headers", "message/global", "message/global-headers":
			if m, err := mail.ReadMessage(bytes.NewReader(append(partBody(p), '\r', '\n', '\r', '\n'))); err == nil {
				if as, err := m.Header.AddressList("To"); err == nil {
					for _, a := range as {
						original = append(original, strings.ToLower(a.Address))
					}
				}
			}
		}
	}
	if report == nil {
		return nil, ErrNotReport
	}
	kind := strings.ToLower(strings.TrimSpace(report.Get("Feedback-Type")))
	if kind == "not-spam" {
		return nil, nil
	}
	var rcpts []string
	for _, v := range report.Values("Original-Rcpt-To") {
		if a := address(v); a != "" {
			rcpts = append(rcpts, a)
		}
	}
	if len(rcpts) == 0 {
		rcpts = original
	}
	reason := "feedback: " + kind
	if ua := report.Get("User-Agent"); ua != "" {
		reason += " (" + ua + ")"
	}
	out := make([]Bounce, 0, len(rcpts))
	for _, a := range rcpts {
		out = append(out, Bounce{Email: a, Kind: Complaint, Reason: clip(reason), Source: "arf"})
	}
	return out, nil
}
