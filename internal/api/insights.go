package api

// Analytics: cohorts (retention), revenue attribution and settings, custom reports, and the
// pieces they add to funnels and to broadcast and journey reports.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
)

// InsightsAnalytics are the ClickHouse queries behind these reports.
type InsightsAnalytics interface {
	CohortCounts(ctx context.Context, sizes, cells segment.Query) (map[int64]uint64, []analytics.CohortCell, error)
	RevenueTotals(ctx context.Context, q segment.Query) (uint64, float64, uint64, error)
	AttributedPurchases(ctx context.Context, q segment.Query) ([]analytics.Purchase, error)
	RecipientRevenue(ctx context.Context, q segment.Query) (map[string]ch.Revenue, error)
	ReportRows(ctx context.Context, q segment.Query) ([]analytics.ReportRow, error)
	FunnelBreakdown(ctx context.Context, q segment.Query, steps int) ([]ch.BreakdownRow, error)
	SplitStats(ctx context.Context, q analytics.SplitQuery) ([]analytics.PathStat, error)
}

// InsightsStore keeps saved reports and revenue settings (Postgres).
type InsightsStore interface {
	ListSavedReports(ctx context.Context, tenant int64, kind string) ([]model.SavedReport, error)
	GetSavedReport(ctx context.Context, tenant int64, kind string, id int64) (model.SavedReport, error)
	SaveReport(ctx context.Context, r model.SavedReport) (int64, error)
	DeleteSavedReport(ctx context.Context, tenant int64, kind string, id int64) error
	RevenueSettings(ctx context.Context, tenant int64) (analytics.RevenueSettings, error)
	PutRevenueSettings(ctx context.Context, tenant int64, rs analytics.RevenueSettings) error
}

const (
	kindCohort = "cohort"
	kindCustom = "custom"
)

func (s *Server) insightRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/cohorts/run", s.tenant(s.runCohort))
	mux.HandleFunc("GET /v1/cohorts", s.tenant(s.listSaved(kindCohort, "cohorts")))
	mux.HandleFunc("POST /v1/cohorts", s.tenant(s.saveSaved(kindCohort)))
	mux.HandleFunc("GET /v1/cohorts/{id}", s.tenant(s.getSaved(kindCohort)))
	mux.HandleFunc("PUT /v1/cohorts/{id}", s.tenant(s.saveSaved(kindCohort)))
	mux.HandleFunc("DELETE /v1/cohorts/{id}", s.tenant(s.deleteSaved(kindCohort)))

	mux.HandleFunc("POST /v1/reports/run", s.tenant(s.runReport))
	mux.HandleFunc("GET /v1/reports", s.tenant(s.listSaved(kindCustom, "reports")))
	mux.HandleFunc("POST /v1/reports", s.tenant(s.saveSaved(kindCustom)))
	mux.HandleFunc("GET /v1/reports/{id}", s.tenant(s.getSaved(kindCustom)))
	mux.HandleFunc("PUT /v1/reports/{id}", s.tenant(s.saveSaved(kindCustom)))
	mux.HandleFunc("DELETE /v1/reports/{id}", s.tenant(s.deleteSaved(kindCustom)))

	mux.HandleFunc("GET /v1/revenue/settings", s.tenant(s.getRevenueSettings))
	mux.HandleFunc("PUT /v1/revenue/settings", s.tenant(s.putRevenueSettings))
	mux.HandleFunc("POST /v1/revenue/report", s.tenant(s.revenueReport))
	mux.HandleFunc("GET /v1/revenue/customer-value", s.tenant(s.customerValue))
	mux.HandleFunc("POST /v1/analytics/paths", s.tenant(s.pathAnalysis))
	mux.HandleFunc("POST /v1/revenue/customer-value/refresh", s.tenant(s.refreshCustomerValue))
}

func (s *Server) insights(w http.ResponseWriter) (InsightsAnalytics, bool) {
	ia, ok := s.Analytics.(InsightsAnalytics)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "reports need the analytics database")
	}
	return ia, ok
}

func (s *Server) insightStore() InsightsStore { return s.Store.(InsightsStore) }

// dateRange fills in a report's range: to defaults to now, from to `days` before it.
func (s *Server) dateRange(from, to time.Time, days int) (time.Time, time.Time) {
	if to.IsZero() {
		to = s.now()
	}
	if days <= 0 {
		days = 30
	}
	if from.IsZero() {
		from = to.AddDate(0, 0, -days)
	}
	return from, to
}

