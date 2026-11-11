package api

// Outbound webhooks: workspace settings for posting events to the customer's URLs.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/safehttp"
	"pulse/internal/webhooks"
)

type WebhookStore interface {
	ListWebhooks(ctx context.Context, tenant int64) ([]model.Webhook, error)
	GetWebhook(ctx context.Context, tenant, id int64) (model.Webhook, error)
	SaveWebhook(ctx context.Context, h model.Webhook) (int64, error)
	DeleteWebhook(ctx context.Context, tenant, id int64) error
	WebhookDeliveries(ctx context.Context, tenant, id int64, limit int) ([]model.WebhookDelivery, error)
	RecordDelivery(ctx context.Context, d webhooks.Delivery) error
}

func (s *Server) webhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/settings/webhooks", s.fullAdmin(s.listWebhooks))
	mux.HandleFunc("POST /v1/settings/webhooks", s.fullAdmin(s.saveWebhook))
	mux.HandleFunc("GET /v1/settings/webhooks/{id}", s.fullAdmin(s.getWebhook))
	mux.HandleFunc("PUT /v1/settings/webhooks/{id}", s.fullAdmin(s.saveWebhook))
	mux.HandleFunc("DELETE /v1/settings/webhooks/{id}", s.fullAdmin(s.deleteWebhook))
	mux.HandleFunc("POST /v1/settings/webhooks/{id}/test", s.fullAdmin(s.testWebhook))
}

func (s *Server) hooks() WebhookStore { return s.Store.(WebhookStore) }

func (s *Server) openWebhook(h *model.Webhook) {
	if s.Box != nil {
		if secret, err := s.Box.Open(h.Sealed); err == nil {
			h.Secret = string(secret)
		}
	}
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request, p model.Principal) {
	hs, err := s.hooks().ListWebhooks(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range hs {
		s.openWebhook(&hs[i])
	}
	if hs == nil {
		hs = []model.Webhook{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": hs})
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h, err := s.hooks().GetWebhook(r.Context(), p.TenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.openWebhook(&h)
	ds, err := s.hooks().WebhookDeliveries(r.Context(), p.TenantID, id, 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ds == nil {
		ds = []model.WebhookDelivery{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhook": h, "deliveries": ds})
}

func validWebhook(h model.Webhook) error {
	var errs []error
	if strings.TrimSpace(h.Name) == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if u, err := url.Parse(h.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		errs = append(errs, errors.New("the URL must be http(s)"))
	}
	if len(h.Events) == 0 || len(h.Events) > 50 {
		errs = append(errs, errors.New("choose 1 to 50 events, or * for every event"))
	}
	for _, e := range h.Events {
		if e != "*" && !event.ValidName(e) {
			errs = append(errs, errors.New("invalid event name "+e))
		}
	}
	return errors.Join(errs...)
}

// saveWebhook creates or edits a webhook. "rotate_secret": true replaces the signing secret.
func (s *Server) saveWebhook(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req struct {
		model.Webhook
		RotateSecret bool `json:"rotate_secret"`
	}
	if !decode(w, r, &req) {
		return
	}
	h := req.Webhook
	h.TenantID, h.ID, h.Sealed = p.TenantID, 0, nil
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		h.ID = id
	} else {
		req.RotateSecret = true
	}
	h.Name = strings.TrimSpace(h.Name)
	if err := validWebhook(h); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RotateSecret {
		if s.Box == nil {
			writeErr(w, http.StatusServiceUnavailable, "webhooks need the server's secret key")
			return
		}
		h.Sealed = s.Box.Seal([]byte(auth.NewToken("whsec_")))
	}
	id, err := s.hooks().SaveWebhook(r.Context(), h)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.hooks().DeleteWebhook(r.Context(), p.TenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testWebhook sends a sample "webhook_test" event right away and reports the receiver's answer.
// The attempt is listed with the webhook's deliveries, marked as a test (never retried).
func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h, err := s.hooks().GetWebhook(r.Context(), p.TenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.openWebhook(&h)
	payload := webhooks.Payload{ID: "test-" + auth.NewToken("")[:12], Event: "webhook_test", Ts: s.now().UTC(), Workspace: p.TenantID,
		Contact: webhooks.PayloadContact{ExternalID: "sample", Attrs: map[string]any{"first_name": "Asha", "city": "Pune"}},
		Props:   map[string]any{"note": "A test delivery from Pulse"}}
	body, _ := json.Marshal(payload)
	start := time.Now()
	status, err := webhooks.Send(r.Context(), safehttp.Client(15*time.Second, s.AllowPrivateWebhooks),
		webhooks.Webhook{ID: h.ID, URL: h.URL, Secret: h.Secret}, payload.ID, payload.Event, body, s.now())
	took := time.Since(start).Milliseconds()
	out := map[string]any{"ok": err == nil, "status": status, "duration_ms": took}
	d := webhooks.Delivery{WebhookID: h.ID, EventID: payload.ID, EventName: payload.Event, Attempt: 1, Status: status,
		DurationMS: int(took), Final: true, Test: true}
	if err != nil {
		out["error"] = err.Error()
		d.Error = err.Error()
	}
	if rerr := s.hooks().RecordDelivery(r.Context(), d); rerr != nil {
		s.Log.Error("record test delivery", "webhook", h.ID, "err", rerr)
	}
	writeJSON(w, http.StatusOK, out)
}
