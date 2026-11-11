package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/automation"
	"pulse/internal/live"
	"pulse/internal/maintenance"
	"pulse/internal/model"
)

// autoMem stores automation and data settings like pg.Store: the data settings' three retention
// values live in the automation settings, and a data-settings PUT merges them in.
type autoMem struct {
	*memStore
	auto     map[int64]automation.Settings
	tracking map[int64]model.TrackingSettings
	counts   map[string]int64
}

func (a *autoMem) AutomationSettings(_ context.Context, t int64) (automation.Settings, error) {
	if s, ok := a.auto[t]; ok {
		return s, nil
	}
	return automation.Defaults(), nil
}
func (a *autoMem) PutAutomationSettings(_ context.Context, t int64, s automation.Settings) error {
	a.auto[t] = s
	return nil
}
func (a *autoMem) CountLog(_ context.Context, _ int64, kind string, _ int) (int64, error) {
	return a.counts[kind], nil
}
func (a *autoMem) LastRetentionRun(context.Context, int64) (time.Time, error) {
	return time.Time{}, nil
}
func (a *autoMem) DataSettings(ctx context.Context, t int64) (model.DataSettings, error) {
	d := model.DefaultDataSettings()
	if tr, ok := a.tracking[t]; ok {
		d.Tracking = tr
	}
	s, _ := a.AutomationSettings(ctx, t)
	d.Retention = model.RetentionSettings{AnonymousDays: s.Retention.AnonymousDays, EventDays: s.Retention.EventDays, AuditDays: s.Retention.AuditDays}
	return d, nil
}
func (a *autoMem) PutDataSettings(ctx context.Context, t int64, d model.DataSettings) error {
	a.tracking[t] = d.Tracking
	s, _ := a.AutomationSettings(ctx, t)
	s.Retention.AnonymousDays, s.Retention.EventDays, s.Retention.AuditDays = d.Retention.AnonymousDays, d.Retention.EventDays, d.Retention.AuditDays
	a.auto[t] = s
	return nil
}

type autoCH struct {
	counted []automation.Delete
}

func (c *autoCH) CountOlder(_ context.Context, _ int64, d automation.Delete, _ time.Time) (uint64, error) {
	c.counted = append(c.counted, d)
	return 42, nil
}

type runNow struct{ tenants []int64 }

func (r *runNow) DeleteContacts(context.Context, int64, []uint32) error { return nil }
func (r *runNow) RunNow(_ context.Context, t int64) (maintenance.Report, error) {
	r.tenants = append(r.tenants, t)
	return maintenance.Report{Tenants: 1, Logs: 3}, nil
}

type analyticsWithCount struct {
	Analytics
	*autoCH
}

func autoServer(t *testing.T) (*Server, *autoMem, *autoCH, *runNow, *miniredis.Miniredis) {
	s, mem, _ := newServer()
	am := &autoMem{memStore: mem, auto: map[int64]automation.Settings{}, tracking: map[int64]model.TrackingSettings{},
		counts: map[string]int64{automation.PGAudit: 7}}
	ch := &autoCH{}
	rn := &runNow{}
	mr := miniredis.RunT(t)
	s.Store, s.Analytics, s.Deleter = am, analyticsWithCount{autoCH: ch}, rn
	s.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s.SweepInterval = 2 * time.Minute
	return s, am, ch, rn, mr
}

