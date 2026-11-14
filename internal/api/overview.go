package api

// The Overview page's summary, GET /v1/stats/summary: what a marketer looks at first, next to the
// live numbers of GET /v1/stats/overview (which stays small and fast, polled every 5 s). The
// summary is the slower part, so it is its own request: the page shows the live numbers first and
// fills the summary in when it arrives, polling it once a minute.
//
// Cost: every query reads an index range of one workspace (contacts added: the contacts_created
// index; broadcasts by status; a few event names in ClickHouse's events sort key; a few broadcast
// runs in message_events). The whole summary is built once a minute per workspace and kept in
// Redis: many people opening the page share it, concurrent misses on one server wait for the
// first, and misses are limited per workspace. Sections are built in parallel; one that fails is
// left out (named in "unavailable") instead of failing the page.
//
// Permissions: everyone signed in may call it, and each caller gets only the sections their role
// can view (summaryFor): new contacts and unsubscribes need contacts view; campaigns sending and
// coming up need one-time campaigns view (outcome counts, beyond "sent so far", also reports view);
// recent results need that and reports view; revenue needs reports view; each warning names the
// areas that may see it (a campaign's failures: one-time campaigns and reports view).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/dispatch"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

const (
	summaryCacheTTL  = time.Minute
	summaryRetryTTL  = 15 * time.Second // a summary with a section missing is retried sooner
	summariesPerMin  = 20               // uncached summaries per workspace and minute
	summaryList      = 5                // campaigns per list
	summaryRecent    = 30               // days a finished campaign stays under "Recently sent"
	summaryBuildTime = 10 * time.Second

	// Warnings ("Needs attention").
	warnFailedMin     = 20   // failed messages before a failure share counts
	warnFailedShare   = 0.10 // of sent + failed
	warnBounceMin     = 50   // email bounces today before a spike counts
	warnBounceSpike   = 3.0  // times the daily average of the 7 days before
	warnQuotaNearUsed = 0.9  // share of a provider's daily quota
)

// OverviewStore is the Postgres side of the summary.
type OverviewStore interface {
	NewContacts(ctx context.Context, tenant int64, today, weekFrom, prevFrom time.Time) (pg.NewContactCounts, error)
	OverviewBroadcasts(ctx context.Context, tenant int64, since time.Time, limit int) (pg.OverviewBroadcasts, error)
}

// OverviewAnalytics is the ClickHouse side of the summary.
type OverviewAnalytics interface {
	Unsubscribes(ctx context.Context, tenant int64, prevFrom, weekFrom time.Time) (week, prev uint64, err error)
	ChannelHealth(ctx context.Context, tenant int64, hourAgo, today, since time.Time) (map[string]ch.SendCounts, error)
	RunCounts(ctx context.Context, tenant int64, runs []ch.RunKey) (map[ch.RunKey]model.MessageCounts, error)
}

type revenueTotaler interface {
	RevenueTotals(ctx context.Context, q segment.Query) (orders uint64, revenue float64, buyers uint64, err error)
}

type providerLister interface {
	ListProviders(ctx context.Context, tenant int64) ([]model.Provider, error)
}

// OverviewSummary is the answer of GET /v1/stats/summary. Days are UTC: the last 7 days are
// week_from to today, the 7 before them prev_from to the day before week_from.
type OverviewSummary struct {
	ComputedAt time.Time        `json:"computed_at"`
	Today      string           `json:"today"`
	WeekFrom   string           `json:"week_from"`
	PrevFrom   string           `json:"prev_from"`
	Audience   *AudienceSummary `json:"audience,omitempty"`
	Campaigns  *CampaignSummary `json:"campaigns,omitempty"`
	Revenue    *RevenueSummary  `json:"revenue,omitempty"`
	Attention  []Attention      `json:"attention"`
	// Unavailable names sections that couldn't be read this time (audience, unsubscribes,
	// campaigns, revenue, health, providers).
	Unavailable []string `json:"unavailable,omitempty"`
}

