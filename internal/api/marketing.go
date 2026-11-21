package api

// Handlers for marketing objects: events, segments, templates, journeys, contacts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/journey"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// ingest accepts one event or {"events": [...]}. It responds 202 once Kafka has acknowledged
// every event, so an accepted event is never lost.
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, tenant int64) {
	// The shared decoding rules: 413 only for a body over the limit, NUL refused, clear messages.
	var body json.RawMessage
	if !decode(w, r, &body) {
		return
	}
	var batch struct {
		Events []event.Event `json:"events"`
	}
	var evs []event.Event
	if err := json.Unmarshal(body, &batch); err == nil && batch.Events != nil {
		evs = batch.Events
	} else {
		var one event.Event
		if err := json.Unmarshal(body, &one); err != nil {
			writeErr(w, http.StatusBadRequest, jsonErrMessage(err))
			return
		}
		evs = []event.Event{one}
	}
	if len(evs) == 0 || len(evs) > maxBatch {
		writeErr(w, http.StatusBadRequest, "send 1-1000 events")
		return
	}
	now := s.now()
	stageNamed := s.stageLookup(tenant)
	for i := range evs {
		e := &evs[i]
		e.TenantID = tenant  // never trust a tenant id in the body
		e.Unverified = false // the server API key proves the sender
		e.FromBrowser = false
		e.ContactID, e.NewContact, e.ChangedAttrs, e.Profile = 0, false, nil, nil
		if event.PlatformOnly(e.Name) {
			writeErr(w, http.StatusBadRequest, "event "+strconv.Itoa(i)+": "+e.Name+" is recorded by Pulse itself (use the contact's company or merge API)")
			return
		}
		if msg := eventSizeProblem(e.Props, e.Attrs); msg != "" {
			writeErr(w, http.StatusBadRequest, "event "+strconv.Itoa(i)+": "+msg)
			return
		}
		if err := e.Normalize(now); err != nil {
			writeErr(w, http.StatusBadRequest, "event "+strconv.Itoa(i)+": "+err.Error())
			return
		}
		if e.Name == event.StageChanged {
			name, ok, err := stageNamed(r.Context(), attr.String(e.Props["stage"]))
			if err != nil {
				s.fail(w, err)
				return
			}
			if !ok {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("event %d: there is no stage called %q", i, attr.String(e.Props["stage"])))
				return
			}
			e.Props["stage"] = name
			delete(e.Props, "rename")
		}
	}
	if msg := s.enforceSchemas(r.Context(), tenant, evs); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	// An identify naming a company links the contact to it (created if new).
	all, err := s.linkCompanies(r.Context(), tenant, evs)
	if err != nil {
		s.fail(w, err)
		return
	}
	recs := make([]*kgo.Record, len(all))
	for i := range all {
		if recs[i], err = kafka.JSON(event.TopicRaw, all[i].RawKey(), &all[i]); err != nil {
			s.fail(w, err)
			return
		}
	}
	if err := s.Pub.Publish(r.Context(), recs...); err != nil {
		s.Log.Error("publish events", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	ids := make([]string, len(evs))
	for i, e := range evs {
		ids[i] = e.ID
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(evs), "ids": ids})
}

// SegmentRefreshStore keeps how a segment is refreshed (live or daily).
type SegmentRefreshStore interface {
	SetSegmentRefresh(ctx context.Context, tenant, id int64, rf *segment.Refresh) error
}

// setRefresh saves a segment's refresh setting when the request has one.
func (s *Server) setRefresh(ctx context.Context, tenant, id int64, rf *segment.Refresh) error {
	if rf == nil {
		return nil
	}
	st, ok := s.Store.(SegmentRefreshStore)
	if !ok {
		return nil
	}
	return st.SetSegmentRefresh(ctx, tenant, id, rf)
}

func (s *Server) createSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Name    string           `json:"name"`
		Rule    json.RawMessage  `json:"rule"`
		Refresh *segment.Refresh `json:"refresh"`
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
	if err := s.checkSegmentRefs(r.Context(), tenant, 0, rule); err != nil {
		s.failRefs(w, err)
		return
	}
	id, err := s.Store.CreateSegment(r.Context(), tenant, req.Name, req.Rule, rule.HasWindow())
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.setRefresh(r.Context(), tenant, id, req.Refresh); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "new"})
}

func (s *Server) listSegments(w http.ResponseWriter, r *http.Request, tenant int64) {
	segs, err := s.Store.ListSegments(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range segs {
		if segs[i].Size, err = s.Members.Size(r.Context(), tenant, segs[i].ID); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": segs})
}

func (s *Server) getSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	seg, err := s.Store.GetSegment(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if seg.Size, err = s.Members.Size(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, seg)
}

func (s *Server) createJourney(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	var req journeyReq
	if !decode(w, r, &req) {
		return
	}
	win, ok := req.valid(w, s.now())
	if !ok || !s.journeyRefsOK(w, r, p, 0, req.Graph) {
		return
	}
	id, err := s.Store.CreateJourney(r.Context(), tenant, req.Name, req.Graph, win)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "draft"})
}

