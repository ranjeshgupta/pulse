package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/reasons"
)

func init() {
	register(Kind{
		ID: "gupshup_whatsapp", Channel: channel.WhatsApp, Name: "Gupshup WhatsApp",
		Description: "Approved WhatsApp template messages (and session text) through Gupshup's WhatsApp API.",
		Notes: "Endpoints are built from Gupshup's public docs (/wa/api/v1/template/msg, /wa/api/v1/msg, v2 message " +
			"events) and tested against mocks only: confirm them with a real account before production use. A " +
			"template's name must be the Gupshup template id (its language is fixed by that template). Messaging " +
			"limits are set by Meta per phone number (tier); set this provider's rate to match. In the Gupshup app's " +
			"webhook settings, set the callback URL to this provider's webhook URL with ?token=… and the v2 format.",
		Config: []Field{
			{Key: "source", Label: "Source number (your WhatsApp business number)", Placeholder: "919812345678", Required: true,
				Hint: "Digits with the country code, no +."},
			{Key: "app_name", Label: "App name (src.name)", Placeholder: "AcmeApp", Required: true,
				Hint: "The Gupshup app the source number belongs to."},
			{Key: "base_url", Label: "API URL", Default: gupshupWhatsAppURL, Placeholder: gupshupWhatsAppURL,
				Hint: "Change it only if Gupshup gives you another API host."},
		},
		Secrets: []Field{
			{Key: "api_key", Label: "API key", Required: true, Hint: "The app's API key (sent in the apikey header)."},
			gupshupTokenField,
		},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			base, err := gupshupBaseURL("gupshup whatsapp", c["base_url"], gupshupWhatsAppURL)
			if err != nil {
				return nil, err
			}
			return &GupshupWhatsApp{HTTP: gupshupClient(d), BaseURL: base, Source: c["source"], AppName: c["app_name"],
				APIKey: s["api_key"], WebhookToken: s["webhook_token"]}, nil
		},
	})
}

// GupshupWhatsApp sends template messages (Content.TemplateName is the Gupshup template id) or,
// without a template, a session text message, and parses Gupshup's v2 webhooks.
type GupshupWhatsApp struct {
	HTTP         *http.Client
	BaseURL      string
	Source       string
	AppName      string
	APIKey       string
	WebhookToken string
}

// Gupshup's own WhatsApp error codes (docs.gupshup.io/docs/error-and-status-messages); other codes
// are Meta's, passed through.
var gupshupWACodes = map[int]string{
	1001: reasons.ProviderRejected, 1002: reasons.NotOnChannel, 1003: reasons.ProviderRejected,
	1004: reasons.Template, 1005: reasons.Template, 1006: reasons.Consent, 1007: reasons.Consent, 1008: reasons.Consent,
	1009: reasons.Template, 1010: reasons.Template, 1011: reasons.Template, 1012: reasons.Consent,
	4001: reasons.RateLimited, 4002: reasons.ProviderError, 4003: reasons.Template, 4004: reasons.ProviderRejected,
	4005: reasons.Template, 500: reasons.ProviderError,
}

// gupshupWACode maps a failed event's code, and whether the user can't get WhatsApp messages at
// all (an SMS fallback follows): 1002 "number does not exist on WhatsApp" and Meta's 131026.
func gupshupWACode(code int) (string, bool) {
	if c, ok := gupshupWACodes[code]; ok {
		return c, code == 1002
	}
	return reasons.MetaCode(code), metaUnreachable[code]
}

func (g *GupshupWhatsApp) build(o Outbound) (gupshupRequest, error) {
	c := o.Rendered.Content
	form := map[string][]string{
		"channel": {"whatsapp"}, "source": {digits(strings.TrimSpace(g.Source))}, "destination": {digits(o.To)},
		"src.name": {g.AppName},
	}
	var path string
	switch {
	case c.TemplateName != "":
		path = "/wa/api/v1/template/msg"
		params := c.BodyParams
		if params == nil {
			params = []string{}
		}
		tpl, _ := json.Marshal(map[string]any{"id": c.TemplateName, "params": params})
		form["template"] = []string{string(tpl)}
		if c.HeaderImageURL != "" {
			msg, _ := json.Marshal(map[string]any{"type": "image", "image": map[string]string{"link": c.HeaderImageURL}})
			form["message"] = []string{string(msg)}
		}
	case c.Text != "":
		path = "/wa/api/v1/msg"
		msg, _ := json.Marshal(map[string]string{"type": "text", "text": c.Text})
		form["message"] = []string{string(msg)}
	default:
		return gupshupRequest{}, Permanent("gupshup whatsapp: the message has no template id or text")
	}
	h := formHeaders()
	h["apikey"] = g.APIKey
	return gupshupRequest{URL: g.BaseURL + path, Form: form, Headers: h, SecretHeaders: []string{"apikey"}}, nil
}

