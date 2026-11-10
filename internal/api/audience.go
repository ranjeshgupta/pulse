package api

// Audience data: static lists, tags, points and scoring rules, attribute definitions, identity
// verification for the web SDK, and the checks that keep segment references valid.
//
// Lists, tags and points change through events (list_joined, tag_added, points_changed, ...), so a
// change is ordered with the contact's other events, creates the contact if needed, shows on the
// timeline and moves segments and journeys at once, exactly like live data.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/importer"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/profile"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
)

const maxListBatch = 10000

type AudienceStore interface {
	ListSegmentsOfKind(ctx context.Context, tenant int64, kind string) ([]model.Segment, error)
	CreateList(ctx context.Context, tenant int64, name string) (int64, error)
	UpdateList(ctx context.Context, tenant, id int64, name string, public *bool, description *string) error
	DistinctTags(ctx context.Context, tenant int64) ([]string, error)
	AttributeDefs(ctx context.Context, tenant int64) ([]pg.AttributeDef, error)
	PutAttributeDef(ctx context.Context, tenant int64, d pg.AttributeDef) error
	DeleteAttributeDef(ctx context.Context, tenant int64, key string) error
	AttrKeys(ctx context.Context, tenant int64) ([]string, error)
	ListPointRules(ctx context.Context, tenant int64) ([]pg.PointRule, error)
	PutPointRule(ctx context.Context, tenant int64, ev string, points int) (int64, error)
	DeletePointRule(ctx context.Context, tenant, id int64) error
	IdentitySettings(ctx context.Context, tenant int64) (pg.IdentitySettings, error)
	SetIdentityRequired(ctx context.Context, tenant int64, required bool) error
	RotateIdentitySecret(ctx context.Context, tenant int64) (string, error)
}

func (s *Server) audience() AudienceStore { return s.Store.(AudienceStore) }

func (s *Server) audienceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/lists", s.tenant(s.listLists))
	mux.HandleFunc("POST /v1/lists", s.tenant(s.createList))
	mux.HandleFunc("PUT /v1/lists/{id}", s.tenant(s.renameList))
	mux.HandleFunc("POST /v1/lists/{id}/members", s.tenant(s.listMembers))

	mux.HandleFunc("GET /v1/tags", s.tenant(s.listTags))
	mux.HandleFunc("POST /v1/contacts/{external_id}/tags", s.tenant(s.contactTags))
	mux.HandleFunc("POST /v1/contacts/{external_id}/points", s.tenant(s.contactPoints))

	mux.HandleFunc("GET /v1/scoring/rules", s.tenant(s.listPointRules))
	mux.HandleFunc("PUT /v1/scoring/rules", s.tenant(s.putPointRule))
	mux.HandleFunc("DELETE /v1/scoring/rules/{id}", s.tenant(s.deletePointRule))

	mux.HandleFunc("GET /v1/attributes", s.tenant(s.listAttributes))
	mux.HandleFunc("PUT /v1/attributes/{key}", s.tenant(s.putAttribute))
	mux.HandleFunc("DELETE /v1/attributes/{key}", s.tenant(s.deleteAttribute))

	mux.HandleFunc("GET /v1/settings/identity", s.admin(s.getIdentity))
	mux.HandleFunc("PUT /v1/settings/identity", s.admin(s.putIdentity))
	mux.HandleFunc("POST /v1/settings/identity/rotate", s.admin(s.rotateIdentity))
}

// publishEvents normalizes and publishes platform events for the tenant.
func (s *Server) publishEvents(ctx context.Context, tenant int64, evs []event.Event) error {
	now := s.now()
	recs := make([]*kgo.Record, 0, len(evs))
	for i := range evs {
		e := &evs[i]
		e.TenantID = tenant
		if err := e.Normalize(now); err != nil {
			return err
		}
		rec, err := kafka.JSON(event.TopicRaw, e.RawKey(), e)
		if err != nil {
			return err
		}
		recs = append(recs, rec)
	}
	return s.Pub.Publish(ctx, recs...)
}

// ---- static lists ----

func (s *Server) listLists(w http.ResponseWriter, r *http.Request, tenant int64) {
	lists, err := s.audience().ListSegmentsOfKind(r.Context(), tenant, "static")
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range lists {
		if lists[i].Size, err = s.Members.Size(r.Context(), tenant, lists[i].ID); err != nil {
			s.fail(w, err)
			return
		}
	}
	if lists == nil {
		lists = []model.Segment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lists": lists})
}

func (s *Server) createList(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	id, err := s.audience().CreateList(r.Context(), tenant, strings.TrimSpace(req.Name))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) renameList(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        string  `json:"name"`
		Public      *bool   `json:"public"`      // offer it on the preference center
		Description *string `json:"description"` // what subscribers get, shown there
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Description != nil && len(*req.Description) > 500 {
		writeErr(w, http.StatusBadRequest, "keep the description under 500 characters")
		return
	}
	if err := s.audience().UpdateList(r.Context(), tenant, id, strings.TrimSpace(req.Name), req.Public, req.Description); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// listMembers adds contacts to a list or removes them. Unknown external ids are created, like
// any other event for a new contact.
func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ExternalIDs []string `json:"external_ids"`
		Action      string   `json:"action"` // add | remove
	}
	if !decode(w, r, &req) {
		return
	}
	name := map[string]string{"add": event.ListJoined, "remove": event.ListLeft}[req.Action]
	if name == "" || len(req.ExternalIDs) == 0 || len(req.ExternalIDs) > maxListBatch {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("send action add or remove and 1-%d external_ids", maxListBatch))
		return
	}
	if !s.isList(w, r, tenant, id) {
		return
	}
	evs := make([]event.Event, 0, len(req.ExternalIDs))
	for _, ext := range req.ExternalIDs {
		if ext = strings.TrimSpace(ext); ext != "" {
			evs = append(evs, event.Event{ExternalID: ext, Name: name, Props: map[string]any{"list_id": id, "source": "manual"}})
		}
	}
	if err := s.publishEvents(r.Context(), tenant, evs); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(evs)})
}

