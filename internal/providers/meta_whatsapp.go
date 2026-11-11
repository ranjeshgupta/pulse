package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "meta_whatsapp", Channel: channel.WhatsApp, Name: "Meta WhatsApp Cloud API",
		Description: "Approved WhatsApp template messages through Meta's Cloud API.",
		Config: []Field{
			{Key: "phone_number_id", Label: "Phone number ID", Placeholder: "106540352242922", Required: true},
			{Key: "waba_id", Label: "WhatsApp Business account ID", Placeholder: "102290129340398"},
		},
		Secrets: []Field{
			{Key: "access_token", Label: "Access token (system user, permanent)", Required: true},
			{Key: "app_secret", Label: "App secret (verifies delivery webhooks)"},
			{Key: "verify_token", Label: "Webhook verify token (any string; enter the same in Meta)"},
		},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			return &MetaWhatsApp{HTTP: d.HTTP, BaseURL: "https://graph.facebook.com", PhoneNumberID: c["phone_number_id"],
				AccessToken: s["access_token"], AppSecret: s["app_secret"], VerifyToken: s["verify_token"]}, nil
		},
	})
}

// MetaWhatsApp sends template messages through the WhatsApp Cloud API, parses its status webhooks
// and answers its webhook verification challenge.
type MetaWhatsApp struct {
	HTTP          *http.Client
	BaseURL       string
	PhoneNumberID string
	AccessToken   string
	AppSecret     string
	VerifyToken   string
}

const metaGraphVersion = "v21.0"

// Graph API error codes, by what the dispatcher should do. Codes not listed fall back to the HTTP status.
var (
	metaUnreachable = map[int]bool{131026: true} // not a WhatsApp user / can't receive
	metaPermanent   = map[int]bool{100: true, 190: true, 131047: true, 131051: true}
	metaTransient   = map[int]bool{4: true, 80007: true, 130429: true, 131048: true, 131056: true}
)

type metaParam struct {
	Type  string            `json:"type"`
	Text  string            `json:"text,omitempty"`
	Image map[string]string `json:"image,omitempty"`
}

type metaComponent struct {
	Type       string      `json:"type"`
	Parameters []metaParam `json:"parameters"`
}

