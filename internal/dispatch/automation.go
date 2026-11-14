package dispatch

// A workspace's automation settings (Settings → Automation & limits): retries and the app inbox
// expiry. Read like the delivery rules, cached for cacheTTL; stores without them (and lookups that
// fail) use the defaults, which are what the dispatcher always did.

import (
	"context"

	"pulse/internal/automation"
)

// AutomationStore is the optional store method for the settings (pg.Store has it).
type AutomationStore interface {
	AutomationSettings(ctx context.Context, tenant int64) (automation.Settings, error)
}

// emailBySegment reports whether the workspace leaves email unsubscribers to segment filters.
func (d *Dispatcher) emailBySegment(ctx context.Context, tenant int64) bool {
	return d.automation(ctx, tenant).Unsubscribe.EmailPolicy == automation.EmailSegment
}

func (d *Dispatcher) automation(ctx context.Context, tenant int64) automation.Settings {
	as, ok := d.store.(AutomationStore)
	if !ok {
		return automation.Defaults()
	}
	if c, ok := d.autoCache.Load(tenant); ok {
		if c := c.(cached[automation.Settings]); d.now().Before(c.exp) {
			return c.v
		}
	}
	a, err := as.AutomationSettings(ctx, tenant)
	if err != nil {
		if d.cfg.Log != nil {
			d.cfg.Log.Error("automation settings", "tenant", tenant, "err", err)
		}
		a = automation.Defaults()
	}
	d.autoCache.Store(tenant, cached[automation.Settings]{v: a, exp: d.now().Add(cacheTTL)})
	return a
}
