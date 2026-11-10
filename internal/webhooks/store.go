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

// Opened adapts a SealedStore to Store, opening each secret with open.
func Opened(s SealedStore, open func([]byte) (string, error)) Store { return opened{s, open} }

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
		out = append(out, Webhook{ID: r.ID, TenantID: r.TenantID, URL: r.URL, Events: r.Events, Secret: secret})
	}
	return out, nil
}