// AudienceSummary: contacts added, and contacts who unsubscribed (nil without ClickHouse); either
// is nil when it couldn't be read.
type AudienceSummary struct {
	New          *pg.NewContactCounts `json:"new,omitempty"`
	Unsubscribed *WeekCounts          `json:"unsubscribed,omitempty"`
}

// WeekCounts compare the last 7 days with the 7 before.
type WeekCounts struct {
	Week uint64 `json:"week"`
	Prev uint64 `json:"prev"`
}

// CampaignSummary lists one-time campaigns. Counts are those of the run shown (unique contacts).
type CampaignSummary struct {
	Sending []CampaignBrief `json:"sending"`
	Next    []CampaignBrief `json:"next"`
	Recent  []CampaignBrief `json:"recent,omitempty"`
}

type CampaignBrief struct {
	pg.BroadcastBrief
	Counts *model.MessageCounts `json:"counts,omitempty"`
}

// RevenueSummary: revenue events (Settings of the Revenue page) in the last 7 days and the 7 before.
// Left out when there were none in either.
type RevenueSummary struct {
	Event    string        `json:"event"`
	Currency string        `json:"currency"`
	Week     RevenueTotals `json:"week"`
	Prev     RevenueTotals `json:"prev"`
}

type RevenueTotals struct {
	Orders  uint64  `json:"orders"`
	Revenue float64 `json:"revenue"`
	Buyers  uint64  `json:"buyers"`
}

// Attention is one "Needs attention" warning. Needs lists the areas whose view access shows it
// (any one of them), Requires areas whose view access it needs as well (all of them); both stay in
// the cache and are removed from answers.
type Attention struct {
	Kind     string   `json:"kind"`  // quota | failing | bounces | campaign_failed
	Level    string   `json:"level"` // problem | warning
	Text     string   `json:"text"`
	Link     string   `json:"link,omitempty"`
	LinkText string   `json:"link_text,omitempty"`
	Needs    []string `json:"needs,omitempty"`
	Requires []string `json:"requires,omitempty"`
}

var errSummaryBusy = errors.New("too many overview requests; try again in a minute")

// overviewSummary: GET /v1/stats/summary.
func (s *Server) overviewSummary(w http.ResponseWriter, r *http.Request, p model.Principal) {
	full, err := s.cachedSummary(r.Context(), p.TenantID)
	if errors.Is(err, errSummaryBusy) {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summaryFor(p.Perms, full))
}

// summaryFlight lets concurrent misses for one workspace on this server share one build.
var summaryFlight = struct {
	sync.Mutex
	calls map[int64]*summaryCall
}{calls: map[int64]*summaryCall{}}

type summaryCall struct {
	done chan struct{}
	b    []byte
	err  error
}

// cachedSummary returns the workspace's whole summary (every section), from Redis when it is
// there, else built (once per server at a time, limited per workspace) and stored.
func (s *Server) cachedSummary(ctx context.Context, tenant int64) (OverviewSummary, error) {
	key := "dash:" + strconv.FormatInt(tenant, 10) + ":summary"
	var out OverviewSummary
	if s.RDB != nil {
		if b, err := s.RDB.Get(ctx, key).Bytes(); err == nil && json.Unmarshal(b, &out) == nil {
			return out, nil
		}
	}
	summaryFlight.Lock()
	c, waiting := summaryFlight.calls[tenant]
	if !waiting {
		c = &summaryCall{done: make(chan struct{})}
		summaryFlight.calls[tenant] = c
	}
	summaryFlight.Unlock()
	if !waiting {
		c.b, c.err = s.buildSummaryOnce(ctx, tenant, key)
		summaryFlight.Lock()
		delete(summaryFlight.calls, tenant)
		summaryFlight.Unlock()
		close(c.done)
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		return out, ctx.Err()
	}
	if c.err != nil {
		return out, c.err
	}
	return out, json.Unmarshal(c.b, &out)
}

