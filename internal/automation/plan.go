package automation

// Retention planning: which deletes a workspace's daily cleanup runs, and which of them a change
// of settings makes shorter (the "what will be deleted" preview).
//
// ClickHouse deletes go per table with a name predicate, so that a delete for one kind of event
// never touches a name kept longer: every event name belongs to exactly one group (an override,
// the page/screen views, or "every other name"), and each group is one DELETE with
// `name IN (...)` or `name NOT IN (...)` and `ts < cutoff`.

import (
	"slices"
	"strconv"
	"time"
)

// Tables and Postgres log kinds a plan deletes from.
const (
	TableEvents        = "events"
	TableMessageEvents = "message_events"

	PGAudit             = "audit"
	PGImports           = "imports"
	PGWebhookDeliveries = "webhook_deliveries"
	PGIntegrationLog    = "integration_log"
	PGSandbox           = "sandbox"
	PGAnonymous         = "anonymous" // contacts: anonymous visitors idle for Days (preview only; cleanup has its own loop)
)

// Delete is one delete of a plan: rows of Table (ClickHouse) or Kind (Postgres) older than Days.
type Delete struct {
	Key   string `json:"key"`   // stable id: events, web_views, override:<days>, message_events, audit, ...
	Store string `json:"store"` // clickhouse | postgres
	Table string `json:"table"` // ClickHouse table, or the Postgres kind
	// Names narrows a ClickHouse events delete: name IN Names, or name NOT IN Names when NotIn.
	// Empty with NotIn false: every row of the workspace.
	Names []string `json:"names,omitempty"`
	NotIn bool     `json:"not_in,omitempty"`
	Days  int      `json:"days"`
}

// Cutoff is the time before which rows are deleted.
func (d Delete) Cutoff(now time.Time) time.Time { return now.AddDate(0, 0, -d.Days) }

// CHPlan lists the ClickHouse deletes for a retention (only those with Days > 0).
func (r Retention) CHPlan() []Delete {
	var out []Delete
	byDays := map[int][]string{}
	for _, n := range r.overrideNames() {
		byDays[r.EventOverrides[n]] = append(byDays[r.EventOverrides[n]], n)
	}
	days := make([]int, 0, len(byDays))
	for d := range byDays {
		days = append(days, d)
	}
	slices.Sort(days)
	for _, d := range days {
		if d > 0 {
			out = append(out, Delete{Key: "override:" + strconv.Itoa(d), Store: "clickhouse", Table: TableEvents, Names: byDays[d], Days: d})
		}
	}
	// Every other name: excluded are the overrides, and the views when they have their own setting.
	excluded := r.overrideNames()
	var views []string
	for _, n := range WebViewNames {
		if _, ok := r.EventOverrides[n]; !ok {
			views = append(views, n)
		}
	}
	separateViews := r.WebViews() != r.EventDays
	if separateViews {
		excluded = append(excluded, views...)
		if r.WebViews() > 0 && len(views) > 0 {
			out = append(out, Delete{Key: "web_views", Store: "clickhouse", Table: TableEvents, Names: views, Days: r.WebViews()})
		}
	}
	if r.EventDays > 0 {
		slices.Sort(excluded)
		out = append(out, Delete{Key: "events", Store: "clickhouse", Table: TableEvents, Names: excluded, NotIn: len(excluded) > 0, Days: r.EventDays})
	}
	if d := r.MessageEvents(); d > 0 {
		out = append(out, Delete{Key: "message_events", Store: "clickhouse", Table: TableMessageEvents, Days: d})
	}
	return out
}

// PGPlan lists the Postgres log deletes (only those with Days > 0). Anonymous visitors are
// cleaned separately (they take their events with them).
func (r Retention) PGPlan() []Delete {
	var out []Delete
	for _, x := range []struct {
		kind string
		days int
	}{{PGAudit, r.AuditDays}, {PGImports, r.ImportDays}, {PGWebhookDeliveries, r.WebhookDeliveryDays},
		{PGIntegrationLog, r.IntegrationLogDays}, {PGSandbox, r.SandboxDays}} {
		if x.days > 0 {
			out = append(out, Delete{Key: x.kind, Store: "postgres", Table: x.kind, Days: x.days})
		}
	}
	return out
}

