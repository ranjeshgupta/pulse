package webhooks

import (
	"context"

	"pulse/internal/model"
)

// SealedStore keeps webhooks with their secrets sealed.
type SealedStore interface {
	ActiveWebhookRows(ctx context.Context) ([]model.Webhook, error)
	RecordDelivery(ctx context.Context, d Delivery) error
}

// Opened adapts a SealedStore to Store, opening each secret with open. When s can remove REST hook
// subscriptions, so can the result (see Unsubscriber).
func Opened(s SealedStore, open func([]byte) (string, error)) Store {
	if u, ok := s.(Unsubscriber); ok {
		return openedUnsub{opened{s, open}, u}
	}
	return opened{s, open}
}

type openedUnsub struct {
	opened
	Unsubscriber
}

type opened struct {
	SealedStore
	open func([]byte) (string, error)
}

func (o opened) ActiveWebhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := o.ActiveWebhookRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Webhook, 0, len(rows))
	for _, r := range rows {
		secret, err := o.open(r.Sealed)
		if err != nil {
			continue // a secret sealed with another key can't sign; skip rather than send unsigned
		}
		out = append(out, Webhook{ID: r.ID, TenantID: r.TenantID, URL: r.URL, Events: r.Events, Secret: secret,
			IntegrationID: r.IntegrationID, SegmentID: r.SegmentID, FormID: r.FormID})
	}
	return out, nil
}
