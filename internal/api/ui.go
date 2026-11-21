package api

// Endpoints behind the web app: editing segments, templates and journeys, contact search and
// timelines, live segment previews and the dashboard.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/channel"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// Analytics is the ClickHouse side of the web app.
type Analytics interface {
	SegmentCount(ctx context.Context, q segment.Query) (uint64, error)
	ContactEvents(ctx context.Context, tenant int64, contact uint32, limit int) ([]ch.EventRow, error)
	Overview(ctx context.Context, tenant int64, days int) (ch.Overview, error)
}

// UIStore is the Postgres side of the web app.
type UIStore interface {
	UpdateSegment(ctx context.Context, tenant, id int64, name string, rule []byte, hasWindow bool) error
	DeleteSegment(ctx context.Context, tenant, id int64) error
	ListTemplates(ctx context.Context, tenant int64, ch string) ([]model.Template, error)
	Template(ctx context.Context, tenant, id int64) (model.Template, error)
	UpdateTemplate(ctx context.Context, t model.Template) error
	DeleteTemplate(ctx context.Context, tenant, id int64) error
	ListJourneys(ctx context.Context, tenant int64) ([]model.Journey, error)
	GetJourney(ctx context.Context, tenant, id int64) (model.Journey, error)
	UpdateJourney(ctx context.Context, tenant, id int64, name string, graph []byte, w schedule.Window) error
	DeleteJourney(ctx context.Context, tenant, id int64) error
	ListContacts(ctx context.Context, tenant int64, afterID uint32, limit int) ([]model.Contact, error)
	Counts(ctx context.Context, tenant int64) (pg.Counts, error)
}

func (s *Server) uiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /v1/segments/{id}", s.tenant(s.updateSegment))
	mux.HandleFunc("DELETE /v1/segments/{id}", s.tenant(s.deleteSegment))
	mux.HandleFunc("POST /v1/segments/preview", s.tenant(s.previewSegment))

	mux.HandleFunc("GET /v1/templates", s.tenant(s.listTemplates))
	mux.HandleFunc("GET /v1/templates/{id}", s.tenant(s.getTemplate))
	mux.HandleFunc("GET /v1/templates/{id}/report", s.tenant(s.templateReport))
	mux.HandleFunc("PUT /v1/templates/{id}", s.tenant(s.updateTemplate))
	mux.HandleFunc("DELETE /v1/templates/{id}", s.tenant(s.deleteTemplate))
	mux.HandleFunc("POST /v1/templates/text", s.tenant(s.templateText))

	mux.HandleFunc("GET /v1/journeys", s.tenant(s.listJourneys))
	mux.HandleFunc("GET /v1/journeys/{id}", s.tenant(s.getJourney))
	mux.HandleFunc("PUT /v1/journeys/{id}", s.authed(s.updateJourney))
	mux.HandleFunc("DELETE /v1/journeys/{id}", s.tenant(s.deleteJourney))

	mux.HandleFunc("GET /v1/contacts", s.authed(s.searchContacts))
	mux.HandleFunc("POST /v1/contacts/view-segment", s.authed(s.viewSegment))
	mux.HandleFunc("GET /v1/contacts/{external_id}/events", s.tenant(s.contactEvents))
	mux.HandleFunc("GET /v1/stats/overview", s.tenant(s.overview))
	mux.HandleFunc("GET /v1/stats/summary", s.authed(s.overviewSummary)) // overview.go
}

func (s *Server) uiStore() UIStore { return s.Store.(UIStore) }

// ---- segments ----

func (s *Server) updateSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name    string           `json:"name"`
		Rule    json.RawMessage  `json:"rule"`
		Refresh *segment.Refresh `json:"refresh"` // omitted: unchanged
	}
	if !decode(w, r, &req) {
		return
	}
	rule, err := segment.Parse(req.Rule)
	if req.Name == "" || err != nil {
		writeErr(w, http.StatusBadRequest, errors.Join(errIf(req.Name == "", "name is required"), err).Error())
		return
	}
	if req.Refresh != nil {
		if err := req.Refresh.Validate(); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.checkSegmentRefs(r.Context(), tenant, id, rule); err != nil {
		s.failRefs(w, err)
		return
	}
	if err := s.uiStore().UpdateSegment(r.Context(), tenant, id, req.Name, req.Rule, rule.HasWindow()); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.setRefresh(r.Context(), tenant, id, req.Refresh); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "new"}) // rebuilt from ClickHouse
}