// shorter reports whether newD keeps less than oldD (0 is forever).
func shorter(oldD, newD int) bool { return newD > 0 && (oldD == 0 || newD < oldD) }

// Shortened lists the deletes of next that remove data cur keeps: a group whose retention got
// shorter (or turned on) for at least one of its event names. These are what the preview counts.
func Shortened(cur, next Retention) []Delete {
	var out []Delete
	for _, d := range next.CHPlan() {
		hit := false
		switch {
		case d.Table == TableMessageEvents:
			hit = shorter(cur.MessageEvents(), d.Days)
		case d.NotIn || len(d.Names) == 0:
			// every other name: today's events setting, and any name that had its own setting
			// before and falls into this group now
			hit = shorter(cur.EventDays, d.Days)
			for _, n := range append(cur.overrideNames(), WebViewNames...) {
				if !slices.Contains(d.Names, n) && shorter(cur.EventDaysFor(n), d.Days) {
					hit = true
				}
			}
		default:
			for _, n := range d.Names {
				if shorter(cur.EventDaysFor(n), d.Days) {
					hit = true
				}
			}
		}
		if hit {
			out = append(out, d)
		}
	}
	old := map[string]int{}
	for _, d := range cur.PGPlan() {
		old[d.Key] = d.Days
	}
	for _, d := range next.PGPlan() {
		if shorter(old[d.Key], d.Days) {
			out = append(out, d)
		}
	}
	if shorter(cur.AnonymousDays, next.AnonymousDays) {
		out = append(out, Delete{Key: PGAnonymous, Store: "postgres", Table: PGAnonymous, Days: next.AnonymousDays})
	}
	return out
}

// RetentionSlot is the UTC hour a workspace's daily cleanup runs, spreading workspaces over the day.
func RetentionSlot(tenant int64) int { return int(tenant % 24) }

// RetentionDue reports whether a workspace's daily cleanup should run now: never ran; or ran
// 23+ hours ago and it is the workspace's hour; or it missed its hour (47+ hours ago).
func RetentionDue(tenant int64, last, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	since := now.Sub(last)
	return since >= 47*time.Hour || since >= 23*time.Hour && now.UTC().Hour() == RetentionSlot(tenant)
}

// BaseBackoff is the retry schedule Pulse always used: 7 waits from 30 seconds to an hour (8 tries
// in all, about 1 h 50 min).
var BaseBackoff = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute,
	10 * time.Minute, 30 * time.Minute, time.Hour}

// Schedule is the wait before each retry: BaseBackoff cut to Attempts-1 waits, each capped at the
// maximum; more attempts than BaseBackoff has keep doubling the last wait, up to the maximum.
func (r Retries) Schedule() []time.Duration {
	n := max(r.Attempts-1, 0)
	maxW := r.MaxBackoff()
	if maxW <= 0 {
		maxW = DefaultMaxBackoff
	}
	out := make([]time.Duration, 0, n)
	var last time.Duration
	for i := range n {
		w := last * 2
		if i < len(BaseBackoff) {
			w = BaseBackoff[i]
		}
		w = min(w, maxW)
		out = append(out, w)
		last = w
	}
	return out
}

// NextRebuild is when reachability counts built at now should be rebuilt: a day later, or at the
// next ReachRebuildHour (UTC) that is at least an hour away.
func (s Schedules) NextRebuild(now time.Time) time.Time {
	if s.ReachRebuildHour < 0 || s.ReachRebuildHour > 23 {
		return now.Add(24 * time.Hour)
	}
	u := now.UTC()
	next := time.Date(u.Year(), u.Month(), u.Day(), s.ReachRebuildHour, 0, 0, 0, time.UTC)
	for next.Sub(u) < time.Hour {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// TenantRun is a workspace's retention settings with when its daily cleanup last ran.
type TenantRun struct {
	Tenant    int64
	Retention Retention
	LastRun   time.Time // zero: never
}