func (s *Server) journeyAction(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if action == "archive" || action == "unarchive" {
		s.archiveJourney(w, r, tenant, id, action == "archive")
		return
	}
	status := map[string]string{"activate": "active", "pause": "paused"}[action]
	if status == "" {
		writeErr(w, http.StatusNotFound, "unknown action")
		return
	}
	if status == "active" && s.fourEyes(w, r, p) {
		return
	}
	s.setJourneyStatus(w, r, p, id, status)
}

// setJourneyStatus activates or pauses a journey (its own buttons, or an approval).
func (s *Server) setJourneyStatus(w http.ResponseWriter, r *http.Request, p model.Principal, id int64, status string) bool {
	tenant := p.TenantID
	if status == "active" {
		if us, ok := s.Store.(UIStore); ok {
			j, err := us.GetJourney(r.Context(), tenant, id)
			if err != nil {
				s.fail(w, err)
				return false
			}
			if j.ArchivedAt != nil {
				writeErr(w, http.StatusConflict, "restore the journey from the archive before activating it")
				return false
			}
			if g, err := journey.ParseGraph(j.Graph); err == nil && !deletePermOK(w, p, g) {
				return false
			}
		}
	}
	if err := s.Store.SetJourneyStatus(r.Context(), tenant, id, status); err != nil {
		s.fail(w, err)
		return false
	}
	s.journeysChanged(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
	return true
}

// JourneyArchiveStore archives journeys that have run (they can't be deleted).
type JourneyArchiveStore interface {
	SetJourneyArchived(ctx context.Context, tenant, id int64, archived bool) error
	JourneysThatRan(ctx context.Context, tenant int64) (map[int64]bool, error)
}

// archiveJourney hides a paused or draft journey from the list (archive) or brings it back
// (unarchive). Its enrollments and reports are kept.
func (s *Server) archiveJourney(w http.ResponseWriter, r *http.Request, tenant, id int64, archived bool) {
	as, ok := s.Store.(JourneyArchiveStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "archiving isn't available")
		return
	}
	if err := as.SetJourneyArchived(r.Context(), tenant, id, archived); err != nil {
		if pg.IsUserError(err) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	s.journeysChanged(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "archived": archived})
}

// journeysChanged tells the journey engines to reload now (activate, pause, edit, archive, delete)
// and waits up to 3 seconds until they have, so "new matches enter it immediately" holds once the
// request answers. Engines also reload every 2 seconds regardless.
func (s *Server) journeysChanged(ctx context.Context) {
	if s.RDB == nil {
		return
	}
	ok, err := journey.Announce(ctx, s.RDB, 3*time.Second)
	if err != nil {
		s.Log.Warn("announce journey change", "err", err)
	} else if !ok {
		s.Log.Warn("a journey worker didn't confirm the change within 3 s; it reloads within 2 s")
	}
}

func (s *Server) journeyStats(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	stats, err := s.Store.JourneyStats(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// getContact returns the profile and the ids of the segments the contact is currently in.
func (s *Server) getContact(w http.ResponseWriter, r *http.Request, tenant int64) {
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	segs, err := s.Store.ListSegments(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	in := []int64{}
	for _, seg := range segs {
		ok, err := s.Members.IsMember(r.Context(), tenant, seg.ID, c.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if ok {
			in = append(in, seg.ID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"contact": c, "segments": in})
}

// explainSegment: GET /v1/contacts/{external_id}/segments/{id}/explain, the segment's rule
// evaluated for this contact now, node by node, with the contact's values and event counts, and
// whether the contact is a member. A daily segment's membership is from its last refresh, so
// the two can differ until the next one; rules older than the counters are left to the sweep.
func (s *Server) explainSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	c, err := s.Store.ContactByExternalID(ctx, tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	seg, err := s.Store.GetSegment(ctx, tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	in, err := s.Members.IsMember(ctx, tenant, id, c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"segment": seg.ID, "name": seg.Name, "kind": seg.Kind, "member": in, "refresh": seg.Refresh}
	if seg.Kind == "static" || s.RDB == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rule, err := segment.Parse(seg.Rule)
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, merged := c.Attrs[event.AttrMergedInto]; merged {
		out["merged"] = true
	}
	ex, err := segment.Explain(ctx, rule, c.Attrs, segment.NewRedisCounter(s.RDB, tenant, c.ID, s.now()))
	if err != nil {
		s.fail(w, err)
		return
	}
	out["explanation"] = ex
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, pg.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, "already exists")
		return
	}
	if code, msg := inputProblem(err); code != 0 {
		writeErr(w, code, msg)
		return
	}
	if ch.LimitExceeded(err) {
		writeErr(w, http.StatusUnprocessableEntity, "this needs more time or memory than a request may use: try a shorter period or fewer conditions")
		return
	}
	s.Log.Error("request failed", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) failRefs(w http.ResponseWriter, err error) {
	if errors.Is(err, errSegmentRef) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.fail(w, err)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func errIf(cond bool, msg string) error {
	if cond {
		return errors.New(msg)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
