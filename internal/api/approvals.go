package api

// Campaign approvals (O6): someone who may edit a campaign asks for it to launch
// (POST /v1/approvals); someone who may publish it approves — it launches as asked — or sends it
// back with a note. With the workspace's four-eyes setting, launching always goes through an
// approval by someone else (API keys are exempt). A campaign changed after the request isn't
// launched by approving it: it has to be asked for again.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"pulse/internal/journey"
	"pulse/internal/personalize"
	"pulse/internal/store/pg"
	"slices"
	"strconv"
	"strings"

	"pulse/internal/auth"
	"pulse/internal/model"
)

// ApprovalStore keeps approval requests (pg.Store).
type ApprovalStore interface {
	RequestApproval(ctx context.Context, a model.Approval) (int64, error)
	ListApprovals(ctx context.Context, tenant int64, status string, limit int) ([]model.Approval, error)
	GetApproval(ctx context.Context, tenant, id int64) (model.Approval, error)
	LatestApproval(ctx context.Context, tenant int64, kind string, id int64) (model.Approval, error)
	DecideApproval(ctx context.Context, tenant, id int64, status string, by int64, decider, note string) (bool, error)
}

func (s *Server) approvalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/approvals", s.authed(s.listApprovals))
	mux.HandleFunc("GET /v1/approvals/latest", s.authed(s.latestApproval))
	mux.HandleFunc("POST /v1/approvals", s.authed(s.requestApproval))
	mux.HandleFunc("POST /v1/approvals/{id}/{action}", s.authed(s.decideApproval))
}

func approvalArea(kind string) string {
	if kind == "journey" {
		return auth.AreaJourneys
	}
	return auth.AreaBroadcasts
}

func (s *Server) approvals(w http.ResponseWriter) (ApprovalStore, bool) {
	as, ok := s.Store.(ApprovalStore)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "approvals aren't available")
	}
	return as, ok
}

// fourEyes refuses a user's direct launch when the workspace needs approvals (true: answered).
func (s *Server) fourEyes(w http.ResponseWriter, r *http.Request, p model.Principal) bool {
	if p.UserID == 0 {
		return false // API keys and automations
	}
	as, ok := s.Store.(AutomationStore)
	if !ok {
		return false
	}
	set, err := as.AutomationSettings(r.Context(), p.TenantID)
	if err != nil || !set.Approvals.FourEyes {
		return false
	}
	writeErr(w, http.StatusConflict, "this workspace needs a second person to approve every launch: use Ask for approval")
	return true
}

// campaignVersion hashes what a campaign sends: its definition (not its status), every template
// it names (main, fallback, A/B variants, journey steps) and the content blocks those use. A change
// to any of them after the request means it has to be asked for again. It also returns the name.
func (s *Server) campaignVersion(ctx context.Context, tenant int64, kind string, id int64) (string, string, error) {
	var def any
	var name string
	switch kind {
	case "broadcast":
		b, err := s.bcStore().GetBroadcast(ctx, tenant, id)
		if err != nil {
			return "", "", err
		}
		b.Status, b.NextRunAt, b.Tags = "", nil, nil
		b.UpdatedAt = b.StartAt // timestamps that saving alone changes aren't the definition
		def, name = b, b.Name
	case "journey":
		us, ok := s.Store.(UIStore)
		if !ok {
			return "", "", errors.New("journeys aren't available")
		}
		j, err := us.GetJourney(ctx, tenant, id)
		if err != nil {
			return "", "", err
		}
		def, name = []any{j.Name, j.Graph, j.StartAt, j.EndAt}, j.Name
	}
	raw, _ := json.Marshal(def)
	h := sha256.New()
	h.Write(raw)
	// The content it sends.
	var doc any
	_ = json.Unmarshal(raw, &doc)
	ids := map[int64]bool{}
	templateIDs(doc, ids)
	sorted := make([]int64, 0, len(ids))
	for tid := range ids {
		sorted = append(sorted, tid)
	}
	slices.Sort(sorted)
	var used []string
	for _, tid := range sorted {
		t, err := s.uiStore().Template(ctx, tenant, tid)
		if err != nil {
			fmt.Fprintf(h, "|t%d:missing", tid)
			continue
		}
		fmt.Fprintf(h, "|t%d:%s\x00%s\x00%s\x00%v", tid, t.Subject, t.HTML, t.Content, t.Transactional)
		used = append(used, personalize.BlockKeys(t.Subject+t.HTML+string(t.Content))...)
	}
	if len(used) > 0 {
		blocks := s.renderBlocks(ctx, tenant)
		slices.Sort(used)
		for _, k := range slices.Compact(used) {
			fmt.Fprintf(h, "|b%s:%s\x00%s", k, blocks[k].HTML, blocks[k].Text)
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:12]), name, nil
}

