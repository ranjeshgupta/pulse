package api

// Campaigns (M3): the Campaigns hub (broadcasts and journeys in one list, by category and tags),
// duplicates, copy and paste of journey steps, exact audience estimates, provider limits and saved
// test contacts.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/delivery"
	"pulse/internal/dispatch"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/providers"
	"pulse/internal/reach"
	"pulse/internal/schedule"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// CampaignStore is the Postgres side of campaigns.
type CampaignStore interface {
	SetBroadcastTags(ctx context.Context, tenant, id int64, tags []string) error
	SetJourneyTags(ctx context.Context, tenant, id int64, tags []string) error
	CampaignTags(ctx context.Context, tenant int64) ([]string, error)
	CreateJourneyTagged(ctx context.Context, tenant int64, name string, graph []byte, w schedule.Window, tags []string) (int64, error)
	ListTestContacts(ctx context.Context, tenant int64) ([]model.TestContact, error)
	GetTestContact(ctx context.Context, tenant, id int64) (model.TestContact, error)
	SaveTestContact(ctx context.Context, tenant int64, tc model.TestContact) (int64, error)
	DeleteTestContact(ctx context.Context, tenant, id int64) error
	SetProviderLimits(ctx context.Context, tenant, id int64, l model.ProviderLimits) error
}

func (s *Server) campStore() CampaignStore { return s.Store.(CampaignStore) }

func (s *Server) campaignRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/campaigns", s.authed(s.listCampaigns))
	mux.HandleFunc("GET /v1/campaigns/tags", s.authed(s.campaignTags))
	mux.HandleFunc("PUT /v1/broadcasts/{id}/tags", s.tenant(s.putBroadcastTags))
	mux.HandleFunc("PUT /v1/journeys/{id}/tags", s.tenant(s.putJourneyTags))
	mux.HandleFunc("POST /v1/broadcasts/{id}/duplicate", s.tenant(s.duplicateBroadcast))
	mux.HandleFunc("POST /v1/journeys/{id}/duplicate", s.authed(s.duplicateJourney))
	mux.HandleFunc("POST /v1/templates/{id}/duplicate", s.tenant(s.duplicateTemplate))
	mux.HandleFunc("POST /v1/segments/{id}/duplicate", s.tenant(s.duplicateSegment))
	mux.HandleFunc("POST /v1/journey-steps/check", s.tenant(s.checkJourneySteps))
	mux.HandleFunc("POST /v1/segments/estimate", s.tenant(s.estimateAudience))
	mux.HandleFunc("GET /v1/segments/{id}/reach", s.tenant(s.segmentReach))
	mux.HandleFunc("PUT /v1/channels/providers/{id}/limits", s.tenant(s.putProviderLimits))
	mux.HandleFunc("GET /v1/test-contacts", s.tenant(s.listTestContacts))
	mux.HandleFunc("POST /v1/test-contacts", s.authed(s.saveTestContact))
	mux.HandleFunc("PUT /v1/test-contacts/{id}", s.authed(s.saveTestContact))
	mux.HandleFunc("DELETE /v1/test-contacts/{id}", s.tenant(s.deleteTestContact))
}

// ---- the hub ----

// Campaign categories (automatic, by type).
const (
	CategoryOneTime    = "one_time"   // broadcasts
	CategoryTrigger    = "trigger"    // journeys started by an event or a form
	CategoryAutomation = "automation" // journeys started by entering a segment
)

