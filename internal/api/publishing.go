package api

// Publishing segments and campaigns, modelled on on-site content: a segment is published or
// unpublished and has a window (start required, end optional); a live journey can be unpublished,
// either letting the people inside finish or holding them where they are, and its dates can be
// changed while it's live.
//
//	PUT  /v1/segments/{id}/publishing  {published, start_at, end_at}
//	GET  /v1/segments/{id}/usage       what uses the segment now (for the Unpublish prompt)
//	POST /v1/journeys/{id}/unpublish   {people: finish | hold}
//	PUT  /v1/journeys/{id}/schedule    {start_at, end_at}

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
)

// SegmentPublishStore changes a segment's publish state and window.
type SegmentPublishStore interface {
	SetSegmentPublishing(ctx context.Context, tenant, id int64, published bool, start time.Time, end *time.Time, rebuild bool) error
}

// JourneyScheduleStore changes a journey's window, live or not.
type JourneyScheduleStore interface {
	SetJourneyWindow(ctx context.Context, tenant, id int64, w schedule.Window) error
}

func (s *Server) publishingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /v1/segments/{id}/publishing", s.tenant(s.putSegmentPublishing))
	mux.HandleFunc("GET /v1/segments/{id}/usage", s.tenant(s.segmentUsage))
	mux.HandleFunc("PUT /v1/journeys/{id}/schedule", s.authed(s.putJourneySchedule))
	// POST /v1/journeys/{id}/unpublish is one of journeyAction's actions (it needs the body).
}

// publishReq is a segment's publish settings. On create every field is optional (published, from
// now, no end); on PUT /publishing, published and start_at are required.
type publishReq struct {
	Published *bool      `json:"published"`
	StartAt   *time.Time `json:"start_at"`
	EndAt     *time.Time `json:"end_at"`
}

func (p publishReq) given() bool { return p.Published != nil || p.StartAt != nil || p.EndAt != nil }

// window returns the settings with defaults for what's missing, or an error a person can act on.
func (p publishReq) settings(now time.Time) (bool, schedule.Window, error) {
	published := p.Published == nil || *p.Published
	w := schedule.Window{Start: now, End: p.EndAt}
	if p.StartAt != nil {
		w.Start = *p.StartAt
	}
	return published, w, w.Validate()
}

// rebuildOnPublish says whether a rule segment must be built afresh after its publish settings
// change: it was frozen and is live now, or it waits for a start in the future (it's rebuilt at
// that start, with the history it missed).
func rebuildOnPublish(before, after string) bool {
	return after == model.SegmentUpcoming || (after == model.SegmentLive && before != model.SegmentLive)
}

// applyPublishing stores a segment's publish settings, rebuilding it when it wakes up.
func (s *Server) applyPublishing(ctx context.Context, seg model.Segment, published bool, w schedule.Window) (model.Segment, error) {
	ps, ok := s.Store.(SegmentPublishStore)
	if !ok {
		return seg, errors.New("publishing segments isn't available")
	}
	now := s.now()
	before := seg.PublishState(now)
	seg.Unpublished, seg.StartAt, seg.EndAt = !published, w.Start, w.End
	after := seg.PublishState(now)
	rebuild := seg.Kind != "static" && rebuildOnPublish(before, after)
	if err := ps.SetSegmentPublishing(ctx, seg.TenantID, seg.ID, published, w.Start, w.End, rebuild); err != nil {
		return seg, err
	}
	if rebuild {
		seg.Status = "new"
	}
	return seg.WithState(now), nil
}

