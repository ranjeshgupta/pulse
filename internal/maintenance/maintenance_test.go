package maintenance

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"pulse/internal/automation"
)

var now = time.Date(2026, 11, 11, 1, 30, 0, 0, time.UTC)

type fake struct {
	runs         []automation.TenantRun
	claimed      []int64
	taken        map[int64]bool // claims another worker won
	settings     automation.Settings
	idle         []uint32
	pgDeleted    []uint32
	chDeleted    []uint32
	logRows      map[string]int // rows left per log kind
	pruned       map[string]int // kind -> days asked
	chOld        map[string]bool
	chDeletes    []automation.Delete
	chCutoffs    []time.Time
	inboxPurged  int
	pruneBatches int
}

func (f *fake) RetentionTenants(context.Context) ([]automation.TenantRun, error) { return f.runs, nil }
func (f *fake) ClaimRetentionRun(_ context.Context, tenant int64, _ time.Time) (bool, error) {
	if f.taken[tenant] {
		return false, nil
	}
	f.claimed = append(f.claimed, tenant)
	return true, nil
}
func (f *fake) AutomationSettings(context.Context, int64) (automation.Settings, error) {
	return f.settings, nil
}
func (f *fake) IdleAnonymous(_ context.Context, _ int64, _, limit int) ([]uint32, error) {
	n := min(limit, len(f.idle))
	out := f.idle[:n]
	f.idle = f.idle[n:]
	return out, nil
}
func (f *fake) DeleteContacts(_ context.Context, _ int64, ids []uint32) error {
	f.pgDeleted = append(f.pgDeleted, ids...)
	return nil
}
func (f *fake) PruneLog(_ context.Context, _ int64, kind string, days, limit int) (int64, error) {
	f.pruneBatches++
	if f.pruned == nil {
		f.pruned = map[string]int{}
	}
	f.pruned[kind] = days
	n := min(limit, f.logRows[kind])
	f.logRows[kind] -= n
	return int64(n), nil
}
func (f *fake) PurgeInbox(context.Context) (int64, error) {
	f.inboxPurged++
	return 2, nil
}

type fakeCH struct{ f *fake }

func (c fakeCH) DeleteContacts(_ context.Context, _ int64, ids []uint32) error {
	c.f.chDeleted = append(c.f.chDeleted, ids...)
	return nil
}
func (c fakeCH) HasOlder(_ context.Context, _ int64, d automation.Delete, _ time.Time) (bool, error) {
	return c.f.chOld[d.Key], nil
}
func (c fakeCH) DeleteOlder(_ context.Context, _ int64, d automation.Delete, cutoff time.Time) error {
	c.f.chDeletes = append(c.f.chDeletes, d)
	c.f.chCutoffs = append(c.f.chCutoffs, cutoff)
	return nil
}

