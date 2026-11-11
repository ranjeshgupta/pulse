// Package bounces reads email delivery, bounce and complaint reports from the common email
// services into one shape. Pulse acts on them: a hard bounce stops email to the address, a
// complaint unsubscribes the contact from email, a soft bounce is only recorded, and a delivery
// report records email_delivered.
//
// Supported bodies: SendGrid event webhook, Postmark bounce / spam / delivery webhooks, Mailgun
// (v3 event-data), Amazon SES notifications through SNS, SparkPost event webhooks, and a generic
// {"email", "type": hard|soft|complaint|delivered, "reason", "ref"} (or a list of them) for
// anything else. Each report carries the message reference Pulse put on the email (see
// tracking.Ref and providers.RefHeaders) when the service echoes it, so the event is linked to the
// message, its campaign and its template; reports without one are recorded on the address's
// contact, unlinked (deliveries without one are dropped: they would count nowhere).
package bounces

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

const (
	Hard      = "hard"
	Soft      = "soft"
	Complaint = "complaint"
	Delivered = "delivered"
)

// Formats of webhook bodies.
const (
	FormatSendGrid  = "sendgrid"
	FormatPostmark  = "postmark"
	FormatMailgun   = "mailgun"
	FormatSES       = "ses" // through Amazon SNS
	FormatSESDirect = "ses_direct"
	FormatSparkPost = "sparkpost"
	FormatGeneric   = "generic"
)

type Bounce struct {
	Email  string `json:"email"`
	Kind   string `json:"kind"` // hard | soft | complaint | delivered
	Reason string `json:"reason,omitempty"`
	Source string `json:"source"` // sendgrid | postmark | mailgun | ses | sparkpost | generic | dsn | arf
	// Ref is the message reference the report quotes (X-Pulse-Ref, the service's metadata, or the
	// original Message-ID); checked when the report is linked.
	Ref string `json:"ref,omitempty"`
	// Key, when set, makes the recorded events' ids stable, so reading the same report twice
	// (a mailbox message whose \Seen flag didn't stick) records it once.
	Key string `json:"-"`
}

// SNSConfirm is an Amazon SNS subscription confirmation: Pulse must GET SubscribeURL once.
type SNSConfirm struct{ SubscribeURL string }

// ErrUnknown is returned for a body none of the parsers recognise.
var ErrUnknown = errors.New("unrecognised bounce webhook body")

// Detect names the format of a webhook body ("" when unknown).
func Detect(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var arr []map[string]any
		if json.Unmarshal(body, &arr) != nil || len(arr) == 0 {
			return FormatGeneric
		}
		if _, ok := arr[0]["msys"]; ok {
			return FormatSparkPost
		}
		if _, ok := arr[0]["event"]; ok {
			return FormatSendGrid
		}
		return FormatGeneric
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	switch {
	case str(m, "Type") == "SubscriptionConfirmation" || str(m, "Type") == "Notification" || str(m, "Type") == "UnsubscribeConfirmation":
		return FormatSES
	case m["notificationType"] != nil || m["eventType"] != nil:
		return FormatSESDirect
	case m["RecordType"] != nil:
		return FormatPostmark
	case m["event-data"] != nil:
		return FormatMailgun
	case m["msys"] != nil:
		return FormatSparkPost
	}
	return FormatGeneric
}

