package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// overviewStore adds the summary's Postgres queries and the providers to the test store.
type overviewStore struct {
	*memStore
	builds    atomic.Int32
	block     chan struct{} // when set, NewContacts waits for it
	failNew   bool
	providers []model.Provider
	since     time.Time
}

func (o *overviewStore) NewContacts(_ context.Context, tenant int64, today, weekFrom, prevFrom time.Time) (pg.NewContactCounts, error) {
	o.builds.Add(1)
	if o.block != nil {
		<-o.block
	}
	if o.failNew {
		return pg.NewContactCounts{}, errors.New("postgres is down")
	}
	if tenant != 1 || weekFrom != today.AddDate(0, 0, -6) || prevFrom != today.AddDate(0, 0, -13) {
		return pg.NewContactCounts{}, errors.New("wrong workspace or days")
	}
	return pg.NewContactCounts{Today: 4, Week: 30, PrevWeek: 20}, nil
}

func (o *overviewStore) OverviewBroadcasts(_ context.Context, _ int64, since time.Time, limit int) (pg.OverviewBroadcasts, error) {
	o.since = since
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	return pg.OverviewBroadcasts{
		Sending: []pg.BroadcastBrief{{ID: 7, Name: "Flash sale", Channel: "email", Status: "running", At: at, Run: at.Unix()}},
		Next:    []pg.BroadcastBrief{{ID: 8, Name: "Weekly digest", Channel: "email", Status: "scheduled", Repeat: "weekly", At: at.Add(48 * time.Hour)}},
		Recent:  []pg.BroadcastBrief{{ID: 6, Name: "Spring catalogue", Channel: "sms", Status: "completed", At: at.Add(-72 * time.Hour), Run: 1000}},
	}, nil
}

func (o *overviewStore) ListProviders(context.Context, int64) ([]model.Provider, error) {
	return o.providers, nil
}

// overviewAnalytics answers the summary's ClickHouse queries (other Analytics methods panic).
type overviewAnalytics struct {
	Analytics
	health map[string]ch.SendCounts
	runs   map[ch.RunKey]model.MessageCounts
	orders uint64
}

func (overviewAnalytics) Unsubscribes(context.Context, int64, time.Time, time.Time) (uint64, uint64, error) {
	return 3, 5, nil
}
func (f overviewAnalytics) ChannelHealth(context.Context, int64, time.Time, time.Time, time.Time) (map[string]ch.SendCounts, error) {
	return f.health, nil
}
func (f overviewAnalytics) RunCounts(_ context.Context, _ int64, runs []ch.RunKey) (map[ch.RunKey]model.MessageCounts, error) {
	out := map[ch.RunKey]model.MessageCounts{}
	for _, r := range runs {
		if c, ok := f.runs[r]; ok {
			out[r] = c
		}
	}
	return out, nil
}
func (f overviewAnalytics) RevenueTotals(_ context.Context, q segment.Query) (uint64, float64, uint64, error) {
	if !strings.Contains(q.SQL, "sum(amount)") {
		return 0, 0, 0, errors.New("not a revenue query")
	}
	return f.orders, float64(f.orders) * 499.999, f.orders, nil
}

func newOverviewServer(t *testing.T) (*Server, *overviewStore, *miniredis.Miniredis) {
	t.Helper()
	s, mem, _ := newServer()
	st := &overviewStore{memStore: mem}
	s.Store = st
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.Analytics = overviewAnalytics{
		health: map[string]ch.SendCounts{"email_sent": {Hour: 900, Today: 5000, Total: 40000}, "email_failed": {Hour: 100, Today: 120, Total: 200}},
		runs:   map[ch.RunKey]model.MessageCounts{{Broadcast: 7, Run: at.Unix()}: {Sent: 1000, Opened: 250}, {Broadcast: 6, Run: 1000}: {Sent: 400, Failed: 100, Clicked: 40}},
		orders: 3,
	}
	s.now = func() time.Time { return time.Date(2026, 11, 14, 12, 30, 0, 0, time.UTC) }
	mr := miniredis.RunT(t)
	s.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return s, st, mr
}

func summaryAs(t *testing.T, s *Server, perms auth.Perms) (OverviewSummary, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	s.overviewSummary(w, httptest.NewRequest("GET", "/v1/stats/summary", nil), model.Principal{TenantID: 1, Perms: perms})
	if w.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", w.Code, w.Body)
	}
	var sum OverviewSummary
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	return sum, raw
}