func (g *GupshupWhatsApp) Send(ctx context.Context, o Outbound) (Result, error) {
	b, err := g.build(o)
	if err != nil {
		return Result{}, err
	}
	req, err := b.httpRequest(ctx)
	if err != nil {
		return Result{}, err
	}
	code, body, hdr, err := call(g.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	var r struct {
		Status    string `json:"status"`
		MessageID string `json:"messageId"`
		Message   any    `json:"message"`
	}
	_ = json.Unmarshal(body, &r)
	if code >= 200 && code < 300 && r.MessageID != "" && r.Status != "error" {
		return Result{ProviderMessageID: r.MessageID}, nil
	}
	msg := strings.TrimSpace(anyString(r.Message))
	low := strings.ToLower(msg)
	switch {
	case code == http.StatusTooManyRequests || code >= 500:
		return Result{}, StatusError("gupshup whatsapp", code, string(body), hdr)
	case code == http.StatusUnauthorized || code == http.StatusForbidden || strings.Contains(low, "authentication"):
		return Result{}, Permanent(fmt.Sprintf("gupshup whatsapp: HTTP %d: %s (check the API key credentials)", code, msg))
	case strings.Contains(low, "invalid destination"):
		return Result{}, BadNumber("gupshup whatsapp: " + msg)
	case code >= 200 && code < 300 && r.Status != "error":
		return Result{}, Permanent(fmt.Sprintf("gupshup whatsapp: response without messageId: %.300s", body)) // may have been sent
	case r.Status == "error" && msg != "":
		return Result{}, Permanent(fmt.Sprintf("gupshup whatsapp: HTTP %d: %s", code, msg))
	}
	return Result{}, StatusError("gupshup whatsapp", code, string(body), hdr)
}

// PreviewRequest returns the request Send would make for o, with the API key masked.
func (g *GupshupWhatsApp) PreviewRequest(o Outbound) (method, url string, headers map[string]string, body string) {
	b, err := g.build(o)
	if err != nil {
		return http.MethodPost, g.BaseURL, nil, err.Error()
	}
	return b.preview()
}

// gupshupWAEvent is a v2 webhook: {"app","timestamp","version","type":"message"|"message-event"|…,
// "payload":{…}}.
type gupshupWAEvent struct {
	Type    string `json:"type"`
	Payload struct {
		ID          string          `json:"id"`
		GsID        string          `json:"gsId"`
		Type        string          `json:"type"`
		Source      string          `json:"source"`
		Destination string          `json:"destination"`
		Payload     json.RawMessage `json:"payload"`
		Sender      struct {
			Phone string `json:"phone"`
		} `json:"sender"`
	} `json:"payload"`
}

// ParseReceipts reads Gupshup's v2 webhooks (the token must match). message-event delivered / read
// / failed become receipts for the Gupshup message id returned at send: gsId on sent, delivered,
// read and asynchronous failures (their id is WhatsApp's), id on enqueued and synchronous failures.
// User messages (type "message") become inbound receipts; everything else is ignored.
func (g *GupshupWhatsApp) ParseReceipts(r *http.Request) ([]Receipt, error) {
	if err := checkGupshupToken(r, g.WebhookToken); err != nil {
		return nil, err
	}
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	b := bytes.TrimSpace(body)
	if len(b) == 0 {
		return nil, nil // Gupshup's check of the callback URL
	}
	var events []gupshupWAEvent
	if b[0] == '[' {
		err = json.Unmarshal(b, &events)
	} else {
		var e gupshupWAEvent
		err = json.Unmarshal(b, &e)
		events = []gupshupWAEvent{e}
	}
	if err != nil {
		return nil, fmt.Errorf("gupshup whatsapp webhook: %w", err)
	}
	var out []Receipt
	for _, e := range events {
		if rc, ok := gupshupWAReceipt(e); ok {
			out = append(out, rc)
		}
	}
	return out, nil
}

func gupshupWAReceipt(e gupshupWAEvent) (Receipt, bool) {
	p := e.Payload
	switch e.Type {
	case "message":
		var in struct {
			Text  string `json:"text"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal(p.Payload, &in)
		text := in.Text
		if text == "" { // button_reply / list_reply
			text = in.Title
		}
		from := p.Source
		if from == "" {
			from = p.Sender.Phone
		}
		if from == "" || text == "" {
			return Receipt{}, false // media, location…: nothing Pulse reads
		}
		return Receipt{ProviderMessageID: p.ID, Status: Inbound, From: plusE164(from), Text: text}, true
	case "message-event":
	default:
		return Receipt{}, false
	}
	id := p.GsID
	if id == "" {
		id = p.ID
	}
	rc := Receipt{ProviderMessageID: id}
	switch p.Type {
	case "delivered", "read":
		rc.Status = p.Type
	case "failed":
		rc.Status = "failed"
		var f struct {
			Code   json.RawMessage `json:"code"`
			Reason string          `json:"reason"`
		}
		_ = json.Unmarshal(p.Payload, &f)
		code, err := strconv.Atoi(strings.Trim(string(f.Code), `" `))
		if err != nil {
			rc.Reason = "gupshup whatsapp: " + f.Reason
			rc.Code = reasons.ProviderRejected
			break
		}
		rc.Code, rc.Unreachable = gupshupWACode(code)
		rc.Reason = fmt.Sprintf("gupshup whatsapp %d: %s", code, f.Reason)
		if _, own := gupshupWACodes[code]; !own {
			// Meta's code: written like the Cloud API adapter's so reason text reads the same.
			rc.Reason = fmt.Sprintf("meta whatsapp %d: %s", code, f.Reason)
		}
	default: // enqueued, sent, deleted
		return Receipt{}, false
	}
	return rc, id != ""
}