func TestAutomationGetAndPut(t *testing.T) {
	s, am, _, _, mr := autoServer(t)
	h := s.Handler()
	w := do(t, h, "GET", "/v1/settings/automation", "key-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	var got struct {
		Settings automation.Settings         `json:"settings"`
		Defaults automation.Settings         `json:"defaults"`
		Limits   map[string]automation.Limit `json:"limits"`
		Server   map[string]any              `json:"server"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Settings.Retries.Attempts != 8 || got.Defaults.Retention.AuditDays != 365 || got.Limits["retention.event_days"].Min != 30 ||
		got.Server["sweep_minutes"] != 2.0 {
		t.Errorf("get: %s", w.Body)
	}
	// a PUT replaces the sections sent and keeps the others
	am.auto[1] = func() automation.Settings { s := automation.Defaults(); s.Retries.Attempts = 3; return s }()
	w = do(t, h, "PUT", "/v1/settings/automation", "key-1",
		`{"retention":{"event_days":365,"event_overrides":{"page_view":90,"purchase":0},"live_feed_size":200},"schedules":{"sweep_minutes":15,"reach_rebuild_hour":2}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	cur := am.auto[1]
	if cur.Retention.EventDays != 365 || cur.Retention.EventOverrides["page_view"] != 90 || cur.Retention.AuditDays != 365 ||
		cur.Retries.Attempts != 3 || cur.Schedules.SweepMinutes != 15 || cur.Schedules.DailySegmentAt != "06:00" {
		t.Errorf("stored %+v", cur)
	}
	if v, _ := mr.Get(live.SizeKey(1)); v != "200" {
		t.Errorf("live feed size in Redis: %q", v)
	}
	// clear validation errors, nothing stored
	for body, want := range map[string]string{
		`{"retention":{"event_days":5}}`:                    "event_days must be 30 to 3650, or 0",
		`{"retries":{"attempts":50}}`:                       "attempts must be 1 to 20",
		`{"retention":{"event_overrides":{"a b":30}}}`:      "not an event name",
		`{"bogus":{}}`:                                      "unknown section",
		`{"default_limits":{"email":{"daily_quota":-5}}}`:   "daily_quota",
		`{"schedules":{"daily_segment_tz":"Nowhere/Land"}}`: "unknown time zone",
	} {
		w := do(t, h, "PUT", "/v1/settings/automation", "key-1", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if am.auto[1].Retention.EventDays != 365 {
		t.Error("an invalid PUT changed the settings")
	}
}

// The old Data & privacy PUT keeps working and doesn't wipe the new settings.
func TestDataSettingsPutKeepsAutomation(t *testing.T) {
	s, am, _, _, _ := autoServer(t)
	h := s.Handler()
	if w := do(t, h, "PUT", "/v1/settings/automation", "key-1", `{"retention":{"event_overrides":{"purchase":0},"sandbox_days":14},"retries":{"attempts":4,"max_backoff_seconds":600}}`); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	w := do(t, h, "PUT", "/v1/settings/data", "key-1", `{"tracking":{"anonymous":true,"geoip":false},"retention":{"anonymous_days":30,"event_days":365,"audit_days":90}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("data put: %d %s", w.Code, w.Body)
	}
	a := am.auto[1]
	if a.Retention.EventDays != 365 || a.Retention.AnonymousDays != 30 || a.Retention.AuditDays != 90 ||
		a.Retention.EventOverrides["purchase"] != 0 || len(a.Retention.EventOverrides) != 1 || a.Retention.SandboxDays != 14 || a.Retries.Attempts != 4 {
		t.Errorf("after the data PUT: %+v", a)
	}
	w = do(t, h, "GET", "/v1/settings/data", "key-1", "")
	if !strings.Contains(w.Body.String(), `"event_days":365`) {
		t.Errorf("data get: %s", w.Body)
	}
}

func TestAutomationPreview(t *testing.T) {
	s, am, ch, _, mr := autoServer(t)
	h := s.Handler()
	cur := automation.Defaults()
	cur.Retention.EventDays = 365
	am.auto[1] = cur
	body := `{"retention":{"event_days":365,"event_overrides":{"page_view":90},"audit_days":30}}`
	w := do(t, h, "POST", "/v1/settings/automation/preview", "key-1", body)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	var out struct {
		Items []PreviewItem `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Items) != 2 || out.Items[0].Key != "override:90" || out.Items[0].Rows != 42 || out.Items[0].OldDays != 365 ||
		out.Items[1].Key != automation.PGAudit || out.Items[1].Rows != 7 || out.Items[1].OldDays != 365 {
		t.Fatalf("items %s", w.Body)
	}
	if am.auto[1].Retention.AuditDays != 365 {
		t.Error("a preview must not save")
	}
	// the same preview again is cached; a different one right after is rate limited
	w = do(t, h, "POST", "/v1/settings/automation/preview", "key-1", body)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"cached":true`) || len(ch.counted) != 1 {
		t.Errorf("cached: %d %s (counted %d)", w.Code, w.Body, len(ch.counted))
	}
	w = do(t, h, "POST", "/v1/settings/automation/preview", "key-1", `{"retention":{"event_days":180}}`)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("rate limit: %d %s", w.Code, w.Body)
	}
	mr.FastForward(10 * time.Second)
	w = do(t, h, "POST", "/v1/settings/automation/preview", "key-1", `{"retention":{"event_days":180}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"key":"events"`) {
		t.Errorf("after the limit: %d %s", w.Code, w.Body)
	}
	// longer retention deletes nothing new: no counting at all
	n := len(ch.counted)
	w = do(t, h, "POST", "/v1/settings/automation/preview", "key-1", `{"retention":{"event_days":400}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items":[]`) || len(ch.counted) != n {
		t.Errorf("longer: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/settings/automation/preview", "key-1", `{"retention":{"event_days":3}}`); w.Code != http.StatusBadRequest {
		t.Errorf("invalid preview: %d", w.Code)
	}
}

func TestRunRetentionNow(t *testing.T) {
	s, _, _, rn, mr := autoServer(t)
	h := s.Handler()
	w := do(t, h, "POST", "/v1/settings/automation/run-retention", "key-2", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"logs":3`) || len(rn.tenants) != 1 || rn.tenants[0] != 2 {
		t.Fatalf("run: %d %s %v", w.Code, w.Body, rn.tenants)
	}
	if mr.Exists("autorun:2") {
		t.Error("lock left behind")
	}
	mr.Set("autorun:2", "1")
	if w := do(t, h, "POST", "/v1/settings/automation/run-retention", "key-2", ""); w.Code != http.StatusConflict {
		t.Errorf("concurrent run: %d", w.Code)
	}
}

func TestDefaultLimitsFor(t *testing.T) {
	s, am, _, _, _ := autoServer(t)
	a := automation.Defaults()
	a.DefaultLimits = map[string]model.ProviderLimits{"email": {DailyQuota: 2000, Timezone: "Asia/Kolkata"}}
	am.auto[1] = a
	ctx := context.Background()
	if got := s.defaultLimitsFor(ctx, 1, "email"); got.DailyQuota != 2000 {
		t.Errorf("email default %+v", got)
	}
	if got := s.defaultLimitsFor(ctx, 1, "sms"); got != (model.ProviderLimits{}) {
		t.Errorf("sms has none: %+v", got)
	}
	preset := &model.ProviderLimits{DailyQuota: 500}
	if got := s.newProviderLimits(ctx, 1, "email", nil, preset); got.DailyQuota != 500 {
		t.Errorf("a kind preset wins over the workspace default: %+v", got)
	}
	if got := s.newProviderLimits(ctx, 1, "email", nil, nil); got.DailyQuota != 2000 {
		t.Errorf("workspace default: %+v", got)
	}
}