// The API key (an admin) gets every section; the days are UTC and the warnings come out right.
func TestOverviewSummaryEverything(t *testing.T) {
	s, st, _ := newOverviewServer(t)
	st.providers = []model.Provider{
		{ID: 1, Name: "Gmail", Channel: "email", Limits: model.ProviderLimits{DailyQuota: 500}},
		{ID: 2, Name: "Twilio", Channel: "sms"}, // no quota: never a warning
	}
	s.RDB.Set(context.Background(), "quota:1:20261114", 500, 0) // used up today

	w := do(t, s.Handler(), "GET", "/v1/stats/summary", "key-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", w.Code, w.Body)
	}
	var sum OverviewSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Today != "2026-11-14" || sum.WeekFrom != "2026-11-08" || sum.PrevFrom != "2026-11-01" {
		t.Errorf("days: %s %s %s", sum.Today, sum.WeekFrom, sum.PrevFrom)
	}
	if a := sum.Audience; a == nil || a.New == nil || a.New.Week != 30 || a.Unsubscribed == nil || a.Unsubscribed.Week != 3 {
		t.Errorf("audience: %+v", a)
	}
	c := sum.Campaigns
	if c == nil || len(c.Sending) != 1 || len(c.Next) != 1 || len(c.Recent) != 1 {
		t.Fatalf("campaigns: %+v", c)
	}
	if c.Sending[0].Counts == nil || c.Sending[0].Counts.Opened != 250 || c.Next[0].Counts != nil || c.Recent[0].Counts.Clicked != 40 {
		t.Errorf("campaign counts: %+v", c)
	}
	if st.since != time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("recent since %s", st.since)
	}
	if r := sum.Revenue; r == nil || r.Week.Orders != 3 || r.Week.Revenue != 1500 || r.Event != "purchase" {
		t.Errorf("revenue: %+v", r)
	}
	kinds := []string{}
	for _, a := range sum.Attention {
		kinds = append(kinds, a.Kind+"/"+a.Level)
		if a.Needs != nil {
			t.Errorf("needs leaked: %+v", a)
		}
	}
	// Problems first: the full quota and the failing email channel, then the campaign.
	if strings.Join(kinds, ",") != "quota/problem,failing/problem,campaign_failed/warning" {
		t.Errorf("attention: %v", kinds)
	}
	if !strings.Contains(sum.Attention[0].Text, "Gmail has used its daily quota of 500 messages") ||
		!strings.Contains(sum.Attention[1].Text, "100 of 1,000 email messages failed in the last hour (10%)") ||
		!strings.Contains(sum.Attention[2].Text, "“Spring catalogue”: 100 of 500 messages failed (20%)") || sum.Attention[2].Link != "/broadcasts/6" {
		t.Errorf("texts: %+v", sum.Attention)
	}
	if len(sum.Unavailable) != 0 {
		t.Errorf("unavailable: %v", sum.Unavailable)
	}
}

// Each role gets only the sections it may view, from the same cached summary.
func TestOverviewSummaryByRole(t *testing.T) {
	s, st, _ := newOverviewServer(t)
	st.providers = []model.Provider{{ID: 1, Name: "Gmail", Channel: "email", Limits: model.ProviderLimits{DailyQuota: 500}}}
	s.RDB.Set(context.Background(), "quota:1:20261114", 470, 0) // 94%: a warning

	sum, raw := summaryAs(t, s, auth.Perms{auth.AreaContacts: auth.View})
	if sum.Audience == nil || raw["campaigns"] != nil || raw["revenue"] != nil || len(sum.Attention) != 0 {
		t.Errorf("contacts only: %v", raw)
	}
	sum, raw = summaryAs(t, s, auth.Perms{auth.AreaBroadcasts: auth.View})
	if raw["audience"] != nil || raw["revenue"] != nil || sum.Campaigns == nil || sum.Campaigns.Recent != nil || len(sum.Campaigns.Sending) != 1 {
		t.Errorf("one-time campaigns only: %v", raw)
	}
	if len(sum.Attention) != 1 || sum.Attention[0].Kind != "campaign_failed" {
		t.Errorf("one-time campaigns only, warnings: %+v", sum.Attention)
	}
	sum, raw = summaryAs(t, s, auth.Perms{auth.AreaReports: auth.View})
	if raw["audience"] != nil || raw["campaigns"] != nil || sum.Revenue == nil || len(sum.Attention) != 0 {
		t.Errorf("reports only: %v", raw)
	}
	sum, _ = summaryAs(t, s, auth.Perms{auth.AreaChannels: auth.View})
	if len(sum.Attention) != 2 || sum.Attention[0].Kind != "failing" || sum.Attention[1].Kind != "quota" || sum.Attention[1].Level != "warning" ||
		!strings.Contains(sum.Attention[1].Text, "used 470 of its daily quota of 500") {
		t.Errorf("channels only: %+v", sum.Attention)
	}
	sum, raw = summaryAs(t, s, auth.Perms{auth.AreaBroadcasts: auth.View, auth.AreaReports: auth.View})
	if sum.Campaigns == nil || len(sum.Campaigns.Recent) != 1 {
		t.Errorf("campaigns and reports: %v", raw)
	}
	if st.builds.Load() != 1 {
		t.Errorf("built %d times, want once for every role", st.builds.Load())
	}
}