func (s *Server) buildSummaryOnce(ctx context.Context, tenant int64, key string) ([]byte, error) {
	if s.RDB != nil && !s.allowTenant(ctx, "rlsum", tenant, summariesPerMin) {
		return nil, errSummaryBusy
	}
	// Shared with every caller waiting on it: one leaving doesn't cancel it for the others.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), summaryBuildTime)
	defer cancel()
	sum := s.buildSummary(bctx, tenant)
	b, err := json.Marshal(sum)
	if err != nil {
		return nil, err
	}
	if s.RDB != nil {
		ttl := summaryCacheTTL
		if len(sum.Unavailable) > 0 {
			ttl = summaryRetryTTL
		}
		s.RDB.Set(bctx, key, b, ttl)
	}
	return b, nil
}

// buildSummary reads every section in parallel.
func (s *Server) buildSummary(ctx context.Context, tenant int64) OverviewSummary {
	now := s.now().UTC()
	today := now.Truncate(24 * time.Hour)
	weekFrom, prevFrom := today.AddDate(0, 0, -6), today.AddDate(0, 0, -13)
	out := OverviewSummary{ComputedAt: now, Today: today.Format(time.DateOnly), WeekFrom: weekFrom.Format(time.DateOnly),
		PrevFrom: prevFrom.Format(time.DateOnly), Attention: []Attention{}}

	ost, _ := s.Store.(OverviewStore)
	oa, _ := s.Analytics.(OverviewAnalytics)
	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		unavailable []string
		health      map[string]ch.SendCounts
		providers   []model.Provider
	)
	run := func(name string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				s.Log.Warn("overview summary", "section", name, "tenant", tenant, "err", err)
				mu.Lock()
				unavailable = append(unavailable, name)
				mu.Unlock()
			}
		}()
	}
	if ost != nil {
		run("audience", func() error {
			n, err := ost.NewContacts(ctx, tenant, today, weekFrom, prevFrom)
			if err != nil {
				return err
			}
			mu.Lock()
			if out.Audience == nil {
				out.Audience = &AudienceSummary{}
			}
			out.Audience.New = &n
			mu.Unlock()
			return nil
		})
		run("campaigns", func() error {
			c, err := s.summaryCampaigns(ctx, ost, oa, tenant, today.AddDate(0, 0, -summaryRecent))
			if err != nil {
				return err
			}
			mu.Lock()
			out.Campaigns = c
			mu.Unlock()
			return nil
		})
	}
	if oa != nil && ost != nil {
		run("unsubscribes", func() error {
			wk, prev, err := oa.Unsubscribes(ctx, tenant, prevFrom, weekFrom)
			if err != nil {
				return err
			}
			mu.Lock()
			if out.Audience == nil {
				out.Audience = &AudienceSummary{}
			}
			out.Audience.Unsubscribed = &WeekCounts{Week: wk, Prev: prev}
			mu.Unlock()
			return nil
		})
	}
	if oa != nil {
		run("health", func() error {
			h, err := oa.ChannelHealth(ctx, tenant, now.Add(-time.Hour), today, today.AddDate(0, 0, -7))
			mu.Lock()
			health = h
			mu.Unlock()
			return err
		})
	}
	if rt, ok := s.Analytics.(revenueTotaler); ok {
		run("revenue", func() error {
			rv, err := s.summaryRevenue(ctx, rt, tenant, prevFrom, weekFrom, now)
			mu.Lock()
			out.Revenue = rv
			mu.Unlock()
			return err
		})
	}
	if pl, ok := s.Store.(providerLister); ok {
		run("providers", func() error {
			ps, err := pl.ListProviders(ctx, tenant)
			if err != nil {
				return err
			}
			if s.RDB != nil {
				for i := range ps {
					if ps[i].Limits.DailyQuota > 0 {
						ps[i].UsageToday, _ = dispatch.UsageToday(ctx, s.RDB, ps[i].ID, ps[i].Limits, now)
					}
				}
			}
			mu.Lock()
			providers = ps
			mu.Unlock()
			return nil
		})
	}
	wg.Wait()
	out.Attention = attentionItems(providers, health, out.Campaigns)
	out.Unavailable = unavailable
	return out
}

