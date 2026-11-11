package providers

// Shared pieces of the Gupshup adapters (gupshup_sms.go, gupshup_whatsapp.go, gupshup_rcs.go).
//
// Gupshup has two API surfaces: the Enterprise gateway (enterprise.smsgupshup.com/GatewayAPI/rest,
// user id + password, used for SMS and RCS) and the self-serve WhatsApp API (api.gupshup.io,
// "apikey" header). Both were built from Gupshup's public docs and tested against mocks only, so
// every base URL is an editable setting.
//
// Gupshup doesn't sign its callbacks, so each provider has a webhook token: the callback URL
// registered at Gupshup is the provider's Pulse webhook URL with ?token=<token> (or the token in an
// X-Pulse-Token header). Callbacks without the matching token are refused, and so is every callback
// while no token is saved (fail closed).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pulse/internal/reasons"
	"pulse/internal/safehttp"
)

const (
	gupshupEnterpriseURL = "https://enterprise.smsgupshup.com/GatewayAPI/rest"
	gupshupWhatsAppURL   = "https://api.gupshup.io"
	// previewMask replaces secrets in PreviewRequest output.
	previewMask = "••••"
)

// gupshupTokenField is the webhook token every Gupshup kind requires.
var gupshupTokenField = Field{Key: "webhook_token", Label: "Webhook token", Required: true,
	Placeholder: "a long random string",
	Hint: "Any long random string. Register this provider's Pulse webhook URL at Gupshup with ?token=<this value> " +
		"appended (e.g. https://pulse.example.com/v1/webhooks/providers/42?token=…); callers may instead send it in an " +
		"X-Pulse-Token header. Pulse refuses delivery reports and replies without the matching token."}

// gupshupBaseURL returns the configured base URL (or the default), checked to be http(s).
func gupshupBaseURL(kind, configured, def string) (string, error) {
	raw := strings.TrimSpace(configured)
	if raw == "" {
		raw = def
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%s: the base URL must be an http(s) URL", kind)
	}
	return strings.TrimRight(raw, "/"), nil
}

// gupshupClient is the HTTP client for user-editable endpoints: it refuses private addresses
// (SSRF) unless the deployment allows them.
func gupshupClient(d Deps) *http.Client { return safehttp.Client(15*time.Second, d.AllowPrivate) }

// checkGupshupToken verifies the webhook token from ?token= or X-Pulse-Token, in constant time.
// No saved token refuses everything.
func checkGupshupToken(r *http.Request, want string) error {
	if want == "" {
		return ErrUnauthorized
	}
	for _, got := range []string{r.URL.Query().Get("token"), r.Header.Get("X-Pulse-Token")} {
		if got != "" && equalSecret(got, want) {
			return nil
		}
	}
	return ErrUnauthorized
}

// gupshupRequest is one request an adapter would send: Send and PreviewRequest both build it, so
// the preview is exactly what goes out.
type gupshupRequest struct {
	URL     string
	Form    url.Values
	Headers map[string]string
	// secret form keys and header names, masked in previews
	SecretForm, SecretHeaders []string
}

func (g gupshupRequest) httpRequest(ctx context.Context) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL, strings.NewReader(g.Form.Encode()))
	if err != nil {
		return nil, Permanent(err.Error())
	}
	for k, v := range g.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (g gupshupRequest) preview() (string, string, map[string]string, string) {
	form := url.Values{}
	for k, v := range g.Form {
		form[k] = append([]string(nil), v...)
	}
	for _, k := range g.SecretForm {
		if form.Has(k) {
			form.Set(k, previewMask)
		}
	}
	h := make(map[string]string, len(g.Headers))
	for k, v := range g.Headers {
		h[k] = v
	}
	for _, k := range g.SecretHeaders {
		if _, ok := h[k]; ok {
			h[k] = previewMask
		}
	}
	body := strings.ReplaceAll(form.Encode(), url.QueryEscape(previewMask), previewMask)
	return http.MethodPost, g.URL, h, body
}

func formHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"}
}

// ---- Enterprise gateway (SMS and RCS) ----

