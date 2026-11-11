package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "msg91", Channel: channel.SMS, Name: "MSG91",
		Description: "DLT-compliant SMS in India through MSG91's Flow API.",
		Config: []Field{
			// The flow (and the DLT template it maps to) must contain the variable ##message##; Pulse
			// puts the whole rendered SMS text in it.
			{Key: "flow_template_id", Label: "Flow template ID (its text must be ##message##)",
				Placeholder: "64f1c2e5d6a7b8c9d0e1f2a3", Required: true},
			{Key: "sender_id", Label: "Sender ID (DLT header)", Placeholder: "ACMEIN"},
		},
		Secrets: []Field{{Key: "auth_key", Label: "Auth key", Required: true}},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			return &MSG91{HTTP: d.HTTP, BaseURL: "https://control.msg91.com", AuthKey: s["auth_key"],
				FlowTemplateID: c["flow_template_id"], SenderID: c["sender_id"]}, nil
		},
	})
}

// MSG91 sends SMS through the v5 Flow API. India's DLT rules only let pre-registered templates
// through, so the flow named by FlowTemplateID must define a ##message## variable (typically its
// whole body) and the rendered Pulse template must match the DLT-approved text it maps to.
type MSG91 struct {
	HTTP           *http.Client
	BaseURL        string
	AuthKey        string
	FlowTemplateID string
	SenderID       string // default DLT header; a template's own sender_id wins
}

func (m *MSG91) Send(ctx context.Context, o Outbound) (Result, error) {
	payload := map[string]any{
		"template_id": m.FlowTemplateID,
		"short_url":   "0",
		"recipients":  []map[string]string{{"mobiles": digits(o.To), "message": o.Rendered.Content.Text}},
	}
	sender := o.Rendered.Content.SenderID
	if sender == "" {
		sender = m.SenderID
	}
	if sender != "" {
		payload["sender"] = sender
	}
	req, err := jsonRequest(ctx, m.BaseURL+"/api/v5/flow", payload)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("authkey", m.AuthKey)
	code, body, hdr, err := call(m.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	if code == http.StatusTooManyRequests || code >= 500 {
		return Result{}, StatusError("msg91", code, string(body), hdr)
	}
	var r struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		if code >= 300 {
			return Result{}, StatusError("msg91", code, string(body), hdr)
		}
		// Accepted or not is unknown; retrying could send twice, so don't.
		return Result{}, Permanent(fmt.Sprintf("msg91: unreadable response: %.300s", body))
	}
	// MSG91 reports most errors (bad auth key, unknown flow, DLT mismatch) as HTTP 200 type=error.
	if r.Type == "error" {
		return Result{}, Permanent(fmt.Sprintf("msg91: %.300s", r.Message))
	}
	if code >= 300 {
		return Result{}, StatusError("msg91", code, string(body), hdr)
	}
	var id string
	if json.Unmarshal(r.Message, &id) != nil || r.Type != "success" || id == "" {
		return Result{}, Permanent(fmt.Sprintf("msg91: unexpected response: %.300s", body))
	}
	return Result{ProviderMessageID: id}, nil
}
