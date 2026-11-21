package providers

// The gateway's custom-request mode: the user designs the request (method, URL, headers, body
// template), says what a success looks like, where the provider's message id is, and how the
// provider's delivery webhook maps to Pulse's receipt statuses.
//
// Templates take the standard variables ({{contact.x}}, {{event.x}}, {{campaign.x}}) plus
// {{message.x}} (the rendered message: to, text, subject, html, title, body…, see
// personalize.MessageKeys). Every value is escaped for where it lands: JSON-escaped in a JSON body
// (like journey webhook bodies; "| json" inserts a whole value), percent-encoded in a form body and
// in query parameters, and refused in headers when it holds a line break. The URL's scheme and host
// can't be templated, so a contact's data can never choose where the request goes.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pulse/internal/channel"
	"pulse/internal/httpauth"
	"pulse/internal/personalize"
)

// CustomRequest is config["request"] of a gateway in custom mode.
type CustomRequest struct {
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Headers     []httpauth.Header `json:"headers,omitempty"`
	ContentType string            `json:"content_type,omitempty"` // application/json by default
	Body        string            `json:"body,omitempty"`
	Success     SuccessRule       `json:"success"`
	// IDPath is where the provider's message id is in a JSON answer (empty: none).
	IDPath  string          `json:"id_path,omitempty"`
	Receipt *ReceiptMapping `json:"receipt,omitempty"`
	// Failure marks answers that refuse the message even with a success status (e.g. HTTP 200 with
	// "status": "error"), checked before the success rule.
	Failure *FailureRule `json:"failure,omitempty"`
	// MatchBy is what delivery reports carry to find their message: "provider" (default: the
	// provider's message id read at IDPath) or "pulse" (Pulse's own message id, which the request
	// sends with {{message.id}} in a field the provider echoes back, such as a client reference).
	MatchBy string `json:"match_by,omitempty"`
	// Poll asks the provider for each message's status, for providers without delivery webhooks.
	Poll *PollRequest `json:"poll,omitempty"`
}

// Match-by values.
const (
	MatchProvider = "provider"
	MatchPulse    = "pulse"
)

// FailureRule: the answer's value at JSONPath is one of Values (case-insensitive). Unreachable
// says the recipient can't get messages on this channel, so the SMS fallback runs.
type FailureRule struct {
	JSONPath    string   `json:"json_path"`
	Values      []string `json:"values"`
	Unreachable bool     `json:"unreachable,omitempty"`
}

// PollRequest is a status request sent for each message still waiting for a final status, every
// EveryMinutes for ForHours after it was sent; its answer is read with the receipt mapping.
// Templates take {{message.provider_id}} (the provider's id), {{message.id}}, {{message.ref}} and
// {{message.to}}.
type PollRequest struct {
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      []httpauth.Header `json:"headers,omitempty"`
	ContentType  string            `json:"content_type,omitempty"`
	Body         string            `json:"body,omitempty"`
	EveryMinutes int               `json:"every_minutes,omitempty"` // default 10
	ForHours     int               `json:"for_hours,omitempty"`     // default 24
}

// Poll limits.
const (
	DefaultPollEvery = 10
	DefaultPollFor   = 24
	MaxPollFor       = 72
)

// Every and For are the poll's interval and how long a message is followed.
func (p PollRequest) Every() time.Duration {
	if p.EveryMinutes <= 0 {
		return DefaultPollEvery * time.Minute
	}
	return time.Duration(p.EveryMinutes) * time.Minute
}

func (p PollRequest) For() time.Duration {
	if p.ForHours <= 0 {
		return DefaultPollFor * time.Hour
	}
	return time.Duration(p.ForHours) * time.Hour
}

// SuccessRule: the status is within [StatusMin, StatusMax] (default 200–299) and, when JSONPath is
// set, the answer's value there equals JSONValue (as text; empty JSONValue: present and not
// false/empty).
type SuccessRule struct {
	StatusMin int    `json:"status_min,omitempty"`
	StatusMax int    `json:"status_max,omitempty"`
	JSONPath  string `json:"json_path,omitempty"`
	JSONValue string `json:"json_value,omitempty"`
}