// checkSegment refuses a segment id of another workspace (or none).
func (s *Server) checkSegment(w http.ResponseWriter, r *http.Request, tenant, id int64) bool {
	if id <= 0 {
		return true
	}
	if _, err := s.Store.GetSegment(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return false
	}
	return true
}

// ---- saved reports (cohorts and custom reports) ----

func validSpec(kind string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("spec is required")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if kind == kindCohort {
		var c analytics.CohortSpec
		if err := dec.Decode(&c); err != nil {
			return errors.New("invalid cohort: " + err.Error())
		}
		return c.Validate()
	}
	var rs analytics.ReportSpec
	if err := dec.Decode(&rs); err != nil {
		return errors.New("invalid report: " + err.Error())
	}
	return rs.Validate()
}

func (s *Server) listSaved(kind, key string) tenantHandler {
	return func(w http.ResponseWriter, r *http.Request, tenant int64) {
		rs, err := s.insightStore().ListSavedReports(r.Context(), tenant, kind)
		if err != nil {
			s.fail(w, err)
			return
		}
		if rs == nil {
			rs = []model.SavedReport{}
		}
		writeJSON(w, http.StatusOK, map[string]any{key: rs})
	}
}

func (s *Server) getSaved(kind string) tenantHandler {
	return func(w http.ResponseWriter, r *http.Request, tenant int64) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		rep, err := s.insightStore().GetSavedReport(r.Context(), tenant, kind, id)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}
}

func (s *Server) saveSaved(kind string) tenantHandler {
	return func(w http.ResponseWriter, r *http.Request, tenant int64) {
		var req struct {
			Name string          `json:"name"`
			Spec json.RawMessage `json:"spec"`
		}
		if !decode(w, r, &req) {
			return
		}
		rep := model.SavedReport{TenantID: tenant, Kind: kind, Name: strings.TrimSpace(req.Name), Spec: req.Spec}
		if r.Method == http.MethodPut {
			id, ok := pathID(w, r)
			if !ok {
				return
			}
			rep.ID = id
		}
		if rep.Name == "" || len(rep.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "name is required (200 characters at most)")
			return
		}
		if err := validSpec(kind, req.Spec); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := s.insightStore().SaveReport(r.Context(), rep)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	}
}

