package api

// Campaign approvals (O6): someone who may edit a campaign asks for it to launch
// (POST /v1/approvals); someone who may publish it approves — it launches as asked — or sends it
// back with a note. With the workspace's four-eyes setting, launching always goes through an
// approval by someone else (API keys are exempt). A campaign changed after the request isn't
// launched by approving it: it has to be asked for again.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
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

// campaignVersion hashes a campaign's definition (not its status), and returns its name.
func (s *Server) campaignVersion(ctx context.Context, tenant int64, kind string, id int64) (string, string, error) {
	var v any
	var name string
	switch kind {
	case "broadcast":
		b, err := s.bcStore().GetBroadcast(ctx, tenant, id)
		if err != nil {
			return "", "", err
		}
		b.Status, b.NextRunAt, b.Tags = "", nil, nil
		b.UpdatedAt = b.StartAt // timestamps that saving alone changes aren't the definition
		v, name = b, b.Name
	case "journey":
		us, ok := s.Store.(UIStore)
		if !ok {
			return "", "", errors.New("journeys aren't available")
		}
		j, err := us.GetJourney(ctx, tenant, id)
		if err != nil {
			return "", "", err
		}
		v, name = []any{j.Name, j.Graph, j.StartAt, j.EndAt}, j.Name
	}
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12]), name, nil
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
		// Launch as asked; the launch writes the answer.
		if a.Kind == "broadcast" {
			s.launchBroadcast(w, r, p.TenantID, a.CampaignID)
		} else {
			s.setJourneyStatus(w, r, p, a.CampaignID, "active")
		}
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
