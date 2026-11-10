// Package bounces reads email bounce and complaint webhooks from the common email services into
// one shape. Pulse acts on them: a hard bounce stops email to the address, a complaint
// unsubscribes the contact from email, and a soft bounce is only recorded.
//
// Supported bodies: SendGrid event webhook, Postmark bounce/spam webhooks, Mailgun (v3 event-data),
// Amazon SES notifications through SNS, and a generic {"email", "type": hard|soft|complaint,
// "reason"} (or a list of them) for anything else.
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
)

type Bounce struct {
	Email  string `json:"email"`
	Kind   string `json:"kind"` // hard | soft | complaint
	Reason string `json:"reason,omitempty"`
	Source string `json:"source"` // sendgrid | postmark | mailgun | ses | generic
}

// SNSConfirm is an Amazon SNS subscription confirmation: Pulse must GET SubscribeURL once.
type SNSConfirm struct{ SubscribeURL string }

// ErrUnknown is returned for a body none of the parsers recognise.
var ErrUnknown = errors.New("unrecognised bounce webhook body")

// Parse recognises the body's format. It returns the bounces, or an SNS confirmation to perform.
func Parse(body []byte) ([]Bounce, *SNSConfirm, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, nil, err
		}
		var out []Bounce
		for _, m := range arr {
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
		b, ok := mailgun(m["event-data"].(map[string]any))
		if !ok {
			return nil, nil, nil
		}
		return []Bounce{b}, nil, nil
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

// snsURL accepts only https://sns.<region>.amazonaws.com/..., so a forged confirmation can't make
// Pulse call an arbitrary address.
//
// The bounce URL itself is the credential (an unguessable per-workspace token), so SNS message
// signatures aren't verified here.
func snsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Port() == "" && u.User == nil && snsHost.MatchString(u.Hostname())
}

var snsHost = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

func sendgrid(m map[string]any) (Bounce, bool) {
	b := Bounce{Email: str(m, "email"), Reason: str(m, "reason"), Source: "sendgrid"}
	switch str(m, "event") {
	case "bounce":
		b.Kind = Hard
		if str(m, "type") == "blocked" {
			b.Kind = Soft
		}
	case "deferred":
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
	switch str(m, "RecordType") {
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
	if ds, ok := ev["delivery-status"].(map[string]any); ok {
		b.Reason = str(ds, "message")
		if b.Reason == "" {
			b.Reason = str(ds, "description")
		}
	}
	switch str(ev, "event") {
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

func ses(m map[string]any) []Bounce {
	kind := str(m, "notificationType")
	if kind == "" {
		kind = str(m, "eventType")
	}
	var out []Bounce
	switch kind {
	case "Bounce":
		bo, _ := m["bounce"].(map[string]any)
		k := Soft
		if str(bo, "bounceType") == "Permanent" {
			k = Hard
		}
		recips, _ := bo["bouncedRecipients"].([]any)
		for _, r := range recips {
			rm, _ := r.(map[string]any)
			if e := str(rm, "emailAddress"); e != "" {
				out = append(out, Bounce{Email: e, Kind: k, Reason: str(rm, "diagnosticCode"), Source: "ses"})
			}
		}
	case "Complaint":
		co, _ := m["complaint"].(map[string]any)
		recips, _ := co["complainedRecipients"].([]any)
		for _, r := range recips {
			rm, _ := r.(map[string]any)
			if e := str(rm, "emailAddress"); e != "" {
				out = append(out, Bounce{Email: e, Kind: Complaint, Reason: str(co, "complaintFeedbackType"), Source: "ses"})
			}
		}
	}
	return out
}

func generic(m map[string]any) (Bounce, bool) {
	b := Bounce{Email: str(m, "email"), Kind: strings.ToLower(str(m, "type")), Reason: str(m, "reason"), Source: "generic"}
	switch b.Kind {
	case Hard, Soft, Complaint:
		return b, b.Email != ""
	}
	return b, false
}
