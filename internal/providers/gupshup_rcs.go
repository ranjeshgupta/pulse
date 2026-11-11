package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "gupshup_rcs", Channel: channel.RCS, Name: "Gupshup RCS",
		Description: "RCS messages (text, rich cards, suggestions) through Gupshup's Enterprise gateway, from an RBM agent Gupshup hosts.",
		Notes: "Built from Gupshup's public docs (Enterprise GatewayAPI SendMessage with the message JSON in msg) and " +
			"tested against mocks only: the docs show only template messages, so the text / rich card / suggestion " +
			"JSON follows the RBM contentMessage format Gupshup's agents use, and the delivery-report and reply " +
			"formats are assumed to match Gupshup's SMS real-time reports and RBM events. Confirm all of it with a " +
			"real account (RCS access is enabled by Gupshup per account) before production use. Throughput is set per " +
			"agent by Gupshup and the carriers; set this provider's rate to what they confirm. Give Gupshup this " +
			"provider's webhook URL with ?token=… for delivery reports and replies.",
		Config: []Field{
			{Key: "base_url", Label: "API URL", Default: gupshupEnterpriseURL, Placeholder: gupshupEnterpriseURL,
				Hint: "The Enterprise GatewayAPI endpoint; change it only if Gupshup gives you another."},
		},
		Secrets: []Field{
			{Key: "userid", Label: "User ID (Enterprise account number)", Placeholder: "2000123456", Required: true},
			{Key: "password", Label: "Password", Required: true},
			gupshupTokenField,
		},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			base, err := gupshupBaseURL("gupshup rcs", c["base_url"], gupshupEnterpriseURL)
			if err != nil {
				return nil, err
			}
			return &GupshupRCS{gupshupEnterprise{HTTP: gupshupClient(d), BaseURL: base,
				UserID: s["userid"], Password: s["password"], WebhookToken: s["webhook_token"]}}, nil
		},
	})
}

// GupshupRCS sends RCS messages through the Enterprise gateway: method=SendMessage with
// msg={"contentMessage": {...}} (text with suggestions, or a standalone rich card).
type GupshupRCS struct {
	gupshupEnterprise
}

// rcsContentMessage builds an RBM contentMessage: a rich card when Card is set (Text is then not
// sent: a message carries one or the other), text otherwise; suggestions on either.
func rcsContentMessage(c channel.Content) map[string]any {
	var sugg []map[string]any
	for _, s := range c.Suggestions {
		sugg = append(sugg, rbmSuggestion(s))
	}
	msg := map[string]any{}
	if c.Card != nil {
		content := map[string]any{"title": c.Card.Title}
		if c.Card.Description != "" {
			content["description"] = c.Card.Description
		}
		if c.Card.ImageURL != "" {
			content["media"] = map[string]any{"height": "MEDIUM", "contentInfo": map[string]string{"fileUrl": c.Card.ImageURL}}
		}
		if len(sugg) > 0 {
			content["suggestions"] = sugg
		}
		msg["richCard"] = map[string]any{"standaloneCard": map[string]any{"cardOrientation": "VERTICAL", "cardContent": content}}
	} else {
		msg["text"] = c.Text
		if len(sugg) > 0 {
			msg["suggestions"] = sugg
		}
	}
	return map[string]any{"contentMessage": msg}
}

func (g *GupshupRCS) build(o Outbound) gupshupRequest {
	b, _ := json.Marshal(rcsContentMessage(o.Rendered.Content))
	return g.request(g.baseForm(o.To, string(b), "TEXT"))
}

func (g *GupshupRCS) Send(ctx context.Context, o Outbound) (Result, error) {
	res, err := g.send(ctx, "gupshup rcs", g.build(o))
	if low := strings.ReplaceAll(strings.ToLower(fmt.Sprint(err)), "gupshup rcs", ""); err != nil && errors.Is(err, ErrPermanent) && strings.Contains(low, "rcs") &&
		(strings.Contains(low, "capab") || strings.Contains(low, "not enabled") || strings.Contains(low, "not supported") ||
			strings.Contains(low, "unsupported")) {
		return Result{}, Unreachable(err.Error()) // the handset can't take RCS: fall back to SMS
	}
	return res, err
}

// PreviewRequest returns the request Send would make for o, with the password masked.
func (g *GupshupRCS) PreviewRequest(o Outbound) (method, url string, headers map[string]string, body string) {
	return g.build(o).preview()
}

// ParseReceipts reads delivery reports in Gupshup's real-time report format (externalId, status or
// eventType DELIVERED | READ | FAILED, cause, errCode) and, as JSON objects without externalId,
// RBM-style events: user messages (senderPhoneNumber, text or suggestionResponse) and
// DELIVERED / READ events with a messageId. The webhook token must match.
func (g *GupshupRCS) ParseReceipts(r *http.Request) ([]Receipt, error) {
	return parseEnterpriseReceipts(r, g.WebhookToken, "gupshup rcs", channel.RCS, gupshupRBMEvent)
}

func gupshupRBMEvent(m map[string]any) []Receipt {
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	rcs, err := parseRBMMessage(b)
	if err != nil {
		return nil
	}
	for i := range rcs {
		if rcs[i].Status == Inbound {
			rcs[i].From = plusE164(rcs[i].From)
		}
	}
	return rcs
}
