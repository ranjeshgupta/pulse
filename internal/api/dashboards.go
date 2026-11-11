package api

// M4 dashboards (Analytics → Users, Campaigns, Products) and the live event feed. Dashboards need
// reports view; the feed shows people's events, so it needs contacts view. Every query is scoped to
// the caller's workspace; dashboard answers are cached for a minute and cache misses are limited per
// workspace, and the feed is limited per workspace too.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/eventschema"
	"pulse/internal/live"
	"pulse/internal/model"
	"pulse/internal/providers"
	"pulse/internal/store/ch"
)

// DashboardAnalytics are the ClickHouse queries behind the dashboards.
type DashboardAnalytics interface {
	Users(ctx context.Context, tenant int64, from, to time.Time, f ch.UsersFilter) (ch.UsersReport, error)
	Products(ctx context.Context, tenant int64, from, to time.Time, pe ch.ProductEvents) (ch.ProductsReport, error)
	CampaignCompare(ctx context.Context, tenant int64, from, to time.Time, revenueEvent, amountProp string, windowDays int) (map[string]*ch.CampaignStats, error)
	WorkspaceReasons(ctx context.Context, tenant int64, from, to time.Time) ([]ch.ReasonCount, error)
}

const (
	dashboardCacheTTL = time.Minute
	dashboardsPerMin  = 60  // cache misses per workspace and minute
	livePerMin        = 120 // feed polls per workspace and minute (every 2 s from a few screens)
	// CampaignWindowDays: the Campaigns dashboard credits revenue within this many days of a
	// recipient's first message of the campaign.
	CampaignWindowDays = 7
)

func (s *Server) dashboardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/analytics/users", s.tenant(s.usersDashboard))
	mux.HandleFunc("GET /v1/analytics/campaigns", s.authed(s.campaignsDashboard))
	mux.HandleFunc("GET /v1/analytics/products", s.tenant(s.productsDashboard))
	mux.HandleFunc("GET /v1/live/events", s.tenant(s.liveEvents))
}

func (s *Server) dashboards(w http.ResponseWriter) (DashboardAnalytics, bool) {
	da, ok := s.Analytics.(DashboardAnalytics)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "dashboards need the analytics database")
	}
	return da, ok
}

// dayRange reads ?from=YYYY-MM-DD&to=YYYY-MM-DD (UTC days, both included): to defaults to today,
// from to def-1 days before it; at most max days.
func (s *Server) dayRange(r *http.Request, def, max int) (time.Time, time.Time, error) {
	q := r.URL.Query()
	today := s.now().UTC().Truncate(24 * time.Hour)
	to, from := today, time.Time{}
	var err error
	if v := q.Get("to"); v != "" {
		if to, err = time.Parse(time.DateOnly, v); err != nil {
			return from, to, errors.New("to must be a date like 2026-11-30")
		}
	}
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse(time.DateOnly, v); err != nil {
			return from, to, errors.New("from must be a date like 2026-11-01")
		}
	} else {
		from = to.AddDate(0, 0, -(def - 1))
	}
	switch {
	case from.After(to):
		return from, to, errors.New("from must be on or before to")
	case to.Sub(from) > time.Duration(max-1)*24*time.Hour:
		return from, to, errors.New("choose at most " + strconv.Itoa(max) + " days")
	}
	return from, to, nil
}

