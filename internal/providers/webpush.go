package providers

// Standard Web Push (RFC 8030 with VAPID, RFC 8292), for browsers subscribed through the web SDK.
// It needs no Firebase project: each workspace signs with its own VAPID key pair. A device whose
// token is a PushSubscription JSON object ({"endpoint": ...}) is sent this way.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// IsWebPushSubscription reports whether a push token is a browser PushSubscription.
func IsWebPushSubscription(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "{")
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
