package api

// The custom gateway setup's "Try it" checks: how Pulse reads a pasted send answer (step 3), how
// it reads a pasted delivery report or status answer (step 4), and one real status request for a
// message id (step 4, "We ask them"). The first two send nothing.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"pulse/internal/model"
	"pulse/internal/providers"
)

func (s *Server) gatewayCheckRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/channels/providers/check-answer", s.admin(s.checkGatewayAnswer))
	mux.HandleFunc("POST /v1/channels/providers/check-report", s.admin(s.checkGatewayReport))
	mux.HandleFunc("POST /v1/channels/providers/check-status", s.admin(s.checkGatewayStatus))
}

type gatewayCheck struct {
	providerDraft
	Status int    `json:"status"` // check-answer: the answer's HTTP status (default 200)
	Body   string `json:"body"`   // the pasted answer or report
	// check-status: the message to ask about
	ProviderMessageID string `json:"provider_message_id"`
	MessageID         string `json:"message_id"`
}

const maxPasted = 256 << 10

// draftGateway builds the gateway being edited; ok false has answered already.
func (s *Server) draftGateway(w http.ResponseWriter, r *http.Request, pr model.Principal, d *gatewayCheck) (*providers.Gateway, map[string]string, bool) {
	if !decode(w, r, d) {
		return nil, nil, false
	}
	if len(d.Body) > maxPasted {
		writeErr(w, http.StatusBadRequest, "paste at most 256 KB")
		return nil, nil, false
	}
	p, _, secrets, code, err := s.draftProvider(r.Context(), pr.TenantID, d.providerDraft)
	if err != nil {
		writeErr(w, code, err.Error())
		return nil, nil, false
	}
	g, ok := p.(*providers.Gateway)
	if !ok || g.Custom == nil {
		writeErr(w, http.StatusBadRequest, "these checks are for a gateway with your own request")
		return nil, nil, false
	}
	return g, secrets, true
}

func (s *Server) checkGatewayAnswer(w http.ResponseWriter, r *http.Request, pr model.Principal) {
	var d gatewayCheck
	g, _, ok := s.draftGateway(w, r, pr, &d)
	if !ok {
		return
	}
	if d.Status == 0 {
		d.Status = http.StatusOK
	}
	res, err := g.CheckAnswer(d.Status, []byte(d.Body))
	out := map[string]any{"accepted": err == nil, "provider_message_id": res.ProviderMessageID}
	switch {
	case err == nil && g.Custom.MatchBy == providers.MatchPulse:
		out["matched_by"] = "pulse"
	case err == nil && res.ProviderMessageID == "" && g.Custom.Receipt != nil:
		out["warning"] = "No message id found where you said it is, so delivery reports can't be matched. Set the id's place, or match reports by Pulse's message id."
	case err != nil:
		out["error"] = err.Error()
		// The recipient can't get messages on this channel: the SMS fallback would run.
		out["fallback"] = errors.Is(err, providers.ErrUnreachable)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) checkGatewayReport(w http.ResponseWriter, r *http.Request, pr model.Principal) {
	var d gatewayCheck
	g, _, ok := s.draftGateway(w, r, pr, &d)
	if !ok {
		return
	}
	rs, err := g.CheckReport([]byte(d.Body))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if rs == nil {
		rs = []providers.Receipt{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipts": rs})
}

func (s *Server) checkGatewayStatus(w http.ResponseWriter, r *http.Request, pr model.Principal) {
	var d gatewayCheck
	g, secrets, ok := s.draftGateway(w, r, pr, &d)
	if !ok {
		return
	}
	if _, _, on := g.Polls(); !on {
		writeErr(w, http.StatusBadRequest, "set up the status request first")
		return
	}
	if strings.TrimSpace(d.ProviderMessageID) == "" && strings.TrimSpace(d.MessageID) == "" {
		writeErr(w, http.StatusBadRequest, "give a message id to ask about")
		return
	}
	if !s.allowTenant(r.Context(), "authtest", pr.TenantID, testAuthsPerMinute) {
		writeErr(w, http.StatusTooManyRequests, "too many tests; try again in a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	key := d.ProviderMessageID
	if key == "" {
		key = d.MessageID
	}
	rs, err := g.Poll(ctx, providers.Outbound{TenantID: pr.TenantID, MessageID: d.MessageID, ProviderMessageID: d.ProviderMessageID, To: d.To}, key)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": scrubSecrets(err.Error(), secrets)})
		return
	}
	if rs == nil {
		rs = []providers.Receipt{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "receipts": rs})
}
