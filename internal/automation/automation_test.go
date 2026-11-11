package automation

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

func ip(n int) *int { return &n }

// The defaults are exactly what Pulse did before the settings existed.
func TestDefaultsAreTodaysBehaviour(t *testing.T) {
	d := Defaults()
	r := d.Retention
	if r.EventDays != 0 || r.MessageEvents() != 0 || r.WebViews() != 0 || r.AnonymousDays != 0 || r.ImportDays != 0 || r.SandboxDays != 0 {
		t.Errorf("events, message events, views, anonymous visitors, imports and the sandbox are kept forever: %+v", r)
	}
	if r.AuditDays != 365 || r.WebhookDeliveryDays != 7 || r.IntegrationLogDays != 7 || r.LiveFeedSize != 1000 {
		t.Errorf("audit 365 days, webhook and integration logs a week, live feed 1000: %+v", r)
	}
	if d.Retries.Attempts != 8 || d.Retries.MaxBackoff() != time.Hour {
		t.Errorf("retries %+v", d.Retries)
	}
	if !reflect.DeepEqual(d.Retries.Schedule(), BaseBackoff) {
		t.Errorf("default schedule %v, want %v", d.Retries.Schedule(), BaseBackoff)
	}
	if d.Schedules.SweepMinutes != 0 || d.Schedules.ReachRebuildHour != -1 || d.Schedules.DailySegmentAt != "06:00" || d.Schedules.DailySegmentTZ != "" {
		t.Errorf("schedules %+v", d.Schedules)
	}
	if d.Messages.InboxExpiryDays != 30 || d.DefaultLimits != nil {
		t.Errorf("messages %+v, limits %v", d.Messages, d.DefaultLimits)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}
	if len(r.CHPlan()) != 0 {
		t.Errorf("no ClickHouse deletes by default: %+v", r.CHPlan())
	}
	if got := r.PGPlan(); len(got) != 3 || got[0].Key != PGAudit || got[1].Key != PGWebhookDeliveries || got[2].Key != PGIntegrationLog {
		t.Errorf("Postgres plan %+v", got)
	}
	// stored JSON from before a field existed keeps that field's default
	s, err := Load([]byte(`{"retention":{"event_days":90}}`))
	if err != nil || s.Retention.EventDays != 90 || s.Retention.AuditDays != 365 || s.Retries.Attempts != 8 {
		t.Errorf("load %+v %v", s, err)
	}
	if s, _ := Load(nil); !reflect.DeepEqual(s, Defaults()) {
		t.Errorf("empty load %+v", s)
	}
}

