package api

// M6 on-site: ordered content variants and focus items, and the overlap view.
//
//	POST /v1/dwc/overlap         {slot_id} or {variants}: how the variants' audiences overlap (content view)
//	POST /v1/focus-items/overlap {kind} or {ids}: the same for live focus items of a kind, in order (content view)
//	PUT  /v1/focus-items/order   {ids}: their order (priority) (content edit)

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/onsite"
)

const (
	overlapCacheTTL   = time.Minute
	overlapPerMinute  = 30 // uncached overlap counts per workspace per minute
	maxFocusOrderSize = 500
)

// FocusOrderStore sets focus items' priorities (pg.Store).
type FocusOrderStore interface {
	SetFocusPriorities(ctx context.Context, tenant int64, ids []int64) error
}

func newVariantID() string { return "v" + auth.NewToken("")[:8] }

// onsiteSegmentsOK: every segment a rule names belongs to the workspace.
func (s *Server) onsiteSegmentsOK(w http.ResponseWriter, r *http.Request, tenant int64, ids []int64) bool {
	var ue errUser
	if err := s.segmentsOK(r.Context(), tenant, ids); errors.As(err, &ue) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	} else if err != nil {
		s.fail(w, err)
		return false
	}
	return true
}

type overlapResp struct {
	onsite.Overlap
	Names             []string        `json:"names"`
	Shadowed          []onsite.Shadow `json:"shadowed"`
	DefaultNeverShown bool            `json:"default_never_shown"`
	Cached            bool            `json:"cached"`
}

// overlap counts (cached a minute per set of audiences; uncached counts are rate-limited).
func (s *Server) overlap(w http.ResponseWriter, r *http.Request, tenant int64, names []string, auds []model.OnsiteAudience) {
	if len(auds) == 0 {
		writeErr(w, http.StatusBadRequest, "add a variant first")
		return
	}
	if len(auds) > onsite.MaxOverlapVariants {
		writeErr(w, http.StatusBadRequest, "at most "+strconv.Itoa(onsite.MaxOverlapVariants)+" variants can be compared")
		return
	}
	var segs []int64
	for i, a := range auds {
		if err := onsite.ValidateAudience("variant "+strconv.Itoa(i+1), a); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		segs = append(append(segs, a.SegmentIDs...), a.ExcludeSegmentIDs...)
	}
	if !s.onsiteSegmentsOK(w, r, tenant, segs) {
		return
	}
	sh, def := onsite.Shadowed(auds)
	resp := overlapResp{Names: names, Shadowed: sh, DefaultNeverShown: def}
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "counts need Redis")
		return
	}
	raw, _ := json.Marshal(auds)
	sum := sha256.Sum256(raw)
	key := "ovl:" + strconv.FormatInt(tenant, 10) + ":" + hex.EncodeToString(sum[:12])
	if b, err := s.RDB.Get(r.Context(), key).Bytes(); err == nil && json.Unmarshal(b, &resp.Overlap) == nil {
		resp.Cached = true
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if !s.allowTenant(r.Context(), "ovlrl", tenant, overlapPerMinute) {
		writeErr(w, http.StatusTooManyRequests, "too many overlap counts; try again in a minute")
		return
	}
	o, err := onsite.CountOverlap(r.Context(), s.RDB, tenant, auds)
	if err != nil {
		s.fail(w, err)
		return
	}
	if b, err := json.Marshal(o); err == nil {
		s.RDB.Set(r.Context(), key, b, overlapCacheTTL)
	}
	resp.Overlap = o
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) dwcOverlap(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		SlotID   int64              `json:"slot_id"`
		Variants []model.DWCVariant `json:"variants"`
	}
	if !decode(w, r, &req) {
		return
	}
	vs := req.Variants
	if req.SlotID != 0 {
		d, err := s.onsite().GetDWCSlot(r.Context(), tenant, req.SlotID)
		if err != nil {
			s.fail(w, err)
			return
		}
		vs = d.Variants
	}
	names := make([]string, len(vs))
	for i, v := range vs {
		names[i] = v.Name
	}
	s.overlap(w, r, tenant, names, onsite.SlotAudiences(vs))
}

func (s *Server) focusOverlap(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Kind string  `json:"kind"`
		IDs  []int64 `json:"ids"`
	}
	if !decode(w, r, &req) {
		return
	}
	items, err := s.onsite().ListFocusItems(r.Context(), tenant, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	var picked []model.FocusItem
	if len(req.IDs) > 0 {
		byID := map[int64]model.FocusItem{}
		for _, it := range items {
			byID[it.ID] = it
		}
		for _, id := range req.IDs {
			if it, ok := byID[id]; ok {
				picked = append(picked, it)
			}
		}
	} else {
		for _, it := range items {
			if it.Kind == req.Kind && it.Status == "active" {
				picked = append(picked, it)
			}
		}
		onsite.SortFocus(picked)
	}
	names := make([]string, len(picked))
	auds := make([]model.OnsiteAudience, len(picked))
	for i, it := range picked {
		names[i], auds[i] = it.Name, it.Targeting.Audience()
	}
	s.overlap(w, r, tenant, names, auds)
}

// focusOrder sets the order focus items are tried in: the first gets priority 1, and so on.
func (s *Server) focusOrder(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxFocusOrderSize {
		writeErr(w, http.StatusBadRequest, "send the focus items in their new order")
		return
	}
	st, ok := s.Store.(FocusOrderStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "ordering is not available")
		return
	}
	if err := st.SetFocusPriorities(r.Context(), tenant, req.IDs); err != nil {
		s.fail(w, err)
		return
	}
	s.forgetOnsite(tenant)
	w.WriteHeader(http.StatusNoContent)
}