// ReceiptMapping reads the provider's delivery webhook: a JSON object, or a list of them at
// ItemsPath (or the body itself when it's an array).
type ReceiptMapping struct {
	ItemsPath  string `json:"items_path,omitempty"`
	IDPath     string `json:"id_path"`
	StatusPath string `json:"status_path"`
	// Statuses maps the provider's status values (case-insensitive) to delivered, read, failed,
	// undeliverable (failed, and the SMS fallback runs) or bounced. Unmapped values are ignored
	// (e.g. "queued").
	Statuses   map[string]string `json:"statuses"`
	ReasonPath string            `json:"reason_path,omitempty"`
	// CodePath is the provider's error code, kept in the reason ("code 1234: reason").
	CodePath string `json:"code_path,omitempty"`
	// Verify is how a receipt proves it comes from the provider, with the receipt token:
	// header (Name holds the token), query (parameter Name holds it), or hmac (header Name holds
	// the hex or base64 HMAC-SHA256 of the body, optionally prefixed sha256=).
	Verify     string `json:"verify"`
	VerifyName string `json:"verify_name"`
}

// Receipt statuses a mapping may produce. "pending" names a status that isn't final (queued,
// sent to the operator…): it is read and ignored, so a status request asks again later.
var receiptStatuses = map[string]bool{"delivered": true, "read": true, "failed": true, "undeliverable": true, "bounced": true, "pending": true}

// ParseCustomRequest reads config["request"].
func ParseCustomRequest(raw string) (CustomRequest, error) {
	var c CustomRequest
	if strings.TrimSpace(raw) == "" {
		return c, errors.New("gateway: custom mode needs a request template")
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, fmt.Errorf("gateway: request template: %w", err)
	}
	return c, nil
}

func (c CustomRequest) contentType() string {
	if c.ContentType == "" {
		return "application/json"
	}
	return c.ContentType
}

func (c CustomRequest) isJSON() bool { return strings.Contains(c.contentType(), "json") }
func (c CustomRequest) isForm() bool {
	return strings.Contains(c.contentType(), "x-www-form-urlencoded")
}

