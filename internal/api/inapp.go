package api

// In-app campaigns: managed in the app, fetched by the app SDK (site key) for the current visitor.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"pulse/internal/inapp"
	"pulse/internal/model"
)

type InAppStore interface {
	ListInApp(ctx context.Context, tenant int64, activeOnly bool) ([]model.InAppMessage, error)
	GetInApp(ctx context.Context, tenant, id int64) (model.InAppMessage, error)
	SaveInApp(ctx context.Context, m model.InAppMessage) (int64, error)
	DeleteInApp(ctx context.Context, tenant, id int64) error
}

type InAppAnalytics interface {
	InAppStats(ctx context.Context, tenant int64) (map[int64]map[string]uint64, error)
}

func (s *Server) inappRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/inapp", s.tenant(s.listInApp))
	mux.HandleFunc("POST /v1/inapp", s.tenant(s.saveInApp))
	mux.HandleFunc("GET /v1/inapp/{id}", s.tenant(s.getInApp))
	mux.HandleFunc("PUT /v1/inapp/{id}", s.tenant(s.saveInApp))
	mux.HandleFunc("DELETE /v1/inapp/{id}", s.tenant(s.deleteInApp))
	mux.HandleFunc("OPTIONS /v1/web/inapp", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/inapp", s.webInApp)
}

func (s *Server) inappStore() InAppStore { return s.Store.(InAppStore) }

func (s *Server) listInApp(w http.ResponseWriter, r *http.Request, tenant int64) {
	ms, err := s.inappStore().ListInApp(r.Context(), tenant, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ms == nil {
		ms = []model.InAppMessage{}
	}
	stats := map[int64]map[string]uint64{}
	if a, ok := s.Analytics.(InAppAnalytics); ok {
		if st, err := a.InAppStats(r.Context(), tenant); err == nil {
			stats = st
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": ms, "stats": stats})
}

func (s *Server) getInApp(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := s.inappStore().GetInApp(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) saveInApp(w http.ResponseWriter, r *http.Request, tenant int64) {
	var m model.InAppMessage
	if !decode(w, r, &m) {
		return
	}
	m.TenantID, m.ID = tenant, 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		m.ID = id
	}
	if m.Status != "active" {
		m.Status = "draft"
	}
	if m.StartAt.IsZero() {
		m.StartAt = s.now()
	}
	if m.Targeting.Frequency == "" {
		m.Targeting.Frequency = "once"
	}
	if err := inapp.Validate(m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, id := range inapp.Segments([]model.InAppMessage{m}) {
		if _, err := s.Store.GetSegment(r.Context(), tenant, id); err != nil {
			writeErr(w, http.StatusBadRequest, "a targeted segment doesn't exist")
			return
		}
	}
	id, err := s.inappStore().SaveInApp(r.Context(), m)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteInApp(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.inappStore().DeleteInApp(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// webInApp answers the SDK: the campaigns this visitor may see on this platform, with their triggers.
// An unverified known user is treated as anonymous, so nobody learns someone else's segments.
func (s *Server) webInApp(w http.ResponseWriter, r *http.Request) {
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	var req struct {
		ID       string `json:"id"`
		UserHash string `json:"user_hash"`
		Platform string `json:"platform"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Platform != "android" && req.Platform != "ios" {
		writeErr(w, http.StatusBadRequest, "platform must be android or ios")
		return
	}
	if !webIdentityOK(t, req.ID, req.UserHash) {
		req.ID = ""
	}
	ms, err := s.inappStore().ListInApp(r.Context(), t.ID, true)
	if err != nil {
		s.fail(w, err)
		return
	}
	member := map[int64]bool{}
	if id := strings.TrimSpace(req.ID); id != "" {
		if c, err := s.Store.ContactByExternalID(r.Context(), t.ID, id); err == nil {
			for _, seg := range inapp.Segments(ms) {
				if in, err := s.Members.IsMember(r.Context(), t.ID, seg, c.ID); err == nil && in {
					member[seg] = true
				}
			}
		}
	}
	out := inapp.For(ms, req.Platform, s.now(), func(id int64) bool { return member[id] })
	type item struct {
		ID        int64                `json:"id"`
		Layout    string               `json:"layout"`
		Content   model.InAppContent   `json:"content"`
		Targeting model.InAppTargeting `json:"trigger"`
		EndAt     *time.Time           `json:"end_at,omitempty"`
	}
	items := make([]item, len(out))
	for i, m := range out {
		tg := m.Targeting
		tg.SegmentIDs, tg.ExcludeSegmentIDs = nil, nil // the app doesn't need the workspace's segment ids
		items[i] = item{ID: m.ID, Layout: m.Layout, Content: m.Content, Targeting: tg, EndAt: m.EndAt}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"messages": items})
}
