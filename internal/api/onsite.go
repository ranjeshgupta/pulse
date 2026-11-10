package api

// On-site content: focus items and Dynamic Web Content (managed in the app) and the public decision
// API the web SDK calls from customer websites with the workspace's site key.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"pulse/internal/model"
	"pulse/internal/onsite"
	"pulse/internal/store/pg"
)

const onsiteCacheTTL = 10 * time.Second

type OnsiteStore interface {
	ListFocusItems(ctx context.Context, tenant int64, activeOnly bool) ([]model.FocusItem, error)
	GetFocusItem(ctx context.Context, tenant, id int64) (model.FocusItem, error)
	SaveFocusItem(ctx context.Context, f model.FocusItem) (int64, error)
	DeleteFocusItem(ctx context.Context, tenant, id int64) error
	ListDWCSlots(ctx context.Context, tenant int64, activeOnly bool) ([]model.DWCSlot, error)
	GetDWCSlot(ctx context.Context, tenant, id int64) (model.DWCSlot, error)
	SaveDWCSlot(ctx context.Context, d model.DWCSlot) (int64, error)
	DeleteDWCSlot(ctx context.Context, tenant, id int64) error
}

func (s *Server) onsiteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/focus-items", s.tenant(s.listFocus))
	mux.HandleFunc("POST /v1/focus-items", s.tenant(s.saveFocus))
	mux.HandleFunc("GET /v1/focus-items/{id}", s.tenant(s.getFocus))
	mux.HandleFunc("PUT /v1/focus-items/{id}", s.tenant(s.saveFocus))
	mux.HandleFunc("DELETE /v1/focus-items/{id}", s.tenant(s.deleteFocus))
	mux.HandleFunc("GET /v1/dwc", s.tenant(s.listDWC))
	mux.HandleFunc("POST /v1/dwc", s.tenant(s.saveDWC))
	mux.HandleFunc("GET /v1/dwc/{id}", s.tenant(s.getDWC))
	mux.HandleFunc("PUT /v1/dwc/{id}", s.tenant(s.saveDWC))
	mux.HandleFunc("DELETE /v1/dwc/{id}", s.tenant(s.deleteDWC))

	mux.HandleFunc("OPTIONS /v1/web/decide", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/decide", s.decide)
}

func (s *Server) onsite() OnsiteStore { return s.Store.(OnsiteStore) }

func (s *Server) listFocus(w http.ResponseWriter, r *http.Request, tenant int64) {
	items, err := s.onsite().ListFocusItems(r.Context(), tenant, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"focus_items": items})
}

func (s *Server) getFocus(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	f, err := s.onsite().GetFocusItem(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) saveFocus(w http.ResponseWriter, r *http.Request, tenant int64) {
	var f model.FocusItem
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
	if f.Status == "" {
		f.Status = "draft"
	}
	if f.StartAt.IsZero() {
		f.StartAt = s.now() // API callers that omit it start now; the app always sends it
	}
	if err := onsite.ValidateFocus(f); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.onsite().SaveFocusItem(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.forgetOnsite(tenant)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteFocus(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.onsite().DeleteFocusItem(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	s.forgetOnsite(tenant)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listDWC(w http.ResponseWriter, r *http.Request, tenant int64) {
	slots, err := s.onsite().ListDWCSlots(r.Context(), tenant, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (s *Server) getDWC(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := s.onsite().GetDWCSlot(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) saveDWC(w http.ResponseWriter, r *http.Request, tenant int64) {
	var d model.DWCSlot
	if !decode(w, r, &d) {
		return
	}
	d.TenantID, d.ID = tenant, 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		d.ID = id
	}
	if d.Status == "" {
		d.Status = "draft"
	}
	if d.StartAt.IsZero() {
		d.StartAt = s.now()
	}
	if d.Variants == nil {
		d.Variants = []model.DWCVariant{}
	}
	if err := onsite.ValidateDWC(d); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.onsite().SaveDWCSlot(r.Context(), d)
	if err != nil {
		if errors.Is(err, pg.ErrConflict) {
			writeErr(w, http.StatusConflict, "a slot with this key already exists")
			return
		}
		s.fail(w, err)
		return
	}
	s.forgetOnsite(tenant)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteDWC(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.onsite().DeleteDWCSlot(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	s.forgetOnsite(tenant)
	w.WriteHeader(http.StatusNoContent)
}

// ---- public decision API ----

type onsiteSet struct {
	items []model.FocusItem
	slots []model.DWCSlot
	exp   time.Time
}

var onsiteCache = struct {
	sync.Mutex
	m map[int64]onsiteSet
}{m: map[int64]onsiteSet{}}

func (s *Server) forgetOnsite(tenant int64) {
	onsiteCache.Lock()
	delete(onsiteCache.m, tenant)
	onsiteCache.Unlock()
}

// activeOnsite caches a workspace's live items for a few seconds: the decision API is hit on every page view.
func (s *Server) activeOnsite(ctx context.Context, tenant int64) (onsiteSet, error) {
	onsiteCache.Lock()
	set, ok := onsiteCache.m[tenant]
	onsiteCache.Unlock()
	if ok && s.now().Before(set.exp) {
		return set, nil
	}
	items, err := s.onsite().ListFocusItems(ctx, tenant, true)
	if err != nil {
		return set, err
	}
	slots, err := s.onsite().ListDWCSlots(ctx, tenant, true)
	if err != nil {
		return set, err
	}
	set = onsiteSet{items: items, slots: slots, exp: s.now().Add(onsiteCacheTTL)}
	onsiteCache.Lock()
	onsiteCache.m[tenant] = set
	onsiteCache.Unlock()
	return set, nil
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*") // public, credential-less API keyed by the site key
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Vary", "Origin")
	h.Set("Access-Control-Allow-Headers", "Content-Type, X-Pulse-Site-Key")
	h.Set("Access-Control-Max-Age", "86400")
}

func (s *Server) corsPreflight(w http.ResponseWriter, _ *http.Request) {
	cors(w)
	w.WriteHeader(http.StatusNoContent)
}

// decide answers "what should this visitor see on this page?". Body: {external_id?, url, slots: [...]}.
// Only the site key is needed, so it never exposes profile data: just content the workspace chose to show.
func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	cors(w)
	t, err := s.Store.TenantBySiteKey(r.Context(), r.Header.Get("X-Pulse-Site-Key"))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid site key")
		return
	}
	var req struct {
		ExternalID string   `json:"external_id"`
		UserHash   string   `json:"user_hash"`
		URL        string   `json:"url"`
		Slots      []string `json:"slots"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !webIdentityOK(t, req.ExternalID, req.UserHash) {
		req.ExternalID = "" // unverified: decide as for an anonymous visitor, never reveal someone's segments
	}
	if len(req.Slots) > 50 {
		writeErr(w, http.StatusBadRequest, "at most 50 slots per request")
		return
	}
	set, err := s.activeOnsite(r.Context(), t.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	member := map[int64]bool{}
	if id := strings.TrimSpace(req.ExternalID); id != "" {
		if c, err := s.Store.ContactByExternalID(r.Context(), t.ID, id); err == nil {
			for _, seg := range onsite.Segments(set.items, set.slots) {
				if in, err := s.Members.IsMember(r.Context(), t.ID, seg, c.ID); err == nil && in {
					member[seg] = true
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, onsite.Decide(set.items, set.slots, req.Slots, req.URL, s.now(), func(id int64) bool { return member[id] }))
}
