package api

// Journey reports and funnels. Broadcast reports are in broadcasts.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
)

type FunnelStore interface {
	ListFunnels(ctx context.Context, tenant int64) ([]model.Funnel, error)
	GetFunnel(ctx context.Context, tenant, id int64) (model.Funnel, error)
	SaveFunnel(ctx context.Context, f model.Funnel) (int64, error)
	DeleteFunnel(ctx context.Context, tenant, id int64) error
}

func (s *Server) reportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/journeys/{id}/report", s.tenant(s.journeyReport))
	mux.HandleFunc("GET /v1/events/names", s.tenant(s.eventNames))
	mux.HandleFunc("GET /v1/attributes/{key}/values", s.tenant(s.attrValues))
	mux.HandleFunc("GET /v1/events/{name}/values", s.tenant(s.propValues))
	mux.HandleFunc("GET /v1/funnels", s.tenant(s.listFunnels))
	mux.HandleFunc("POST /v1/funnels", s.tenant(s.saveFunnel))
	mux.HandleFunc("GET /v1/funnels/{id}", s.tenant(s.getFunnel))
	mux.HandleFunc("PUT /v1/funnels/{id}", s.tenant(s.saveFunnel))
	mux.HandleFunc("DELETE /v1/funnels/{id}", s.tenant(s.deleteFunnel))
	mux.HandleFunc("POST /v1/funnels/run", s.tenant(s.runFunnel))
}

func (s *Server) funnels() FunnelStore { return s.Store.(FunnelStore) }

