package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/reasons"
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
		Secrets: []Field{{Key: "auth_key", Label: "Auth key", Required: true},
			{Key: "webhook_token", Label: "Webhook token (for delivery reports)", Placeholder: "a long random string",
				Hint: "Any long random string. In MSG91's SMS webhook (On Report Received), use this provider's Pulse " +
					"webhook URL with ?token=<this value> appended, or add an X-Pulse-Token header with it. Pulse " +
					"refuses delivery reports without the matching token, and all of them while it is empty."}},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			return &MSG91{HTTP: d.HTTP, BaseURL: "https://control.msg91.com", AuthKey: s["auth_key"],
				FlowTemplateID: c["flow_template_id"], SenderID: c["sender_id"], WebhookToken: s["webhook_token"]}, nil
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
	WebhookToken   string // checked on delivery report webhooks (?token= or X-Pulse-Token)
}

func (m *MSG91) Send(ctx context.Context, o Outbound) (Result, error) {
	rcpt := map[string]string{"mobiles": digits(o.To), "message": o.Rendered.Content.Text}
	if crqidOK(o.Ref) {
		// MSG91 returns CRQID as-is in its delivery reports.
		rcpt["CRQID"] = o.Ref
	}
	payload := map[string]any{
		"template_id": m.FlowTemplateID,
		"short_url":   "0",
		"recipients":  []map[string]string{rcpt},
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

// crqidOK reports whether s fits MSG91's CRQID: 1 to 80 letters, digits, _ . or -.
// A message reference always fits; anything else is not sent.
func crqidOK(s string) bool {
	if s == "" || len(s) > 80 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

// msg91Report is one SMS delivery report from MSG91's webhook (On Report Received). Its fields
// are those of MSG91's default payload template.
type msg91Report struct {
	RequestID     string `json:"requestId"`
	Status        any    `json:"status"`
	EventName     string `json:"eventName"`
	CRQID         string `json:"CRQID"`
	FailureReason string `json:"failureReason"`
}

// msg91Statuses maps MSG91's SMS report status codes to a receipt status and reason code.
// 0 (sent) and unknown codes are not final and are ignored.
var msg91Statuses = map[string]struct{ status, code, label string }{
	"1":  {"delivered", "", ""},
	"2":  {"failed", "", "failed"},
	"9":  {"failed", reasons.Consent, "blocked as NDNC"},
	"16": {"failed", reasons.ProviderRejected, "rejected"},
	"25": {"failed", reasons.ProviderRejected, "rejected"},
	"17": {"failed", reasons.Blocked, "number blocked"},
	"20": {"failed", reasons.ProviderRejected, "destination country blocked"},
}

// ParseReceipts reads MSG91's SMS delivery report webhook: one JSON object per number (a JSON
// array is read too). The receipt is for the requestId the send returned, and carries the CRQID
// (the message reference) when the report has one. The webhook token must match.
func (m *MSG91) ParseReceipts(r *http.Request) ([]Receipt, error) {
	if err := checkGupshupToken(r, m.WebhookToken); err != nil {
		return nil, err
	}
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	b := bytes.TrimSpace(body)
	if len(b) == 0 {
		return nil, nil
	}
	var reports []msg91Report
	if b[0] == '[' {
		err = json.Unmarshal(b, &reports)
	} else {
		var one msg91Report
		err = json.Unmarshal(b, &one)
		reports = []msg91Report{one}
	}
	if err != nil {
		return nil, fmt.Errorf("msg91 webhook: %w", err)
	}
	var out []Receipt
	for _, rep := range reports {
		if rc, ok := msg91Receipt(rep); ok {
			out = append(out, rc)
		}
	}
	return out, nil
}

func msg91Receipt(rep msg91Report) (Receipt, bool) {
	id := strings.TrimSpace(rep.RequestID)
	st, ok := msg91Statuses[strings.TrimSpace(anyString(rep.Status))]
	if !ok {
		switch strings.ToLower(strings.TrimSpace(rep.EventName)) {
		case "delivered":
			st, ok = msg91Statuses["1"], true
		case "failed":
			st, ok = msg91Statuses["2"], true
		case "rejected":
			st, ok = msg91Statuses["16"], true
		}
	}
	if id == "" || !ok {
		return Receipt{}, false
	}
	rc := Receipt{ProviderMessageID: id, Status: st.status}
	if ref := strings.TrimSpace(rep.CRQID); ref != "" && ref != "0" { // "0": no CRQID was sent
		rc.Ref = ref
	}
	if st.status == "failed" {
		rc.Code = st.code
		rc.Reason = "msg91: " + st.label
		if fr := strings.TrimSpace(rep.FailureReason); fr != "" {
			rc.Reason += ": " + fr
		}
		if rc.Code == "" {
			rc.Code = reasons.ProviderRejected
		}
	}
	return rc, true
}