// Validate checks the template: method, a fixed scheme and host, templates that parse, a valid
// success rule and receipt mapping.
func (c CustomRequest) Validate() error {
	switch strings.ToUpper(c.Method) {
	case "GET", "POST", "PUT", "PATCH":
	default:
		return errors.New("gateway: the method must be GET, POST, PUT or PATCH")
	}
	if _, err := fixedOrigin(c.URL); err != nil {
		return err
	}
	for _, h := range c.Headers {
		if !headerNameOK(h.Name) {
			return fmt.Errorf("gateway: %q isn't a valid header name", h.Name)
		}
		if err := checkTemplate("header "+h.Name, h.Value); err != nil {
			return err
		}
	}
	if err := checkTemplate("body", c.Body); err != nil {
		return err
	}
	if err := checkTemplate("URL", c.URL); err != nil {
		return err
	}
	s := c.Success
	if s.StatusMin != 0 || s.StatusMax != 0 {
		if s.StatusMin < 100 || s.StatusMax > 599 || s.StatusMin > s.StatusMax {
			return errors.New("gateway: the success status range must be within 100–599, lowest first")
		}
	}
	if f := c.Failure; f != nil {
		if strings.TrimSpace(f.JSONPath) == "" || len(f.Values) == 0 {
			return errors.New("gateway: the failure rule needs a field and at least one value")
		}
	}
	switch c.MatchBy {
	case "", MatchProvider, MatchPulse:
	default:
		return errors.New("gateway: reports are matched by the provider's id or Pulse's message id")
	}
	if p := c.Poll; p != nil {
		switch strings.ToUpper(p.Method) {
		case "GET", "POST":
		default:
			return errors.New("gateway: the status request's method must be GET or POST")
		}
		if _, err := fixedOrigin(p.URL); err != nil {
			return fmt.Errorf("gateway: status request address: %s", strings.TrimPrefix(err.Error(), "gateway: "))
		}
		for _, h := range p.Headers {
			if !headerNameOK(h.Name) {
				return fmt.Errorf("gateway: status request: %q isn't a valid header name", h.Name)
			}
			if err := checkTemplate("status request header "+h.Name, h.Value); err != nil {
				return err
			}
		}
		if err := checkTemplate("status request URL", p.URL); err != nil {
			return err
		}
		if err := checkTemplate("status request body", p.Body); err != nil {
			return err
		}
		if p.EveryMinutes < 0 || p.EveryMinutes > 1440 {
			return errors.New("gateway: ask for the status every 1 to 1,440 minutes")
		}
		if p.ForHours < 0 || p.ForHours > MaxPollFor {
			return fmt.Errorf("gateway: follow a message for 1 to %d hours", MaxPollFor)
		}
		if c.Receipt == nil {
			return errors.New("gateway: the status request needs the report mapping (where the id and status are)")
		}
		if c.MatchBy != MatchPulse && c.IDPath == "" && !strings.Contains(p.URL+p.Body, "message.id") && !strings.Contains(p.URL+p.Body, "message.ref") {
			return errors.New("gateway: the status request needs the provider's message id (set where it is in the answer) or {{message.id}}")
		}
	}
	if r := c.Receipt; r != nil {
		if r.IDPath == "" || r.StatusPath == "" || len(r.Statuses) == 0 {
			return errors.New("gateway: the receipt mapping needs an id path, a status path and status values")
		}
		folded := map[string]string{}
		for k, v := range r.Statuses {
			if !receiptStatuses[v] {
				return fmt.Errorf("gateway: receipt status %q maps to %q; use delivered, read, failed, undeliverable, bounced or pending", k, v)
			}
			if o, dup := folded[strings.ToLower(k)]; dup {
				return fmt.Errorf("gateway: receipt statuses %q and %q differ only in case (statuses are matched ignoring case)", o, k)
			}
			folded[strings.ToLower(k)] = k
		}
		switch r.Verify {
		case "":
			if c.Poll == nil {
				return errors.New("gateway: receipts are verified by a header token, a query token or an HMAC")
			}
			// Only polled: the webhook accepts nothing.
		case "header", "hmac":
			if !headerNameOK(r.VerifyName) {
				return errors.New("gateway: name the receipt header that carries the token or signature")
			}
		case "query":
			if r.VerifyName == "" {
				return errors.New("gateway: name the receipt query parameter that carries the token")
			}
		default:
			return errors.New("gateway: receipts are verified by a header token, a query token or an HMAC")
		}
	}
	return nil
}

func headerNameOK(n string) bool {
	if n == "" {
		return false
	}
	for _, r := range n {
		if r <= ' ' || r >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}

// fixedOrigin checks the URL's scheme and host carry no template and are http(s).
func fixedOrigin(raw string) (*url.URL, error) {
	end := len(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		if j := strings.IndexAny(raw[i+3:], "/?#"); j >= 0 {
			end = i + 3 + j
		}
	}
	origin := raw[:end]
	u, err := url.Parse(origin)
	if strings.Contains(origin, "{{") || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("gateway: the request URL must start with a fixed http(s)://host (variables only in its path and query)")
	}
	return u, nil
}

func checkTemplate(what, src string) error {
	if !strings.Contains(src, "{{") {
		return nil
	}
	r := personalize.New(personalize.Vars{})
	if _, err := r.Text(what, src, nil); err != nil {
		return fmt.Errorf("gateway: %s: %w", what, err)
	}
	return nil
}