// gupshupEnterprise is the Enterprise gateway account shared by the SMS and RCS adapters.
type gupshupEnterprise struct {
	HTTP         *http.Client
	BaseURL      string
	UserID       string
	Password     string
	WebhookToken string
}

// baseForm holds the parameters every SendMessage call carries.
func (e *gupshupEnterprise) baseForm(to, msg, msgType string) url.Values {
	return url.Values{
		"method": {"SendMessage"}, "send_to": {digits(to)}, "msg": {msg}, "msg_type": {msgType},
		"userid": {e.UserID}, "password": {e.Password}, "auth_scheme": {"plain"}, "v": {"1.1"}, "format": {"json"},
	}
}

func (e *gupshupEnterprise) request(form url.Values) gupshupRequest {
	return gupshupRequest{URL: e.BaseURL, Form: form, Headers: formHeaders(), SecretForm: []string{"password"}}
}

// send posts a SendMessage call and reads the gateway's answer: JSON
// {"response":{"id","phone","details","status":"success"|"error"}} (format=json), or the text form
// "success | <phone> | <id>" / "error | <code> | <message>".
func (e *gupshupEnterprise) send(ctx context.Context, name string, g gupshupRequest) (Result, error) {
	req, err := g.httpRequest(ctx)
	if err != nil {
		return Result{}, err
	}
	code, body, hdr, err := call(e.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	if code == http.StatusTooManyRequests || code >= 500 {
		return Result{}, StatusError(name, code, string(body), hdr)
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return Result{}, Permanent(fmt.Sprintf("%s: HTTP %d: the user id or password (credentials) was refused", name, code))
	}
	status, id, details, ok := parseEnterpriseResponse(body)
	if !ok {
		if code >= 300 {
			return Result{}, StatusError(name, code, string(body), hdr)
		}
		// Accepted or not is unknown; retrying could send twice.
		return Result{}, Permanent(fmt.Sprintf("%s: unreadable response: %.300s", name, body))
	}
	if strings.EqualFold(status, "success") && id != "" && code < 300 {
		return Result{ProviderMessageID: id}, nil
	}
	if strings.EqualFold(status, "error") {
		return Result{}, enterpriseError(name, id, details)
	}
	if code >= 300 {
		return Result{}, StatusError(name, code, string(body), hdr)
	}
	return Result{}, Permanent(fmt.Sprintf("%s: unexpected response: %.300s", name, body))
}

func parseEnterpriseResponse(body []byte) (status, id, details string, ok bool) {
	var r struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(body, &r) == nil && len(r.Response) > 0 {
		var one struct {
			ID      any    `json:"id"`
			Details string `json:"details"`
			Status  string `json:"status"`
		}
		var many []json.RawMessage
		raw := r.Response
		if json.Unmarshal(raw, &many) == nil && len(many) > 0 { // a list for multiple recipients
			raw = many[0]
		}
		if json.Unmarshal(raw, &one) == nil && one.Status != "" {
			return one.Status, strings.TrimSpace(anyString(one.ID)), one.Details, true
		}
	}
	parts := strings.Split(strings.TrimSpace(string(body)), "|")
	if len(parts) >= 3 {
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		switch strings.ToLower(parts[0]) {
		case "success": // success | phone | id
			return parts[0], parts[2], "", true
		case "error": // error | code | message
			return parts[0], parts[1], strings.Join(parts[2:], "|"), true
		}
	}
	return "", "", "", false
}

// enterpriseError classifies a gateway error ("102": authentication failed, "175": international
// sending disabled, "124": pack expired …). All are permanent: a retry sends the same request.
func enterpriseError(name, code, details string) error {
	reason := fmt.Sprintf("%s error %s: %s", name, code, details)
	low := strings.ToLower(details)
	switch {
	case code == "102" || strings.Contains(low, "authentication") || strings.Contains(low, "password"):
		return Permanent(reason + " (check the user id and password credentials)")
	case strings.Contains(low, "invalid") && (strings.Contains(low, "phone") || strings.Contains(low, "number") ||
		strings.Contains(low, "mobile") || strings.Contains(low, "send_to") || strings.Contains(low, "destination")):
		return BadNumber(reason)
	}
	return Permanent(reason)
}

func anyString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprint(v)
}