// templateIDs collects every number under a key ending in "template_id" in a decoded JSON value.
func templateIDs(v any, out map[int64]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if strings.HasSuffix(k, "template_id") {
				if n, ok := e.(float64); ok && n > 0 {
					out[int64(n)] = true
				}
				continue
			}
			templateIDs(e, out)
		}
	case []any:
		for _, e := range x {
			templateIDs(e, out)
		}
	case string:
		// a journey graph is stored as JSON text inside the definition
		if strings.HasPrefix(strings.TrimSpace(x), "{") && strings.Contains(x, "template_id") {
			var inner any
			if json.Unmarshal([]byte(x), &inner) == nil {
				templateIDs(inner, out)
			}
		}
	}
}

func (s *Server) requestApproval(w http.ResponseWriter, r *http.Request, p model.Principal) {
	as, ok := s.approvals(w)
	if !ok {
		return
	}
	var req struct {
		Kind       string `json:"kind"`
		CampaignID int64  `json:"campaign_id"`
		Note       string `json:"note"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Kind != "broadcast" && req.Kind != "journey" {
		writeErr(w, http.StatusBadRequest, "kind: broadcast or journey")
		return
	}
	if !p.Perms.Allows(approvalArea(req.Kind), auth.Edit) {
		writeErr(w, http.StatusForbidden, "your role can't edit this campaign")
		return
	}
	if len(req.Note) > 1000 {
		writeErr(w, http.StatusBadRequest, "the note is at most 1,000 characters")
		return
	}
	version, _, err := s.campaignVersion(r.Context(), p.TenantID, req.Kind, req.CampaignID)
	if err != nil {
		s.fail(w, err)
		return
	}
	id, err := as.RequestApproval(r.Context(), model.Approval{TenantID: p.TenantID, Kind: req.Kind, CampaignID: req.CampaignID,
		Version: version, RequestedBy: p.UserID, Requester: p.Actor, Note: strings.TrimSpace(req.Note)})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "pending"})
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request, p model.Principal) {
	as, ok := s.approvals(w)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" && status != "withdrawn" {
		writeErr(w, http.StatusBadRequest, "status: pending, approved, rejected or withdrawn")
		return
	}
	list, err := as.ListApprovals(r.Context(), p.TenantID, status, 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []model.Approval{}
	for _, a := range list {
		if p.Perms.Allows(approvalArea(a.Kind), auth.View) {
			out = append(out, a)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": out})
}

func (s *Server) latestApproval(w http.ResponseWriter, r *http.Request, p model.Principal) {
	as, ok := s.approvals(w)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 || (kind != "broadcast" && kind != "journey") {
		writeErr(w, http.StatusBadRequest, "kind and id")
		return
	}
	if !p.Perms.Allows(approvalArea(kind), auth.View) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	a, err := as.LatestApproval(r.Context(), p.TenantID, kind, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"approval": nil})
		return
	}
	cur, _, _ := s.campaignVersion(r.Context(), p.TenantID, kind, id)
	writeJSON(w, http.StatusOK, map[string]any{"approval": a, "changed_since": cur != a.Version})
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request, p model.Principal) {
	as, ok := s.approvals(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	var req struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	a, err := as.GetApproval(r.Context(), p.TenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if a.Status != "pending" {
		writeErr(w, http.StatusConflict, "this request was already "+a.Status)
		return
	}
	switch action {
	case "withdraw":
		if a.RequestedBy != p.UserID && !p.Perms.Allows(approvalArea(a.Kind), auth.Publish) {
			writeErr(w, http.StatusForbidden, "only who asked, or someone who may publish, can withdraw it")
			return
		}
		_, err = as.DecideApproval(r.Context(), p.TenantID, id, "withdrawn", p.UserID, p.Actor, "")
	case "reject", "approve":
		if !p.Perms.Allows(approvalArea(a.Kind), auth.Publish) {
			writeErr(w, http.StatusForbidden, "your role can't launch this campaign")
			return
		}
		if p.UserID != 0 && a.RequestedBy == p.UserID {
			writeErr(w, http.StatusForbidden, "someone other than who asked must decide")
			return
		}
		if action == "reject" {
			_, err = as.DecideApproval(r.Context(), p.TenantID, id, "rejected", p.UserID, p.Actor, strings.TrimSpace(req.Note))
			break
		}
		cur, _, verr := s.campaignVersion(r.Context(), p.TenantID, a.Kind, a.CampaignID)
		if verr != nil {
			s.fail(w, verr)
			return
		}
		if cur != a.Version {
			_, _ = as.DecideApproval(r.Context(), p.TenantID, id, "withdrawn", p.UserID, p.Actor, "the campaign changed after it was asked for")
			writeErr(w, http.StatusConflict, "the campaign changed after it was asked for: ask for approval again")
			return
		}
		won, derr := as.DecideApproval(r.Context(), p.TenantID, id, "approved", p.UserID, p.Actor, strings.TrimSpace(req.Note))
		if derr != nil || !won {
			if derr == nil {
				writeErr(w, http.StatusConflict, "someone decided this request a moment ago")
				return
			}
			s.fail(w, derr)
			return
		}
		// Launch as asked, then make sure nothing changed in between (a save racing the approval):
		// if it did, the launch is undone.
		var out map[string]any
		if a.Kind == "broadcast" {
			var code int
			var lerr error
			if out, code, lerr = s.scheduleBroadcastNow(r.Context(), p.TenantID, a.CampaignID); lerr != nil {
				_, _ = as.DecideApproval(r.Context(), p.TenantID, id, "withdrawn", p.UserID, p.Actor, "launching failed: "+lerr.Error())
				if code == 0 {
					s.fail(w, lerr)
				} else {
					writeErr(w, code, lerr.Error())
				}
				return
			}
		} else {
			if g, gerr := s.journeyGraph(r.Context(), p.TenantID, a.CampaignID); gerr == nil && !deletePermOK(w, p, g) {
				return
			}
			if lerr := s.Store.SetJourneyStatus(r.Context(), p.TenantID, a.CampaignID, "active"); lerr != nil {
				s.fail(w, lerr)
				return
			}
			s.journeysChanged(r.Context())
			out = map[string]any{"id": a.CampaignID, "status": "active"}
		}
		if after, _, _ := s.campaignVersion(r.Context(), p.TenantID, a.Kind, a.CampaignID); after != a.Version {
			if a.Kind == "broadcast" {
				_ = s.bcStore().SetBroadcastSchedule(r.Context(), p.TenantID, a.CampaignID, "draft", nil)
			} else {
				_ = s.Store.SetJourneyStatus(r.Context(), p.TenantID, a.CampaignID, "paused")
				s.journeysChanged(r.Context())
			}
			writeErr(w, http.StatusConflict, "the campaign changed while it was being approved, so it wasn't launched: ask for approval again")
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	default:
		writeErr(w, http.StatusNotFound, "unknown action")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": map[string]string{"withdraw": "withdrawn", "reject": "rejected"}[action]})
}

// journeyGraph loads a journey's graph.
func (s *Server) journeyGraph(ctx context.Context, tenant, id int64) (journey.Graph, error) {
	us, ok := s.Store.(UIStore)
	if !ok {
		return journey.Graph{}, errors.New("journeys aren't available")
	}
	j, err := us.GetJourney(ctx, tenant, id)
	if err != nil {
		return journey.Graph{}, err
	}
	return journey.ParseGraph(j.Graph)
}

// ---- four-eyes: no unapproved content through a live campaign ----

// liveUsing names the live campaigns (scheduled, running or testing one-time campaigns, active
// journeys) that send any of the templates tids.
func (s *Server) liveUsing(ctx context.Context, tenant int64, tids map[int64]bool) ([]string, error) {
	sends := func(def any) bool {
		raw, _ := json.Marshal(def)
		var doc any
		_ = json.Unmarshal(raw, &doc)
		ids := map[int64]bool{}
		templateIDs(doc, ids)
		for id := range ids {
			if tids[id] {
				return true
			}
		}
		return false
	}
	var names []string
	bs, err := s.bcStore().ListBroadcasts(ctx, tenant)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		if (b.Status == "scheduled" || b.Status == "running" || b.Status == "testing") && sends(b) {
			names = append(names, b.Name)
		}
	}
	if us, ok := s.Store.(UIStore); ok {
		js, err := us.ListJourneys(ctx, tenant)
		if err != nil {
			return nil, err
		}
		for _, j := range js {
			if j.Status == "active" && sends([]any{j.Graph}) {
				names = append(names, j.Name)
			}
		}
	}
	return names, nil
}

// liveLocked refuses (409) an edit to content that live campaigns send when the workspace needs
// four-eyes launches: the change would reach people without the approval their launch needed.
// API keys are exempt, as from approvals. It returns true when it answered.
func (s *Server) liveLocked(w http.ResponseWriter, r *http.Request, p model.Principal, tids map[int64]bool) bool {
	as, ok := s.Store.(AutomationStore)
	if p.UserID == 0 || !ok || len(tids) == 0 {
		return false
	}
	set, err := as.AutomationSettings(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err) // fail closed: an unreadable setting never lets an edit through
		return true
	}
	if !set.Approvals.FourEyes {
		return false
	}
	names, err := s.liveUsing(r.Context(), p.TenantID, tids)
	if err != nil {
		s.fail(w, err)
		return true
	}
	if len(names) == 0 {
		return false
	}
	if len(names) > 5 {
		names = append(names[:5], fmt.Sprintf("%d more", len(names)-5))
	}
	writeErr(w, http.StatusConflict, "this workspace needs approval for what campaigns send, and "+strings.Join(names, ", ")+
		" send this content right now: pause them first, make the change, then ask for approval again")
	return true
}

// guardTemplateEdit wraps a template update or delete with liveLocked.
func (s *Server) guardTemplateEdit(h tenantHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p model.Principal) {
		if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil && s.liveLocked(w, r, p, map[int64]bool{id: true}) {
			return
		}
		h(w, r, p.TenantID)
	})
}

// TemplateTexts reads every template's wording (pg.Store).
type TemplateTexts interface {
	TemplateTexts(ctx context.Context, tenant int64) ([]model.Template, error)
}

// normalizeBlockKey is a block key as saveBlock stores it (the guard must see the same key).
func normalizeBlockKey(k string) string { return strings.TrimSpace(k) }

// templateBlockKeys are the blocks a template names, found the way the renderer finds them: in
// the subject and HTML, and in every string of its content and translations once decoded.
func templateBlockKeys(t model.Template) map[string]bool {
	out := map[string]bool{}
	add := func(src string) {
		for _, k := range personalize.BlockKeys(src) {
			out[k] = true
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			add(x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	content := func(raw json.RawMessage) {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			walk(v)
		}
	}
	add(t.Subject)
	add(t.HTML)
	content(t.Content)
	for _, tr := range t.Translations {
		add(tr.Subject)
		add(tr.HTML)
		content(tr.Content)
	}
	return out
}

// guardBlockEdit wraps creating, changing or deleting a content block with liveLocked, for the
// templates that name the block's current key and the key it is given: creating a block, or
// renaming one, under a key a live template already names changes what that template sends.
func (s *Server) guardBlockEdit(h tenantHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p model.Principal) {
		bs, ok1 := s.Store.(BlockStore)
		bu, ok2 := s.Store.(TemplateTexts)
		if !ok1 || !ok2 {
			h(w, r, p.TenantID)
			return
		}
		keys := map[string]bool{}
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			if err != nil {
				writeErr(w, http.StatusBadRequest, "the request couldn't be read")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var in struct {
				Key string `json:"key"`
			}
			// Decoded exactly as saveBlock decodes it (decode: json.Decoder, first value), so both
			// see the same key; a body it can't read is refused here, not passed on.
			if err := json.NewDecoder(bytes.NewReader(body)).Decode(&in); err != nil {
				writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
				return
			}
			// The key as saveBlock will store it; one it would refuse saves nothing anyway.
			if k := normalizeBlockKey(in.Key); personalize.BlockKeyRe.MatchString(k) {
				keys[k] = true
			}
		}
		if r.PathValue("id") != "" {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err == nil {
				b, err := bs.GetBlock(r.Context(), p.TenantID, id)
				switch {
				case err == nil:
					keys[b.Key] = true
				case !errors.Is(err, pg.ErrNotFound):
					s.fail(w, err)
					return
				}
			}
		}
		tids := map[int64]bool{}
		if len(keys) > 0 {
			ts, err := bu.TemplateTexts(r.Context(), p.TenantID)
			if err != nil {
				s.fail(w, err)
				return
			}
			for _, t := range ts {
				for k := range templateBlockKeys(t) {
					if keys[k] {
						tids[t.ID] = true
					}
				}
			}
		}
		if s.liveLocked(w, r, p, tids) {
			return
		}
		h(w, r, p.TenantID)
	})
}
