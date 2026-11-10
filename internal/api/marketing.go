package api

// Handlers for marketing objects: events, segments, templates, journeys, contacts.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
)

// ingest accepts one event or {"events": [...]}. It responds 202 once Kafka has acknowledged
// every event, so an accepted event is never lost.
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, tenant int64) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
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
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		evs = []event.Event{one}
	}
	if len(evs) == 0 || len(evs) > maxBatch {
		writeErr(w, http.StatusBadRequest, "send 1-1000 events")
		return
	}
	now := s.now()
	recs := make([]*kgo.Record, len(evs))
	for i := range evs {
		e := &evs[i]
		e.TenantID = tenant // never trust a tenant id in the body
		e.ContactID, e.NewContact, e.ChangedAttrs, e.Profile = 0, false, nil, nil
		if e.Name == event.ContactMerged || e.Name == event.ContactMergedInto {
			writeErr(w, http.StatusBadRequest, "event "+strconv.Itoa(i)+": "+e.Name+" is recorded by Pulse itself")
			return
		}
		if err := e.Normalize(now); err != nil {
			writeErr(w, http.StatusBadRequest, "event "+strconv.Itoa(i)+": "+err.Error())
			return
		}
		if recs[i], err = kafka.JSON(event.TopicRaw, e.RawKey(), e); err != nil {
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

func (s *Server) createSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Name string          `json:"name"`
		Rule json.RawMessage `json:"rule"`
	}
	if !decode(w, r, &req) {
		return
	}
	rule, err := segment.Parse(req.Rule)
	if req.Name == "" || err != nil {
		writeErr(w, http.StatusBadRequest, errors.Join(errIf(req.Name == "", "name is required"), err).Error())
		return
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

func (s *Server) createJourney(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req journeyReq
	if !decode(w, r, &req) {
		return
	}
	win, ok := req.valid(w, s.now())
	if !ok {
		return
	}
	id, err := s.Store.CreateJourney(r.Context(), tenant, req.Name, req.Graph, win)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "draft"})
}

func (s *Server) journeyAction(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	status := map[string]string{"activate": "active", "pause": "paused"}[r.PathValue("action")]
	if status == "" {
		writeErr(w, http.StatusNotFound, "unknown action")
		return
	}
	if err := s.Store.SetJourneyStatus(r.Context(), tenant, id, status); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
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

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, pg.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, "already exists")
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

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
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