// ---- Enterprise delivery reports (SMS and RCS) ----

// dlrCauses maps the Enterprise delivery report causes (and their errCode) to reason codes.
// From docs.gupshup.io/docs/real-time-delivery-reports.
var dlrCauses = map[string]string{
	"ABSENT_SUBSCRIBER": reasons.NotOnChannel, "INBOXFULL": reasons.NotOnChannel,
	"CALL_BARRED": reasons.Blocked, "BLOCKED": reasons.Blocked, "BLOCKED_FOR_USER": reasons.Blocked,
	"UNKNOWN_SUBSCRIBER": reasons.InvalidNumber,
	"SERVICE_DOWN":       reasons.ProviderError, "SYSTEM_FAILURE": reasons.ProviderError, "CONGESTION": reasons.ProviderError,
	"NO_ACK_FROM_OPERATOR": reasons.ProviderError, "SMSCTIMEDOUT": reasons.ProviderError, "DEFERRED": reasons.ProviderError,
	"DND_FAIL":    reasons.Consent,
	"DND_TIMEOUT": reasons.SpamPolicy, "OUTSIDE_WORKING_HOURS": reasons.SpamPolicy, "BLOCKED_MASK": reasons.SpamPolicy,
	"MSG_DOES_NOT_MATCH_TEMPLATE": reasons.Template, "TEMPLATE_NOT_MATCHED": reasons.Template, "TEMPLATE_NOT_FOUND": reasons.Template,
	"HEADER_NOT_FOUND": reasons.Template, "HEADER_RESERVED": reasons.Template, "HEADER_NOT_REGISTERED_FOR_TEMPLATE": reasons.Template,
	"CANCEL_CAUSEID": reasons.ProviderRejected, "CANCEL_SCHEDULE": reasons.ProviderRejected, "OTHER": reasons.ProviderRejected,
}

var dlrErrCodes = map[string]string{
	"1": "ABSENT_SUBSCRIBER", "2": "CALL_BARRED", "3": "UNKNOWN_SUBSCRIBER", "4": "SERVICE_DOWN", "5": "SYSTEM_FAILURE",
	"6": "DND_FAIL", "7": "BLOCKED", "8": "DND_TIMEOUT", "9": "OUTSIDE_WORKING_HOURS", "10": "DEFERRED", "11": "INBOXFULL",
	"12": "CONGESTION", "13": "NO_ACK_FROM_OPERATOR", "22": "BLOCKED_FOR_USER", "23": "UNKNOWN_SUBSCRIBER",
	"0b": "BLOCKED_MASK", "0c": "SMSCTIMEDOUT", "0d": "CANCEL_CAUSEID", "0e": "CANCEL_SCHEDULE",
	"38": "MSG_DOES_NOT_MATCH_TEMPLATE", "67": "HEADER_NOT_FOUND", "70": "HEADER_RESERVED", "74": "TEMPLATE_NOT_MATCHED",
}

// dlrCause names a failure from its cause, or its errCode when the cause is missing or unknown.
func dlrCause(cause, errCode string) string {
	c := strings.ToUpper(strings.TrimSpace(cause))
	if _, ok := dlrCauses[c]; ok {
		return c
	}
	n := strings.TrimLeft(strings.ToLower(strings.TrimSpace(errCode)), "0")
	if name, ok := dlrErrCodes[n]; ok {
		return name
	}
	return c
}

// rcsNotCapable: causes that say the handset can't take RCS (an SMS fallback should follow).
func rcsNotCapable(cause string) bool {
	c := strings.ToUpper(cause)
	return strings.Contains(c, "RCS") || strings.Contains(c, "CAPAB") || strings.Contains(c, "NOT_SUPPORTED") ||
		strings.Contains(c, "UNSUPPORTED") || strings.Contains(c, "NOT_ENABLED")
}