// MessageVars is the message.* namespace for o.
func MessageVars(o Outbound) map[string]any {
	c := o.Rendered.Content
	m := map[string]any{
		"id": o.MessageID, "channel": o.Channel, "to": o.To, "from": o.From, "platform": o.Platform,
		"subject": o.Rendered.Subject, "html": o.Rendered.HTML, "text": c.Text, "title": c.Title, "body": c.Body,
		"image_url": c.ImageURL, "url": c.URL, "sender_id": c.SenderID, "dlt_template_id": c.DLTTemplateID,
		"dlt_entity_id": c.DLTEntityID, "template_name": c.TemplateName, "language": c.Language,
		"header_image_url": c.HeaderImageURL, "ref": o.Ref, "amp": c.AMP, "provider_id": o.ProviderMessageID,
	}
	if o.Channel == channel.WhatsApp || len(c.BodyParams) > 0 {
		params := make([]any, len(c.BodyParams))
		for i, p := range c.BodyParams {
			params[i] = p
		}
		m["params"] = params
	}
	if len(c.Suggestions) > 0 {
		var s []any
		b, _ := json.Marshal(c.Suggestions)
		_ = json.Unmarshal(b, &s)
		m["suggestions"] = s
	}
	if c.Card != nil {
		var card map[string]any
		b, _ := json.Marshal(c.Card)
		_ = json.Unmarshal(b, &card)
		m["card"] = card
	}
	if len(c.Data) > 0 {
		d := map[string]any{}
		for k, v := range c.Data {
			d[k] = v
		}
		m["data"] = d
	}
	return m
}

func (c CustomRequest) renderer(o Outbound) *personalize.Renderer {
	var v personalize.Vars
	if o.Vars != nil {
		v = *o.Vars
	}
	v.Message = MessageVars(o)
	return personalize.New(v)
}

// Build renders the request for o (without authentication).
func (c CustomRequest) Build(ctx context.Context, o Outbound) (*http.Request, []byte, error) {
	r := c.renderer(o)
	target, err := renderURL(r, c.URL)
	if err != nil {
		return nil, nil, Permanent(err.Error())
	}
	var body []byte
	if strings.TrimSpace(c.Body) != "" {
		var s string
		switch {
		case c.isJSON():
			s, err = r.JSON("body", c.Body, nil)
		case c.isForm():
			s, err = renderForm(r, c.Body)
		default:
			s, err = r.Text("body", c.Body, nil)
		}
		if err != nil {
			return nil, nil, Permanent("gateway: " + err.Error())
		}
		body = []byte(s)
	}
	method := strings.ToUpper(c.Method)
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, nil, Permanent(err.Error())
	}
	if body != nil {
		req.Header.Set("Content-Type", c.contentType())
	}
	req.Header.Set("Accept", "application/json, */*")
	for _, h := range c.Headers {
		v, err := r.Text("header", h.Value, nil)
		if err != nil {
			return nil, nil, Permanent("gateway: header " + h.Name + ": " + err.Error())
		}
		if strings.ContainsAny(v, "\r\n") {
			return nil, nil, Permanent("gateway: header " + h.Name + " would contain a line break")
		}
		req.Header.Set(h.Name, v)
	}
	if req.Header.Get("Idempotency-Key") == "" && o.MessageID != "" {
		req.Header.Set("Idempotency-Key", o.MessageID)
	}
	return req, body, nil
}

// renderURL fills a URL template: the path as text (each value path-escaped), each query value
// percent-encoded. Scheme and host are fixed (Validate).
func renderURL(r *personalize.Renderer, raw string) (string, error) {
	origin, err := fixedOrigin(raw)
	if err != nil {
		return "", err
	}
	rest := strings.TrimPrefix(raw, origin.Scheme+"://"+origin.Host)
	path, query, _ := strings.Cut(rest, "?")
	var b strings.Builder
	b.WriteString(origin.Scheme + "://" + origin.Host)
	if strings.Contains(path, "{{") {
		segs := strings.Split(path, "/")
		for i, s := range segs {
			v, err := r.Text("URL", s, nil)
			if err != nil {
				return "", fmt.Errorf("gateway: URL: %w", err)
			}
			if strings.Contains(s, "{{") {
				v = url.PathEscape(v)
			}
			segs[i] = v
		}
		path = strings.Join(segs, "/")
	}
	b.WriteString(path)
	if query != "" {
		q, err := renderForm(r, query)
		if err != nil {
			return "", fmt.Errorf("gateway: URL: %w", err)
		}
		b.WriteString("?" + q)
	}
	return b.String(), nil
}