// journeyReport: message outcomes and engagement per step and channel, plus enrollment and goal
// counts (from the journey's own state).
func (s *Server) journeyReport(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ra, ok := s.reports(w)
	if !ok {
		return
	}
	stats, err := s.Store.JourneyStats(r.Context(), tenant, id) // also checks the journey exists
	if err != nil {
		s.fail(w, err)
		return
	}
	sc := ch.Scope{Tenant: tenant, Source: "journey", SourceID: id}
	rep, err := ra.MessageReport(r.Context(), sc, "", 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.messageRevenue(r.Context(), &rep, sc, 7, false); err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"messages": rep, "enrollments": stats}
	if s.RDB != nil {
		steps, err := journey.ReadStepStats(r.Context(), s.RDB, tenant, id)
		if err != nil {
			s.fail(w, err)
			return
		}
		out["steps"] = steps
	}
	if us, ok := s.Store.(UIStore); ok {
		if j, err := us.GetJourney(r.Context(), tenant, id); err == nil {
			if splits := s.journeySplits(r.Context(), j); len(splits) > 0 {
				out["splits"] = splits
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) eventNames(w http.ResponseWriter, r *http.Request, tenant int64) {
	ra, ok := s.reports(w)
	if !ok {
		return
	}
	names, err := ra.EventNames(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"names": names})
}

func (s *Server) listFunnels(w http.ResponseWriter, r *http.Request, tenant int64) {
	fs, err := s.funnels().ListFunnels(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if fs == nil {
		fs = []model.Funnel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"funnels": fs})
}

func (s *Server) getFunnel(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	f, err := s.funnels().GetFunnel(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) saveFunnel(w http.ResponseWriter, r *http.Request, tenant int64) {
	var f model.Funnel
	if !decode(w, r, &f) {
		return
	}
	f.TenantID, f.ID = tenant, 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		f.ID = id
	}
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if _, err := funnelSteps(f.Steps, f.WindowDays); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.funnels().SaveFunnel(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteFunnel(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.funnels().DeleteFunnel(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// funnelSteps decodes and validates stored steps.
func funnelSteps(steps []model.FunnelStep, windowDays int) ([]segment.FunnelStep, error) {
	out := make([]segment.FunnelStep, len(steps))
	for i, st := range steps {
		out[i].Event = st.Event
		if len(st.Where) > 0 && string(st.Where) != "null" {
			if err := json.Unmarshal(st.Where, &out[i].Where); err != nil {
				return nil, errors.New("step " + strconv.Itoa(i+1) + ": invalid property filters")
			}
		}
	}
	return out, segment.ValidateFunnel(out, windowDays)
}

type funnelStepResult struct {
	Event    string  `json:"event"`
	Label    string  `json:"label,omitempty"`
	Contacts uint64  `json:"contacts"`
	OfFirst  float64 `json:"of_first"`    // share of step 1, 0-1
	OfPrev   float64 `json:"of_previous"` // share of the step before
	Dropped  uint64  `json:"dropped"`     // left between the previous step and this one
}

// runFunnel computes a funnel, saved or not: {steps, window_days, from, to, segment_id}.
// The date range defaults to the last 30 days.
func (s *Server) runFunnel(w http.ResponseWriter, r *http.Request, tenant int64) {
	ra, ok := s.reports(w)
	if !ok {
		return
	}
	var req struct {
		Steps      []model.FunnelStep `json:"steps"`
		WindowDays int                `json:"window_days"`
		From       time.Time          `json:"from"`
		To         time.Time          `json:"to"`
		SegmentID  int64              `json:"segment_id"`
		// Breakdown splits the counts by a profile attribute (the top 10 values, then "other").
		Breakdown *struct {
			Attr string `json:"attr"`
		} `json:"breakdown"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Breakdown != nil && !attr.ValidKey(req.Breakdown.Attr) {
		writeErr(w, http.StatusBadRequest, "choose the attribute to break the funnel down by, e.g. geo_country")
		return
	}
	steps, err := funnelSteps(req.Steps, req.WindowDays)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.To.IsZero() {
		req.To = s.now()
	}
	if req.From.IsZero() {
		req.From = req.To.AddDate(0, 0, -30)
	}
	if req.To.Sub(req.From) > 366*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "pick a date range of a year or less")
		return
	}
	if req.SegmentID > 0 {
		if _, err := s.Store.GetSegment(r.Context(), tenant, req.SegmentID); err != nil {
			s.fail(w, err)
			return
		}
	}
	q, err := segment.FunnelSQL(tenant, steps, req.WindowDays, req.From, req.To, req.SegmentID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	reached, err := ra.FunnelLevels(r.Context(), q, len(steps))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"steps": funnelResults(req.Steps, reached), "from": req.From, "to": req.To}
	if req.Breakdown != nil {
		bd, err := s.funnelBreakdown(r.Context(), tenant, steps, req.WindowDays, req.From, req.To, req.SegmentID, req.Breakdown.Attr, reached)
		if err != nil {
			s.fail(w, err)
			return
		}
		out["breakdown"] = bd
	}
	writeJSON(w, http.StatusOK, out)
}

func funnelResults(steps []model.FunnelStep, reached []uint64) []funnelStepResult {
	out := make([]funnelStepResult, len(steps))
	for i, st := range steps {
		res := funnelStepResult{Event: st.Event, Label: st.Label, Contacts: reached[i]}
		if reached[0] > 0 {
			res.OfFirst = float64(reached[i]) / float64(reached[0])
		}
		if i == 0 {
			res.OfPrev = 1
		} else if reached[i-1] > 0 {
			res.OfPrev = float64(reached[i]) / float64(reached[i-1])
			res.Dropped = reached[i-1] - reached[i]
		}
		out[i] = res
	}
	return out
}

// ValueSuggester suggests values for the segment builder: the most common values of an attribute
// or of an event property, from a sample (see ch.Store.AttrValues).
type ValueSuggester interface {
	AttrValues(ctx context.Context, tenant int64, key string, limit int) ([]ch.ValueCount, error)
	PropValues(ctx context.Context, tenant int64, name, prop string, limit int) ([]ch.ValueCount, error)
}

const (
	suggestLimit = 25
	suggestTTL   = 10 * time.Minute
)

// attrValues: GET /v1/attributes/{key}/values, the most common values of a contact attribute.
func (s *Server) attrValues(w http.ResponseWriter, r *http.Request, tenant int64) {
	key := r.PathValue("key")
	if !attr.ValidKey(key) {
		writeErr(w, http.StatusBadRequest, "invalid attribute name")
		return
	}
	if s.personalData(r.Context(), tenant, key) {
		writeJSON(w, http.StatusOK, map[string]any{"values": []ch.ValueCount{}}) // never suggest personal data
		return
	}
	s.suggest(w, r, fmt.Sprintf("vals:%d:a:%s", tenant, key), func(vs ValueSuggester) ([]ch.ValueCount, error) {
		return vs.AttrValues(r.Context(), tenant, key, suggestLimit)
	})
}

// propValues: GET /v1/events/{name}/values?prop=url, the most common values of an event property
// in the last 30 days.
func (s *Server) propValues(w http.ResponseWriter, r *http.Request, tenant int64) {
	name, prop := r.PathValue("name"), r.URL.Query().Get("prop")
	if !event.ValidName(name) || !attr.ValidKey(prop) {
		writeErr(w, http.StatusBadRequest, "invalid event or property name")
		return
	}
	s.suggest(w, r, fmt.Sprintf("vals:%d:e:%s:%s", tenant, name, prop), func(vs ValueSuggester) ([]ch.ValueCount, error) {
		return vs.PropValues(r.Context(), tenant, name, prop, suggestLimit)
	})
}

// personalData reports whether an attribute holds personal data: identifiers built in (email, phone,
// names, address, owner, user ids) or marked PII in the workspace's attribute definitions.
func (s *Server) personalData(ctx context.Context, tenant int64, key string) bool {
	switch strings.ToLower(key) {
	case "email", "phone", "mobile", "first_name", "last_name", "name", "full_name", "address", "address_line_1",
		"address_line_2", "zip", "zip_code", "postal_code", "geo_postal", "owner_id", "owner_name", "user_id", "external_id":
		return true
	}
	if a, ok := s.Store.(AudienceStore); ok {
		if defs, err := a.AttributeDefs(ctx, tenant); err == nil {
			for _, d := range defs {
				if d.Key == key && d.PII {
					return true
				}
			}
		}
	}
	return false
}

// suggest answers from a short Redis cache, so typing in the builder doesn't rescan ClickHouse.
func (s *Server) suggest(w http.ResponseWriter, r *http.Request, key string, load func(ValueSuggester) ([]ch.ValueCount, error)) {
	if s.RDB != nil {
		if b, err := s.RDB.Get(r.Context(), key).Bytes(); err == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
	}
	vs, ok := s.Analytics.(ValueSuggester)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"values": []ch.ValueCount{}})
		return
	}
	vals, err := load(vs)
	if err != nil {
		s.fail(w, err)
		return
	}
	b, _ := json.Marshal(map[string]any{"values": vals})
	if s.RDB != nil {
		s.RDB.Set(r.Context(), key, b, suggestTTL)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
