package api

// Channels: provider settings, test sends, the sandbox inbox, push device registration and
// delivery-receipt webhooks.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/dispatch"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/providers"
	"pulse/internal/store/pg"
)

type ChannelStore interface {
	ListProviders(ctx context.Context, tenant int64) ([]model.Provider, error)
	GetProvider(ctx context.Context, tenant, id int64) (model.Provider, error)
	SaveProvider(ctx context.Context, p model.Provider, sealed []byte) (int64, error)
	DeleteProvider(ctx context.Context, tenant, id int64) error
	ProviderForWebhook(ctx context.Context, id int64) (model.Provider, []byte, error)
	ListSandbox(ctx context.Context, tenant int64, ch string, limit int) ([]model.SandboxMessage, error)
	ResolveContacts(ctx context.Context, tenant int64, externalIDs []string) (map[string]model.Contact, map[string]bool, error)
	UpsertDevice(ctx context.Context, tenant int64, contact uint32, d model.Device) error
}

func (s *Server) channelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/channels/catalog", s.tenant(s.catalog))
	mux.HandleFunc("GET /v1/channels/providers", s.tenant(s.listProviders))
	mux.HandleFunc("POST /v1/channels/providers", s.admin(s.saveProvider))
	mux.HandleFunc("PUT /v1/channels/providers/{id}", s.admin(s.saveProvider))
	mux.HandleFunc("DELETE /v1/channels/providers/{id}", s.admin(s.deleteProvider))
	mux.HandleFunc("POST /v1/channels/test", s.tenant(s.testSend))
	mux.HandleFunc("GET /v1/sandbox/messages", s.tenant(s.sandboxMessages))
	mux.HandleFunc("POST /v1/sandbox/receipts", s.tenant(s.sandboxReceipt))
	mux.HandleFunc("POST /v1/sandbox/inbound", s.tenant(s.sandboxInbound))
	mux.HandleFunc("POST /v1/devices", s.tenant(s.registerDevice))
	mux.HandleFunc("POST /v1/webhooks/providers/{id}", s.providerWebhook)
	mux.HandleFunc("GET /v1/webhooks/providers/{id}", s.providerWebhookChallenge)
}

// providerWebhookChallenge answers providers that verify a webhook URL before using it.
func (s *Server) providerWebhookChallenge(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.webhookProvider(w, r)
	if !ok {
		return
	}
	cr, ok := p.(providers.ChallengeResponder)
	if !ok {
		writeErr(w, http.StatusNotFound, "this provider has no webhook challenge")
		return
	}
	resp, ok := cr.Challenge(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "invalid challenge")
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(resp))
}

// webhookProvider builds the adapter a webhook URL points to, with its secrets.
func (s *Server) webhookProvider(w http.ResponseWriter, r *http.Request) (providers.Provider, model.Provider, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, model.Provider{}, false
	}
	cfg, sealed, err := s.chStore().ProviderForWebhook(r.Context(), id)
	if errors.Is(err, pg.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil, cfg, false
	} else if err != nil {
		s.fail(w, err)
		return nil, cfg, false
	}
	secrets, err := dispatch.OpenSecrets(s.Box, sealed)
	if err != nil {
		s.fail(w, err)
		return nil, cfg, false
	}
	p, err := providers.New(cfg.Kind, providers.Deps{}, cfg.Config, secrets)
	if err != nil {
		s.fail(w, err)
		return nil, cfg, false
	}
	return p, cfg, true
}

func (s *Server) chStore() ChannelStore { return s.Store.(ChannelStore) }

