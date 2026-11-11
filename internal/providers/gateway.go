package providers

// The custom HTTP gateway: Pulse POSTs each rendered message as JSON to the customer's endpoint,
// signed like outbound webhooks. It connects any gateway Pulse has no adapter for (a regional SMS
// aggregator, an in-house notification service). The endpoint answers 2xx to accept, optionally
// with {"id": "..."} (used to match delivery receipts posted back to the provider's webhook URL as
// {"id", "status": delivered|failed|read, "unreachable", "reason"}); 404 or 410 means the recipient
// can't be reached; other 4xx are permanent; 429 and 5xx are retried.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"pulse/internal/channel"
	"pulse/internal/safehttp"
)

func init() {
	for _, ch := range channel.External {
		register(Kind{
			ID: "http_" + ch, Channel: ch, Name: "Custom HTTP gateway",
			Description: "POSTs each message as signed JSON to your endpoint, for any gateway Pulse has no adapter for.",
			Config:      []Field{{Key: "url", Label: "Endpoint URL", Placeholder: "https://gateway.example.com/pulse", Required: true}},
			Secrets:     []Field{{Key: "signing_secret", Label: "Signing secret (we sign each request with it)", Required: true}},
			New: func(d Deps, c, s map[string]string) (Provider, error) {
				u, err := url.Parse(c["url"])
				if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
					return nil, errors.New("gateway: the endpoint must be an http(s) URL")
				}
				return &Gateway{HTTP: safehttp.Client(15*time.Second, d.AllowPrivate), URL: c["url"], Secret: s["signing_secret"]}, nil
			},
		})
	}
}

type Gateway struct {
	HTTP   *http.Client
	URL    string
	Secret string
}

type gatewayMessage struct {
	MessageID string            `json:"message_id"`
	Channel   string            `json:"channel"`
	To        string            `json:"to"`
	Platform  string            `json:"platform,omitempty"`
	Subject   string            `json:"subject,omitempty"`
	HTML      string            `json:"html,omitempty"`
	Text      string            `json:"text,omitempty"` // email: the plain-text part
	Content   channel.Content   `json:"content"`
	Headers   map[string]string `json:"headers,omitempty"`
	// Ref is the email's message reference: a gateway that relays to a sending service should pass
	// it on (X-Pulse-Ref) so bounce emails can be matched.
	Ref string `json:"ref,omitempty"`
	// email: attachments as signed download links (valid 7 days), not inline bytes, so requests
	// stay small whatever the files' size.
	Attachments []gatewayAttachment `json:"attachments,omitempty"`
}

type gatewayAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	URL         string `json:"url"`
}

func (g *Gateway) Send(ctx context.Context, o Outbound) (Result, error) {
	msg := gatewayMessage{MessageID: o.MessageID, Channel: o.Channel, To: o.To, Platform: o.Platform,
		Subject: o.Rendered.Subject, HTML: o.Rendered.HTML, Content: o.Rendered.Content, Headers: o.Headers, Ref: o.Ref}
	if o.Channel == channel.Email {
		msg.Text = o.Rendered.Content.Text
	}
	for _, a := range o.Attachments {
		if a.URL == "" {
			return Result{}, Permanent("gateway: attachments need download links, which need PULSE_SECRET_KEY")
		}
		msg.Attachments = append(msg.Attachments, gatewayAttachment{Filename: a.Filename, ContentType: a.ContentType, Size: len(a.Data), URL: a.URL})
	}
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL, bytes.NewReader(body))
	if err != nil {
		return Result{}, Permanent(err.Error())
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	m := hmac.New(sha256.New, []byte(g.Secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pulse-Signature", "t="+ts+",v1="+hex.EncodeToString(m.Sum(nil)))
	req.Header.Set("Idempotency-Key", o.MessageID)
	code, resp, hdr, err := call(g.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	switch {
	case code >= 200 && code < 300:
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(resp, &r)
		return Result{ProviderMessageID: r.ID}, nil
	case code == http.StatusNotFound || code == http.StatusGone:
		return Result{}, Unreachable("gateway: recipient not reachable")
	}
	return Result{}, StatusError("gateway", code, string(resp), hdr)
}

// ParseReceipts accepts delivery receipts the gateway posts back, signed the same way.
func (g *Gateway) ParseReceipts(r *http.Request) ([]Receipt, error) {
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	if !VerifySignature(g.Secret, r.Header.Get("X-Pulse-Signature"), body, time.Now()) {
		return nil, ErrUnauthorized
	}
	var in struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		Unreachable bool   `json:"unreachable"`
		Reason      string `json:"reason"`
		Code        string `json:"reason_code"`
		Kind        string `json:"kind"`
		From        string `json:"from"`
		Text        string `json:"text"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	if in.Status == Inbound {
		return []Receipt{{ProviderMessageID: in.ID, Status: Inbound, From: in.From, Text: in.Text}}, nil
	}
	switch in.Status {
	case "delivered", "failed", "read", "complained":
	case "bounced":
		if in.Kind != "hard" && in.Kind != "soft" {
			in.Kind = "hard"
		}
	default:
		in.ID = ""
	}
	if in.ID == "" {
		return nil, errors.New("gateway receipt needs id and status (delivered, failed, read, bounced, complained or inbound)")
	}
	return []Receipt{{ProviderMessageID: in.ID, Status: in.Status, Unreachable: in.Unreachable, Reason: in.Reason, Code: in.Code, Kind: in.Kind}}, nil
}

// VerifySignature checks "t=<unix>,v1=<hex hmac>" over "<t>.<body>", within 5 minutes of now.
func VerifySignature(secret, header string, body []byte, now time.Time) bool {
	var ts, sig string
	for _, part := range bytes.Split([]byte(header), []byte(",")) {
		k, v, _ := bytes.Cut(bytes.TrimSpace(part), []byte("="))
		switch string(k) {
		case "t":
			ts = string(v)
		case "v1":
			sig = string(v)
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || secret == "" || now.Sub(time.Unix(t, 0)).Abs() > 5*time.Minute {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	return equalSecret(hex.EncodeToString(m.Sum(nil)), sig)
}