func (s *Server) isList(w http.ResponseWriter, r *http.Request, tenant, id int64) bool {
	seg, err := s.Store.GetSegment(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return false
	}
	if seg.Kind != "static" {
		writeErr(w, http.StatusBadRequest, "that is a rule-based segment; only lists take members by hand")
		return false
	}
	return true
}

// ---- tags and points ----

func (s *Server) listTags(w http.ResponseWriter, r *http.Request, tenant int64) {
	tags, err := s.audience().DistinctTags(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if tags == nil {
		tags = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) contactTags(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if !decode(w, r, &req) {
		return
	}
	for _, t := range append(append([]string{}, req.Add...), req.Remove...) {
		if !segment.ValidTag(t) {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("tag %q: use letters, digits, - and _ (up to 64)", t))
			return
		}
	}
	ext := r.PathValue("external_id")
	var evs []event.Event
	if len(req.Add) > 0 {
		evs = append(evs, event.Event{ExternalID: ext, Name: event.TagAdded, Props: map[string]any{"tags": req.Add, "source": "manual"}})
	}
	if len(req.Remove) > 0 {
		evs = append(evs, event.Event{ExternalID: ext, Name: event.TagRemoved, Props: map[string]any{"tags": req.Remove, "source": "manual"}})
	}
	if len(evs) == 0 {
		writeErr(w, http.StatusBadRequest, "send tags to add or remove")
		return
	}
	if err := s.publishEvents(r.Context(), tenant, evs); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(evs)})
}

func (s *Server) contactPoints(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Delta  int    `json:"delta"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Delta == 0 || req.Delta > 1_000_000 || req.Delta < -1_000_000 {
		writeErr(w, http.StatusBadRequest, "delta must be a non-zero whole number")
		return
	}
	ev := event.Event{ExternalID: r.PathValue("external_id"), Name: event.PointsChanged,
		Props: map[string]any{"delta": req.Delta, "reason": req.Reason, "source": "manual"}}
	if err := s.publishEvents(r.Context(), tenant, []event.Event{ev}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": 1})
}

// ---- scoring rules ----

func (s *Server) listPointRules(w http.ResponseWriter, r *http.Request, tenant int64) {
	rules, err := s.audience().ListPointRules(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rules == nil {
		rules = []pg.PointRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) putPointRule(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Event  string `json:"event"`
		Points int    `json:"points"`
	}
	if !decode(w, r, &req) {
		return
	}
	switch {
	case !event.ValidName(req.Event):
		writeErr(w, http.StatusBadRequest, "event: use letters, digits, _ . : - (starting with a letter)")
		return
	case req.Event == event.PointsChanged || event.ServerOnly(req.Event):
		writeErr(w, http.StatusBadRequest, "points can't be given for that event")
		return
	case req.Points == 0 || req.Points > 100000 || req.Points < -100000:
		writeErr(w, http.StatusBadRequest, "points must be a non-zero whole number up to 100000")
		return
	}
	id, err := s.audience().PutPointRule(r.Context(), tenant, req.Event, req.Points)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.bumpScoring(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deletePointRule(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.audience().DeletePointRule(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	s.bumpScoring(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// bumpScoring tells profile workers to reload point rules (see profile.ScoringVersionKey).
func (s *Server) bumpScoring(ctx context.Context) {
	if s.RDB != nil {
		if err := s.RDB.Incr(ctx, profile.ScoringVersionKey).Err(); err != nil {
			s.Log.Warn("bump scoring version", "err", err)
		}
	}
}

// ---- attribute definitions ----

var attrTypes = map[string]bool{"string": true, "number": true, "boolean": true, "date": true}

// listAttributes returns the defined attributes and every other key seen on contacts.
func (s *Server) listAttributes(w http.ResponseWriter, r *http.Request, tenant int64) {
	defs, err := s.audience().AttributeDefs(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	keys, err := s.audience().AttrKeys(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	defined := map[string]bool{}
	for _, d := range defs {
		defined[d.Key] = true
	}
	undefined := []string{}
	for _, k := range keys {
		if !defined[k] {
			undefined = append(undefined, k)
		}
	}
	if defs == nil {
		defs = []pg.AttributeDef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"attributes": defs, "undefined": undefined})
}

func (s *Server) putAttribute(w http.ResponseWriter, r *http.Request, tenant int64) {
	var d pg.AttributeDef
	if !decode(w, r, &d) {
		return
	}
	d.Key = r.PathValue("key")
	if d.Type == "" {
		d.Type = "string"
	}
	d.Label = strings.TrimSpace(d.Label)
	if d.Label == "" {
		d.Label = d.Key
	}
	switch {
	case !attr.ValidKey(d.Key):
		writeErr(w, http.StatusBadRequest, "key: letters, digits and _, starting with a letter")
		return
	case !attrTypes[d.Type]:
		writeErr(w, http.StatusBadRequest, "type must be string, number, boolean or date")
		return
	case len(d.Label) > 120 || len(d.Description) > 500:
		writeErr(w, http.StatusBadRequest, "label or description too long")
		return
	}
	if err := s.audience().PutAttributeDef(r.Context(), tenant, d); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteAttribute(w http.ResponseWriter, r *http.Request, tenant int64) {
	if err := s.audience().DeleteAttributeDef(r.Context(), tenant, r.PathValue("key")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// coerceAttrs converts imported text to the defined types, so "42" becomes a number and "yes" a
// boolean. Values that don't parse are reported, not stored as the wrong type.
func coerceAttrs(attrs map[string]any, types map[string]string) error {
	return importer.CoerceAttrs(attrs, types)
}

// ---- identity verification ----

func (s *Server) getIdentity(w http.ResponseWriter, r *http.Request, p model.Principal) {
	v, err := s.audience().IdentitySettings(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putIdentity(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req struct {
		Required bool `json:"required"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.audience().SetIdentityRequired(r.Context(), p.TenantID, req.Required); err != nil {
		s.fail(w, err)
		return
	}
	s.getIdentity(w, r, p)
}