// The summary is cached a minute per workspace; a summary with a section missing for 15 seconds.
func TestOverviewSummaryCache(t *testing.T) {
	s, st, mr := newOverviewServer(t)
	all := auth.Perms{auth.AreaContacts: auth.View}
	summaryAs(t, s, all)
	summaryAs(t, s, all)
	if st.builds.Load() != 1 {
		t.Fatalf("builds %d", st.builds.Load())
	}
	mr.FastForward(61 * time.Second)
	summaryAs(t, s, all)
	if st.builds.Load() != 2 {
		t.Fatalf("after a minute: builds %d", st.builds.Load())
	}

	mr.FastForward(61 * time.Second)
	st.failNew = true
	sum, _ := summaryAs(t, s, all)
	if len(sum.Unavailable) != 1 || sum.Unavailable[0] != "audience" || sum.Audience == nil || sum.Audience.New != nil || sum.Audience.Unsubscribed == nil {
		t.Errorf("a failed section: %+v %+v", sum.Unavailable, sum.Audience)
	}
	st.failNew = false
	mr.FastForward(16 * time.Second)
	sum, _ = summaryAs(t, s, all)
	if len(sum.Unavailable) != 0 || sum.Audience.New == nil {
		t.Errorf("retried after 15 s: %+v", sum.Unavailable)
	}
}

// Uncached summaries are limited per workspace; concurrent misses share one build.
func TestOverviewSummaryLimits(t *testing.T) {
	s, st, _ := newOverviewServer(t)
	st.block = make(chan struct{})
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			s.overviewSummary(w, httptest.NewRequest("GET", "/v1/stats/summary", nil), model.Principal{TenantID: 1})
			if w.Code != http.StatusOK {
				t.Errorf("summary: %d %s", w.Code, w.Body)
			}
		}()
	}
	for st.builds.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // the others are waiting for the first
	close(st.block)
	wg.Wait()
	if st.builds.Load() != 1 {
		t.Errorf("concurrent misses built %d times", st.builds.Load())
	}

	ctx := context.Background()
	s.RDB.Del(ctx, "dash:1:summary")
	for s.allowTenant(ctx, "rlsum", 1, summariesPerMin) {
	}
	w := httptest.NewRecorder()
	s.overviewSummary(w, httptest.NewRequest("GET", "/v1/stats/summary", nil), model.Principal{TenantID: 1, Perms: auth.Perms{}})
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("over the limit: %d %s", w.Code, w.Body)
	}
}

// Without ClickHouse the summary still has the contacts added and the campaign lists.
func TestOverviewSummaryWithoutAnalytics(t *testing.T) {
	s, _, _ := newOverviewServer(t)
	s.Analytics = nil
	sum, raw := summaryAs(t, s, auth.Perms{auth.AreaContacts: auth.View, auth.AreaBroadcasts: auth.View, auth.AreaReports: auth.View})
	if sum.Audience == nil || sum.Audience.New == nil || sum.Audience.Unsubscribed != nil || raw["revenue"] != nil {
		t.Errorf("audience: %v", raw)
	}
	if sum.Campaigns == nil || sum.Campaigns.Recent[0].Counts != nil {
		t.Errorf("campaigns: %+v", sum.Campaigns)
	}
}

func TestAttentionThresholds(t *testing.T) {
	// Healthy: few failures, a normal bounce day, a quota far from full.
	healthy := attentionItems(
		[]model.Provider{{Name: "Gmail", Channel: "email", Limits: model.ProviderLimits{DailyQuota: 1000}, UsageToday: 899}},
		map[string]ch.SendCounts{"sms_sent": {Hour: 100}, "sms_failed": {Hour: 19}, "email_bounced": {Today: 60, Total: 60 + 7*30}},
		&CampaignSummary{Recent: []CampaignBrief{{BroadcastBrief: pg.BroadcastBrief{ID: 1, Name: "A"}, Counts: &model.MessageCounts{Sent: 1000, Failed: 50}}}},
	)
	if len(healthy) != 0 {
		t.Errorf("healthy: %+v", healthy)
	}
	got := attentionItems(nil, map[string]ch.SendCounts{"email_bounced": {Today: 90, Total: 90 + 7*20}}, nil)
	if len(got) != 1 || got[0].Kind != "bounces" || got[0].Text != "90 emails bounced today, against about 20 a day last week." {
		t.Errorf("bounce spike: %+v", got)
	}
	// A new workspace's first bounces: below the minimum, no spike.
	if got := attentionItems(nil, map[string]ch.SendCounts{"email_bounced": {Today: 49, Total: 49}}, nil); len(got) != 0 {
		t.Errorf("few bounces: %+v", got)
	}
	got = attentionItems([]model.Provider{{Channel: "sms", Limits: model.ProviderLimits{DailyQuota: 10}, UsageToday: 12}}, nil, nil)
	if len(got) != 1 || !strings.HasPrefix(got[0].Text, "The SMS provider has used its daily quota of 10 messages.") {
		t.Errorf("unnamed provider: %+v", got)
	}
}

func TestNum(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := num(n); got != want {
			t.Errorf("num(%d) = %s, want %s", n, got, want)
		}
	}
	if pct(1, 3) != "33%" || pct(0, 0) != "0%" {
		t.Error("pct")
	}
}
