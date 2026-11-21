package providers

// Status requests (polls) and dry runs of the custom gateway: for providers that don't post
// delivery reports, Pulse asks for each message's status until it is final; and the gateway setup
// can check a pasted answer or report before anything is sent.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Poller is a provider that is asked for message statuses (the custom gateway with a status request).
type Poller interface {
	// Polls says how often a sent message's status is asked for, and for how long; ok false: never.
	Polls() (every, follow time.Duration, ok bool)
	// Poll asks for the status of one message: o carries the ids (MessageID, ProviderMessageID,
	// Ref) and the address. Receipts come back with key as their ProviderMessageID.
	Poll(ctx context.Context, o Outbound, key string) ([]Receipt, error)
}

func (g *Gateway) Polls() (time.Duration, time.Duration, bool) {
	if g.Custom == nil || g.Custom.Poll == nil {
		return 0, 0, false
	}
	return g.Custom.Poll.Every(), g.Custom.Poll.For(), true
}

func (g *Gateway) Poll(ctx context.Context, o Outbound, key string) ([]Receipt, error) {
	if g.Custom == nil || g.Custom.Poll == nil {
		return nil, errors.New("gateway: no status request")
	}
	pr := g.Custom.pollRequest()
	code, resp, hdr, err := authCall(ctx, g.HTTP, g.Auth, func() (*http.Request, []byte, error) { return pr.Build(ctx, o) })
	if err != nil {
		return nil, err
	}
	if code < 200 || code > 299 {
		return nil, StatusError("gateway status request", code, string(resp), hdr)
	}
	doc, err := decodeReport(resp, nil)
	if err != nil {
		return nil, Permanent("gateway status request: " + err.Error())
	}
	return pollReceipts(g.Custom.Receipt.mapWith(doc, key), o, key), nil
}

// pollReceipts keeps the receipts of a status answer that are about this message: those naming
// its provider id, Pulse id or reference; or the only one when the answer names none of them
// (status APIs often answer for one message without repeating its id).
func pollReceipts(rs []Receipt, o Outbound, key string) []Receipt {
	var out []Receipt
	for _, rc := range rs {
		id := rc.ProviderMessageID
		if id == key || id == o.ProviderMessageID && id != "" || id == o.MessageID || id == o.Ref && id != "" {
			rc.ProviderMessageID = key
			out = append(out, rc)
		}
	}
	if len(out) == 0 && len(rs) == 1 {
		rc := rs[0]
		rc.ProviderMessageID = key
		out = append(out, rc)
	}
	return out
}

// CheckAnswer is how the gateway would read a send answer (the setup's "Try it" with a pasted
// answer): the provider's message id, or why the message counts as refused.
func (g *Gateway) CheckAnswer(code int, body []byte) (Result, error) {
	if g.Custom == nil {
		return Result{}, errors.New("only your own request mode reads answers this way")
	}
	return g.Custom.Result(code, body, nil)
}

// CheckReport maps a pasted delivery report or status answer, without checking where it came from.
func (g *Gateway) CheckReport(body []byte) ([]Receipt, error) {
	if g.Custom == nil || g.Custom.Receipt == nil {
		return nil, errors.New("set where the message id and status are first")
	}
	doc, err := decodeReport(body, nil)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return g.Custom.Receipt.Map(doc), nil
}