func (s *Server) catalog(w http.ResponseWriter, _ *http.Request, _ int64) {
	writeJSON(w, http.StatusOK, map[string]any{"channels": channel.All, "providers": providers.Catalog()})
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request, tenant int64) {
	ps, err := s.chStore().ListProviders(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range ps {
		ps[i].WebhookURL = s.webhookURL(ps[i].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": ps})
}

func (s *Server) webhookURL(id int64) string {
	return s.PublicURL + "/v1/webhooks/providers/" + strconv.FormatInt(id, 10)
}

// saveProvider creates (POST) or updates (PUT) a provider. Secrets are encrypted before storage and
// never returned; omitting them on update keeps the stored ones.
func (s *Server) saveProvider(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req model.Provider
	if !decode(w, r, &req) {
		return
	}
	req.TenantID = p.TenantID
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		cur, err := s.chStore().GetProvider(r.Context(), p.TenantID, id)
		if err != nil {
			s.fail(w, err)
			return
		}
		req.ID, req.Channel, req.Kind = id, cur.Channel, cur.Kind // kind and channel are fixed
	}
	kind, ok := providers.Lookup(req.Kind)
	if !ok || kind.Channel != req.Channel {
		writeErr(w, http.StatusBadRequest, "unknown provider for this channel")
		return
	}
	if req.Name == "" {
		req.Name = kind.Name
	}
	if req.Config == nil {
		req.Config = map[string]string{}
	}
	if err := kind.Validate(req.Config, req.Secrets, req.ID == 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Building the adapter now catches malformed credentials (e.g. a broken service-account JSON)
	// at save time, instead of as endlessly retried sends.
	if len(req.Secrets) > 0 {
		if _, err := providers.New(req.Kind, providers.Deps{}, req.Config, req.Secrets); err != nil {
			writeErr(w, http.StatusBadRequest, "these settings don't work: "+err.Error())
			return
		}
	}
	var sealed []byte
	if len(req.Secrets) > 0 {
		if s.Box == nil {
			writeErr(w, http.StatusServiceUnavailable, "server has no secret key configured")
			return
		}
		b, _ := json.Marshal(req.Secrets)
		sealed = s.Box.Seal(b)
	}
	id, err := s.chStore().SaveProvider(r.Context(), req, sealed)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Twilio only reports delivery when each message names a callback URL: point it at this provider.
	if req.Kind == "twilio" && req.Config["status_callback_url"] == "" {
		req.ID, req.Config["status_callback_url"] = id, s.webhookURL(id)
		if _, err := s.chStore().SaveProvider(r.Context(), req, nil); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "webhook_url": s.webhookURL(id)})
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.chStore().DeleteProvider(r.Context(), p.TenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testSend sends a template to one contact now, through the normal dispatch path.
func (s *Server) testSend(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		TemplateID         int64  `json:"template_id"`
		FallbackTemplateID int64  `json:"fallback_template_id"`
		ExternalID         string `json:"external_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	t, err := s.uiStore().Template(r.Context(), tenant, req.TemplateID)
	if err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, req.ExternalID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if req.FallbackTemplateID != 0 && !channel.FallsBackToSMS(t.Channel) {
		writeErr(w, http.StatusBadRequest, "only WhatsApp and RCS messages take an SMS fallback")
		return
	}
	m := model.Message{ID: "test:" + auth.NewToken(""), TenantID: tenant, ContactID: c.ID, Channel: t.Channel,
		TemplateID: t.ID, FallbackTemplateID: req.FallbackTemplateID}
	rec, err := kafka.JSON(channel.Topic(t.Channel), event.ContactKey(tenant, c.ID), m)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Pub.Publish(r.Context(), rec); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"message_id": m.ID})
}

func (s *Server) sandboxMessages(w http.ResponseWriter, r *http.Request, tenant int64) {
	ms, err := s.chStore().ListSandbox(r.Context(), tenant, r.URL.Query().Get("channel"), 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": ms})
}

// sandboxReceipt simulates a provider delivery report for a sandbox message, e.g. a WhatsApp message
// that later fails as undeliverable, which triggers the SMS fallback.
func (s *Server) sandboxReceipt(w http.ResponseWriter, r *http.Request, tenant int64) {
	var rc providers.Receipt
	if !decode(w, r, &rc) {
		return
	}
	if rc.ProviderMessageID == "" || (rc.Status != "delivered" && rc.Status != "failed" && rc.Status != "read") {
		writeErr(w, http.StatusBadRequest, "provider_message_id and status (delivered|failed|read) are required")
		return
	}
	if err := s.publishReceipts(r.Context(), tenant, []providers.Receipt{rc}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// registerDevice stores a push token for a contact (called by the mobile/web SDKs or a backend).
func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		ExternalID string `json:"external_id"`
		Token      string `json:"token"`
		Platform   string `json:"platform"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.ExternalID == "" || req.Token == "" || len(req.Token) > 4096 ||
		(req.Platform != "android" && req.Platform != "ios" && req.Platform != "web") {
		writeErr(w, http.StatusBadRequest, "external_id, token and platform (android|ios|web) are required")
		return
	}
	found, _, err := s.chStore().ResolveContacts(r.Context(), tenant, []string{req.ExternalID})
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.chStore().UpsertDevice(r.Context(), tenant, found[req.ExternalID].ID, model.Device{Token: req.Token, Platform: req.Platform}); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// providerWebhook receives delivery reports. The adapter verifies the provider's signature with the
// stored secrets before anything is trusted.
func (s *Server) providerWebhook(w http.ResponseWriter, r *http.Request) {
	p, cfg, ok := s.webhookProvider(w, r)
	if !ok {
		return
	}
	tenant := cfg.TenantID
	parser, ok := p.(providers.ReceiptParser)
	if !ok {
		writeErr(w, http.StatusNotFound, "this provider does not accept webhooks")
		return
	}
	receipts, err := parser.ParseReceipts(r)
	if errors.Is(err, providers.ErrUnauthorized) {
		writeErr(w, http.StatusUnauthorized, "invalid signature")
		return
	} else if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var delivery []providers.Receipt
	for _, rc := range receipts {
		if rc.Status != providers.Inbound {
			delivery = append(delivery, rc)
			continue
		}
		if err := s.handleInbound(r.Context(), tenant, cfg.Channel, rc); err != nil {
			s.Log.Error("inbound message", "provider", cfg.ID, "err", err)
			writeErr(w, http.StatusServiceUnavailable, "retry")
			return
		}
	}
	if err := s.publishReceipts(r.Context(), tenant, delivery); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "retry")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) publishReceipts(ctx context.Context, tenant int64, rs []providers.Receipt) error {
	if len(rs) == 0 {
		return nil
	}
	recs := make([]*kgo.Record, 0, len(rs))
	for _, rc := range rs {
		rec, err := kafka.JSON(dispatch.TopicReceipts, []byte(strconv.FormatInt(tenant, 10)+":"+rc.ProviderMessageID),
			dispatch.ReceiptRecord{TenantID: tenant, Receipt: rc})
		if err != nil {
			return err
		}
		recs = append(recs, rec)
	}
	return s.Pub.Publish(ctx, recs...)
}