func TestValidate(t *testing.T) {
	bad := map[string]func(s *Settings){
		"event_days must be 30 to 3650":         func(s *Settings) { s.Retention.EventDays = 10 },
		"message_event_days must be 30":         func(s *Settings) { s.Retention.MessageEventDays = ip(5) },
		"web_view_days must be 7":               func(s *Settings) { s.Retention.WebViewDays = ip(4000) },
		"audit_days":                            func(s *Settings) { s.Retention.AuditDays = 29 },
		"webhook_delivery_days must be 1 to 90": func(s *Settings) { s.Retention.WebhookDeliveryDays = 0 },
		"integration_log_days":                  func(s *Settings) { s.Retention.IntegrationLogDays = 91 },
		"live_feed_size must be 100 to 10000":   func(s *Settings) { s.Retention.LiveFeedSize = 50 },
		"attempts must be 1 to 20":              func(s *Settings) { s.Retries.Attempts = 0 },
		"max_backoff_seconds":                   func(s *Settings) { s.Retries.MaxBackoffSeconds = 10 },
		"sweep_minutes":                         func(s *Settings) { s.Schedules.SweepMinutes = 2000 },
		"reach_rebuild_hour":                    func(s *Settings) { s.Schedules.ReachRebuildHour = 24 },
		"daily_segment_at":                      func(s *Settings) { s.Schedules.DailySegmentAt = "6am" },
		"unknown time zone":                     func(s *Settings) { s.Schedules.DailySegmentTZ = "Mars/Base" },
		"inbox_expiry_days":                     func(s *Settings) { s.Messages.InboxExpiryDays = 0 },
		"not an event name":                     func(s *Settings) { s.Retention.EventOverrides = map[string]int{"bad name": 30} },
		"event_overrides[page_view]":            func(s *Settings) { s.Retention.EventOverrides = map[string]int{"page_view": 3} },
		"not a channel with providers":          func(s *Settings) { s.DefaultLimits = map[string]model.ProviderLimits{"inbox": {DailyQuota: 5}} },
		"daily_quota must be":                   func(s *Settings) { s.DefaultLimits = map[string]model.ProviderLimits{"email": {DailyQuota: -1}} },
		"default_limits[sms]: unknown time zone": func(s *Settings) {
			s.DefaultLimits = map[string]model.ProviderLimits{"sms": {DailyQuota: 5, Timezone: "Nowhere"}}
		},
	}
	for want, f := range bad {
		s := Defaults()
		f(&s)
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	s := Defaults()
	s.Retention.EventDays, s.Retention.AnonymousDays, s.Retention.SandboxDays = 0, 30, 14
	s.Retention.EventOverrides = map[string]int{"page_view": 90, "purchase": 0}
	s.Schedules.SweepMinutes, s.Schedules.ReachRebuildHour, s.Schedules.DailySegmentTZ = 15, 3, "Asia/Kolkata"
	s.DefaultLimits = map[string]model.ProviderLimits{"email": {RatePerSecond: 10, DailyQuota: 500, Timezone: "Asia/Kolkata"}, "sms": {}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.DefaultLimits["sms"]; ok {
		t.Error("an empty channel default is dropped")
	}
	too := Defaults()
	too.Retention.EventOverrides = map[string]int{}
	for i := range MaxOverrides + 1 {
		too.Retention.EventOverrides["e"+strconv.Itoa(i)] = 30
	}
	if err := too.Validate(); err == nil {
		t.Error("too many overrides")
	}
}

// A PUT replaces the sections it sends (fields left out take defaults) and keeps the others.
func TestApply(t *testing.T) {
	cur := Defaults()
	cur.Retention.EventDays = 365
	cur.Retention.EventOverrides = map[string]int{"page_view": 30}
	cur.Retries.Attempts = 3
	next, err := Apply(cur, []byte(`{"retention":{"event_days":90,"event_overrides":{"purchase":0}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if next.Retention.EventDays != 90 || !reflect.DeepEqual(next.Retention.EventOverrides, map[string]int{"purchase": 0}) ||
		next.Retention.AuditDays != 365 || next.Retries.Attempts != 3 {
		t.Errorf("next %+v", next)
	}
	if cur.Retention.EventOverrides["page_view"] != 30 {
		t.Error("Apply changed the current settings")
	}
	for _, body := range []string{`[]`, `{"nope":{}}`, `{"retries":{"tries":3}}`, `{"retries":"x"}`} {
		if _, err := Apply(cur, []byte(body)); err == nil {
			t.Errorf("%s: want an error", body)
		}
	}
}

func names(ds []Delete) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Key)
	}
	return out
}

// Per-name overrides win over the type-wide settings, and every name is in exactly one delete.
func TestCHPlan(t *testing.T) {
	r := Defaults().Retention
	r.EventDays = 365
	r.EventOverrides = map[string]int{"page_view": 90, "purchase": 0, "click": 90, "login": 30}
	plan := r.CHPlan()
	if got := names(plan); !reflect.DeepEqual(got, []string{"override:30", "override:90", "events", "message_events"}) {
		t.Fatalf("plan %v", got)
	}
	if !reflect.DeepEqual(plan[1].Names, []string{"click", "page_view"}) || plan[1].NotIn || plan[1].Days != 90 {
		t.Errorf("override group %+v", plan[1])
	}
	ev := plan[2]
	if !ev.NotIn || !reflect.DeepEqual(ev.Names, []string{"click", "login", "page_view", "purchase"}) || ev.Days != 365 {
		t.Errorf("type-wide delete must skip every overridden name, purchase (forever) included: %+v", ev)
	}
	if plan[3].Table != TableMessageEvents || plan[3].Days != 365 || len(plan[3].Names) != 0 {
		t.Errorf("message events follow events by default: %+v", plan[3])
	}
	// views with their own setting: their own delete, excluded from the type-wide one
	r.WebViewDays = ip(30)
	r.MessageEventDays = ip(0)
	plan = r.CHPlan()
	if got := names(plan); !reflect.DeepEqual(got, []string{"override:30", "override:90", "web_views", "events"}) {
		t.Fatalf("plan %v", got)
	}
	if !reflect.DeepEqual(plan[2].Names, []string{"screen_view", "landing_page_viewed"}) {
		t.Errorf("views without an override: %v", plan[2].Names)
	}
	for _, n := range []string{"screen_view", "landing_page_viewed", "page_view"} {
		if !slices.Contains(plan[3].Names, n) {
			t.Errorf("%s must be excluded from the type-wide delete", n)
		}
	}
	// events kept forever, views not
	r = Defaults().Retention
	r.WebViewDays = ip(60)
	if got := r.CHPlan(); len(got) != 1 || got[0].Key != "web_views" || got[0].Days != 60 {
		t.Errorf("only views: %+v", got)
	}
	// every name is deleted by at most one delete, with its own days
	r = Defaults().Retention
	r.EventDays, r.WebViewDays = 400, ip(100)
	r.EventOverrides = map[string]int{"screen_view": 0, "a": 50}
	for _, n := range []string{"a", "b", "page_view", "screen_view", "landing_page_viewed"} {
		var hits []int
		for _, d := range r.CHPlan() {
			if d.Table != TableEvents {
				continue
			}
			if in := slices.Contains(d.Names, n); in != d.NotIn {
				hits = append(hits, d.Days)
			}
		}
		want := r.EventDaysFor(n)
		if want == 0 && len(hits) != 0 || want > 0 && (len(hits) != 1 || hits[0] != want) {
			t.Errorf("%s: deleted by %v, want %d days", n, hits, want)
		}
	}
}

func TestShortened(t *testing.T) {
	cur := Defaults().Retention
	cur.EventDays = 365
	cur.EventOverrides = map[string]int{"purchase": 0}
	next := cur
	if got := Shortened(cur, next); len(got) != 0 {
		t.Errorf("unchanged: %v", names(got))
	}
	next.EventOverrides = map[string]int{"purchase": 0, "page_view": 90}
	if got := names(Shortened(cur, next)); !reflect.DeepEqual(got, []string{"override:90"}) {
		t.Errorf("a new shorter override: %v", got)
	}
	// longer is not shorter
	next = cur
	next.EventDays = 400
	if got := Shortened(cur, next); len(got) != 0 {
		t.Errorf("longer retention: %v", names(got))
	}
	// dropping the forever override of purchase puts it under 365 days: the type-wide delete counts
	next = cur
	next.EventOverrides = nil
	if got := names(Shortened(cur, next)); !reflect.DeepEqual(got, []string{"events"}) {
		t.Errorf("removed override: %v", got)
	}
	// Postgres logs and anonymous visitors
	next = cur
	next.AuditDays, next.ImportDays, next.WebhookDeliveryDays, next.AnonymousDays = 90, 30, 14, 30
	if got := names(Shortened(cur, next)); !reflect.DeepEqual(got, []string{PGAudit, PGImports, PGAnonymous}) {
		t.Errorf("postgres: %v", got)
	}
	// message events turned on
	cur = Defaults().Retention
	next = cur
	next.MessageEventDays = ip(180)
	if got := names(Shortened(cur, next)); !reflect.DeepEqual(got, []string{"message_events"}) {
		t.Errorf("message events: %v", got)
	}
}

func TestRetentionDue(t *testing.T) {
	now := time.Date(2026, 11, 11, 5, 10, 0, 0, time.UTC)
	if !RetentionDue(7, time.Time{}, now) {
		t.Error("never ran: due")
	}
	if RetentionDue(7, now.Add(-2*time.Hour), now) {
		t.Error("ran 2 hours ago: not due")
	}
	if !RetentionDue(5, now.Add(-24*time.Hour), now) || RetentionDue(6, now.Add(-24*time.Hour), now) {
		t.Error("a day later, only in the workspace's own hour")
	}
	if !RetentionDue(6, now.Add(-48*time.Hour), now) {
		t.Error("missed its hour: due anyway")
	}
	if RetentionSlot(29) != 5 {
		t.Error("slot")
	}
}

func TestRetrySchedule(t *testing.T) {
	m := time.Minute
	cases := []struct {
		r    Retries
		want []time.Duration
	}{
		{Retries{Attempts: 1, MaxBackoffSeconds: 3600}, []time.Duration{}},
		{Retries{Attempts: 4, MaxBackoffSeconds: 3600}, []time.Duration{30 * time.Second, m, 2 * m}},
		{Retries{Attempts: 8, MaxBackoffSeconds: 300}, []time.Duration{30 * time.Second, m, 2 * m, 5 * m, 5 * m, 5 * m, 5 * m}},
		{Retries{Attempts: 11, MaxBackoffSeconds: 4 * 3600}, append(slices.Clone(BaseBackoff), 2*time.Hour, 4*time.Hour, 4*time.Hour)},
	}
	for _, c := range cases {
		if got := c.r.Schedule(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: %v, want %v", c.r, got, c.want)
		}
	}
}

func TestNextRebuild(t *testing.T) {
	now := time.Date(2026, 11, 11, 5, 30, 0, 0, time.UTC)
	if got := (Schedules{ReachRebuildHour: -1}).NextRebuild(now); got != now.Add(24*time.Hour) {
		t.Errorf("default: a day later, got %v", got)
	}
	if got := (Schedules{ReachRebuildHour: 3}).NextRebuild(now); got != time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC) {
		t.Errorf("3 UTC: %v", got)
	}
	if got := (Schedules{ReachRebuildHour: 6}).NextRebuild(now); got != time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC) {
		t.Errorf("less than an hour away: the next day, got %v", got)
	}
	if got := (Schedules{ReachRebuildHour: 9}).NextRebuild(now); got != time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) {
		t.Errorf("later today: %v", got)
	}
}

func TestProviderLimitsPrecedence(t *testing.T) {
	ws := model.ProviderLimits{DailyQuota: 1000}
	preset := &model.ProviderLimits{DailyQuota: 500, Timezone: "UTC"}
	own := &model.ProviderLimits{RatePerSecond: 5}
	if got := ProviderLimits(own, preset, ws); got != *own {
		t.Errorf("explicit wins: %+v", got)
	}
	if got := ProviderLimits(&model.ProviderLimits{}, preset, ws); got != *preset {
		t.Errorf("then the preset: %+v", got)
	}
	if got := ProviderLimits(nil, nil, ws); got != ws {
		t.Errorf("then the workspace: %+v", got)
	}
}

func TestJSONShape(t *testing.T) {
	b, _ := json.Marshal(Defaults())
	for _, k := range []string{`"message_event_days":null`, `"web_view_days":null`, `"attempts":8`, `"reach_rebuild_hour":-1`, `"inbox_expiry_days":30`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("missing %s in %s", k, b)
		}
	}
}