func (m *MetaWhatsApp) Send(ctx context.Context, o Outbound) (Result, error) {
	c := o.Rendered.Content
	lang := c.Language
	if lang == "" {
		lang = "en"
	}
	var comps []metaComponent
	if c.HeaderImageURL != "" {
		comps = append(comps, metaComponent{Type: "header",
			Parameters: []metaParam{{Type: "image", Image: map[string]string{"link": c.HeaderImageURL}}}})
	}
	if len(c.BodyParams) > 0 {
		ps := make([]metaParam, len(c.BodyParams))
		for i, p := range c.BodyParams {
			ps[i] = metaParam{Type: "text", Text: p}
		}
		comps = append(comps, metaComponent{Type: "body", Parameters: ps})
	}
	tpl := map[string]any{"name": c.TemplateName, "language": map[string]string{"code": lang}}
	if len(comps) > 0 {
		tpl["components"] = comps
	}
	payload := map[string]any{
		"messaging_product": "whatsapp", "recipient_type": "individual", "to": digits(o.To),
		"type": "template", "template": tpl,
		// Echoed back in status webhooks; the Cloud API has no idempotency key.
		"biz_opaque_callback_data": o.MessageID,
	}
	req, err := jsonRequest(ctx, m.BaseURL+"/"+metaGraphVersion+"/"+url.PathEscape(m.PhoneNumberID)+"/messages", payload)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+m.AccessToken)
	code, body, hdr, err := call(m.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	var r struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		Error *struct {
			Message   string `json:"message"`
			Code      int    `json:"code"`
			ErrorData struct {
				Details string `json:"details"`
			} `json:"error_data"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &r)
	if code >= 200 && code < 300 && len(r.Messages) > 0 && r.Messages[0].ID != "" {
		return Result{ProviderMessageID: r.Messages[0].ID}, nil
	}
	if r.Error != nil {
		reason := fmt.Sprintf("meta whatsapp %d: %s", r.Error.Code, r.Error.Message)
		if r.Error.ErrorData.Details != "" {
			reason += " (" + r.Error.ErrorData.Details + ")"
		}
		switch e := r.Error.Code; {
		case metaUnreachable[e]:
			return Result{}, Unreachable(reason)
		case metaPermanent[e], e >= 132000 && e <= 132015: // 132xxx: template missing, paused, params mismatch
			return Result{}, Permanent(reason)
		case metaTransient[e]:
			return Result{}, fmt.Errorf("%s", reason)
		}
	}
	if code >= 200 && code < 300 {
		return Result{}, Permanent(fmt.Sprintf("meta whatsapp: response without message id: %.300s", body)) // may have been sent: don't retry
	}
	return Result{}, StatusError("meta whatsapp", code, string(body), hdr)
}

// ParseReceipts handles a "messages" webhook: X-Hub-Signature-256 must be the HMAC-SHA256 of the raw
// body keyed by the app secret. Statuses become receipts; incoming messages and "sent" are ignored.
func (m *MetaWhatsApp) ParseReceipts(r *http.Request) ([]Receipt, error) {
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	sig, ok := strings.CutPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
	if !ok || m.AppSecret == "" {
		return nil, ErrUnauthorized
	}
	mac := hmac.New(sha256.New, []byte(m.AppSecret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !equalSecret(strings.ToLower(sig), want) {
		return nil, ErrUnauthorized
	}
	var p struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						ID   string `json:"id"`
						From string `json:"from"` // digits without +
						Type string `json:"type"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
						Button struct {
							Text string `json:"text"`
						} `json:"button"`
					} `json:"messages"`
					Statuses []struct {
						ID     string `json:"id"`
						Status string `json:"status"`
						Errors []struct {
							Code    int    `json:"code"`
							Title   string `json:"title"`
							Message string `json:"message"`
						} `json:"errors"`
					} `json:"statuses"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("meta whatsapp webhook: %w", err)
	}
	var out []Receipt
	for _, e := range p.Entry {
		for _, ch := range e.Changes {
			for _, msg := range ch.Value.Messages {
				text := msg.Text.Body
				if msg.Type == "button" { // a quick-reply button, e.g. "Stop promotions"
					text = msg.Button.Text
				}
				if msg.From != "" {
					out = append(out, Receipt{ProviderMessageID: msg.ID, Status: Inbound, From: "+" + strings.TrimPrefix(msg.From, "+"), Text: text})
				}
			}
			for _, s := range ch.Value.Statuses {
				rc := Receipt{ProviderMessageID: s.ID}
				switch s.Status {
				case "delivered", "read":
					rc.Status = s.Status
				case "failed":
					rc.Status = "failed"
					for _, er := range s.Errors {
						rc.Unreachable = rc.Unreachable || metaUnreachable[er.Code]
						if rc.Reason == "" {
							rc.Reason = fmt.Sprintf("meta whatsapp %d: %s", er.Code, er.Title)
						}
					}
				default: // sent (accepted by WhatsApp), or a status Pulse doesn't track
					continue
				}
				if rc.ProviderMessageID != "" {
					out = append(out, rc)
				}
			}
		}
	}
	return out, nil
}

// Challenge answers Meta's GET verification of the webhook URL.
func (m *MetaWhatsApp) Challenge(r *http.Request) (string, bool) {
	q := r.URL.Query()
	if q.Get("hub.mode") != "subscribe" || m.VerifyToken == "" || !equalSecret(q.Get("hub.verify_token"), m.VerifyToken) {
		return "", false
	}
	return q.Get("hub.challenge"), true
}
