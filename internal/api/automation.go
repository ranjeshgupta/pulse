package api

// Settings → Automation & limits (package automation): retention per log type and event name,
// retries, schedules, inbox expiry and default provider limits.
//
//	GET  /v1/settings/automation               current settings, defaults, allowed ranges, server facts
//	PUT  /v1/settings/automation               replace the sections sent (audited like every change)
//	POST /v1/settings/automation/preview       what a change would delete now (rows per delete)
//	POST /v1/settings/automation/run-retention run this workspace's cleanup now (tests, after a change)

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"pulse/internal/automation"
	"pulse/internal/live"
	"pulse/internal/maintenance"
	"pulse/internal/model"
	"pulse/internal/reach"
)

// AutomationStore is the Postgres side (pg.Store).
type AutomationStore interface {
	AutomationSettings(ctx context.Context, tenant int64) (automation.Settings, error)
	PutAutomationSettings(ctx context.Context, tenant int64, a automation.Settings) error
	CountLog(ctx context.Context, tenant int64, kind string, days int) (int64, error)
	LastRetentionRun(ctx context.Context, tenant int64) (time.Time, error)
}

// RetentionAnalytics counts what a ClickHouse retention delete would remove (ch.Store).
type RetentionAnalytics interface {
	CountOlder(ctx context.Context, tenant int64, d automation.Delete, cutoff time.Time) (uint64, error)
}

// RetentionRunner runs one workspace's cleanup now (maintenance.Cleaner, the server's Deleter).
type RetentionRunner interface {
	RunNow(ctx context.Context, tenant int64) (maintenance.Report, error)
}

const (
	previewCacheTTL = time.Minute     // the same preview again within a minute is served from cache
	previewEvery    = 5 * time.Second // at most one uncached preview per workspace this often
	previewTimeout  = 20 * time.Second
	runRetentionTTL = 10 * time.Minute // a run-now holds the workspace's lock at most this long
)

func (s *Server) automationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/settings/automation", s.authed(s.getAutomation))
	mux.HandleFunc("PUT /v1/settings/automation", s.authed(s.putAutomation))
	mux.HandleFunc("POST /v1/settings/automation/preview", s.authed(s.previewAutomation))
	mux.HandleFunc("POST /v1/settings/automation/run-retention", s.authed(s.runRetention))
	mux.HandleFunc("GET /v1/unsubscribe-topics", s.tenant(s.listTopics))
}

// listTopics returns the workspace's subscription topics and its email unsubscribe policy.
func (s *Server) listTopics(w http.ResponseWriter, r *http.Request, tenant int64) {
	u := s.unsubscribeSettings(r.Context(), tenant)
	writeJSON(w, http.StatusOK, map[string]any{"topics": u.Topics, "email_policy": u.EmailPolicy, "link_mode": u.LinkMode})
}

func (s *Server) automationStore(w http.ResponseWriter) (AutomationStore, bool) {
	as, ok := s.Store.(AutomationStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "automation settings are not available")
	}
	return as, ok
}

// sweepDefault is the server's segment sweep interval in minutes (the setting's 0).
func (s *Server) sweepDefault() float64 {
	if s.SweepInterval > 0 {
		return s.SweepInterval.Minutes()
	}
	return 5
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request, p model.Principal) {
	as, ok := s.automationStore(w)
	if !ok {
		return
	}
	a, err := as.AutomationSettings(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	var lastRun *time.Time
	if t, err := as.LastRetentionRun(r.Context(), p.TenantID); err == nil && !t.IsZero() {
		lastRun = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": a,
		"defaults": automation.Defaults(),
		"limits":   automation.Limits,
		"server": map[string]any{
			"sweep_minutes":      s.sweepDefault(), // what sweep_minutes 0 means
			"web_view_names":     automation.WebViewNames,
			"limit_channels":     automation.LimitChannels,
			"max_overrides":      automation.MaxOverrides,
			"retention_hour_utc": automation.RetentionSlot(p.TenantID), // when the daily cleanup runs
			"last_retention_run": lastRun,
		},
	})
}

