// Package providers holds the delivery provider adapters (SMTP, Twilio, MSG91, Meta WhatsApp Cloud,
// Google RCS Business Messaging, FCM, sandbox) behind one interface, plus the catalog the web app
// uses to render each provider's settings form.
package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"pulse/internal/channel"
)

// Errors adapters return so the dispatcher can decide what to do. Anything else is transient and retried.
var (
	// ErrUnreachable: the recipient can't get messages on this channel (not a WhatsApp user, RCS not
	// supported, invalid push token). WhatsApp and RCS messages then fall back to SMS.
	ErrUnreachable = errors.New("recipient unreachable on this channel")
	// ErrPermanent: retrying won't help (rejected template, invalid number, bad credentials).
	ErrPermanent = errors.New("permanent delivery failure")
	// ErrBadNumber: the phone number itself is invalid or can't receive messages (always also
	// ErrPermanent). An SMS that fails this way marks the contact sms_bounced.
	ErrBadNumber = errors.New("invalid or unreachable phone number")
	// ErrUnauthorized: a webhook's signature did not verify.
	ErrUnauthorized = errors.New("webhook signature invalid")
)

// Unreachable and Permanent wrap a provider's own reason with the sentinel.
func Unreachable(reason string) error { return fmt.Errorf("%w: %s", ErrUnreachable, reason) }
func Permanent(reason string) error   { return fmt.Errorf("%w: %s", ErrPermanent, reason) }

// BadNumber is a permanent failure caused by the recipient's phone number.
func BadNumber(reason string) error {
	return fmt.Errorf("%w: %w: %s", ErrPermanent, ErrBadNumber, reason)
}

// Bounced reports whether an SMS failure means the number bounced: the provider says the number
// is invalid, or that it can't be reached on SMS.
func Bounced(err error) bool { return errors.Is(err, ErrBadNumber) || errors.Is(err, ErrUnreachable) }

// Outbound is one rendered message to one recipient address.
type Outbound struct {
	TenantID  int64
	MessageID string // Pulse's id; adapters pass it to the provider where possible (idempotency, receipts)
	Channel   string
	To        string // email address, E.164 phone number or push token
	Platform  string // push: android | ios | web
	From      string // email sender
	Rendered  channel.Rendered
	Headers   map[string]string // email: extra headers (List-Unsubscribe)
	// email: files to attach (SMTP sends them in multipart/mixed; the custom gateway gets URLs)
	Attachments []Attachment
}

// Attachment is one file attached to an email.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
	// URL is a signed, expiring download link for the file, for providers that take links rather
	// than bytes (the custom HTTP gateway). Empty without a server secret key.
	URL string
}

// Result is what the provider returned for an accepted message.
type Result struct {
	ProviderMessageID string
}

type Provider interface {
	Send(ctx context.Context, o Outbound) (Result, error)
}

// Receipt is a parsed delivery report, or (Status "inbound") a message the contact sent us, such
// as a STOP reply.
type Receipt struct {
	ProviderMessageID string `json:"provider_message_id"`
	Status            string `json:"status"`      // delivered | failed | read | inbound
	Unreachable       bool   `json:"unreachable"` // the recipient isn't reachable on the channel (triggers fallback)
	Reason            string `json:"reason,omitempty"`
	From              string `json:"from,omitempty"` // inbound: the sender's E.164 number
	Text              string `json:"text,omitempty"` // inbound: the message text
	// inbound RCS: a tapped suggestion ("reply" or "action") and its postback data
	Suggestion string `json:"suggestion,omitempty"`
	Postback   string `json:"postback,omitempty"`
}

// Inbound is the Status of a receipt that is a message from the contact.
const Inbound = "inbound"

// ReceiptParser is implemented by adapters that accept delivery webhooks. It must verify the
// provider's signature (return ErrUnauthorized when it fails) before parsing anything.
type ReceiptParser interface {
	ParseReceipts(r *http.Request) ([]Receipt, error)
}

// ChallengeResponder is implemented by adapters whose provider verifies the webhook URL with a GET
// request first (Meta's hub.challenge). ok=false means the challenge is invalid.
type ChallengeResponder interface {
	Challenge(r *http.Request) (response string, ok bool)
}

