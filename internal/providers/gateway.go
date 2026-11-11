package providers

// The custom HTTP gateway, in two modes.
//
// Standard: Pulse POSTs each rendered message as JSON to the customer's endpoint, signed like
// outbound webhooks. It connects any gateway Pulse has no adapter for (a regional SMS aggregator,
// an in-house notification service). The endpoint answers 2xx to accept, optionally with
// {"id": "..."} (used to match delivery receipts posted back to the provider's webhook URL as
// {"id", "status": delivered|failed|read, "unreachable", "reason"}); 404 or 410 means the recipient
// can't be reached; other 4xx are permanent; 429 and 5xx are retried.
//
// Custom request (config mode=custom, gateway_custom.go): the request is the user's own method,
// URL, headers and body template, with a success rule, a message-id path and a receipt mapping,
// so a third-party API can be called directly.
//
// Both modes take custom HTTP authentication (package httpauth, config auth): basic, bearer, API
// key, OAuth2 client credentials or a login flow, with tokens cached per provider.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pulse/internal/channel"
	"pulse/internal/httpauth"
	"pulse/internal/safehttp"
)

func init() {
	for _, ch := range channel.External {
		register(Kind{
			ID: "http_" + ch, Channel: ch, Name: "Custom HTTP gateway",
			Description: "Sends each message to your endpoint: as signed JSON, or as a request you design (method, URL, headers, body), with any authentication.",
			Auth:        true, CustomRequest: true,
			Config: []Field{
				{Key: "mode", Label: "Request", Options: []string{GatewayStandard, GatewayCustom}, Default: GatewayStandard,
					Hint: "standard: Pulse's signed JSON; custom: your own request template"},
				{Key: "url", Label: "Endpoint URL", Placeholder: "https://gateway.example.com/pulse",
					Hint: "Standard mode. Custom mode takes the URL in the request template."},
			},
			Secrets: []Field{
				{Key: "signing_secret", Label: "Signing secret (we sign each request with it)",
					Hint: "Standard mode: required. Delivery receipts posted back must be signed with it too."},
				{Key: "receipt_token", Label: "Receipt token",
					Hint: "Custom mode: receipts posted to the webhook URL must carry it (see the receipt mapping); without one, receipts are refused."},
			},
			New: newGateway,
		})
	}
}

// Gateway modes.
const (
	GatewayStandard = "standard"
	GatewayCustom   = "custom"
)

// TokenScope names a provider's cached auth tokens: per workspace and provider.
func TokenScope(tenant, provider int64) string {
	return strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(provider, 10)
}

// authFor builds a provider's custom HTTP authentication from config["auth"].
func authFor(d Deps, c, s map[string]string, client *http.Client) (*httpauth.Auth, error) {
	cfg, err := httpauth.Parse(c["auth"])
	if err != nil {
		return nil, err
	}
	if cfg.Type == httpauth.None {
		return nil, nil
	}
	return httpauth.New(cfg, s, httpauth.Options{HTTP: client, Cache: d.Tokens, Scope: d.Scope, AllowHTTP: d.AllowPrivate})
}

func newGateway(d Deps, c, s map[string]string) (Provider, error) {
	client := safehttp.Client(15*time.Second, d.AllowPrivate)
	a, err := authFor(d, c, s, client)
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	g := &Gateway{HTTP: client, URL: c["url"], Secret: s["signing_secret"], Auth: a, ReceiptToken: s["receipt_token"], secrets: s}
	switch c["mode"] {
	case "", GatewayStandard:
		u, err := url.Parse(c["url"])
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, errors.New("gateway: the endpoint must be an http(s) URL")
		}
		if g.Secret == "" {
			return nil, errors.New("gateway: the signing secret is required")
		}
	case GatewayCustom:
		cr, err := ParseCustomRequest(c["request"])
		if err != nil {
			return nil, err
		}
		if err := cr.Validate(); err != nil {
			return nil, err
		}
		g.Custom = &cr
	default:
		return nil, errors.New("gateway: mode must be standard or custom")
	}
	return g, nil
}

