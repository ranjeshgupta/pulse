package api

// Email bounces and complaints, posted by the email service to a per-workspace URL
// (/v1/bounces/{token}). A hard bounce marks the address (email_bounced) so no more email goes to
// it; a complaint unsubscribes the contact from email; a soft bounce is recorded only.

import (
	"context"
	"io"
	"net/http"
	"time"

	"pulse/internal/bounces"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/safehttp"
)

// AttrEmailBounced marks a contact whose address hard-bounced: email to them is suppressed.
const AttrEmailBounced = consent.AttrEmailBounced

type BounceStore interface {
	TenantByBounceToken(ctx context.Context, token string) (int64, error)
	BounceToken(ctx context.Context, tenant int64) (string, error)
}

func (s *Server) bounceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/bounces/{token}", s.bounceWebhook)
	mux.HandleFunc("GET /v1/settings/bounces", s.admin(s.bounceSettings))
	mux.HandleFunc("POST /v1/sandbox/bounce", s.tenant(s.sandboxBounce))
}

func (s *Server) bounceSettings(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tok, err := s.Store.(BounceStore).BounceToken(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": s.PublicURL + "/v1/bounces/" + tok,
		"formats": []string{"sendgrid", "postmark", "mailgun", "ses", "generic"}})
}

func (s *Server) bounceWebhook(w http.ResponseWriter, r *http.Request) {
	tenant, err := s.Store.(BounceStore).TenantByBounceToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	bs, confirm, err := bounces.Parse(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if confirm != nil {
		// Amazon SNS: confirming the subscription is a GET to its (checked) SubscribeURL.
		resp, err := safehttp.Client(10*time.Second, false).Get(confirm.SubscribeURL)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "couldn't confirm the SNS subscription")
			return
		}
		resp.Body.Close()
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := s.applyBounces(r.Context(), tenant, bs); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "retry")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// applyBounces records each bounce on the contact with that email. Unknown addresses are skipped.
func (s *Server) applyBounces(ctx context.Context, tenant int64, bs []bounces.Bounce) error {
	var evs []event.Event
	for _, b := range bs {
		c, err := s.formStore().ContactByEmail(ctx, tenant, b.Email)
		if err != nil {
			continue
		}
		props := map[string]any{"kind": b.Kind, "reason": b.Reason, "source": b.Source, "email": b.Email}
		switch b.Kind {
		case bounces.Hard:
			evs = append(evs, event.Event{ExternalID: c.ExternalID, Name: "email_bounced", Props: props,
				Attrs: map[string]any{AttrEmailBounced: true}})
		case bounces.Soft:
			evs = append(evs, event.Event{ExternalID: c.ExternalID, Name: "email_bounced", Props: props})
		case bounces.Complaint:
			evs = append(evs, event.Event{ExternalID: c.ExternalID, Name: "email_complained", Props: props},
				event.Event{ExternalID: c.ExternalID, Name: consent.Unsubscribed, Props: map[string]any{"channel": "email", "source": "complaint"}})
		}
	}
	if len(evs) == 0 {
		return nil
	}
	return s.publishEvents(ctx, tenant, evs)
}

// sandboxBounce simulates a bounce or complaint for an address: {email, type: hard|soft|complaint}.
func (s *Server) sandboxBounce(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Email string `json:"email"`
		Type  string `json:"type"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Type != bounces.Hard && req.Type != bounces.Soft && req.Type != bounces.Complaint || req.Email == "" {
		writeErr(w, http.StatusBadRequest, "email and type (hard, soft or complaint) are required")
		return
	}
	if _, err := s.formStore().ContactByEmail(r.Context(), tenant, req.Email); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.applyBounces(r.Context(), tenant, []bounces.Bounce{{Email: req.Email, Kind: req.Type, Reason: "simulated", Source: "sandbox"}}); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