func (s *Server) putSegmentPublishing(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req publishReq
	if !decode(w, r, &req) {
		return
	}
	if req.Published == nil || req.StartAt == nil {
		writeErr(w, http.StatusBadRequest, "published and start_at are required")
		return
	}
	published, win, err := req.settings(s.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	seg, err := s.Store.GetSegment(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	seg.TenantID = tenant
	if seg, err = s.applyPublishing(r.Context(), seg, published, win); err != nil {
		s.fail(w, err)
		return
	}
	if seg.Size, err = s.Members.Size(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, seg)
}

// usageRef is one live thing that uses a segment.
type usageRef struct {
	Kind   string `json:"kind"` // segment | journey | broadcast | focus | content | inapp
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

// segmentUsage lists what uses a segment now: rule segments that read it, live or paused journeys
// (trigger, conditions, list steps), one-time campaigns that are scheduled or sending, and
// published on-site and in-app messages. Drafts and finished campaigns aren't listed.
func (s *Server) segmentUsage(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.Store.GetSegment(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	refs, err := s.usesOf(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uses": refs})
}

func (s *Server) usesOf(ctx context.Context, tenant, id int64) ([]usageRef, error) {
	refs := []usageRef{}
	segs, err := s.Store.ListSegments(ctx, tenant)
	if err != nil {
		return nil, err
	}
	for _, sg := range segs {
		if sg.Kind == "static" || len(sg.Rule) == 0 || sg.ID == id {
			continue
		}
		if slices.Contains(segmentRefsOf(sg), id) {
			refs = append(refs, usageRef{Kind: "segment", ID: sg.ID, Name: sg.Name})
		}
	}
	if us, ok := s.Store.(UIStore); ok {
		js, err := us.ListJourneys(ctx, tenant)
		if err != nil {
			return nil, err
		}
		for _, j := range js {
			if (j.Status != "active" && j.Status != "paused") || j.ArchivedAt != nil {
				continue
			}
			if g, err := journey.ParseGraph(j.Graph); err == nil && slices.Contains(journeySegmentIDs(g), id) {
				refs = append(refs, usageRef{Kind: "journey", ID: j.ID, Name: j.Name, Status: j.Status})
			}
		}
	}
	if bs, ok := s.Store.(BroadcastStore); ok {
		list, err := bs.ListBroadcasts(ctx, tenant)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			if b.Status != "scheduled" && b.Status != "running" && b.Status != "testing" {
				continue
			}
			if slices.Contains(broadcastSegmentIDs(b), id) {
				refs = append(refs, usageRef{Kind: "broadcast", ID: b.ID, Name: b.Name, Status: b.Status})
			}
		}
	}
	if ost, ok := s.Store.(OnsiteStore); ok {
		fs, err := ost.ListFocusItems(ctx, tenant, true)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			a := f.Targeting.Audience()
			if slices.Contains(a.SegmentIDs, id) || slices.Contains(a.ExcludeSegmentIDs, id) {
				refs = append(refs, usageRef{Kind: "focus", ID: f.ID, Name: f.Name, Status: f.Status})
			}
		}
		ds, err := ost.ListDWCSlots(ctx, tenant, true)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			for _, v := range d.Variants {
				a := v.Rules()
				if slices.Contains(a.SegmentIDs, id) || slices.Contains(a.ExcludeSegmentIDs, id) {
					refs = append(refs, usageRef{Kind: "content", ID: d.ID, Name: d.Name, Status: d.Status})
					break
				}
			}
		}
	}
	if is, ok := s.Store.(InAppStore); ok {
		ms, err := is.ListInApp(ctx, tenant, true)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			if slices.Contains(m.Targeting.SegmentIDs, id) || slices.Contains(m.Targeting.ExcludeSegmentIDs, id) {
				refs = append(refs, usageRef{Kind: "inapp", ID: m.ID, Name: m.Name, Status: m.Status})
			}
		}
	}
	return refs, nil
}

func segmentRefsOf(sg model.Segment) []int64 {
	var r segment.Rule
	if json.Unmarshal(sg.Rule, &r) != nil {
		return nil
	}
	return r.SegmentRefs()
}

// journeySegmentIDs lists every segment and list a journey names: its trigger, conditions and
// list steps.
func journeySegmentIDs(g journey.Graph) []int64 {
	var ids []int64
	if g.Trigger.SegmentID != 0 {
		ids = append(ids, g.Trigger.SegmentID)
	}
	for _, n := range g.Nodes {
		for _, sid := range append(append([]int64{n.SegmentID, n.ListID}, n.AddLists...), n.RemoveLists...) {
			if sid != 0 {
				ids = append(ids, sid)
			}
		}
	}
	return ids
}

// broadcastSegmentIDs lists a one-time campaign's audience segments, its exclusions included.
func broadcastSegmentIDs(b model.Broadcast) []int64 {
	ids := append([]int64{b.SegmentID}, b.IncludeSegmentIDs...)
	return append(ids, b.ExcludeSegmentIDs...)
}

// errSegmentNotLive refuses a new send to a segment that isn't live.
var errSegmentNotLive = errors.New("segment not live")

// segmentsLiveFor checks that the segments a new send uses are published and inside their window
// at the time it starts (from: the send's first run, or now for a journey): an upcoming segment is
// fine when it starts by then. The error names the first one that isn't.
func (s *Server) segmentsLiveFor(ctx context.Context, tenant int64, ids []int64, from time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	segs, err := s.Store.ListSegments(ctx, tenant)
	if err != nil {
		return err
	}
	now := s.now()
	for _, sg := range segs {
		if !slices.Contains(ids, sg.ID) {
			continue
		}
		switch sg.PublishState(now) {
		case model.SegmentUnpublished:
			return fmt.Errorf("%w: the segment %q is unpublished; publish it first", errSegmentNotLive, sg.Name)
		case model.SegmentEnded:
			return fmt.Errorf("%w: the segment %q has ended; move its end date later first", errSegmentNotLive, sg.Name)
		case model.SegmentUpcoming:
			if sg.StartAt.After(from) {
				return fmt.Errorf("%w: the segment %q only starts %s", errSegmentNotLive, sg.Name, sg.StartAt.UTC().Format("2 Jan 2006 15:04 UTC"))
			}
		}
	}
	return nil
}

// unpublishJourney stops a live journey: "finish" ends its window now (nobody new enters, people
// inside finish their steps); "hold" pauses it (everyone waits where they are, steps can change).
func (s *Server) unpublishJourney(w http.ResponseWriter, r *http.Request, p model.Principal, id int64) {
	var req struct {
		People string `json:"people"` // finish | hold
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	switch req.People {
	case "hold":
		s.setJourneyStatus(w, r, p, id, "paused")
		return
	case "finish":
	default:
		writeErr(w, http.StatusBadRequest, `people must be "finish" or "hold"`)
		return
	}
	us, ok := s.Store.(UIStore)
	js, ok2 := s.Store.(JourneyScheduleStore)
	if !ok || !ok2 {
		writeErr(w, http.StatusNotImplemented, "unpublishing isn't available")
		return
	}
	j, err := us.GetJourney(r.Context(), p.TenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if j.Status != "active" {
		writeErr(w, http.StatusConflict, "only a live campaign can be unpublished")
		return
	}
	now := s.now()
	win := schedule.Window{Start: j.StartAt, End: j.EndAt}
	if !now.After(win.Start) { // it hasn't started, so nobody is inside: back to paused
		s.setJourneyStatus(w, r, p, id, "paused")
		return
	}
	if win.End == nil || win.End.After(now) {
		win.End = &now
	}
	if err := js.SetJourneyWindow(r.Context(), p.TenantID, id, win); err != nil {
		s.fail(w, err)
		return
	}
	s.journeysChanged(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": j.Status, "start_at": win.Start, "end_at": win.End})
}

// putJourneySchedule changes a journey's dates, also while it's live: move the end later (or clear
// it) to let people in again, or end it at a set time. People already inside always finish.
// Reopening a live journey whose window had ended is a launch: it needs a live trigger segment and,
// in a workspace that requires it, a second person's approval.
func (s *Server) putJourneySchedule(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		StartAt *time.Time `json:"start_at"`
		EndAt   *time.Time `json:"end_at"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.StartAt == nil {
		writeErr(w, http.StatusBadRequest, "start_at is required")
		return
	}
	win := schedule.Window{Start: *req.StartAt, End: req.EndAt}
	if err := win.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	us, ok := s.Store.(UIStore)
	js, ok2 := s.Store.(JourneyScheduleStore)
	if !ok || !ok2 {
		writeErr(w, http.StatusNotImplemented, "changing dates isn't available")
		return
	}
	j, err := us.GetJourney(r.Context(), tenant, id)
	if err != nil {
		if errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		s.fail(w, err)
		return
	}
	if j.ArchivedAt != nil {
		writeErr(w, http.StatusConflict, "restore the journey from the archive before changing its dates")
		return
	}
	now := s.now()
	if j.Status == "active" && win.State(now) != "ended" {
		if (schedule.Window{Start: j.StartAt, End: j.EndAt}).State(now) == "ended" && s.fourEyes(w, r, p) {
			return // reopening an ended live journey is a launch
		}
		if g, err := journey.ParseGraph(j.Graph); err == nil && g.Trigger.SegmentID != 0 {
			if err := s.segmentsLiveFor(r.Context(), tenant, []int64{g.Trigger.SegmentID}, win.Start); err != nil {
				if errors.Is(err, errSegmentNotLive) {
					writeErr(w, http.StatusConflict, err.Error())
				} else {
					s.fail(w, err)
				}
				return
			}
		}
	}
	if err := js.SetJourneyWindow(r.Context(), tenant, id, win); err != nil {
		s.fail(w, err)
		return
	}
	s.journeysChanged(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "start_at": win.Start, "end_at": win.End})
}