// cachedJSON answers from a minute's cache, else builds the answer (limited per workspace).
func (s *Server) cachedJSON(w http.ResponseWriter, r *http.Request, tenant int64, key string, build func(ctx context.Context) (any, error)) {
	ctx := r.Context()
	full := "dash:" + strconv.FormatInt(tenant, 10) + ":" + key
	if s.RDB != nil {
		if b, err := s.RDB.Get(ctx, full).Bytes(); err == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
		if !s.allowTenant(ctx, "rldash", tenant, dashboardsPerMin) {
			writeErr(w, http.StatusTooManyRequests, "too many dashboard requests; try again in a minute")
			return
		}
	}
	v, err := build(ctx)
	if err != nil {
		var ue errUser
		if errors.As(err, &ue) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	b, _ := json.Marshal(v)
	if s.RDB != nil {
		s.RDB.Set(ctx, full, b, dashboardCacheTTL)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// ---- Users ----

var platforms = []string{"web", "android", "ios", "other"}

func (s *Server) usersDashboard(w http.ResponseWriter, r *http.Request, tenant int64) {
	da, ok := s.dashboards(w)
	if !ok {
		return
	}
	from, to, err := s.dayRange(r, 90, 366)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	f := ch.UsersFilter{Platform: q.Get("platform")}
	if f.Platform != "" && !slices.Contains(platforms, f.Platform) {
		writeErr(w, http.StatusBadRequest, "platform must be web, android, ios or other")
		return
	}
	if q.Has("source") {
		f.Source, f.HasSource = strings.ToLower(q.Get("source")), true
		if len(f.Source) > 64 {
			writeErr(w, http.StatusBadRequest, "source is too long")
			return
		}
	}
	key := "users:" + from.Format(time.DateOnly) + ":" + to.Format(time.DateOnly) + ":" + f.Platform + ":" + strconv.FormatBool(f.HasSource) + ":" + f.Source
	s.cachedJSON(w, r, tenant, key, func(ctx context.Context) (any, error) {
		rep, err := da.Users(ctx, tenant, from, to, f)
		if err != nil {
			return nil, err
		}
		return map[string]any{"from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly), "report": rep,
			"computed_at": s.now().UTC()}, nil
	})
}

// ---- Campaigns ----

// CampaignDashRow is one campaign on the Campaigns dashboard.
type CampaignDashRow struct {
	CampaignRow
	Counts  model.MessageCounts `json:"counts"`
	Reasons []ch.ReasonCount    `json:"reasons"`
	Revenue float64             `json:"revenue"`
	Buyers  uint64              `json:"buyers"`
}

// Campaigns are listed only to roles that may view them (broadcasts, journeys), as on the hub; the
// workspace-wide totals by reason stay for everyone with reports view.
func (s *Server) campaignsDashboard(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	canB, canJ := p.Perms.Allows(auth.AreaBroadcasts, auth.View), p.Perms.Allows(auth.AreaJourneys, auth.View)
	da, ok := s.dashboards(w)
	if !ok {
		return
	}
	from, to, err := s.dayRange(r, 30, 366)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	f := CampaignFilter{Category: q.Get("category"), Channel: q.Get("channel"), Tag: q.Get("tag"), Q: q.Get("q")}
	key := "camp:" + strconv.FormatBool(canB) + strconv.FormatBool(canJ) + ":" + from.Format(time.DateOnly) + ":" + to.Format(time.DateOnly) + ":" + f.Category + ":" + f.Channel + ":" + f.Tag + ":" + f.Q
	s.cachedJSON(w, r, tenant, key, func(ctx context.Context) (any, error) {
		rs := analytics.DefaultRevenue()
		if is, ok := s.Store.(InsightsStore); ok {
			if got, err := is.RevenueSettings(ctx, tenant); err == nil {
				rs = got
			}
		}
		end := to.AddDate(0, 0, 1)
		stats, err := da.CampaignCompare(ctx, tenant, from, end, rs.Event, rs.AmountProp, CampaignWindowDays)
		if err != nil {
			return nil, err
		}
		reasonsAll, err := da.WorkspaceReasons(ctx, tenant, from, end)
		if err != nil {
			return nil, err
		}
		rows, err := s.campaignRows(ctx, tenant, canB, canJ)
		if err != nil {
			return nil, err
		}
		out := []CampaignDashRow{}
		for _, row := range rows {
			st := stats[row.Kind+":"+strconv.FormatInt(row.ID, 10)]
			if st == nil || !f.Match(row) {
				continue // no messages in the period, or filtered out
			}
			out = append(out, CampaignDashRow{CampaignRow: row, Counts: st.Counts, Reasons: st.Reasons, Revenue: st.Revenue, Buyers: st.Buyers})
		}
		slices.SortStableFunc(out, func(a, b CampaignDashRow) int { return int(b.Counts.Sent) - int(a.Counts.Sent) })
		return map[string]any{"from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly), "campaigns": out, "reasons": reasonsAll,
			"revenue_event": rs.Event, "currency": rs.Currency, "window_days": CampaignWindowDays,
			"delivery_reports": s.deliveryReported(ctx, tenant), "computed_at": s.now().UTC()}, nil
	})
}

// deliveryReported says per channel whether its default provider reports deliveries.
func (s *Server) deliveryReported(ctx context.Context, tenant int64) map[string]bool {
	out := map[string]bool{"email": false, "sms": false, "whatsapp": false, "rcs": false, "push": false, "inbox": false}
	cs, ok := s.Store.(ChannelStore)
	if !ok {
		return out
	}
	ps, err := cs.ListProviders(ctx, tenant)
	if err != nil {
		return out
	}
	for _, p := range ps {
		if p.IsDefault && providers.ReportsDelivery(p.Kind, p.Config) {
			out[p.Channel] = true
		}
	}
	return out
}

// ---- Products ----

func (s *Server) productsDashboard(w http.ResponseWriter, r *http.Request, tenant int64) {
	da, ok := s.dashboards(w)
	if !ok {
		return
	}
	from, to, err := s.dayRange(r, 30, 366)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	pe := ch.ProductEvents{View: q.Get("view_event"), Cart: q.Get("cart_event")}
	if pe.View == "" {
		pe.View = "product_viewed"
	}
	if pe.Cart == "" {
		pe.Cart = "add_to_cart"
	}
	if !event.ValidName(pe.View) || !event.ValidName(pe.Cart) {
		writeErr(w, http.StatusBadRequest, "invalid event name")
		return
	}
	key := "prod:" + from.Format(time.DateOnly) + ":" + to.Format(time.DateOnly) + ":" + pe.View + ":" + pe.Cart
	s.cachedJSON(w, r, tenant, key, func(ctx context.Context) (any, error) {
		rs := analytics.DefaultRevenue()
		if is, ok := s.Store.(InsightsStore); ok {
			if got, err := is.RevenueSettings(ctx, tenant); err == nil {
				rs = got
			}
		}
		pe.Purchase, pe.AmountProp = rs.Event, rs.AmountProp
		rep, err := da.Products(ctx, tenant, from, to.AddDate(0, 0, 1), pe)
		if err != nil {
			return nil, err
		}
		return map[string]any{"from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly), "report": rep,
			"events":   map[string]string{"view": pe.View, "cart": pe.Cart, "purchase": pe.Purchase, "amount": pe.AmountProp},
			"currency": rs.Currency, "computed_at": s.now().UTC()}, nil
	})
}

// BuyersRule is the segment rule for "bought this product in these days" (the Products
// dashboard's "Create segment from buyers").
func BuyersRule(purchase, code string, from, to time.Time) map[string]any {
	return map[string]any{"event": purchase, "op": "gte", "count": 1, "from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly),
		"where": []map[string]any{{"prop": "product_code", "op": "eq", "value": code}}}
}

// ---- live feed ----

// liveEvents: GET /v1/live/events?cursor=&name=&contact=&limit= — the workspace's newest events
// (from the live stream), after the cursor; personal properties (event schemas) are removed.
func (s *Server) liveEvents(w http.ResponseWriter, r *http.Request, tenant int64) {
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "the live feed needs Redis")
		return
	}
	q := r.URL.Query()
	cursor, name, contact := q.Get("cursor"), q.Get("name"), q.Get("contact")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if !live.ValidCursor(cursor) || (name != "" && !event.ValidName(strings.TrimSuffix(name, "*"))) || len(contact) > 200 {
		writeErr(w, http.StatusBadRequest, "invalid cursor, name or contact")
		return
	}
	if !s.allowTenant(r.Context(), "rllive", tenant, livePerMin) {
		writeErr(w, http.StatusTooManyRequests, "the live feed is polled too often; slow down")
		return
	}
	// Filters apply to what was read: read more than asked so a filter still finds matches.
	items, next, err := live.Read(r.Context(), s.RDB, tenant, cursor, min(live.Keep, limit*5))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []live.Item{}
	schemas := map[string]*eventschema.Schema{}
	for _, it := range items {
		if !liveMatch(it, name, contact) {
			continue
		}
		it.Props = s.publicProps(r.Context(), tenant, it.Name, it.Props, schemas)
		out = append(out, it)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "cursor": next, "max_per_second": live.MaxPerSecond})
}

// liveMatch applies the feed's filters: an event name (a trailing * matches a prefix) and a
// contact (external id, case-insensitive substring).
func liveMatch(it live.Item, name, contact string) bool {
	if name != "" {
		if p, ok := strings.CutSuffix(name, "*"); ok {
			if !strings.HasPrefix(it.Name, p) {
				return false
			}
		} else if it.Name != name {
			return false
		}
	}
	return contact == "" || strings.Contains(strings.ToLower(it.ExternalID), strings.ToLower(contact))
}

// publicProps removes the properties an event schema marks personal.
func (s *Server) publicProps(ctx context.Context, tenant int64, name string, raw json.RawMessage, cache map[string]*eventschema.Schema) json.RawMessage {
	if len(raw) == 0 || s.Schemas == nil {
		return raw
	}
	sc, ok := cache[name]
	if !ok {
		sc = s.Schemas.Lookup(ctx, tenant, name)
		cache[name] = sc
	}
	if sc == nil || len(sc.PersonalPaths()) == 0 {
		return raw
	}
	var props map[string]any
	if json.Unmarshal(raw, &props) != nil {
		return nil
	}
	return eventschema.MessageProps(sc, props)
}