// Parse recognises the body's format. It returns the reports, or an SNS confirmation to perform.
func Parse(body []byte) ([]Bounce, *SNSConfirm, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, nil, err
		}
		var out []Bounce
		for _, m := range arr {
			if ms, ok := m["msys"].(map[string]any); ok {
				if b, ok := sparkpost(ms); ok {
					out = append(out, b)
				}
				continue
			}
			if _, ok := m["event"]; ok {
				if b, ok := sendgrid(m); ok {
					out = append(out, b)
				}
				continue
			}
			if b, ok := generic(m); ok {
				out = append(out, b)
			}
		}
		return out, nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, nil, err
	}
	switch {
	case str(m, "Type") == "SubscriptionConfirmation":
		u := str(m, "SubscribeURL")
		if !snsURL(u) {
			return nil, nil, errors.New("SubscribeURL is not an Amazon SNS address")
		}
		return nil, &SNSConfirm{SubscribeURL: u}, nil
	case str(m, "Type") == "UnsubscribeConfirmation":
		return nil, nil, nil
	case str(m, "Type") == "Notification":
		var inner map[string]any
		if err := json.Unmarshal([]byte(str(m, "Message")), &inner); err != nil {
			return nil, nil, errors.New("SNS notification without an SES message")
		}
		return ses(inner), nil, nil
	case m["notificationType"] != nil || m["eventType"] != nil: // SES via other transports
		return ses(m), nil, nil
	case m["RecordType"] != nil:
		b, ok := postmark(m)
		if !ok {
			return nil, nil, nil
		}
		return []Bounce{b}, nil, nil
	case m["event-data"] != nil:
		ed, _ := m["event-data"].(map[string]any)
		b, ok := mailgun(ed)
		if !ok {
			return nil, nil, nil
		}
		return []Bounce{b}, nil, nil
	case m["msys"] != nil:
		ms, _ := m["msys"].(map[string]any)
		if b, ok := sparkpost(ms); ok {
			return []Bounce{b}, nil, nil
		}
		return nil, nil, nil
	}
	if b, ok := generic(m); ok {
		return []Bounce{b}, nil, nil
	}
	return nil, nil, ErrUnknown
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
}

// msgIDRef takes a reference out of a Message-ID ("<p1…@example.com>"); "" when it isn't one.
func msgIDRef(v string) string {
	v = strings.Trim(strings.TrimSpace(v), "<>")
	if at := strings.IndexByte(v, '@'); at > 0 {
		v = v[:at]
	}
	if strings.HasPrefix(v, "p1") && strings.Contains(v, ".") {
		return v
	}
	return ""
}

// snsURL accepts only https://sns.<region>.amazonaws.com/..., so a forged confirmation or signing
// certificate URL can't make Pulse call an arbitrary address.
func snsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Port() == "" && u.User == nil && snsHost.MatchString(u.Hostname())
}

var snsHost = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

func sendgrid(m map[string]any) (Bounce, bool) {
	b := Bounce{Email: str(m, "email"), Reason: str(m, "reason"), Source: "sendgrid", Ref: str(m, "pulse_ref")}
	if b.Ref == "" {
		b.Ref = msgIDRef(str(m, "smtp-id"))
	}
	switch str(m, "event") {
	case "delivered":
		b.Kind = Delivered
		b.Reason = str(m, "response")
	case "bounce":
		b.Kind = Hard
		if str(m, "type") == "blocked" {
			b.Kind = Soft
		}
	case "deferred", "dropped":
		b.Kind = Soft
		if b.Reason == "" {
			b.Reason = str(m, "response")
		}
	case "spamreport":
		b.Kind = Complaint
	default:
		return b, false
	}
	return b, b.Email != ""
}

func postmark(m map[string]any) (Bounce, bool) {
	b := Bounce{Email: str(m, "Email"), Reason: str(m, "Description"), Source: "postmark"}
	if md := obj(m, "Metadata"); md != nil {
		b.Ref = str(md, "pulse_ref") + str(md, "pulse_ref2")
	}
	switch str(m, "RecordType") {
	case "Delivery":
		b.Kind, b.Email, b.Reason = Delivered, str(m, "Recipient"), str(m, "Details")
	case "Bounce":
		switch str(m, "Type") {
		case "HardBounce", "BadEmailAddress", "ManuallyDeactivated", "Blocked":
			b.Kind = Hard
		default:
			b.Kind = Soft
		}
	case "SpamComplaint":
		b.Kind = Complaint
	default:
		return b, false
	}
	return b, b.Email != ""
}

func mailgun(ev map[string]any) (Bounce, bool) {
	b := Bounce{Email: str(ev, "recipient"), Source: "mailgun"}
	if uv := obj(ev, "user-variables"); uv != nil {
		b.Ref = str(uv, "pulse_ref")
	}
	if b.Ref == "" {
		b.Ref = msgIDRef(str(obj(obj(ev, "message"), "headers"), "message-id"))
	}
	if ds := obj(ev, "delivery-status"); ds != nil {
		b.Reason = str(ds, "message")
		if b.Reason == "" {
			b.Reason = str(ds, "description")
		}
	}
	switch str(ev, "event") {
	case "delivered":
		b.Kind = Delivered
	case "failed":
		b.Kind = Soft
		if str(ev, "severity") == "permanent" {
			b.Kind = Hard
		}
	case "complained":
		b.Kind = Complaint
	default:
		return b, false
	}
	return b, b.Email != ""
}