func cleaner(f *fake) *Cleaner {
	return &Cleaner{Store: f, Analytics: fakeCH{f}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
}

func TestRunTenant(t *testing.T) {
	r := automation.Defaults().Retention
	r.AnonymousDays, r.EventDays, r.AuditDays, r.ImportDays = 30, 365, 90, 0
	r.EventOverrides = map[string]int{"page_view": 90, "purchase": 0}
	f := &fake{logRows: map[string]int{automation.PGAudit: batch + 3, automation.PGWebhookDeliveries: 4},
		chOld: map[string]bool{"override:90": true, "events": true, "message_events": false}}
	for i := 0; i < batch+10; i++ {
		f.idle = append(f.idle, uint32(i+1))
	}
	rep := cleaner(f).RunTenant(context.Background(), 1, r)
	if rep.Anonymous != int64(batch+10) || len(f.pgDeleted) != batch+10 || len(f.chDeleted) != batch+10 {
		t.Errorf("anonymous deleted %d (pg %d, ch %d), want %d in two batches", rep.Anonymous, len(f.pgDeleted), len(f.chDeleted), batch+10)
	}
	// ClickHouse: the page_view override and the type-wide delete (without page_view and
	// purchase); message events had nothing old, so their delete is skipped
	if len(f.chDeletes) != 2 || f.chDeletes[0].Key != "override:90" || f.chDeletes[1].Key != "events" || rep.EventRuns != 2 || rep.Skipped != 1 {
		t.Fatalf("deletes %+v, report %+v", f.chDeletes, rep)
	}
	if ev := f.chDeletes[1]; !ev.NotIn || !slices.Contains(ev.Names, "purchase") || !slices.Contains(ev.Names, "page_view") {
		t.Errorf("the type-wide delete must keep the names with their own setting: %+v", ev)
	}
	if want := now.AddDate(0, 0, -90); !f.chCutoffs[0].Equal(want) {
		t.Errorf("cutoff %v, want %v", f.chCutoffs[0], want)
	}
	// Postgres logs in batches; imports are kept forever (0) so never asked
	if rep.Logs != int64(batch+3+4) || f.pruned[automation.PGAudit] != 90 || f.pruned[automation.PGWebhookDeliveries] != 7 || f.pruned[automation.PGIntegrationLog] != 7 {
		t.Errorf("logs %d, pruned %v", rep.Logs, f.pruned)
	}
	if _, ok := f.pruned[automation.PGImports]; ok {
		t.Error("imports are kept forever by default")
	}
}

// Each workspace runs once a day in its own hour; a claim another worker won is skipped.
func TestRunIsDailyPerWorkspace(t *testing.T) {
	r := automation.Defaults().Retention
	f := &fake{logRows: map[string]int{}, taken: map[int64]bool{4: true},
		runs: []automation.TenantRun{
			{Tenant: 1, Retention: r, LastRun: now.Add(-24 * time.Hour)}, // its hour (1 UTC): due
			{Tenant: 2, Retention: r, LastRun: now.Add(-24 * time.Hour)}, // not its hour
			{Tenant: 3, Retention: r},                                    // never ran: due
			{Tenant: 4, Retention: r},                                    // due, but another worker has it
			{Tenant: 25, Retention: r, LastRun: now.Add(-3 * time.Hour)}, // its hour, but ran today
		}}
	rep, err := cleaner(f).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.claimed, []int64{1, 3}) || rep.Tenants != 2 || rep.Inbox != 2 {
		t.Errorf("claimed %v, report %+v", f.claimed, rep)
	}
}

// Defaults delete nothing from ClickHouse and keep today's log pruning.
func TestDefaultsDeleteNoEvents(t *testing.T) {
	f := &fake{logRows: map[string]int{}, chOld: map[string]bool{"events": true, "message_events": true}}
	rep := cleaner(f).RunTenant(context.Background(), 1, automation.Defaults().Retention)
	if len(f.chDeletes) != 0 || rep.Anonymous != 0 || len(f.pgDeleted) != 0 {
		t.Errorf("defaults deleted events or contacts: %+v", f.chDeletes)
	}
	want := map[string]int{automation.PGAudit: 365, automation.PGWebhookDeliveries: 7, automation.PGIntegrationLog: 7}
	if len(f.pruned) != len(want) {
		t.Errorf("pruned %v, want %v", f.pruned, want)
	}
	for k, d := range want {
		if f.pruned[k] != d {
			t.Errorf("%s: %d days, want %d", k, f.pruned[k], d)
		}
	}
}

func TestRunNowUsesCurrentSettings(t *testing.T) {
	s := automation.Defaults()
	s.Retention.SandboxDays = 3
	f := &fake{settings: s, logRows: map[string]int{automation.PGSandbox: 2}}
	rep, err := cleaner(f).RunNow(context.Background(), 9)
	if err != nil || rep.Logs != 2 || f.pruned[automation.PGSandbox] != 3 || len(f.claimed) != 0 {
		t.Errorf("report %+v, err %v, pruned %v, claimed %v", rep, err, f.pruned, f.claimed)
	}
}
