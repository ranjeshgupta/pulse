// Package maintenance deletes data past each workspace's retention settings (Settings →
// Automation & limits, package automation), like Mautic's mautic:maintenance:cleanup.
//
// Each workspace is cleaned once a day, in its own hour (automation.RetentionDue spreads them over
// the day), and the run is claimed in Postgres (retention_runs) so several workers never repeat it.
// A run deletes:
//   - anonymous visitors idle for N days, with their events (in batches)
//   - ClickHouse events and message events older than their retention, one DELETE per group of
//     event names (automation.Retention.CHPlan: per-name settings win, so a type-wide delete never
//     removes a name kept longer), skipped when nothing is older than the cutoff
//   - Postgres logs (audit log, import history, webhook deliveries, integration log, sandbox
//     inbox) in batches of 5,000
//
// and refreshes the workspace's live feed size. Expired app inbox messages are purged for everyone
// on every pass.
//
// Rule segments drop deleted contacts at their next sweep (their ClickHouse rows are gone); static
// lists keep the ids until the list is next changed, but they no longer match any contact.
package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/automation"
	"pulse/internal/live"
)

const batch = 5000

type Store interface {
	RetentionTenants(ctx context.Context) ([]automation.TenantRun, error)
	ClaimRetentionRun(ctx context.Context, tenant int64, prev time.Time) (bool, error)
	AutomationSettings(ctx context.Context, tenant int64) (automation.Settings, error)
	IdleAnonymous(ctx context.Context, tenant int64, days, limit int) ([]uint32, error)
	DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error
	// PruneLog deletes up to limit rows of a log kind (automation.PG*) older than days.
	PruneLog(ctx context.Context, tenant int64, kind string, days, limit int) (int64, error)
	PurgeInbox(ctx context.Context) (int64, error)
}

type Analytics interface {
	DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error
	HasOlder(ctx context.Context, tenant int64, d automation.Delete, cutoff time.Time) (bool, error)
	DeleteOlder(ctx context.Context, tenant int64, d automation.Delete, cutoff time.Time) error
}

type Cleaner struct {
	Store     Store
	Analytics Analytics
	Log       *slog.Logger
	// RDB, when set, receives each workspace's live feed size (live.SizeKey).
	RDB redis.Cmdable
	Now func() time.Time
}

// Report says what a run deleted.
type Report struct {
	Tenants   int   `json:"tenants"`   // workspaces cleaned
	Anonymous int64 `json:"anonymous"` // anonymous visitors deleted
	Logs      int64 `json:"logs"`      // Postgres log rows deleted
	Inbox     int64 `json:"inbox"`     // expired inbox messages purged
	EventRuns int   `json:"event_deletes"`
	Skipped   int   `json:"event_deletes_skipped"` // nothing older than the cutoff
}

func (r *Report) add(o Report) {
	r.Tenants += o.Tenants
	r.Anonymous += o.Anonymous
	r.Logs += o.Logs
	r.EventRuns += o.EventRuns
	r.Skipped += o.Skipped
}

func (c *Cleaner) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Run does one pass: it purges expired inbox messages and cleans every workspace that is due.
func (c *Cleaner) Run(ctx context.Context) (Report, error) {
	var rep Report
	var err error
	if rep.Inbox, err = c.Store.PurgeInbox(ctx); err != nil {
		return rep, err
	}
	ts, err := c.Store.RetentionTenants(ctx)
	if err != nil {
		return rep, err
	}
	now := c.now()
	for _, t := range ts {
		if ctx.Err() != nil {
			break
		}
		if !automation.RetentionDue(t.Tenant, t.LastRun, now) {
			continue
		}
		ok, err := c.Store.ClaimRetentionRun(ctx, t.Tenant, t.LastRun)
		if err != nil {
			c.Log.Error("claim cleanup", "tenant", t.Tenant, "err", err)
			continue
		}
		if !ok {
			continue // another worker has it
		}
		rep.add(c.RunTenant(ctx, t.Tenant, t.Retention))
	}
	return rep, ctx.Err()
}

// RunNow cleans one workspace at once with its current settings (POST
// /v1/settings/automation/run-retention), without moving its daily run.
func (c *Cleaner) RunNow(ctx context.Context, tenant int64) (Report, error) {
	a, err := c.Store.AutomationSettings(ctx, tenant)
	if err != nil {
		return Report{}, err
	}
	return c.RunTenant(ctx, tenant, a.Retention), ctx.Err()
}

// RunTenant cleans one workspace. Errors are logged and the other deletes still run; whatever
// failed is retried at the next day's run.
func (c *Cleaner) RunTenant(ctx context.Context, tenant int64, r automation.Retention) Report {
	rep := Report{Tenants: 1}
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
	now := c.now()
	for _, d := range r.CHPlan() {
		if ctx.Err() != nil {
			break
		}
		cutoff := d.Cutoff(now)
		found, err := c.Analytics.HasOlder(ctx, tenant, d, cutoff)
		if err != nil {
			c.Log.Error("cleanup check", "tenant", tenant, "delete", d.Key, "err", err)
			continue
		}
		if !found {
			rep.Skipped++
			continue
		}
		if err := c.Analytics.DeleteOlder(ctx, tenant, d, cutoff); err != nil {
			c.Log.Error("cleanup events", "tenant", tenant, "delete", d.Key, "err", err)
			continue
		}
		rep.EventRuns++
	}
	for _, d := range r.PGPlan() {
		for ctx.Err() == nil {
			n, err := c.Store.PruneLog(ctx, tenant, d.Table, d.Days, batch)
			if err != nil {
				c.Log.Error("cleanup log", "tenant", tenant, "log", d.Table, "err", err)
				break
			}
			rep.Logs += n
			if n < batch {
				break
			}
		}
	}
	if c.RDB != nil {
		if err := live.SetSize(ctx, c.RDB, tenant, r.LiveFeedSize); err != nil {
			c.Log.Error("live feed size", "tenant", tenant, "err", err)
		}
	}
	return rep
}

// Loop runs a pass now and then every interval until ctx ends.
func (c *Cleaner) Loop(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if rep, err := c.Run(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("maintenance", "err", err)
		} else if rep.Tenants > 0 || rep.Inbox > 0 {
			c.Log.Info("maintenance", "workspaces", rep.Tenants, "anonymous_deleted", rep.Anonymous, "log_rows_deleted", rep.Logs,
				"inbox_purged", rep.Inbox, "event_deletes", rep.EventRuns, "event_deletes_skipped", rep.Skipped)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// DeleteContacts permanently deletes contacts the same way cleanup does: their ClickHouse events,
// snapshots and memberships first (if that fails nothing is deleted yet), then their Postgres rows
// (profile, devices, inbox, journey state). Used by the journey step "delete contact".
func (c *Cleaner) DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error {
	if err := c.Analytics.DeleteContacts(ctx, tenant, ids); err != nil {
		return err
	}
	return c.Store.DeleteContacts(ctx, tenant, ids)
}