func (s *Server) rotateIdentity(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if _, err := s.audience().RotateIdentitySecret(r.Context(), p.TenantID); err != nil {
		s.fail(w, err)
		return
	}
	s.getIdentity(w, r, p)
}

// UserHash is what the customer's server sends to the browser for a signed-in user:
// hex(HMAC-SHA256(identity secret, user id)).
func UserHash(secret, userID string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(userID))
	return hex.EncodeToString(m.Sum(nil))
}

// webIdentityOK reports whether a browser may act as externalID: always for anonymous ids and
// when verification is off, otherwise only with the user's hash.
func webIdentityOK(t model.Tenant, externalID, hash string) bool {
	return !t.RequireSignedIdentify || strings.HasPrefix(externalID, "anon-") || validUserHash(t.IdentitySecret, externalID, hash)
}

func validUserHash(secret, userID, hash string) bool {
	return hash != "" && hmac.Equal([]byte(UserHash(secret, userID)), []byte(strings.ToLower(hash)))
}

// ---- segment references ----

var errSegmentRef = errors.New("segment reference")

// checkSegmentRefs makes sure every segment a rule references exists in the workspace and that
// saving rule as segment self (0 when new) would not create a cycle.
func (s *Server) checkSegmentRefs(ctx context.Context, tenant, self int64, rule segment.Rule) error {
	refs := rule.SegmentRefs()
	if len(refs) == 0 {
		return nil
	}
	segs, err := s.Store.ListSegments(ctx, tenant)
	if err != nil {
		return err
	}
	byID := map[int64]model.Segment{}
	for _, sg := range segs {
		byID[sg.ID] = sg
	}
	for _, ref := range refs {
		if _, ok := byID[ref]; !ok {
			return fmt.Errorf("%w: segment %d doesn't exist", errSegmentRef, ref)
		}
		if ref == self {
			return fmt.Errorf("%w: a segment can't include itself", errSegmentRef)
		}
	}
	if self == 0 {
		return nil
	}
	// Walk what the referenced segments reference; reaching self would be a cycle.
	seen := map[int64]bool{}
	stack := append([]int64{}, refs...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		sg := byID[id]
		if sg.Kind == "static" || len(sg.Rule) == 0 {
			continue
		}
		r, err := segment.Parse(sg.Rule)
		if err != nil {
			continue
		}
		for _, next := range r.SegmentRefs() {
			if next == self {
				return fmt.Errorf("%w: %q already depends on this segment, so it can't be included here", errSegmentRef, sg.Name)
			}
			stack = append(stack, next)
		}
	}
	return nil
}

// dependentsOf names the rule segments that reference id.
func (s *Server) dependentsOf(ctx context.Context, tenant, id int64) ([]string, error) {
	segs, err := s.Store.ListSegments(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, sg := range segs {
		if sg.Kind == "static" || len(sg.Rule) == 0 {
			continue
		}
		var r segment.Rule
		if json.Unmarshal(sg.Rule, &r) != nil {
			continue
		}
		for _, ref := range r.SegmentRefs() {
			if ref == id {
				names = append(names, sg.Name)
			}
		}
	}
	return names, nil
}