func (s *Server) deleteSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if deps, err := s.dependentsOf(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	} else if len(deps) > 0 {
		writeErr(w, http.StatusConflict, "these segments use it; change them first: "+strings.Join(deps, ", "))
		return
	}
	if err := s.uiStore().DeleteSegment(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	if d, ok := s.Members.(interface {
		Delete(ctx context.Context, tenant, segment int64) error
	}); ok {
		_ = d.Delete(r.Context(), tenant, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewSegment counts who a rule would match right now, while it is being edited.
func (s *Server) previewSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	if s.Analytics == nil {
		writeErr(w, http.StatusServiceUnavailable, "analytics unavailable")
		return
	}
	var req struct {
		Rule json.RawMessage `json:"rule"`
	}
	if !decode(w, r, &req) {
		return
	}
	rule, err := segment.Parse(req.Rule)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	q, err := segment.ToSQL(rule, tenant, s.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	n, err := s.Analytics.SegmentCount(ctx, q)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// ---- templates ----

type templateReq struct {
	Name    string          `json:"name"`
	Channel string          `json:"channel"`
	Subject string          `json:"subject"`
	HTML    string          `json:"html"`
	Content json.RawMessage `json:"content"`
	Builder json.RawMessage `json:"builder"`
	// Transactional templates skip frequency caps and quiet hours.
	Transactional bool `json:"transactional"`
	// MissingValues: "empty" (default) or "skip" when a variable has no value and no default.
	MissingValues string `json:"missing_values"`
	// Hidden: written inside a one-time campaign, kept out of template lists (omitted on update:
	// unchanged; false makes it a reusable template).
	Hidden *bool `json:"hidden"`
}

func (req templateReq) template(tenant int64) model.Template {
	if req.Channel == "" {
		req.Channel = channel.Email
	}
	return model.Template{TenantID: tenant, Name: req.Name, Channel: req.Channel, Subject: req.Subject,
		HTML: req.HTML, Content: req.Content, Builder: req.Builder, Transactional: req.Transactional, MissingValues: req.MissingValues,
		Hidden: req.Hidden != nil && *req.Hidden}
}

func (s *Server) validTemplate(w http.ResponseWriter, r *http.Request, t model.Template) bool {
	err := channel.Validate(t)
	if err == nil {
		err = s.checkTemplateSaveTargets(t)
	}
	if t.Name == "" || err != nil {
		writeErr(w, http.StatusBadRequest, errors.Join(errIf(t.Name == "", "name is required"), err).Error())
		return false
	}
	if c, _ := channel.ParseContent(t.Content); len(c.Attachments) > 0 {
		var ue errUser
		if err := s.checkAttachments(r.Context(), t.TenantID, c.Attachments); errors.As(err, &ue) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return false
		} else if err != nil {
			s.fail(w, err)
			return false
		}
	}
	return true
}

// templateText generates an email's plain-text part from HTML as written (personalization tags stay
// tags), for authors who want to start from it and edit.
func (s *Server) templateText(w http.ResponseWriter, r *http.Request, _ int64) {
	var req struct {
		HTML string `json:"html"`
	}
	if !decode(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": channel.TextFromHTML(req.HTML)})
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req templateReq
	if !decode(w, r, &req) {
		return
	}
	t := req.template(tenant)
	if err := s.prepareTemplateAMP(&t); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.validTemplate(w, r, t) {
		return
	}
	id, err := s.Store.CreateTemplate(r.Context(), t)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request, tenant int64) {
	ch := r.URL.Query().Get("channel")
	if ch != "" && !channel.Valid(ch) {
		writeErr(w, http.StatusBadRequest, "unknown channel")
		return
	}
	ts, err := s.uiStore().ListTemplates(r.Context(), tenant, ch)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": ts})
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.uiStore().Template(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := s.uiStore().Template(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req templateReq
	if !decode(w, r, &req) {
		return
	}
	t := req.template(tenant)
	t.ID, t.Channel = id, cur.Channel // a template's channel is fixed
	if req.Hidden == nil {
		t.Hidden = cur.Hidden
	}
	if err := s.prepareTemplateAMP(&t); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.validTemplate(w, r, t) {
		return
	}
	if err := s.uiStore().UpdateTemplate(r.Context(), t); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.uiStore().DeleteTemplate(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- journeys ----

func (s *Server) listJourneys(w http.ResponseWriter, r *http.Request, tenant int64) {
	js, err := s.uiStore().ListJourneys(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	type row struct {
		model.Journey
		Stats map[string]map[string]int `json:"stats"`
		// HasRun: someone has entered it, so it can be archived but not deleted.
		HasRun bool `json:"has_run"`
	}
	ran := map[int64]bool{}
	if as, ok := s.Store.(JourneyArchiveStore); ok {
		if ran, err = as.JourneysThatRan(r.Context(), tenant); err != nil {
			s.fail(w, err)
			return
		}
	}
	out := make([]row, len(js))
	for i, j := range js {
		st, err := s.Store.JourneyStats(r.Context(), tenant, j.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		j.Problem = journeyProblem(j)
		out[i] = row{j, st, ran[j.ID]}
	}
	writeJSON(w, http.StatusOK, map[string]any{"journeys": out})
}

func (s *Server) getJourney(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := s.uiStore().GetJourney(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	j.Problem = journeyProblem(j)
	writeJSON(w, http.StatusOK, j)
}

// journeyProblem runs the journey workers' own check (journey.ParseGraph) on a live journey's
// saved steps: one that fails it is skipped by the workers, so the app says so instead of showing
// it as running. Drafts and stopped journeys are checked when they're saved or started.
func journeyProblem(j model.Journey) string {
	if j.Status != "active" || j.ArchivedAt != nil { // what the workers load (ActiveJourneys)
		return ""
	}
	if _, err := journey.ParseGraph(j.Graph); err != nil {
		return err.Error()
	}
	return ""
}

func (s *Server) updateJourney(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req journeyReq
	if !decode(w, r, &req) {
		return
	}
	win, ok := req.valid(w, s.now())
	if !ok || !s.journeyRefsOK(w, r, p, id, req.Graph) {
		return
	}
	if err := s.uiStore().UpdateJourney(r.Context(), tenant, id, req.Name, req.Graph, win); err != nil {
		if errors.Is(err, pg.ErrJourneyActive) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	s.journeysChanged(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteJourney(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.uiStore().DeleteJourney(r.Context(), tenant, id); err != nil {
		if !errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	s.journeysChanged(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ---- contacts and dashboard ----

// contactEvents: GET /v1/contacts/{external_id}/events — the contact's timeline, newest first;
// filters ?name=a,b, ?kind=activity|pulse, ?before=<time>, ?limit=N (default 100, at most 200).
func (s *Server) contactEvents(w http.ResponseWriter, r *http.Request, tenant int64) {
	if s.Analytics == nil {
		writeErr(w, http.StatusServiceUnavailable, "analytics unavailable")
		return
	}
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	names, cond, err := eventFilter(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	before, limit, err := pageParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.URL.Query().Get("limit") == "" {
		limit = 100
	}
	var evs []ch.EventRow
	if ca, ok := s.Analytics.(ContactAnalytics); ok && (len(names) > 0 || cond != "" || !before.IsZero() || limit != 100) {
		evs, err = ca.ContactEventsFiltered(r.Context(), tenant, c.ID, names, cond, before, limit)
	} else {
		evs, err = s.Analytics.ContactEvents(r.Context(), tenant, c.ID, limit)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request, tenant int64) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 14
	}
	counts, err := s.uiStore().Counts(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"counts": counts, "days": days}
	if s.Analytics != nil {
		o, err := s.Analytics.Overview(r.Context(), tenant, days)
		if err != nil {
			s.fail(w, err)
			return
		}
		out["activity"] = o
	}
	writeJSON(w, http.StatusOK, out)
}

// journeyReq is the body of journey create and update.
type journeyReq struct {
	Name    string          `json:"name"`
	Graph   json.RawMessage `json:"graph"`
	StartAt *time.Time      `json:"start_at"` // required in the app; API callers that omit it start now
	EndAt   *time.Time      `json:"end_at"`   // empty: runs until stopped
}

func (req journeyReq) valid(w http.ResponseWriter, now time.Time) (schedule.Window, bool) {
	win := schedule.Window{Start: now, End: req.EndAt}
	if req.StartAt != nil {
		win.Start = *req.StartAt
	}
	_, err := journey.ParseGraph(req.Graph)
	err = errors.Join(errIf(req.Name == "", "name is required"), err, win.Validate())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return win, false
	}
	return win, true
}
