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
	// ErrUnauthorized: a webhook's signature did not verify.
	ErrUnauthorized = errors.New("webhook signature invalid")
)

// Unreachable and Permanent wrap a provider's own reason with the sentinel.
func Unreachable(reason string) error { return fmt.Errorf("%w: %s", ErrUnreachable, reason) }
func Permanent(reason string) error   { return fmt.Errorf("%w: %s", ErrPermanent, reason) }

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
// other 4xx permanent unless the adapter knows better.
func StatusError(provider string, code int, body string) error {
	msg := fmt.Sprintf("%s: HTTP %d: %.300s", provider, code, body)
	if code == http.StatusTooManyRequests || code >= 500 {
		return errors.New(msg)
	}
	return Permanent(msg)
}