func (s *Server) deleteSaved(kind string) tenantHandler {
	return func(w http.ResponseWriter, r *http.Request, tenant int64) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := s.insightStore().DeleteSavedReport(r.Context(), tenant, kind, id); err != nil {
			s.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- cohorts ----

// runCohort computes a retention triangle: {spec, from, to}; the range (cohort starts) defaults to
// the last 8 periods.
func (s *Server) runCohort(w http.ResponseWriter, r *http.Request, tenant int64) {
	ia, ok := s.insights(w)
	if !ok {
		return
	}
	var req struct {
		Spec analytics.CohortSpec `json:"spec"`
		From time.Time            `json:"from"`
		To   time.Time            `json:"to"`
	}
	if !decode(w, r, &req) {
		return
	}
	c := req.Spec
	if err := c.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.checkSegment(w, r, tenant, c.SegmentID) {
		return
	}
	now := s.now().UTC()
	if req.To.IsZero() {
		req.To = now
	}
	if req.From.IsZero() {
		req.From = c.Period.Add(c.Period.Start(req.To), -7)
	}
	sizes, cells, err := analytics.CohortQueries(tenant, c, req.From, req.To)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sz, cs, err := ia.CohortCounts(r.Context(), sizes, cells)
	if err != nil {
		s.fail(w, err)
		return
	}
	rows := analytics.CohortTable(c, req.From, req.To, now, sz, cs)
	writeJSON(w, http.StatusOK, map[string]any{
		"period": c.Period, "periods": c.Periods, "from": req.From, "to": req.To,
		"cohorts": rows, "average": analytics.CohortAverage(rows, c.Periods),
	})
}

// ---- revenue ----

func (s *Server) revenueSettings(ctx context.Context, tenant int64) (analytics.RevenueSettings, error) {
	st, ok := s.Store.(InsightsStore)
	if !ok {
		return analytics.DefaultRevenue(), nil
	}
	return st.RevenueSettings(ctx, tenant)
}

func (s *Server) getRevenueSettings(w http.ResponseWriter, r *http.Request, tenant int64) {
	rs, err := s.revenueSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": rs})
}

func (s *Server) putRevenueSettings(w http.ResponseWriter, r *http.Request, tenant int64) {
	var rs analytics.RevenueSettings
	if !decode(w, r, &rs) {
		return
	}
	rs.Event, rs.AmountProp, rs.Currency = strings.TrimSpace(rs.Event), strings.TrimSpace(rs.AmountProp), strings.ToUpper(strings.TrimSpace(rs.Currency))
	if err := rs.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.insightStore().PutRevenueSettings(r.Context(), tenant, rs); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": rs})
}

type attribution struct {
	Credits     []analytics.Credit `json:"credits"`
	Revenue     float64            `json:"revenue"`     // attributed
	Conversions float64            `json:"conversions"` // attributed purchases
}

// revenueReport: {from, to, model, window_days} -> totals and credit per broadcast/journey and per
// UTM campaign. The range defaults to the last 30 days, the model to last touch, the window to 7 days.
func (s *Server) revenueReport(w http.ResponseWriter, r *http.Request, tenant int64) {
	ia, ok := s.insights(w)
	if !ok {
		return
	}
	var req struct {
		From       time.Time `json:"from"`
		To         time.Time `json:"to"`
		Model      string    `json:"model"`
		WindowDays int       `json:"window_days"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Model == "" {
		req.Model = analytics.LastTouch
	}
	if req.WindowDays == 0 {
		req.WindowDays = 7
	}
	if !analytics.ValidModel(req.Model) {
		writeErr(w, http.StatusBadRequest, "the model is last_touch, first_touch or linear")
		return
	}
	req.From, req.To = s.dateRange(req.From, req.To, 30)
	if err := analytics.CheckRange(req.From, req.To); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rs, err := s.revenueSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	orders, revenue, buyers, err := ia.RevenueTotals(r.Context(), analytics.RevenueTotalsSQL(tenant, rs, req.From, req.To))
	if err != nil {
		s.fail(w, err)
		return
	}
	names := s.sourceNames(r.Context(), tenant)
	out := map[string]any{
		"settings": rs, "from": req.From, "to": req.To, "model": req.Model, "window_days": req.WindowDays,
		"totals": map[string]any{"orders": orders, "revenue": round2(revenue), "buyers": buyers},
	}
	for key, family := range map[string]string{"messages": analytics.TouchMessages, "campaigns": analytics.TouchUTM} {
		q, err := analytics.AttributionSQL(tenant, rs, family, req.WindowDays, req.From, req.To)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		ps, err := ia.AttributedPurchases(r.Context(), q)
		if err != nil {
			s.fail(w, err)
			return
		}
		a := attribution{Credits: analytics.Attribute(req.Model, ps)}
		for i, c := range a.Credits {
			a.Revenue += c.Revenue
			a.Conversions += c.Conversions
			a.Credits[i].Name = names[c.Kind+"|"+strconv.FormatInt(c.ID, 10)]
		}
		a.Revenue, a.Conversions = round2(a.Revenue), round2(a.Conversions)
		out[key] = a
	}
	writeJSON(w, http.StatusOK, out)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// sourceNames maps "broadcast|id" and "journey|id" to names, for reports.
func (s *Server) sourceNames(ctx context.Context, tenant int64) map[string]string {
	out := map[string]string{}
	if bs, ok := s.Store.(BroadcastStore); ok {
		if list, err := bs.ListBroadcasts(ctx, tenant); err == nil {
			for _, b := range list {
				out["broadcast|"+strconv.FormatInt(b.ID, 10)] = b.Name
			}
		}
	}
	if us, ok := s.Store.(UIStore); ok {
		if list, err := us.ListJourneys(ctx, tenant); err == nil {
			for _, j := range list {
				out["journey|"+strconv.FormatInt(j.ID, 10)] = j.Name
			}
		}
	}
	return out
}

// messageRevenue adds recipients' revenue to a broadcast or journey report: revenue events a
// recipient did within days of their first message. Reports without any leave it out.
func (s *Server) messageRevenue(ctx context.Context, rep *ch.Report, sc ch.Scope, days int, byVariant bool) error {
	ia, ok := s.Analytics.(InsightsAnalytics)
	if !ok {
		return nil
	}
	rs, err := s.revenueSettings(ctx, sc.Tenant)
	if err != nil {
		return err
	}
	if days <= 0 {
		days = 7
	}
	scope, args := sc.Where()
	tot, err := ia.RecipientRevenue(ctx, analytics.RecipientRevenueSQL(sc.Tenant, rs, scope, args, "''", days))
	if err != nil {
		return err
	}
	rr := &ch.RevenueReport{Revenue: tot[""], Event: rs.Event, Currency: rs.Currency, Days: days}
	if rr.Orders == 0 {
		return nil
	}
	rr.Revenue.Revenue = round2(rr.Revenue.Revenue)
	if byVariant {
		vs, err := ia.RecipientRevenue(ctx, analytics.RecipientRevenueSQL(sc.Tenant, rs, scope, args, "variant", days))
		if err != nil {
			return err
		}
		delete(vs, "") // not an A/B test
		for k, v := range vs {
			v.Revenue = round2(v.Revenue)
			vs[k] = v
		}
		if len(vs) > 0 {
			rr.ByVariant = vs
		}
	}
	rep.Revenue = rr
	return nil
}

// ---- custom reports ----

// runReport computes a custom report: {spec, from, to}; the range defaults to the spec's
// range_days (else 30) before now.
func (s *Server) runReport(w http.ResponseWriter, r *http.Request, tenant int64) {
	ia, ok := s.insights(w)
	if !ok {
		return
	}
	var req struct {
		Spec analytics.ReportSpec `json:"spec"`
		From time.Time            `json:"from"`
		To   time.Time            `json:"to"`
	}
	if !decode(w, r, &req) {
		return
	}
	sp := req.Spec
	if err := sp.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.checkSegment(w, r, tenant, sp.SegmentID) {
		return
	}
	req.From, req.To = s.dateRange(req.From, req.To, sp.RangeDays)
	q, err := analytics.ReportSQL(tenant, sp, req.From, req.To)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := ia.ReportRows(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	if p, ok := sp.TimeGroup(); ok {
		rows = analytics.FillTime(p, req.From, req.To, rows)
	}
	labels := map[string]string{}
	if sp.GroupBy.Type == analytics.GroupCampaign {
		names := s.sourceNames(r.Context(), tenant)
		for _, row := range rows {
			labels[row.Group] = names[row.Group]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "labels": labels, "from": req.From, "to": req.To, "max_groups": analytics.MaxGroups})
}

// ---- funnel breakdown ----

type breakdownResult struct {
	Attr  string            `json:"attr"`
	Rows  []ch.BreakdownRow `json:"rows"`
	Other []uint64          `json:"other,omitempty"` // contacts with any other value (or no snapshot row yet)
}

func (s *Server) funnelBreakdown(ctx context.Context, tenant int64, steps []segment.FunnelStep, windowDays int, from, to time.Time, segmentID int64, attrKey string, reached []uint64) (*breakdownResult, error) {
	ia, ok := s.Analytics.(InsightsAnalytics)
	if !ok {
		return nil, errors.New("breakdowns need the analytics database")
	}
	q, err := segment.FunnelBreakdownSQL(tenant, steps, windowDays, from, to, segmentID, attrKey)
	if err != nil {
		return nil, err
	}
	rows, err := ia.FunnelBreakdown(ctx, q, len(steps))
	if err != nil {
		return nil, err
	}
	res := &breakdownResult{Attr: attrKey, Rows: rows}
	if res.Rows == nil {
		res.Rows = []ch.BreakdownRow{}
	}
	other := make([]uint64, len(steps))
	any := false
	for i := range other {
		var top uint64
		for _, r := range rows {
			top += r.Reached[i]
		}
		if reached[i] > top {
			other[i] = reached[i] - top
			any = true
		}
	}
	if any {
		res.Other = other
	}
	return res, nil
}

// ---- journey A/B tests in the report ----

type splitReport struct {
	Metric   string                 `json:"metric,omitempty"`
	Paths    []analytics.PathStat   `json:"paths"`
	Decision *journey.SplitDecision `json:"decision,omitempty"`
}

// journeySplits reports every split step's paths: contacts per path and, for A/B tests with a
// winner setting, the metric's hits and the decision.
func (s *Server) journeySplits(ctx context.Context, j model.Journey) map[string]splitReport {
	ia, ok := s.Analytics.(InsightsAnalytics)
	if !ok {
		return nil
	}
	g, err := journey.ParseGraph(j.Graph)
	if err != nil {
		return nil
	}
	ds, _ := journey.Decisions(j.SplitWinners)
	out := map[string]splitReport{}
	for id, n := range g.Nodes {
		if n.Type != journey.NodeSplit {
			continue
		}
		metric := "" // a plain split: contacts per path only
		if n.Winner != nil {
			metric = n.Winner.Metric
		}
		stats, err := ia.SplitStats(ctx, journey.SplitTest(j, g, id, metric))
		if err != nil {
			s.Log.Error("journey split stats", "journey", j.ID, "step", id, "err", err)
			continue
		}
		if stats == nil {
			stats = []analytics.PathStat{}
		}
		rep := splitReport{Paths: stats, Metric: metric}
		if d, ok := ds[id]; ok {
			d := d
			rep.Decision = &d
		}
		out[id] = rep
	}
	return out
}
