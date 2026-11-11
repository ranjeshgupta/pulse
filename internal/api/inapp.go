package api

// In-app campaigns: managed in the app, fetched by the app SDK (site key) for the current visitor.

import (
	"context"
	"net/http"
	"strconv"
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
	mux.HandleFunc("GET /v1/inapp/check", s.tenant(s.checkInApp))
	mux.HandleFunc("POST /v1/inapp/{id}/test", s.tenant(s.testInApp))
	mux.HandleFunc("OPTIONS /v1/web/inapp", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/inapp", s.webInApp)
}

func (s *Server) inappStore() InAppStore { return s.Store.(InAppStore) }

const inappTestTTL = 15 * time.Minute

func inappTestKey(tenant int64, externalID string) string {
	return "inapp:test:" + strconv.FormatInt(tenant, 10) + ":" + externalID
}

// testInApp queues a campaign (draft or live) for one contact: their app shows it on its next
// fetch within 15 minutes, once, whatever its targeting.
func (s *Server) testInApp(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ExternalID string `json:"external_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if _, err := s.inappStore().GetInApp(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.Store.ContactByExternalID(r.Context(), tenant, req.ExternalID); err != nil {
		s.fail(w, err)
		return
	}
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "test sends need Redis")
		return
	}
	if err := s.RDB.Set(r.Context(), inappTestKey(tenant, req.ExternalID), id, inappTestTTL).Err(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": true, "expires_in_seconds": int(inappTestTTL.Seconds())})
}

// testCampaigns returns (and uses up) a campaign queued for this visitor by testInApp.
func (s *Server) testCampaigns(ctx context.Context, tenant int64, externalID string) []model.InAppMessage {
	if s.RDB == nil {
		return nil
	}
	id, err := s.RDB.GetDel(ctx, inappTestKey(tenant, externalID)).Int64()
	if err != nil {
		return nil
	}
	m, err := s.inappStore().GetInApp(ctx, tenant, id)
	if err != nil {
		return nil
	}
	m.Targeting.Frequency, m.Targeting.DelaySeconds = "always", 0
	return []model.InAppMessage{m}
}

// checkInApp lists the live campaigns a contact would get on a platform now (?external_id=&platform=).
func (s *Server) checkInApp(w http.ResponseWriter, r *http.Request, tenant int64) {
	q := r.URL.Query()
	platform := q.Get("platform")
	if platform != "android" && platform != "ios" {
		platform = "android"
	}
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, q.Get("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ms, err := s.inappStore().ListInApp(r.Context(), tenant, true)
	if err != nil {
		s.fail(w, err)
		return
	}
	member := map[int64]bool{}
	for _, seg := range inapp.Segments(ms) {
		if in, err := s.Members.IsMember(r.Context(), tenant, seg, c.ID); err == nil && in {
			member[seg] = true
		}
	}
	out := inapp.For(ms, platform, s.now(), func(id int64) bool { return member[id] })
	writeJSON(w, http.StatusOK, map[string]any{"messages": out, "platform": platform})
}

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
// A signed-in user's segments only count with their user hash (as for the inbox, whatever the
// workspace's verification setting); without it they get what an anonymous visitor gets, so the
// public site key can't reveal someone's segments.
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
	if !strings.HasPrefix(req.ID, "anon-") && !validUserHash(t.IdentitySecret, req.ID, req.UserHash) {
		req.ID = ""
	}
	if !s.allowWeb(r.Context(), t.ID, r, 1) {
		writeErr(w, http.StatusTooManyRequests, "too many requests from this app")
		return
	}
	ms, err := s.inappStore().ListInApp(r.Context(), t.ID, true)
	if err != nil {
		s.fail(w, err)
		return
	}
	member := map[int64]bool{}
	// Personalised text shows the contact's own data, so it needs a verified identity (identity
	// secret + user hash); everyone else gets the variables' defaults, as an anonymous visitor.
	verified := verifiedIdentity(t, strings.TrimSpace(req.ID), req.UserHash)
	who := model.Contact{Attrs: map[string]any{}}
	if id := strings.TrimSpace(req.ID); id != "" {
		if c, err := s.Store.ContactByExternalID(r.Context(), t.ID, id); err == nil {
			if verified {
				who = c
			}
			for _, seg := range inapp.Segments(ms) {
				if in, err := s.Members.IsMember(r.Context(), t.ID, seg, c.ID); err == nil && in {
					member[seg] = true
				}
			}
		}
	}
	out := inapp.For(ms, req.Platform, s.now(), func(id int64) bool { return member[id] })
	if req.ID != "" {
		out = append(s.testCampaigns(r.Context(), t.ID, req.ID), out...)
	}
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
		items[i] = item{ID: m.ID, Layout: m.Layout, Content: inapp.Personalize(m.Content, who), Targeting: tg, EndAt: m.EndAt}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"messages": items})
}