// nextAutomation reads a PUT or preview body: the sections sent replace the current ones.
func (s *Server) nextAutomation(w http.ResponseWriter, r *http.Request, as AutomationStore, tenant int64) (cur, next automation.Settings, ok bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the request")
		return cur, next, false
	}
	cur, err = as.AutomationSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return cur, next, false
	}
	next, err = automation.Apply(cur, body)
	if err == nil {
		err = next.Validate()
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return cur, next, false
	}
	return cur, next, true
}

func (s *Server) putAutomation(w http.ResponseWriter, r *http.Request, p model.Principal) {
	as, ok := s.automationStore(w)
	if !ok {
		return
	}
	cur, next, ok := s.nextAutomation(w, r, as, p.TenantID)
	if !ok {
		return
	}
	if err := as.PutAutomationSettings(r.Context(), p.TenantID, next); err != nil {
		s.fail(w, err)
		return
	}
	if s.RDB != nil {
		ctx := context.WithoutCancel(r.Context())
		if err := live.SetSize(ctx, s.RDB, p.TenantID, next.Retention.LiveFeedSize); err != nil {
			s.Log.Error("live feed size", "tenant", p.TenantID, "err", err)
		}
		// A new rebuild hour applies to the current reachability counts too.
		if next.Schedules.ReachRebuildHour != cur.Schedules.ReachRebuildHour {
			if next.Schedules.ReachRebuildHour >= 0 {
				s.RDB.ExpireAt(ctx, reach.BuiltKey(p.TenantID), next.Schedules.NextRebuild(s.now()))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": next})
}

// PreviewItem is one delete a change would run, with the rows it would remove now.
type PreviewItem struct {
	automation.Delete
	Cutoff time.Time `json:"cutoff"`
	// OldDays is what this data was kept for before (0: forever); for a group of event names,
	// the shortest of its names' old settings (OldByName has each).
	OldDays   int            `json:"old_days"`
	OldByName map[string]int `json:"old_days_by_name,omitempty"`
	Rows      int64          `json:"rows"`
	Error     string         `json:"error,omitempty"` // could not count
}

func oldDays(cur automation.Retention, d automation.Delete) (int, map[string]int) {
	switch {
	case d.Store == "postgres":
		return map[string]int{automation.PGAudit: cur.AuditDays, automation.PGImports: cur.ImportDays,
			automation.PGWebhookDeliveries: cur.WebhookDeliveryDays, automation.PGIntegrationLog: cur.IntegrationLogDays,
			automation.PGSandbox: cur.SandboxDays, automation.PGAnonymous: cur.AnonymousDays}[d.Table], nil
	case d.Table == automation.TableMessageEvents:
		return cur.MessageEvents(), nil
	case d.NotIn || len(d.Names) == 0:
		return cur.EventDays, nil
	}
	by := map[string]int{}
	least := -1
	for _, n := range d.Names {
		by[n] = cur.EventDaysFor(n)
		if by[n] > 0 && (least < 0 || by[n] < least) {
			least = by[n]
		}
	}
	return max(least, 0), by
}

func (s *Server) previewAutomation(w http.ResponseWriter, r *http.Request, p model.Principal) {
	as, ok := s.automationStore(w)
	if !ok {
		return
	}
	cur, next, ok := s.nextAutomation(w, r, as, p.TenantID)
	if !ok {
		return
	}
	changes := automation.Shortened(cur.Retention, next.Retention)
	if len(changes) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"items": []PreviewItem{}, "approximate": false, "cached": false})
		return
	}
	plan, _ := json.Marshal(changes)
	sum := sha256.Sum256(plan)
	tenant := strconv.FormatInt(p.TenantID, 10)
	cacheKey := "autoprev:" + tenant + ":" + hex.EncodeToString(sum[:8])
	if s.RDB != nil {
		if b, err := s.RDB.Get(r.Context(), cacheKey).Bytes(); err == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
		// Counting scans old data: one uncached preview per workspace every few seconds.
		if ok, err := s.RDB.SetNX(r.Context(), "autoprevrl:"+tenant, 1, previewEvery).Result(); err == nil && !ok {
			writeErr(w, http.StatusTooManyRequests, "a preview is already being counted; try again in a few seconds")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
	defer cancel()
	now := s.now()
	ra, _ := s.Analytics.(RetentionAnalytics)
	items := make([]PreviewItem, 0, len(changes))
	for _, d := range changes {
		it := PreviewItem{Delete: d, Cutoff: d.Cutoff(now)}
		it.OldDays, it.OldByName = oldDays(cur.Retention, d)
		var err error
		switch {
		case d.Store == "postgres":
			it.Rows, err = as.CountLog(ctx, p.TenantID, d.Table, d.Days)
		case ra == nil:
			err = errors.New("analytics not available")
		default:
			var n uint64
			n, err = ra.CountOlder(ctx, p.TenantID, d, it.Cutoff)
			it.Rows = int64(n)
		}
		if err != nil {
			s.Log.Error("retention preview", "tenant", p.TenantID, "delete", d.Key, "err", err)
			it.Error = "could not count these rows"
		}
		items = append(items, it)
	}
	out := map[string]any{"items": items, "approximate": true, "cached": false}
	if s.RDB != nil {
		cachedOut := map[string]any{"items": items, "approximate": true, "cached": true}
		if b, err := json.Marshal(cachedOut); err == nil {
			s.RDB.Set(context.WithoutCancel(r.Context()), cacheKey, b, previewCacheTTL)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) runRetention(w http.ResponseWriter, r *http.Request, p model.Principal) {
	rr, ok := s.Deleter.(RetentionRunner)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "cleanup is not available on this server")
		return
	}
	lock := "autorun:" + strconv.FormatInt(p.TenantID, 10)
	if s.RDB != nil {
		ok, err := s.RDB.SetNX(r.Context(), lock, 1, runRetentionTTL).Result()
		if err != nil {
			s.fail(w, err)
			return
		}
		if !ok {
			writeErr(w, http.StatusConflict, "a cleanup of this workspace is already running")
			return
		}
		defer s.RDB.Del(context.WithoutCancel(r.Context()), lock)
	}
	// The run finishes even if the caller goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), runRetentionTTL)
	defer cancel()
	rep, err := rr.RunNow(ctx, p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep})
}

// defaultLimitsFor is the workspace's default sending limits for new providers of a channel
// (Settings → Automation & limits); zero when none are set or they can't be read.
func (s *Server) defaultLimitsFor(ctx context.Context, tenant int64, ch string) model.ProviderLimits {
	as, ok := s.Store.(AutomationStore)
	if !ok {
		return model.ProviderLimits{}
	}
	a, err := as.AutomationSettings(ctx, tenant)
	if err != nil {
		s.Log.Error("automation settings", "tenant", tenant, "err", err)
		return model.ProviderLimits{}
	}
	return a.LimitsFor(ch)
}

// unsubscribeSettings is the workspace's unsubscribe section (defaults when it can't be read).
func (s *Server) unsubscribeSettings(ctx context.Context, tenant int64) automation.Unsubscribe {
	as, ok := s.Store.(AutomationStore)
	if !ok {
		return automation.DefaultUnsubscribe()
	}
	a, err := as.AutomationSettings(ctx, tenant)
	if err != nil {
		s.Log.Error("automation settings", "tenant", tenant, "err", err)
		return automation.DefaultUnsubscribe()
	}
	return a.Unsubscribe
}

// topicProblem checks a campaign's or step's topic: "" or one of the workspace's topics.
func (s *Server) topicProblem(ctx context.Context, tenant int64, topic string) error {
	if topic == "" {
		return nil
	}
	if _, ok := s.unsubscribeSettings(ctx, tenant).Topic(topic); !ok {
		return fmt.Errorf("the topic %q doesn't exist (Settings → Bounces & unsubscribes)", topic)
	}
	return nil
}

// newProviderLimits picks the limits a new provider starts with: explicit (sent with it) >
// preset (its kind's DefaultLimits, e.g. Gmail) > the workspace default for the channel.
func (s *Server) newProviderLimits(ctx context.Context, tenant int64, ch string, explicit, preset *model.ProviderLimits) model.ProviderLimits {
	return automation.ProviderLimits(explicit, preset, s.defaultLimitsFor(ctx, tenant, ch))
}
