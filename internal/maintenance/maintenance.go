// Package maintenance deletes data past each workspace's retention settings, like Mautic's
// mautic:maintenance:cleanup: anonymous visitors idle for N days (with their events), events older
// than N days, and old audit log entries. Expired app inbox messages are purged for everyone.
//
// Rule segments drop deleted contacts at their next sweep (their ClickHouse rows are gone); static
// lists keep the ids until the list is next changed, but they no longer match any contact.
package maintenance

import (
	"context"
	"log/slog"
	"time"

	"pulse/internal/model"
)

const batch = 5000

type Store interface {
	Retentions(ctx context.Context) (map[int64]model.RetentionSettings, error)
	IdleAnonymous(ctx context.Context, tenant int64, days, limit int) ([]uint32, error)
	DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error
	PruneAudit(ctx context.Context, tenant int64, days int) (int64, error)
	PurgeInbox(ctx context.Context) (int64, error)
}

type Analytics interface {
	DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error
	DeleteEventsBefore(ctx context.Context, tenant int64, days int) error
}

type Cleaner struct {
	Store     Store
	Analytics Analytics
	Log       *slog.Logger
}

// Report says what one run deleted.
type Report struct {
	Anonymous int64
	Audit     int64
	Inbox     int64
	EventRuns int
}

// Run does one cleanup pass over every workspace.
func (c *Cleaner) Run(ctx context.Context) (Report, error) {
	var rep Report
	var err error
	if rep.Inbox, err = c.Store.PurgeInbox(ctx); err != nil {
		return rep, err
	}
	rs, err := c.Store.Retentions(ctx)
	if err != nil {
		return rep, err
	}
	for tenant, r := range rs {
		if r.AnonymousDays > 0 {
			for ctx.Err() == nil {
				ids, err := c.Store.IdleAnonymous(ctx, tenant, r.AnonymousDays, batch)
				if err != nil || len(ids) == 0 {
					if err != nil {
						c.Log.Error("cleanup anonymous", "tenant", tenant, "err", err)
					}
					break
				}
				// ClickHouse first: if it fails, the Postgres rows stay and the next run retries.
				if err := c.Analytics.DeleteContacts(ctx, tenant, ids); err != nil {
					c.Log.Error("cleanup anonymous events", "tenant", tenant, "err", err)
					break
				}
				if err := c.Store.DeleteContacts(ctx, tenant, ids); err != nil {
					c.Log.Error("cleanup anonymous contacts", "tenant", tenant, "err", err)
					break
				}
				rep.Anonymous += int64(len(ids))
				if len(ids) < batch {
					break
				}
			}
		}
		if r.EventDays > 0 {
			if err := c.Analytics.DeleteEventsBefore(ctx, tenant, r.EventDays); err != nil {
				c.Log.Error("cleanup events", "tenant", tenant, "err", err)
			} else {
				rep.EventRuns++
			}
		}
		if r.AuditDays > 0 {
			n, err := c.Store.PruneAudit(ctx, tenant, r.AuditDays)
			if err != nil {
				c.Log.Error("cleanup audit log", "tenant", tenant, "err", err)
			}
			rep.Audit += n
		}
	}
	return rep, ctx.Err()
}

// Loop runs a pass now and then every interval until ctx ends.
func (c *Cleaner) Loop(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if rep, err := c.Run(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("maintenance", "err", err)
		} else if rep.Anonymous+rep.Audit+rep.Inbox > 0 || rep.EventRuns > 0 {
			c.Log.Info("maintenance", "anonymous_deleted", rep.Anonymous, "audit_deleted", rep.Audit, "inbox_purged", rep.Inbox, "event_cleanups", rep.EventRuns)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