// renderForm fills "a={{x}}&b=c": each key and value rendered and percent-encoded.
func renderForm(r *personalize.Renderer, src string) (string, error) {
	var parts []string
	for _, pair := range strings.Split(src, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		kv, err := r.Text("form", k, nil)
		if err != nil {
			return "", err
		}
		vv, err := r.Text("form", v, nil)
		if err != nil {
			return "", err
		}
		if !strings.Contains(k, "{{") {
			kv, _ = url.QueryUnescape(kv)
		}
		if !strings.Contains(v, "{{") {
			vv, _ = url.QueryUnescape(vv)
		}
		parts = append(parts, url.QueryEscape(kv)+"="+url.QueryEscape(vv))
	}
	return strings.Join(parts, "&"), nil
}

// Result decides a custom request's outcome from the answer.
func (c CustomRequest) Result(code int, body []byte, hdr http.Header) (Result, error) {
	lo, hi := c.Success.StatusMin, c.Success.StatusMax
	if lo == 0 && hi == 0 {
		lo, hi = 200, 299
	}
	ok := code >= lo && code <= hi
	var doc any
	parsed := json.Unmarshal(body, &doc) == nil
	if f := c.Failure; f != nil && parsed {
		if v, found := httpauth.Lookup(doc, f.JSONPath); found {
			got := httpauth.Scalar(v)
			for _, want := range f.Values {
				if strings.EqualFold(got, want) {
					why := fmt.Sprintf("gateway: HTTP %d, %s is %q: %.300s", code, f.JSONPath, got, body)
					if f.Unreachable {
						return Result{}, Unreachable(why)
					}
					return Result{}, Permanent(why)
				}
			}
		}
	}
	if ok && c.Success.JSONPath != "" {
		v, found := httpauth.Lookup(doc, c.Success.JSONPath)
		got := httpauth.Scalar(v)
		switch {
		case !parsed || !found:
			ok = false
		case c.Success.JSONValue == "":
			ok = got != "" && got != "false" && got != "0"
		default:
			ok = strings.EqualFold(got, c.Success.JSONValue)
		}
		if !ok {
			// The provider answered, but not with success: a refusal, not something to retry.
			return Result{}, Permanent(fmt.Sprintf("gateway: HTTP %d, %s is %q, not %q: %.300s", code, c.Success.JSONPath, got, c.Success.JSONValue, body))
		}
	}
	if ok {
		var id string
		if c.IDPath != "" && parsed {
			v, _ := httpauth.Lookup(doc, c.IDPath)
			id = httpauth.Scalar(v)
		}
		return Result{ProviderMessageID: id}, nil
	}
	if code == http.StatusNotFound || code == http.StatusGone {
		return Result{}, Unreachable("gateway: recipient not reachable")
	}
	if code >= 200 && code < 300 {
		return Result{}, Permanent(fmt.Sprintf("gateway: HTTP %d is outside the success range: %.300s", code, body))
	}
	return Result{}, StatusError("gateway", code, string(body), hdr)
}

// ParseReceipts verifies a receipt with token (fail closed: no token, no receipts) and maps it.
func (c CustomRequest) ParseReceipts(r *http.Request, body []byte, token string) ([]Receipt, error) {
	m := c.Receipt
	if m == nil {
		return nil, errors.New("this gateway has no receipt mapping")
	}
	if token == "" || !verifyReceipt(r, body, m, token) {
		return nil, ErrUnauthorized
	}
	doc, err := decodeReport(body, r.URL.Query())
	if err != nil {
		return nil, err
	}
	return m.Map(doc), nil
}

// Map reads receipts from a decoded webhook or status answer.
func (m *ReceiptMapping) Map(doc any) []Receipt { return m.mapWith(doc, "") }