// sesRef finds the reference of an SES notification: its message tags, the original headers, or
// the original Message-ID.
func sesRef(m map[string]any) string {
	mail := obj(m, "mail")
	if tags := obj(mail, "tags"); tags != nil {
		if vs, ok := tags["pulse_ref"].([]any); ok && len(vs) > 0 {
			if v, _ := vs[0].(string); v != "" {
				return v
			}
		}
	}
	if hs, ok := mail["headers"].([]any); ok {
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if strings.EqualFold(str(hm, "name"), "X-Pulse-Ref") {
				return str(hm, "value")
			}
		}
	}
	return msgIDRef(str(obj(mail, "commonHeaders"), "messageId"))
}

func ses(m map[string]any) []Bounce {
	kind := str(m, "notificationType")
	if kind == "" {
		kind = str(m, "eventType")
	}
	ref := sesRef(m)
	var out []Bounce
	switch kind {
	case "Bounce":
		bo := obj(m, "bounce")
		k := Soft
		if str(bo, "bounceType") == "Permanent" {
			k = Hard
		}
		recips, _ := bo["bouncedRecipients"].([]any)
		for _, r := range recips {
			rm, _ := r.(map[string]any)
			if e := str(rm, "emailAddress"); e != "" {
				out = append(out, Bounce{Email: e, Kind: k, Reason: str(rm, "diagnosticCode"), Source: "ses", Ref: ref})
			}
		}
	case "Complaint":
		co := obj(m, "complaint")
		recips, _ := co["complainedRecipients"].([]any)
		for _, r := range recips {
			rm, _ := r.(map[string]any)
			if e := str(rm, "emailAddress"); e != "" {
				out = append(out, Bounce{Email: e, Kind: Complaint, Reason: str(co, "complaintFeedbackType"), Source: "ses", Ref: ref})
			}
		}
	case "Delivery":
		de := obj(m, "delivery")
		recips, _ := de["recipients"].([]any)
		for _, r := range recips {
			if e, _ := r.(string); e != "" {
				out = append(out, Bounce{Email: e, Kind: Delivered, Reason: str(de, "smtpResponse"), Source: "ses", Ref: ref})
			}
		}
	}
	return out
}

// sparkpostHard are SparkPost bounce classes that are permanent (invalid recipient, no RCPT,
// unsubscribe, admin failure); every other class is temporary.
var sparkpostHard = map[string]bool{"10": true, "25": true, "30": true, "90": true}

func sparkpost(ms map[string]any) (Bounce, bool) {
	var ev map[string]any
	for _, k := range []string{"message_event", "track_event", "unsubscribe_event", "relay_event", "gen_event"} {
		if ev = obj(ms, k); ev != nil {
			break
		}
	}
	if ev == nil {
		return Bounce{}, false
	}
	b := Bounce{Email: str(ev, "rcpt_to"), Reason: str(ev, "reason"), Source: "sparkpost"}
	for _, k := range []string{"rcpt_meta", "metadata"} {
		if md := obj(ev, k); md != nil && b.Ref == "" {
			b.Ref = str(md, "pulse_ref")
		}
	}
	switch str(ev, "type") {
	case "delivery":
		b.Kind = Delivered
		b.Reason = str(ev, "raw_reason")
	case "bounce", "out_of_band":
		b.Kind = Soft
		if sparkpostHard[str(ev, "bounce_class")] {
			b.Kind = Hard
		}
	case "spam_complaint":
		b.Kind = Complaint
		if b.Reason == "" {
			b.Reason = str(ev, "fbtype")
		}
	default:
		return b, false
	}
	return b, b.Email != ""
}

func generic(m map[string]any) (Bounce, bool) {
	b := Bounce{Email: str(m, "email"), Kind: strings.ToLower(str(m, "type")), Reason: str(m, "reason"), Source: "generic", Ref: str(m, "ref")}
	switch b.Kind {
	case Hard, Soft, Complaint, Delivered:
		return b, b.Email != ""
	}
	return b, false
}
