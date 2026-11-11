package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "twilio", Channel: channel.SMS, Name: "Twilio",
		Description: "SMS through Twilio's Programmable Messaging API, worldwide.",
		Config: []Field{
			{Key: "account_sid", Label: "Account SID", Placeholder: "AC0123456789abcdef0123456789abcdef", Required: true},
			{Key: "from", Label: "From number (leave empty to use a Messaging Service)", Placeholder: "+14155550123"},
			{Key: "messaging_service_sid", Label: "Messaging Service SID", Placeholder: "MG0123456789abcdef0123456789abcdef"},
			{Key: "status_callback_url", Label: "Delivery report URL (this provider's Pulse webhook URL)",
				Placeholder: "https://pulse.example.com/v1/webhooks/providers/42"},
		},
		Secrets: []Field{{Key: "auth_token", Label: "Auth token", Required: true}},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			if c["from"] == "" && c["messaging_service_sid"] == "" {
				return nil, errors.New("twilio: set a From number or a Messaging Service SID")
			}
			return &Twilio{HTTP: d.HTTP, BaseURL: "https://api.twilio.com", AccountSID: c["account_sid"],
				AuthToken: s["auth_token"], From: c["from"], MessagingServiceSID: c["messaging_service_sid"],
				StatusCallback: c["status_callback_url"]}, nil
		},
	})
}

// Twilio sends SMS through the Messages API and verifies its status callbacks.
type Twilio struct {
	HTTP                *http.Client
	BaseURL             string
	AccountSID          string
	AuthToken           string
	From                string
	MessagingServiceSID string // used when set (Twilio then picks the sender); From otherwise
	StatusCallback      string // public webhook URL sent with each message, so receipts come back
}

// Twilio error codes that mean the number can never receive this message; any other 4xx is
// permanent too, the codes only make the reason readable.
var twilioPermanent = map[int]string{
	21211: "invalid 'To' number",
	21214: "'To' number cannot be reached",
	21408: "region not enabled for this account",
	21610: "recipient replied STOP",
	21612: "cannot route to this number",
	21614: "'To' number is not a mobile number",
}

// twilioBadNumber are the permanent errors caused by the number itself (an SMS bounce).
var twilioBadNumber = map[int]bool{21211: true, 21214: true, 21612: true, 21614: true}

func (t *Twilio) Send(ctx context.Context, o Outbound) (Result, error) {
	form := url.Values{"To": {o.To}, "Body": {o.Rendered.Content.Text}}
	if t.MessagingServiceSID != "" {
		form.Set("MessagingServiceSid", t.MessagingServiceSID)
	} else {
		form.Set("From", t.From)
	}
	if t.StatusCallback != "" {
		form.Set("StatusCallback", t.StatusCallback)
	}
	endpoint := t.BaseURL + "/2010-04-01/Accounts/" + url.PathEscape(t.AccountSID) + "/Messages.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, Permanent(err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(t.AccountSID, t.AuthToken)
	code, body, hdr, err := call(t.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	var r struct {
		SID     string `json:"sid"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &r)
	if code >= 200 && code < 300 && r.SID != "" {
		return Result{ProviderMessageID: r.SID}, nil
	}
	if reason, ok := twilioPermanent[r.Code]; ok {
		if twilioBadNumber[r.Code] {
			return Result{}, BadNumber(fmt.Sprintf("twilio %d: %s: %s", r.Code, reason, r.Message))
		}
		return Result{}, Permanent(fmt.Sprintf("twilio %d: %s: %s", r.Code, reason, r.Message))
	}
	if code >= 200 && code < 300 {
		return Result{}, Permanent(fmt.Sprintf("twilio: response without sid: %.300s", body)) // may have been sent: don't retry
	}
	return Result{}, StatusError("twilio", code, string(body), hdr)
}

// ParseReceipts handles a status callback (one message per request).
func (t *Twilio) ParseReceipts(r *http.Request) ([]Receipt, error) {
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("twilio callback: %w", err)
	}
	if !t.validSignature(r, form) {
		return nil, ErrUnauthorized
	}
	// An incoming message (the number's "A message comes in" webhook) has a body and no delivery status.
	if form.Get("MessageStatus") == "" && (form.Get("SmsStatus") == "received" || form.Has("Body")) {
		if form.Get("From") == "" {
			return nil, errors.New("twilio inbound message without From")
		}
		return []Receipt{{ProviderMessageID: form.Get("MessageSid"), Status: Inbound, From: form.Get("From"), Text: form.Get("Body")}}, nil
	}
	var status string
	switch form.Get("MessageStatus") {
	case "delivered":
		status = "delivered"
	case "read":
		status = "read"
	case "undelivered", "failed":
		status = "failed"
	default: // queued, sending, sent, accepted...: not final
		return nil, nil
	}
	rc := Receipt{ProviderMessageID: form.Get("MessageSid"), Status: status}
	if c := form.Get("ErrorCode"); c != "" && status == "failed" {
		rc.Reason = "twilio error " + c
		if m := form.Get("ErrorMessage"); m != "" {
			rc.Reason += ": " + m
		}
		// 30003 unreachable handset, 30005 unknown destination, 30006 landline or unreachable carrier.
		rc.Unreachable = c == "30003" || c == "30005" || c == "30006"
	}
	if rc.ProviderMessageID == "" {
		return nil, errors.New("twilio callback without MessageSid")
	}
	return []Receipt{rc}, nil
}

// validSignature checks X-Twilio-Signature: base64(HMAC-SHA1(auth token, URL + sorted key+value
// pairs)). Twilio signs the public URL, so it is rebuilt from the proxy headers; with and without an
// explicit port, since Twilio may or may not include it.
func (t *Twilio) validSignature(r *http.Request, form url.Values) bool {
	got := r.Header.Get("X-Twilio-Signature")
	if got == "" || t.AuthToken == "" {
		return false
	}
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var params strings.Builder
	for _, k := range keys {
		vs := append([]string(nil), form[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			params.WriteString(k + v)
		}
	}
	for _, u := range publicURLs(r) {
		mac := hmac.New(sha1.New, []byte(t.AuthToken))
		mac.Write([]byte(u + params.String()))
		if equalSecret(base64.StdEncoding.EncodeToString(mac.Sum(nil)), got) {
			return true
		}
	}
	return false
}

// publicURLs is the URL the provider called, as seen from outside the reverse proxy.
func publicURLs(r *http.Request) []string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	uri := r.URL.RequestURI() // path and raw query as received
	out := []string{scheme + "://" + host + uri}
	if h, _, err := net.SplitHostPort(host); err == nil {
		out = append(out, scheme+"://"+h+uri)
	} else if scheme == "https" {
		out = append(out, scheme+"://"+host+":443"+uri)
	} else {
		out = append(out, scheme+"://"+host+":80"+uri)
	}
	return out
}