type Gateway struct {
	HTTP   *http.Client
	URL    string
	Secret string
	// Auth authenticates every request (nil: none).
	Auth *httpauth.Auth
	// Custom is the custom-request mode's template (nil: standard mode).
	Custom       *CustomRequest
	ReceiptToken string
	secrets      map[string]string // masked in previews
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

// standardRequest builds the signed JSON request of standard mode.
func (g *Gateway) standardRequest(ctx context.Context, o Outbound, now time.Time) (*http.Request, []byte, error) {
	msg := gatewayMessage{MessageID: o.MessageID, Channel: o.Channel, To: o.To, Platform: o.Platform,
		Subject: o.Rendered.Subject, HTML: o.Rendered.HTML, Content: o.Rendered.Content, Headers: o.Headers, Ref: o.Ref}
	if o.Channel == channel.Email {
		msg.Text = o.Rendered.Content.Text
	}
	for _, a := range o.Attachments {
		if a.URL == "" {
			return nil, nil, Permanent("gateway: attachments need download links, which need PULSE_SECRET_KEY")
		}
		msg.Attachments = append(msg.Attachments, gatewayAttachment{Filename: a.Filename, ContentType: a.ContentType, Size: len(a.Data), URL: a.URL})
	}
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL, nil)
	if err != nil {
		return nil, nil, Permanent(err.Error())
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	m := hmac.New(sha256.New, []byte(g.Secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pulse-Signature", "t="+ts+",v1="+hex.EncodeToString(m.Sum(nil)))
	req.Header.Set("Idempotency-Key", o.MessageID)
	return req, body, nil
}

// build returns the request of either mode (without authentication).
func (g *Gateway) build(ctx context.Context, o Outbound) (*http.Request, []byte, error) {
	if g.Custom != nil {
		return g.Custom.Build(ctx, o)
	}
	return g.standardRequest(ctx, o, time.Now())
}

func (g *Gateway) Send(ctx context.Context, o Outbound) (Result, error) {
	code, resp, hdr, err := authCall(ctx, g.HTTP, g.Auth, func() (*http.Request, []byte, error) { return g.build(ctx, o) })
	if err != nil {
		return Result{}, err
	}
	if g.Custom != nil {
		return g.Custom.Result(code, resp, hdr)
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

// authCall sends the request newReq builds, authenticated by a (nil: as is), and returns the
// status, size-limited body and headers. Refused credentials are permanent.
func authCall(ctx context.Context, c *http.Client, a *httpauth.Auth, newReq func() (*http.Request, []byte, error)) (int, []byte, http.Header, error) {
	if a == nil {
		req, body, err := newReq()
		if err != nil {
			return 0, nil, nil, err
		}
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
		}
		return call(c, req)
	}
	resp, err := a.Do(ctx, c, newReq)
	switch {
	case errors.Is(err, httpauth.ErrRejected), errors.Is(err, httpauth.ErrConfig):
		return 0, nil, nil, Permanent("authentication: " + err.Error())
	case err != nil:
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, body, resp.Header, nil
}

// PreviewRequest returns the exact request Send would make for o, with secrets and tokens masked.
func (g *Gateway) PreviewRequest(o Outbound) (method, target string, headers map[string]string, body string) {
	req, b, err := g.build(context.Background(), o)
	if err != nil {
		return "", "", nil, "error: " + err.Error()
	}
	if g.Auth != nil {
		if b2, err := g.Auth.Apply(req, b, httpauth.Token{Value: "{{token}}"}); err == nil {
			b = b2
		}
	}
	return maskRequest(req, b, g.secrets)
}

// maskRequest flattens a request for a preview: every secret value is replaced by ••••, and
// authorization headers are masked whole (basic credentials are encoded).
func maskRequest(req *http.Request, body []byte, secrets map[string]string) (method, target string, headers map[string]string, out string) {
	var vals []string
	for _, v := range secrets {
		if len(v) >= 3 {
			vals = append(vals, v, url.QueryEscape(v))
		}
	}
	mask := func(s string) string {
		for _, v := range vals {
			s = strings.ReplaceAll(s, v, previewMask)
		}
		return s
	}
	headers = map[string]string{}
	for k := range req.Header {
		v := mask(req.Header.Get(k))
		if strings.EqualFold(k, "Authorization") {
			if scheme, _, ok := strings.Cut(v, " "); ok && scheme != "" {
				v = scheme + " " + previewMask
				if strings.Contains(req.Header.Get(k), "{{token}}") {
					v = req.Header.Get(k)
				}
			} else {
				v = previewMask
			}
		}
		headers[k] = v
	}
	return req.Method, mask(req.URL.String()), headers, mask(string(body))
}

// ParseReceipts accepts delivery receipts the gateway posts back: in standard mode signed like
// the requests; in custom mode carrying the receipt token, mapped by the receipt mapping.
func (g *Gateway) ParseReceipts(r *http.Request) ([]Receipt, error) {
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	if g.Custom != nil {
		return g.Custom.ParseReceipts(r, body, g.ReceiptToken)
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

// AuthTester is implemented by providers with custom HTTP authentication ("Test connection").
type AuthTester interface {
	TestAuth(ctx context.Context) (httpauth.TestResult, error)
}

// TestAuth runs the login or token request now.
func (g *Gateway) TestAuth(ctx context.Context) (httpauth.TestResult, error) {
	if g.Auth == nil {
		return httpauth.TestResult{Type: httpauth.None, Static: true}, nil
	}
	return g.Auth.Test(ctx)
}

// Previewer is implemented by providers that can show the exact request they would send.
type Previewer interface {
	PreviewRequest(o Outbound) (method, url string, headers map[string]string, body string)
}
