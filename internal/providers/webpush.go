package providers

// Standard Web Push (RFC 8030 with VAPID, RFC 8292), for browsers subscribed through the web SDK.
// It needs no Firebase project: each workspace signs with its own VAPID key pair. A device whose
// token is a PushSubscription JSON object ({"endpoint": ...}) is sent this way.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// IsWebPushSubscription reports whether a push token is a browser PushSubscription.
func IsWebPushSubscription(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "{")
}

// webPushHosts are the browser push services a subscription's endpoint may point at. Browsers only
// hand out endpoints on these, so anything else came from somebody's script, and sending there would
// let a public site key make Pulse post to any address (an internal service, a cloud metadata URL).
var webPushHosts = []string{
	"fcm.googleapis.com", "android.googleapis.com", // Chrome, Edge (Chromium), Opera, Samsung
	"push.services.mozilla.com", // Firefox (updates.push.services.mozilla.com)
	"notify.windows.com",        // legacy Edge (*.notify.windows.com)
	"push.apple.com",            // Safari (web.push.apple.com)
}

// CheckWebPushSubscription reports whether token is a browser PushSubscription whose endpoint is an
// https URL on a known push service.
func CheckWebPushSubscription(token string) error {
	var sub struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal([]byte(token), &sub); err != nil || sub.Endpoint == "" {
		return errors.New("not a valid browser push subscription")
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return errors.New("the push subscription's endpoint must be an https URL of a browser push service")
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range webPushHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return nil
		}
	}
	return fmt.Errorf("the push subscription's endpoint %q isn't a known browser push service", host)
}

// GenerateVAPIDKeys returns a new key pair (base64url, as browsers and the protocol use them).
func GenerateVAPIDKeys() (private, public string, err error) { return webpush.GenerateVAPIDKeys() }

type WebPush struct {
	HTTP       *http.Client
	PublicKey  string
	PrivateKey string
	Subject    string // a mailto: or https: contact for the push services
}

// WebPushPayload is what the service worker (/sdk/pulse-sw.js) shows.
type WebPushPayload struct {
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Image     string            `json:"image,omitempty"`
	URL       string            `json:"url,omitempty"`
	Data      map[string]string `json:"data,omitempty"`
	MessageID string            `json:"message_id"`
}

func (p *WebPush) Send(ctx context.Context, o Outbound) (Result, error) {
	if err := CheckWebPushSubscription(o.To); err != nil {
		return Result{}, Unreachable(err.Error())
	}
	var sub webpush.Subscription
	if err := json.Unmarshal([]byte(o.To), &sub); err != nil || sub.Endpoint == "" {
		return Result{}, Unreachable("not a valid browser push subscription")
	}
	c := o.Rendered.Content
	body, _ := json.Marshal(WebPushPayload{Title: c.Title, Body: c.Body, Image: c.ImageURL, URL: c.URL, Data: c.Data, MessageID: o.MessageID})
	resp, err := webpush.SendNotificationWithContext(ctx, body, &sub, &webpush.Options{
		HTTPClient: p.HTTP, Subscriber: p.Subject, VAPIDPublicKey: p.PublicKey, VAPIDPrivateKey: p.PrivateKey,
		TTL: 24 * 3600, Urgency: webpush.UrgencyNormal,
	})
	if err != nil {
		return Result{}, fmt.Errorf("web push: %w", err) // network: transient
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return Result{ProviderMessageID: resp.Header.Get("Location")}, nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return Result{}, Unreachable("the browser unsubscribed from push") // the device is removed
	}
	return Result{}, StatusError("web push", resp.StatusCode, string(msg), resp.Header)
}
