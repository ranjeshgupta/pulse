package api

// On-site content: focus items and Dynamic Web Content (managed in the app) and the public decision
// API the web SDK calls from customer websites with the workspace's site key.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"pulse/internal/event"
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
	mux.HandleFunc("POST /v1/dwc/overlap", s.tenant(s.dwcOverlap))
	mux.HandleFunc("POST /v1/focus-items/overlap", s.tenant(s.focusOverlap))
	mux.HandleFunc("PUT /v1/focus-items/order", s.tenant(s.focusOrder))

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
	if !s.onsiteSegmentsOK(w, r, tenant, append(slices.Clone(f.Targeting.SegmentIDs), f.Targeting.ExcludeSegmentIDs...)) {
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
	writeJSON(w, http.StatusOK, withShadows(d))
}

// dwcView is a slot with the variants that can never be shown.
type dwcView struct {
	model.DWCSlot
	Shadowed          []onsite.Shadow `json:"shadowed"`
	DefaultNeverShown bool            `json:"default_never_shown"`
}

func withShadows(d model.DWCSlot) dwcView {
	sh, def := onsite.Shadowed(onsite.SlotAudiences(d.Variants))
	return dwcView{DWCSlot: d, Shadowed: sh, DefaultNeverShown: def}
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
	if d.Platform == "" {
		d.Platform = onsite.PlatformWeb
	}
	if err := onsite.ValidateDWC(d); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Pre-M6 variants (one segment) move into their audience; every variant gets a stable id.
	used := map[string]bool{}
	for _, v := range d.Variants {
		used[v.ID] = true
	}
	var segs []int64
	for i := range d.Variants {
		v := &d.Variants[i]
		v.Audience, v.SegmentID = v.Rules(), 0
		for v.ID == "" {
			if id := newVariantID(); !used[id] {
				v.ID = id
			}
		}
		used[v.ID] = true
		segs = append(append(segs, v.Audience.SegmentIDs...), v.Audience.ExcludeSegmentIDs...)
	}
	if !s.onsiteSegmentsOK(w, r, tenant, segs) {
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
	d.ID = id
	v := withShadows(d)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "variants": d.Variants, "shadowed": v.Shadowed, "default_never_shown": v.DefaultNeverShown})
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
		Device     string   `json:"device"` // desktop | mobile | tablet, from the SDK
	}
	if !decode(w, r, &req) {
		return
	}
	if !webIdentityOK(t, req.ExternalID, req.UserHash) {
		req.ExternalID = "" // unverified: decide as for an anonymous visitor, never reveal someone's segments
	}
	verified := verifiedIdentity(t, strings.TrimSpace(req.ExternalID), req.UserHash)
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
	var known *model.Contact
	if id := strings.TrimSpace(req.ExternalID); id != "" {
		if c, err := s.Store.ContactByExternalID(r.Context(), t.ID, id); err == nil {
			known = &c
			for _, seg := range onsite.Segments(set.items, set.slots) {
				if in, err := s.Members.IsMember(r.Context(), t.ID, seg, c.ID); err == nil && in {
					member[seg] = true
				}
			}
		}
	}
	v := onsite.Visitor{PageURL: req.URL, Device: req.Device, IsMember: func(id int64) bool { return member[id] }}
	if known != nil && verified {
		v.Attrs = known.Attrs // contact-field rules read the contact's data: verified identities only (M2)
	}
	d := onsite.DecideFor(set.items, set.slots, req.Slots, s.now(), v)
	if known != nil {
		s.journeyOnsite(r.Context(), t.ID, *known, req.URL, req.Slots, verified, &d)
	}
	writeJSON(w, http.StatusOK, d)
}

// journeyOnsite applies what journeys set for a known contact: focus items queued by "show focus
// item" steps (shown on this page view, once), content pushed into slots by "push dynamic content"
// steps (it wins over the slot's own content), and dwc_requested for every requested slot, which
// "requests dynamic content" decisions wait for.
// Content a step personalised with the contact's data is shown only to a verified identity
// (verified); other visitors get its anonymous version (the variables' defaults).
func (s *Server) journeyOnsite(ctx context.Context, tenant int64, c model.Contact, pageURL string, slots []string, verified bool, d *onsite.Decision) {
	if s.RDB == nil {
		return
	}
	p := onsite.Pushes{RDB: s.RDB}
	if ids, err := p.TakeFocus(ctx, tenant, c.ID); err == nil {
		shown := map[int64]bool{}
		for _, f := range d.Focus {
			shown[f.ID] = true
		}
		for _, id := range ids {
			if shown[id] {
				continue
			}
			if f, err := s.onsite().GetFocusItem(ctx, tenant, id); err == nil {
				d.Focus = append(d.Focus, f)
				shown[id] = true
			}
		}
	} else {
		s.Log.Error("journey focus items", "err", err)
	}
	var wanted []string
	for _, sl := range slots {
		if slotKey.MatchString(sl) {
			wanted = append(wanted, sl)
		}
	}
	if len(wanted) == 0 {
		return
	}
	if pushed, err := p.DWC(ctx, tenant, c.ID, wanted); err == nil {
		for k, v := range pushed {
			d.Content[k] = onsite.ResolveDWC(v, verified)
		}
	} else {
		s.Log.Error("journey dynamic content", "err", err)
	}
	evs := make([]event.Event, len(wanted))
	for i, sl := range wanted {
		evs[i] = event.Event{ExternalID: c.ExternalID, Name: event.DWCRequested, Props: map[string]any{"slot": sl, "url": pageURL}}
	}
	if err := s.publishEvents(ctx, tenant, evs); err != nil {
		s.Log.Error("record dwc_requested", "err", err)
	}
}

var slotKey = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