// mapWith is Map where an entry without an id is about the message with id fallback (a status
// answer about one message); fallback "" skips such entries.
func (m *ReceiptMapping) mapWith(doc any, fallback string) []Receipt {
	items := []any{doc}
	if m.ItemsPath != "" {
		v, _ := httpauth.Lookup(doc, m.ItemsPath)
		arr, _ := v.([]any)
		items = arr
	} else if arr, ok := doc.([]any); ok {
		items = arr
	}
	var out []Receipt
	for _, it := range items {
		idv, _ := httpauth.Lookup(it, m.IDPath)
		stv, _ := httpauth.Lookup(it, m.StatusPath)
		id, st := httpauth.Scalar(idv), httpauth.Scalar(stv)
		if id == "" {
			id = fallback
		}
		mapped := m.status(st)
		if id == "" || mapped == "" {
			continue
		}
		rc := Receipt{ProviderMessageID: id, Status: mapped}
		if m.ReasonPath != "" {
			v, _ := httpauth.Lookup(it, m.ReasonPath)
			rc.Reason = httpauth.Scalar(v)
		}
		if m.CodePath != "" {
			if v, _ := httpauth.Lookup(it, m.CodePath); httpauth.Scalar(v) != "" {
				rc.Reason = strings.TrimSuffix("code "+httpauth.Scalar(v)+": "+rc.Reason, ": ")
			}
		}
		switch mapped {
		case "undeliverable":
			rc.Status, rc.Unreachable = "failed", true
		case "bounced":
			rc.Kind = "hard"
		}
		if rc.Status == "failed" && rc.Reason == "" {
			rc.Reason = "the provider reported a failure (" + st + ")"
		}
		out = append(out, rc)
	}
	return out
}

// status maps a provider status: an exact match first, else the case-insensitive match whose
// key sorts first (so a mapping saved before keys differing only in case were refused still maps
// the same way every time).
func (m *ReceiptMapping) status(st string) string {
	if v, ok := m.Statuses[st]; ok {
		return v
	}
	best, mapped := "", ""
	for k, v := range m.Statuses {
		if strings.EqualFold(k, st) && (mapped == "" || k < best) {
			best, mapped = k, v
		}
	}
	return mapped
}

func verifyReceipt(r *http.Request, body []byte, m *ReceiptMapping, token string) bool {
	switch m.Verify {
	case "header":
		return equalSecret(strings.TrimSpace(r.Header.Get(m.VerifyName)), token)
	case "query":
		return equalSecret(r.URL.Query().Get(m.VerifyName), token)
	case "hmac":
		sig := strings.TrimPrefix(strings.TrimSpace(r.Header.Get(m.VerifyName)), "sha256=")
		mac := hmac.New(sha256.New, []byte(token))
		mac.Write(body)
		sum := mac.Sum(nil)
		return sig != "" && (equalSecret(hex.EncodeToString(sum), strings.ToLower(sig)) ||
			equalSecret(base64.StdEncoding.EncodeToString(sum), sig))
	}
	return false
}

// decodeReport reads a delivery report or status answer: JSON, or a form (in the body, or in the
// query string when the body is empty) read as a flat object.
func decodeReport(body []byte, query url.Values) (any, error) {
	var doc any
	if err := json.Unmarshal(body, &doc); err == nil {
		return doc, nil
	}
	form, ferr := url.ParseQuery(string(body))
	if ferr != nil || len(form) == 0 || strings.TrimSpace(string(body)) == "" {
		if len(query) == 0 {
			return nil, errors.New("the report isn't JSON or a form")
		}
		form = query
	}
	obj := map[string]any{}
	for k := range form {
		obj[k] = form.Get(k)
	}
	return obj, nil
}

// pollRequest is the status request as a CustomRequest, so it renders the same way.
func (c CustomRequest) pollRequest() CustomRequest {
	p := c.Poll
	return CustomRequest{Method: p.Method, URL: p.URL, Headers: p.Headers, ContentType: p.ContentType, Body: p.Body}
}