// dlrReceipt turns one Enterprise delivery report into a receipt (ok=false: not final, or not a report).
// get reads a field by name. ch is "sms" or "rcs".
func dlrReceipt(get func(string) string, name, ch string) (Receipt, bool) {
	id := get("externalId")
	if id == "" {
		return Receipt{}, false
	}
	st := get("status")
	if st == "" {
		st = get("eventType")
	}
	rc := Receipt{ProviderMessageID: id}
	switch strings.ToUpper(strings.TrimSpace(st)) {
	case "SUCCESS", "DELIVERED", "DELIVRD":
		rc.Status = "delivered"
	case "READ":
		rc.Status = "read"
	case "FAIL", "FAILED", "FAILURE", "UNDELIVERED", "UNDELIV", "REJECTED", "EXPIRED":
		rc.Status = "failed"
	default: // UNKNOWN, SENT, ENQUEUED…: not final
		return Receipt{}, false
	}
	if rc.Status != "failed" {
		return rc, true
	}
	cause, errCode := strings.TrimSpace(get("cause")), strings.TrimSpace(get("errCode"))
	named := dlrCause(cause, errCode)
	rc.Code = dlrCauses[named]
	if rc.Code == "" {
		rc.Code = reasons.ProviderRejected
	}
	label := cause
	if label == "" {
		label = named
	}
	rc.Reason = strings.TrimSpace(fmt.Sprintf("%s %s: %s", name, errCode, label))
	if errCode == "" {
		rc.Reason = fmt.Sprintf("%s: %s", name, label)
	}
	switch {
	case named == "UNKNOWN_SUBSCRIBER":
		// SMS: the number bounced; RCS: not reachable over RCS either way, so fall back to SMS.
		rc.Unreachable = true
		if ch == "rcs" {
			rc.Code = reasons.NotOnChannel
		}
	case ch == "rcs" && rcsNotCapable(cause):
		rc.Unreachable, rc.Code = true, reasons.NotOnChannel
	}
	return rc, true
}

// enterpriseCallbacks splits a callback into report objects: a JSON array (Gupshup's POST, up to 20
// events) or one JSON object (returned as objects), or a form body or (GET) the query string
// (returned as values, without the webhook token).
func enterpriseCallbacks(r *http.Request, body []byte) ([]map[string]any, url.Values, error) {
	b := bytes.TrimSpace(body)
	switch {
	case len(b) > 0 && b[0] == '[':
		var list []map[string]any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&list); err != nil {
			return nil, nil, fmt.Errorf("gupshup callback: %w", err)
		}
		return list, nil, nil
	case len(b) > 0 && b[0] == '{':
		var one map[string]any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&one); err != nil {
			return nil, nil, fmt.Errorf("gupshup callback: %w", err)
		}
		return []map[string]any{one}, nil, nil
	case len(b) > 0:
		form, err := url.ParseQuery(string(b))
		if err != nil {
			return nil, nil, fmt.Errorf("gupshup callback: %w", err)
		}
		return nil, form, nil
	}
	q := r.URL.Query()
	q.Del("token")
	return nil, q, nil
}

func mapGetter(m map[string]any) func(string) string {
	return func(k string) string { return strings.TrimSpace(anyString(m[k])) }
}

// plusE164 returns a phone number as +digits.
func plusE164(phone string) string {
	p := strings.TrimSpace(strings.ReplaceAll(phone, " ", ""))
	if p == "" {
		return ""
	}
	return "+" + strings.TrimPrefix(p, "+")
}

// ---- GSM-7 ----

const (
	gsm7Basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	gsm7Ext   = "\f^{}\\[~]|€"
)

// isGSM7 reports whether text fits the GSM 03.38 alphabet (with its extension table); anything
// else must be sent as Unicode.
func isGSM7(text string) bool {
	for _, r := range text {
		if !strings.ContainsRune(gsm7Basic, r) && !strings.ContainsRune(gsm7Ext, r) {
			return false
		}
	}
	return true
}

// GetReceiver is implemented by adapters whose provider may deliver receipts with GET (Gupshup's
// delivery report URL can be called either way).
type GetReceiver interface{ ReceivesGet() bool }

func (g *GupshupSMS) ReceivesGet() bool { return true }
func (g *GupshupRCS) ReceivesGet() bool { return true }