// CampaignRow is one line of the Campaigns hub.
type CampaignRow struct {
	// Problem: a live journey the workers skip (see journeyProblem).
	Problem   string     `json:"problem,omitempty"`
	Kind      string     `json:"kind"` // broadcast | journey
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Category  string     `json:"category"`
	Channels  []string   `json:"channels"`
	Status    string     `json:"status"`
	Tags      []string   `json:"tags"`
	Repeat    string     `json:"repeat,omitempty"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	StartAt   time.Time  `json:"start_at"`
	EndAt     *time.Time `json:"end_at,omitempty"`
	Trigger   string     `json:"trigger,omitempty"` // journeys: segment | event | form (the event's name in TriggerEvent)
	// TriggerEvent is the event of an event trigger.
	TriggerEvent string `json:"trigger_event,omitempty"`
	SegmentID    int64  `json:"segment_id,omitempty"`
	// Audience is the size of the audience segment now (broadcasts, segment journeys).
	Audience     *int64     `json:"audience"`
	Sent         uint64     `json:"sent"`
	LastActivity *time.Time `json:"last_activity,omitempty"`
}

// CampaignTotaler is the ClickHouse side of the hub.
type CampaignTotaler interface {
	CampaignTotals(ctx context.Context, tenant int64) (map[string]ch.CampaignTotal, error)
}

// JourneyChannels lists the channels a journey's send steps use, in a fixed order.
func JourneyChannels(g journey.Graph) []string {
	set := map[string]bool{}
	for _, n := range g.Nodes {
		if c := journey.SendChannel(n.Type); c != "" {
			set[c] = true
		}
		for _, cm := range n.Channels {
			set[cm.Channel] = true
		}
	}
	out := []string{}
	for _, c := range channel.All {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

// JourneyCategory is a journey's automatic category.
func JourneyCategory(t journey.Trigger) string {
	if t.Type == journey.TriggerSegmentEntered {
		return CategoryAutomation
	}
	return CategoryTrigger
}

// CampaignFilter selects rows of the hub.
type CampaignFilter struct {
	Category, Status, Channel, Tag, Q string
}

// Match reports whether a row passes the filter (tags and names ignore case).
func (f CampaignFilter) Match(r CampaignRow) bool {
	if f.Category != "" && r.Category != f.Category {
		return false
	}
	if f.Status != "" && r.Status != f.Status {
		return false
	}
	if f.Channel != "" && !slices.Contains(r.Channels, f.Channel) {
		return false
	}
	if f.Tag != "" && !slices.ContainsFunc(r.Tags, func(t string) bool { return strings.EqualFold(t, f.Tag) }) {
		return false
	}
	if q := strings.TrimSpace(f.Q); q != "" && !strings.Contains(strings.ToLower(r.Name), strings.ToLower(q)) &&
		!slices.ContainsFunc(r.Tags, func(t string) bool { return strings.EqualFold(t, q) }) {
		return false
	}
	return true
}

// listCampaigns is the hub: every broadcast and journey the caller may see, with counts per tab.
// ?category=one_time|trigger|automation&status=&channel=&tag=&q=
func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request, p model.Principal) {
	canB, canJ := p.Perms.Allows(auth.AreaBroadcasts, auth.View), p.Perms.Allows(auth.AreaJourneys, auth.View)
	if !canB && !canJ {
		writeErr(w, http.StatusForbidden, "your role doesn't allow this (view access to broadcasts or journeys is needed)")
		return
	}
	rows, err := s.campaignRows(r.Context(), p.TenantID, canB, canJ)
	if err != nil {
		s.fail(w, err)
		return
	}
	q := r.URL.Query()
	f := CampaignFilter{Category: q.Get("category"), Status: q.Get("status"), Channel: q.Get("channel"), Tag: q.Get("tag"), Q: q.Get("q")}
	counts := map[string]int{CategoryOneTime: 0, CategoryTrigger: 0, CategoryAutomation: 0}
	out := []CampaignRow{}
	tabless := f
	tabless.Category = ""
	for _, row := range rows {
		if row.Status == "archived" && f.Status != "archived" {
			continue // archived journeys only on request, like the journeys list
		}
		if tabless.Match(row) {
			counts[row.Category]++
		}
		if f.Match(row) {
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].LastActivity, out[j].LastActivity
		if a == nil || b == nil {
			return b == nil && a != nil
		}
		return a.After(*b)
	})
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": out, "counts": counts})
}

// campaignRows lists the workspace's broadcasts (withB) and journeys (withJ) as hub rows with
// their totals.
func (s *Server) campaignRows(ctx context.Context, tenant int64, withB, withJ bool) ([]CampaignRow, error) {
	canB, canJ := withB, withJ
	var rows []CampaignRow
	sizes := map[int64]*int64{}
	size := func(seg int64) *int64 {
		if seg == 0 {
			return nil
		}
		if v, ok := sizes[seg]; ok {
			return v
		}
		var v *int64
		if n, err := s.Members.Size(ctx, tenant, seg); err == nil {
			v = &n
		}
		sizes[seg] = v
		return v
	}
	if canB {
		bs, err := s.bcStore().ListBroadcasts(ctx, tenant)
		if err != nil {
			return nil, err
		}
		for _, b := range bs {
			row := CampaignRow{Kind: "broadcast", ID: b.ID, Name: b.Name, Category: CategoryOneTime, Channels: []string{b.Channel},
				Status: b.Status, Tags: b.Tags, Repeat: b.Repeat, NextRunAt: b.NextRunAt, StartAt: b.StartAt, EndAt: b.EndAt,
				SegmentID: b.SegmentID, Audience: size(b.SegmentID)}
			if channel.FallsBackToSMS(b.Channel) && b.FallbackTemplateID != 0 {
				row.Channels = append(row.Channels, channel.SMS)
			}
			u := b.UpdatedAt
			row.LastActivity = &u
			rows = append(rows, row)
		}
	}
	if canJ {
		js, err := s.uiStore().ListJourneys(ctx, tenant)
		if err != nil {
			return nil, err
		}
		for _, j := range js {
			var g journey.Graph
			_ = json.Unmarshal(j.Graph, &g) // stored graphs were validated; a bad one shows without channels
			status := j.Status
			if j.ArchivedAt != nil {
				status = "archived"
			}
			row := CampaignRow{Kind: "journey", ID: j.ID, Name: j.Name, Category: JourneyCategory(g.Trigger),
				Channels: JourneyChannels(g), Status: status, Tags: j.Tags, StartAt: j.StartAt, EndAt: j.EndAt, Problem: journeyProblem(j)}
			switch g.Trigger.Type {
			case journey.TriggerSegmentEntered:
				row.Trigger, row.SegmentID, row.Audience = "segment", g.Trigger.SegmentID, size(g.Trigger.SegmentID)
			case journey.TriggerEvent:
				row.Trigger, row.TriggerEvent = "event", g.Trigger.Event
			case journey.TriggerFormSubmitted:
				row.Trigger = "form"
			}
			rows = append(rows, row)
		}
	}
	if ct, ok := s.Analytics.(CampaignTotaler); ok {
		if totals, err := ct.CampaignTotals(ctx, tenant); err == nil {
			for i := range rows {
				t, ok := totals[rows[i].Kind+":"+strconv.FormatInt(rows[i].ID, 10)]
				if !ok {
					continue
				}
				rows[i].Sent = t.Sent
				if rows[i].LastActivity == nil || t.Last.After(*rows[i].LastActivity) {
					last := t.Last
					rows[i].LastActivity = &last
				}
			}
		} else {
			s.Log.Warn("campaign totals", "err", err)
		}
	}
	return rows, nil
}

// campaignTags suggests tags: those used on broadcasts and journeys, most used first.
func (s *Server) campaignTags(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if !p.Perms.Allows(auth.AreaBroadcasts, auth.View) && !p.Perms.Allows(auth.AreaJourneys, auth.View) {
		writeErr(w, http.StatusForbidden, "your role doesn't allow this (view access to broadcasts or journeys is needed)")
		return
	}
	tags, err := s.campStore().CampaignTags(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": nonNil(tags)})
}

func (s *Server) decodeTags(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var req struct {
		Tags []string `json:"tags"`
	}
	if !decode(w, r, &req) {
		return nil, false
	}
	tags, err := model.NormalizeTags(req.Tags)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return tags, true
}

func (s *Server) putBroadcastTags(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tags, ok := s.decodeTags(w, r)
	if !ok {
		return
	}
	if err := s.campStore().SetBroadcastTags(r.Context(), tenant, id, tags); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "tags": tags})
}

func (s *Server) putJourneyTags(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tags, ok := s.decodeTags(w, r)
	if !ok {
		return
	}
	if err := s.campStore().SetJourneyTags(r.Context(), tenant, id, tags); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "tags": tags})
}

// ---- duplicates ----

// copyName names a duplicate: "Name (copy)".
func copyName(name string) string {
	const max = 200
	out := name + " (copy)"
	if len([]rune(out)) > max {
		out = string([]rune(out)[:max])
	}
	return out
}

// duplicateBroadcast copies a broadcast as a draft. A template written inside the campaign (hidden)
// is copied too, so editing one doesn't change the other.
func (s *Server) duplicateBroadcast(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	b, err := s.bcStore().GetBroadcast(ctx, tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if t, err := s.uiStore().Template(ctx, tenant, b.TemplateID); err == nil && t.Hidden && b.AB == nil {
		t.Name = copyName(t.Name)
		if b.TemplateID, err = s.Store.CreateTemplate(ctx, t); err != nil {
			s.fail(w, err)
			return
		}
	}
	b.ID, b.Name, b.Status, b.NextRunAt = 0, copyName(b.Name), "draft", nil
	if b.StartAt.Before(s.now()) {
		b.StartAt = s.now().Truncate(time.Minute)
		if b.EndAt != nil && !b.EndAt.After(b.StartAt) {
			b.EndAt = nil
		}
	}
	nid, err := s.bcStore().SaveBroadcast(ctx, b)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": nid, "status": "draft"})
}

// duplicateJourney copies a journey as a draft (same steps, tags and window; nobody in it).
func (s *Server) duplicateJourney(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := s.uiStore().GetJourney(r.Context(), p.TenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.journeyRefsOK(w, r, p, 0, j.Graph) { // e.g. a Delete contact step still needs contacts edit
		return
	}
	win := schedule.Window{Start: j.StartAt, End: j.EndAt}
	if win.Start.Before(s.now()) {
		win.Start = s.now().Truncate(time.Minute)
	}
	if win.End != nil && !win.End.After(win.Start) {
		win.End = nil
	}
	nid, err := s.campStore().CreateJourneyTagged(r.Context(), p.TenantID, copyName(j.Name), j.Graph, win, j.Tags)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": nid, "status": "draft"})
}

func (s *Server) duplicateTemplate(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.uiStore().Template(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	t.Name, t.Hidden = copyName(t.Name), false
	nid, err := s.Store.CreateTemplate(r.Context(), t)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": nid})
}

// duplicateSegment copies a dynamic segment's rule and refresh setting (it is built from scratch).
func (s *Server) duplicateSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	seg, err := s.Store.GetSegment(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if seg.Kind == "static" {
		writeErr(w, http.StatusBadRequest, "static segments are lists of contacts: export it and import the file into a new one")
		return
	}
	rule, err := segment.Parse(seg.Rule)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	nid, err := s.Store.CreateSegment(r.Context(), tenant, copyName(seg.Name), seg.Rule, rule.HasWindow())
	if err != nil {
		s.fail(w, err)
		return
	}
	if seg.Refresh != nil {
		if err := s.setRefresh(r.Context(), tenant, nid, seg.Refresh); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": nid, "status": "new"})
}

// ---- copy and paste of journey steps ----

// checkJourneySteps checks a clipboard of journey steps before it is pasted: it returns the steps
// with links to steps that weren't copied, and references to things this workspace doesn't have,
// removed, and a note for each.
func (s *Server) checkJourneySteps(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Clipboard json.RawMessage `json:"clipboard"`
	}
	if !decode(w, r, &req) {
		return
	}
	clip, err := journey.ParseClipboard(req.Clipboard)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	known, err := s.existingRefs(r.Context(), tenant, clip.Refs())
	if err != nil {
		s.fail(w, err)
		return
	}
	out, notes, err := journey.CheckClipboard(clip, func(ref journey.Ref) bool { return known[ref] })
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clipboard": out, "notes": nonNil(notes)})
}

// existingRefs reports which references exist in the workspace. Kinds it can't check are
// treated as present (saving the journey checks them).
func (s *Server) existingRefs(ctx context.Context, tenant int64, refs []journey.Ref) (map[journey.Ref]bool, error) {
	out := map[journey.Ref]bool{}
	exists := func(err error) (bool, error) {
		if errors.Is(err, pg.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	}
	var journeys []int64
	for _, ref := range refs {
		var ok bool
		var err error
		switch ref.Kind {
		case "template":
			var t model.Template
			t, err = s.uiStore().Template(ctx, tenant, ref.ID)
			ok, err = exists(err)
			ok = ok && t.TenantID == tenant
		case "segment":
			_, err = s.Store.GetSegment(ctx, tenant, ref.ID)
			ok, err = exists(err)
		case "form":
			if fs, has := s.Store.(FormStore); has {
				_, err = fs.GetForm(ctx, tenant, ref.ID)
				ok, err = exists(err)
			} else {
				ok = true
			}
		case "asset":
			if as, has := s.Store.(AssetStore); has {
				_, err = as.GetAsset(ctx, tenant, ref.ID)
				ok, err = exists(err)
			} else {
				ok = true
			}
		case "company":
			if js, has := s.Store.(JourneyRefStore); has {
				_, err = js.GetCompany(ctx, tenant, ref.ID)
				ok, err = exists(err)
			} else {
				ok = true
			}
		case "journey":
			journeys = append(journeys, ref.ID)
			continue
		case "user":
			us, lerr := s.Store.ListUsers(ctx, tenant)
			if lerr != nil {
				return nil, lerr
			}
			ok = slices.ContainsFunc(us, func(u model.User) bool { return u.ID == ref.ID })
		default:
			ok = true
		}
		if err != nil {
			return nil, err
		}
		out[ref] = ok
	}
	if len(journeys) > 0 {
		if js, has := s.Store.(JourneyRefStore); has {
			found, err := js.JourneyIDs(ctx, tenant, journeys)
			if err != nil {
				return nil, err
			}
			for _, id := range journeys {
				out[journey.Ref{Kind: "journey", ID: id}] = found[id]
			}
		} else {
			for _, id := range journeys {
				out[journey.Ref{Kind: "journey", ID: id}] = true
			}
		}
	}
	return out, nil
}

// ---- audience estimates ----

const (
	estimateCacheTTL  = 30 * time.Second
	estimatesPerMin   = 60 // per workspace, cache misses only
	capExactLimit     = 50000
	capSample         = 5000
	estimateTimeLimit = 10 * time.Second
)

// EstimateRequest is an audience to count.
type EstimateRequest struct {
	Include []int64 `json:"include"`
	Exclude []int64 `json:"exclude"`
	Channel string  `json:"channel"`
	// Transactional messages skip frequency caps.
	Transactional bool `json:"transactional"`
}

// Estimate is the exact audience after each step.
type Estimate struct {
	reach.Counts
	// Capped: reachable contacts a frequency cap of the channel would hold back now (marketing
	// messages only). Exact up to 50,000 reachable contacts, else from a sample (Approximate).
	Capped      *reach.CapResult `json:"capped,omitempty"`
	Cap         *delivery.Cap    `json:"cap,omitempty"`
	WillSend    int64            `json:"will_send"`
	Ready       bool             `json:"ready"` // reachability is built (else the last steps read 0)
	ComputedAt  time.Time        `json:"computed_at"`
	CachedUntil time.Time        `json:"cached_until"`
}

// allowTenant is a fixed-window limit per workspace (all its users together).
func (s *Server) allowTenant(ctx context.Context, prefix string, tenant int64, limit int) bool {
	if s.RDB == nil {
		return true
	}
	key := prefix + ":" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(s.now().Unix()/60, 10)
	n, err := s.RDB.Incr(ctx, key).Result()
	if err != nil {
		return true
	}
	if n == 1 {
		s.RDB.Expire(ctx, key, 2*time.Minute)
	}
	return n <= int64(limit)
}

// segmentsOK checks that every segment belongs to the workspace.
func (s *Server) segmentsOK(ctx context.Context, tenant int64, ids []int64) error {
	for _, id := range ids {
		if _, err := s.Store.GetSegment(ctx, tenant, id); errors.Is(err, pg.ErrNotFound) {
			return errUser(fmt.Sprintf("segment %d doesn't exist", id))
		} else if err != nil {
			return err
		}
	}
	return nil
}

// estimateAudience counts exactly who a campaign would reach: members of the included segments,
// after the exclusions, after consent for the channel, with an address on it, and (marketing
// messages) not held back by a frequency cap. Results are cached for 30 seconds and cache misses
// are limited per workspace.
func (s *Server) estimateAudience(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req EstimateRequest
	if !decode(w, r, &req) {
		return
	}
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "estimates need Redis")
		return
	}
	if len(req.Include) == 0 || len(req.Include) > 10 || len(req.Exclude) > 10 {
		writeErr(w, http.StatusBadRequest, "choose 1 to 10 segments, and exclude at most 10")
		return
	}
	if req.Channel != "" && !channel.Valid(req.Channel) {
		writeErr(w, http.StatusBadRequest, "unknown channel")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), estimateTimeLimit)
	defer cancel()
	var ue errUser
	if err := s.segmentsOK(ctx, tenant, append(slices.Clone(req.Include), req.Exclude...)); errors.As(err, &ue) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	norm := req
	norm.Include, norm.Exclude = sortedIDs(req.Include), sortedIDs(req.Exclude)
	kb, _ := json.Marshal(norm)
	sum := sha1.Sum(kb)
	cacheKey := "rchest:" + strconv.FormatInt(tenant, 10) + ":" + hex.EncodeToString(sum[:])
	if b, err := s.RDB.Get(ctx, cacheKey).Bytes(); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}
	if !s.allowTenant(ctx, "rlest", tenant, estimatesPerMin) {
		writeErr(w, http.StatusTooManyRequests, "too many estimates; try again in a minute")
		return
	}
	est, err := s.estimate(ctx, tenant, norm)
	if err != nil {
		s.fail(w, err)
		return
	}
	b, _ := json.Marshal(est)
	s.RDB.Set(ctx, cacheKey, b, estimateCacheTTL)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func sortedIDs(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

func (s *Server) estimate(ctx context.Context, tenant int64, req EstimateRequest) (Estimate, error) {
	now := s.now()
	var cp *delivery.Cap
	if req.Channel != "" && !req.Transactional {
		if ds, ok := s.Store.(DeliveryStore); ok {
			rules, err := ds.DeliveryRules(ctx, tenant)
			if err != nil {
				return Estimate{}, err
			}
			if c, ok := rules.Caps[req.Channel]; ok {
				cp = &c
			}
		}
	}
	counts, kept, err := reach.Count(ctx, s.RDB, reach.Query{Tenant: tenant, Include: req.Include, Exclude: req.Exclude,
		Channel: req.Channel}, cp != nil)
	if err != nil {
		return Estimate{}, err
	}
	est := Estimate{Counts: counts, WillSend: counts.Reachable, ComputedAt: now, CachedUntil: now.Add(estimateCacheTTL)}
	if est.Ready, err = reach.Ready(ctx, s.RDB, tenant); err != nil {
		return Estimate{}, err
	}
	if cp != nil {
		defer s.RDB.Unlink(context.WithoutCancel(ctx), kept)
		res, err := reach.Capped(ctx, s.RDB, kept, reach.CapCheck{
			Key: func(c uint32) string { return dispatch.CapKey(tenant, c, req.Channel) }, Since: now.Add(-time.Duration(cp.Hours) * time.Hour),
			Max: cp.Max, ExactLimit: capExactLimit, Sample: capSample})
		if err != nil {
			return Estimate{}, err
		}
		est.Capped, est.Cap = &res, cp
		est.WillSend = max(0, counts.Reachable-res.Capped)
	}
	return est, nil
}

// segmentReach: how many members of a segment each channel reaches (after consent, with an address).
func (s *Server) segmentReach(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "reach needs Redis")
		return
	}
	if _, err := s.Store.GetSegment(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	cacheKey := "rchseg:" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(id, 10)
	if b, err := s.RDB.Get(r.Context(), cacheKey).Bytes(); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}
	if !s.allowTenant(r.Context(), "rlest", tenant, estimatesPerMin) {
		writeErr(w, http.StatusTooManyRequests, "too many estimates; try again in a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), estimateTimeLimit)
	defer cancel()
	size, chans, err := reach.ByChannel(ctx, s.RDB, tenant, id, channel.All)
	if err != nil {
		s.fail(w, err)
		return
	}
	ready, err := reach.Ready(ctx, s.RDB, tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	b, _ := json.Marshal(map[string]any{"segment": id, "size": size, "channels": chans, "ready": ready, "computed_at": s.now()})
	s.RDB.Set(ctx, cacheKey, b, estimateCacheTTL)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// ---- provider limits ----

func (s *Server) putProviderLimits(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var l model.ProviderLimits
	if !decode(w, r, &l) {
		return
	}
	if err := dispatch.ValidateLimits(l); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.campStore().SetProviderLimits(r.Context(), tenant, id, l); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "limits": l})
}

// ---- test contacts ----

func (s *Server) listTestContacts(w http.ResponseWriter, r *http.Request, tenant int64) {
	tcs, err := s.campStore().ListTestContacts(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if tcs == nil {
		tcs = []model.TestContact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"test_contacts": tcs})
}

// ValidTestContact checks a test contact: a name, and a contact or at least one address.
func ValidTestContact(tc model.TestContact) error {
	var errs []error
	if strings.TrimSpace(tc.Name) == "" || len(tc.Name) > 100 {
		errs = append(errs, errors.New("give the test contact a name (up to 100 characters)"))
	}
	if tc.ExternalID == "" && tc.Email == "" && tc.Phone == "" && tc.PushToken == "" {
		errs = append(errs, errors.New("choose a contact, or enter an email address, phone number or push token"))
	}
	if tc.Email != "" && !emailRE.MatchString(tc.Email) {
		errs = append(errs, errors.New("the email address isn't valid"))
	}
	if tc.Phone != "" && !e164RE.MatchString(tc.Phone) {
		errs = append(errs, errors.New("write the phone number with its country code, e.g. +919812345678"))
	}
	if len(tc.PushToken) > 4096 {
		errs = append(errs, errors.New("the push token is too long"))
	}
	if providers.IsWebPushSubscription(tc.PushToken) {
		if err := providers.CheckWebPushSubscription(tc.PushToken); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// A test contact with bare addresses (no contact behind it) can be sent anything, with no consent
// check, by anyone with content edit: so only someone who may change channels (channels edit) can
// save one. Content editors send tests to that list, or to real contacts.
func (s *Server) saveTestContact(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	var tc model.TestContact
	if !decode(w, r, &tc) {
		return
	}
	tc.ID = 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		tc.ID = id
	}
	tc.Name, tc.Email, tc.Phone = strings.TrimSpace(tc.Name), strings.TrimSpace(tc.Email), strings.TrimSpace(tc.Phone)
	tc.ExternalID, tc.PushToken = strings.TrimSpace(tc.ExternalID), strings.TrimSpace(tc.PushToken)
	if err := ValidTestContact(tc); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if tc.ExternalID == "" && !p.Perms.Allows(auth.AreaChannels, auth.Edit) {
		writeErr(w, http.StatusForbidden, "saving a test contact with its own addresses needs edit access to channels; choose an existing contact instead")
		return
	}
	if tc.ExternalID != "" {
		if _, err := s.Store.ContactByExternalID(r.Context(), tenant, tc.ExternalID); errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "there is no contact "+tc.ExternalID)
			return
		} else if err != nil {
			s.fail(w, err)
			return
		}
	}
	id, err := s.campStore().SaveTestContact(r.Context(), tenant, tc)
	if err != nil {
		if errors.Is(err, pg.ErrNotFound) {
			s.fail(w, err)
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteTestContact(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.campStore().DeleteTestContact(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var (
	emailRE = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	e164RE  = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
)

// testAddress is a test contact's address for a channel ("" when it has none).
func testAddress(tc model.TestContact, ch string) string {
	switch ch {
	case channel.Email:
		return tc.Email
	case channel.Push:
		return tc.PushToken
	case channel.Inbox:
		return ""
	}
	return tc.Phone
}