// summaryCampaigns lists the one-time campaigns sending, coming up and just sent, with the
// outcomes of the runs sending and just sent.
func (s *Server) summaryCampaigns(ctx context.Context, ost OverviewStore, oa OverviewAnalytics, tenant int64, since time.Time) (*CampaignSummary, error) {
	bs, err := ost.OverviewBroadcasts(ctx, tenant, since, summaryList)
	if err != nil {
		return nil, err
	}
	brief := func(list []pg.BroadcastBrief) []CampaignBrief {
		out := make([]CampaignBrief, len(list))
		for i, b := range list {
			out[i] = CampaignBrief{BroadcastBrief: b}
		}
		return out
	}
	c := &CampaignSummary{Sending: brief(bs.Sending), Next: brief(bs.Next), Recent: brief(bs.Recent)}
	if oa == nil {
		return c, nil
	}
	var runs []ch.RunKey
	for _, l := range [][]CampaignBrief{c.Sending, c.Recent} {
		for _, b := range l {
			if b.Run != 0 {
				runs = append(runs, ch.RunKey{Broadcast: b.ID, Run: b.Run})
			}
		}
	}
	counts, err := oa.RunCounts(ctx, tenant, runs)
	if err != nil {
		return nil, err
	}
	for _, l := range [][]CampaignBrief{c.Sending, c.Recent} {
		for i := range l {
			if n, ok := counts[ch.RunKey{Broadcast: l[i].ID, Run: l[i].Run}]; ok {
				l[i].Counts = &n
			} else if l[i].Run != 0 {
				l[i].Counts = &model.MessageCounts{}
			}
		}
	}
	return c, nil
}

// summaryRevenue totals revenue events of the last 7 days and the 7 before; nil when none.
func (s *Server) summaryRevenue(ctx context.Context, rt revenueTotaler, tenant int64, prevFrom, weekFrom, now time.Time) (*RevenueSummary, error) {
	rs, err := s.revenueSettings(ctx, tenant)
	if err != nil {
		return nil, err
	}
	totals := func(from, to time.Time) (RevenueTotals, error) {
		o, rev, b, err := rt.RevenueTotals(ctx, analytics.RevenueTotalsSQL(tenant, rs, from, to))
		return RevenueTotals{Orders: o, Revenue: math.Round(rev*100) / 100, Buyers: b}, err
	}
	wk, err := totals(weekFrom, now)
	if err != nil {
		return nil, err
	}
	prev, err := totals(prevFrom, weekFrom)
	if err != nil {
		return nil, err
	}
	if wk.Orders == 0 && prev.Orders == 0 {
		return nil, nil
	}
	return &RevenueSummary{Event: rs.Event, Currency: rs.Currency, Week: wk, Prev: prev}, nil
}