// BodyChallenger is implemented by adapters whose provider verifies the webhook URL with a POST
// (Google RBM's clientToken / secret handshake). isChallenge=false means body is an ordinary
// webhook; ok=false means it is a challenge that didn't verify.
type BodyChallenger interface {
	BodyChallenge(body []byte) (response []byte, isChallenge, ok bool)
}

// Field describes one setting in a provider's form.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
}

// Kind is one provider type in the catalog.
type Kind struct {
	ID          string  `json:"id"`
	Channel     string  `json:"channel"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Config      []Field `json:"config"`
	Secrets     []Field `json:"secrets"`
	// New builds an adapter from saved settings.
	New func(d Deps, config, secrets map[string]string) (Provider, error) `json:"-"`
}

// Deps are shared resources adapters may need.
type Deps struct {
	HTTP    *http.Client
	Sandbox SandboxSink
	// AllowPrivate lets user-entered endpoints (the custom gateway) reach private addresses
	// (local development only).
	AllowPrivate bool
}

var kinds = map[string]Kind{}

func register(k Kind) { kinds[k.ID] = k }

// Catalog lists every provider kind, by channel then name.
func Catalog() []Kind {
	out := make([]Kind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k)
	}
	order := map[string]int{}
	for i, c := range channel.All {
		order[c] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Channel != out[j].Channel {
			return order[out[i].Channel] < order[out[j].Channel]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Lookup returns a kind by id.
func Lookup(id string) (Kind, bool) {
	k, ok := kinds[id]
	return k, ok
}

// Validate checks that the required settings are present (secrets may be omitted on update to keep the stored ones).
func (k Kind) Validate(config, secrets map[string]string, requireSecrets bool) error {
	for _, f := range k.Config {
		if f.Required && config[f.Key] == "" {
			return fmt.Errorf("%s is required", f.Label)
		}
	}
	if requireSecrets {
		for _, f := range k.Secrets {
			if f.Required && secrets[f.Key] == "" {
				return fmt.Errorf("%s is required", f.Label)
			}
		}
	}
	return nil
}

// New builds a provider of the given kind.
func New(kind string, d Deps, config, secrets map[string]string) (Provider, error) {
	k, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", kind)
	}
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	return k.New(d, config, secrets)
}

// StatusError classifies an HTTP status from a provider API: 5xx and 429 are transient,
// other 4xx permanent unless the adapter knows better. Pass the response headers when there are
// any: a transient error then carries the provider's Retry-After (see RetryAfter).
func StatusError(provider string, code int, body string, h ...http.Header) error {
	msg := fmt.Sprintf("%s: HTTP %d: %.300s", provider, code, body)
	if code == http.StatusTooManyRequests || code >= 500 {
		err := errors.New(msg)
		if len(h) > 0 && h[0] != nil {
			if after := ParseRetryAfter(h[0].Get("Retry-After"), time.Now()); after > 0 {
				return &RetryAfterError{Err: err, After: after}
			}
		}
		return err
	}
	return Permanent(msg)
}

// RetryAfterError is a transient failure for which the provider said how long to wait (HTTP
// Retry-After on a 429 or 503). The dispatcher waits at least that long before the next try.
type RetryAfterError struct {
	Err   error
	After time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }
func (e *RetryAfterError) Unwrap() error { return e.Err }

// RetryAfter returns the wait a provider asked for, if the error carries one.
func RetryAfter(err error) (time.Duration, bool) {
	var ra *RetryAfterError
	if errors.As(err, &ra) && ra.After > 0 {
		return ra.After, true
	}
	return 0, false
}

// Transient reports whether retrying a failed send later may succeed: anything but a permanent
// failure or an unreachable recipient (network errors, timeouts, 429 and 5xx).
func Transient(err error) bool {
	return err != nil && !errors.Is(err, ErrPermanent) && !errors.Is(err, ErrUnreachable)
}

// ParseRetryAfter reads a Retry-After header: delay seconds ("120") or an HTTP date. It returns 0
// when the header is missing, unreadable or in the past.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