// attentionItems are the warnings: problems first, then warnings, each list in a stable order.
func attentionItems(providers []model.Provider, health map[string]ch.SendCounts, camps *CampaignSummary) []Attention {
	var problems, warnings []Attention
	add := func(a Attention) {
		if a.Level == "problem" {
			problems = append(problems, a)
		} else {
			warnings = append(warnings, a)
		}
	}
	channels := []string{auth.AreaChannels}
	for _, p := range providers {
		q := p.Limits.DailyQuota
		if q <= 0 {
			continue
		}
		name := p.Name
		if name == "" {
			name = "The " + channelWords(p.Channel) + " provider"
		}
		switch {
		case p.UsageToday >= q:
			add(Attention{Kind: "quota", Level: "problem", Needs: channels, Link: "/settings/channels", LinkText: "Channels",
				Text: fmt.Sprintf("%s has used its daily quota of %s messages. More %s messages wait until its next day starts.", name, num(q), channelWords(p.Channel))})
		case float64(p.UsageToday) >= warnQuotaNearUsed*float64(q):
			add(Attention{Kind: "quota", Level: "warning", Needs: channels, Link: "/settings/channels", LinkText: "Channels",
				Text: fmt.Sprintf("%s has used %s of its daily quota of %s messages.", name, num(p.UsageToday), num(q))})
		}
	}
	for _, c := range channel.All {
		sent, failed := health[c+"_sent"].Hour, health[c+"_failed"].Hour
		if failedTooMany(sent, failed) {
			add(Attention{Kind: "failing", Level: "problem", Needs: channels, Link: "/settings/channels", LinkText: "Check the provider",
				Text: fmt.Sprintf("%s of %s %s messages failed in the last hour (%s).", num(int64(failed)), num(int64(sent+failed)), channelWords(c), pct(failed, sent+failed))})
		}
	}
	if b, ok := health["email_bounced"]; ok {
		before := float64(b.Total-b.Today) / 7
		if b.Today >= warnBounceMin && float64(b.Today) >= warnBounceSpike*math.Max(before, 1) {
			add(Attention{Kind: "bounces", Level: "warning", Needs: []string{auth.AreaReports, auth.AreaChannels}, Link: "/analytics/campaigns", LinkText: "Campaign performance",
				Text: fmt.Sprintf("%s emails bounced today, against about %s a day last week.", num(int64(b.Today)), num(int64(math.Round(before))))})
		}
	}
	if camps != nil {
		for _, l := range [][]CampaignBrief{camps.Sending, camps.Recent} {
			for _, b := range l {
				if b.Counts == nil || !failedTooMany(b.Counts.Sent, b.Counts.Failed) {
					continue
				}
				add(Attention{Kind: "campaign_failed", Level: "warning", Needs: []string{auth.AreaBroadcasts}, Requires: []string{auth.AreaReports}, Link: "/broadcasts/" + strconv.FormatInt(b.ID, 10), LinkText: "Open the campaign",
					Text: fmt.Sprintf("“%s”: %s of %s messages failed (%s).", b.Name, num(int64(b.Counts.Failed)), num(int64(b.Counts.Sent+b.Counts.Failed)), pct(b.Counts.Failed, b.Counts.Sent+b.Counts.Failed))})
			}
		}
	}
	return append(append([]Attention{}, problems...), warnings...)
}

func failedTooMany(sent, failed uint64) bool {
	return failed >= warnFailedMin && float64(failed) >= warnFailedShare*float64(sent+failed)
}

// num writes a whole number with thousands separators (1,234,567).
func num(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

func pct(n, of uint64) string {
	if of == 0 {
		return "0%"
	}
	return strconv.Itoa(int(math.Round(float64(n)*100/float64(of)))) + "%"
}

// summaryFor keeps the sections a role may view.
func summaryFor(p auth.Perms, full OverviewSummary) OverviewSummary {
	out := full
	if !p.Allows(auth.AreaContacts, auth.View) {
		out.Audience = nil
	}
	if !p.Allows(auth.AreaBroadcasts, auth.View) {
		out.Campaigns = nil
	} else if full.Campaigns != nil && !p.Allows(auth.AreaReports, auth.View) {
		// Outcomes are reports: keep only how many were sent so far (the Campaigns hub shows that
		// to one-time campaigns view too), and no recent results.
		c := *full.Campaigns
		c.Recent = nil
		c.Sending = make([]CampaignBrief, len(full.Campaigns.Sending))
		for i, b := range full.Campaigns.Sending {
			if b.Counts != nil {
				b.Counts = &model.MessageCounts{Sent: b.Counts.Sent}
			}
			c.Sending[i] = b
		}
		out.Campaigns = &c
	}
	if !p.Allows(auth.AreaReports, auth.View) {
		out.Revenue = nil
	}
	out.Attention = []Attention{}
	for _, a := range full.Attention {
		if !allowsAny(p, a.Needs) || !allowsAll(p, a.Requires) {
			continue
		}
		a.Needs, a.Requires = nil, nil
		out.Attention = append(out.Attention, a)
	}
	return out
}

func allowsAny(p auth.Perms, areas []string) bool {
	for _, a := range areas {
		if p.Allows(a, auth.View) {
			return true
		}
	}
	return false
}

func allowsAll(p auth.Perms, areas []string) bool {
	for _, a := range areas {
		if !p.Allows(a, auth.View) {
			return false
		}
	}
	return true
}
