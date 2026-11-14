package api

// Channels: provider settings, test sends, the sandbox inbox, push device registration and
// delivery-receipt webhooks.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"pulse/internal/reach"
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
	s.providerToolRoutes(mux)
}

// providerWebhookChallenge answers providers that verify a webhook URL before using it.
func (s *Server) providerWebhookChallenge(w http.ResponseWriter, r *http.Request) {
	p, cfg, ok := s.webhookProvider(w, r)
	if !ok {
		return
	}
	cr, ok := p.(providers.ChallengeResponder)
	if g, get := p.(providers.GetReceiver); !ok && get && g.ReceivesGet() {
		s.receipts(w, r, p, cfg) // delivery reports sent with GET (Gupshup)
		return
	}
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
	p, err := providers.New(cfg.Kind, s.providerDeps(cfg.TenantID, cfg.ID), cfg.Config, secrets)
	if err != nil {
		s.fail(w, err)
		return nil, cfg, false
	}
	return p, cfg, true
}

func (s *Server) chStore() ChannelStore { return s.Store.(ChannelStore) }

// providerFor checks that a chosen provider is one of the workspace's, for this channel.
func (s *Server) providerFor(ctx context.Context, tenant, id int64, ch string) error {
	cs, ok := s.Store.(ChannelStore)
	if !ok {
		return errors.New("providers aren't available")
	}
	p, err := cs.GetProvider(ctx, tenant, id)
	if errors.Is(err, pg.ErrNotFound) || (err == nil && p.Channel != ch) {
		return fmt.Errorf("the chosen provider isn't one of this workspace's %s providers", channelWords(ch))
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Server) catalog(w http.ResponseWriter, _ *http.Request, _ int64) {
	writeJSON(w, http.StatusOK, map[string]any{"channels": channel.All, "providers": providers.Catalog(), "send_rates": s.SendRates})
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request, tenant int64) {
	ps, err := s.chStore().ListProviders(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range ps {
		ps[i].WebhookURL = s.webhookURL(ps[i].ID)
		if s.RDB != nil {
			ps[i].UsageToday, _ = dispatch.UsageToday(r.Context(), s.RDB, ps[i].ID, ps[i].Limits, s.now())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": ps})
}

func (s *Server) webhookURL(id int64) string {
	return s.PublicURL + "/v1/webhooks/providers/" + strconv.FormatInt(id, 10)
}

// saveProvider creates (POST) or updates (PUT) a provider. Secrets are encrypted before storage and
// never returned; on update, a secret left empty keeps its stored value (merged key by key), unless
// an endpoint's host changed: then the stored secrets are dropped and must be entered again, so
// credentials never follow a URL to a new host. A new provider starts with its kind's preset
// limits (Gmail's daily quota…) or the workspace's default limits for the channel.
func (s *Server) saveProvider(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req model.Provider
	if !decode(w, r, &req) {
		return
	}
	req.TenantID = p.TenantID
	stored := map[string]string{}
	var prevConfig map[string]string
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
		prevConfig = cur.Config
		if stored, err = s.storedSecrets(r.Context(), p.TenantID, id); err != nil {
			s.fail(w, err)
			return
		}
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
	hostsChanged := prevConfig != nil && providers.EndpointsChanged(prevConfig, req.Config)
	if hostsChanged {
		stored = map[string]string{} // whoever points it elsewhere enters the credentials again
	}
	if prevConfig != nil && !hostsChanged && providers.CredentialSettingsChanged(prevConfig, req.Config) {
		for k := range stored {
			if req.Secrets[k] == "" {
				writeErr(w, http.StatusBadRequest, "the authentication or request settings changed: enter every secret again to save them")
				return
			}
		}
	}
	merged := mergeSecrets(stored, req.Secrets)
	refuse := func(msg string) {
		if hostsChanged {
			msg += " (an endpoint's host changed, so the saved secrets were cleared: enter them again)"
		}
		writeErr(w, http.StatusBadRequest, msg)
	}
	if err := kind.Validate(req.Config, merged, true); err != nil {
		refuse(err.Error())
		return
	}
	if err := providers.CheckConfig(req.Kind, req.Config, merged, s.AllowPrivateWebhooks); err != nil {
		refuse(err.Error())
		return
	}
	// Building the adapter now catches malformed credentials (e.g. a broken service-account JSON)
	// and settings (a request template that doesn't parse) at save time, instead of as endlessly
	// retried sends.
	if _, err := providers.New(req.Kind, providers.Deps{AllowPrivate: s.AllowPrivateWebhooks}, req.Config, merged); err != nil {
		refuse("these settings don't work: " + err.Error())
		return
	}
	var sealed []byte
	if len(req.Secrets) > 0 || hostsChanged {
		if s.Box == nil {
			writeErr(w, http.StatusServiceUnavailable, "server has no secret key configured")
			return
		}
		b, _ := json.Marshal(merged)
		sealed = s.Box.Seal(b)
	}
	id, err := s.chStore().SaveProvider(r.Context(), req, sealed)
	if err != nil {
		s.fail(w, err)
		return
	}
	if req.ID == 0 {
		var explicit *model.ProviderLimits
		if req.Limits != (model.ProviderLimits{}) {
			explicit = &req.Limits
		}
		if l := s.newProviderLimits(r.Context(), p.TenantID, req.Channel, explicit, kind.DefaultLimits); l != (model.ProviderLimits{}) {
			if err := dispatch.ValidateLimits(l); err == nil {
				if err := s.campStore().SetProviderLimits(r.Context(), p.TenantID, id, l); err != nil {
					s.fail(w, err)
					return
				}
			}
		}
	}
	// Twilio only reports delivery when each message names a callback URL: point it at this provider.
	if req.Kind == "twilio" && req.Config["status_callback_url"] == "" {
		req.ID, req.Config["status_callback_url"] = id, s.webhookURL(id)
		if _, err := s.chStore().SaveProvider(r.Context(), req, nil); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "webhook_url": s.webhookURL(id), "secrets_cleared": hostsChanged})
}

// mergeSecrets is stored overlaid with the non-empty edited values.
func mergeSecrets(stored, edited map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range edited {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// storedSecrets opens a provider's saved secrets (of this workspace only).
func (s *Server) storedSecrets(ctx context.Context, tenant, id int64) (map[string]string, error) {
	cfg, sealed, err := s.chStore().ProviderForWebhook(ctx, id)
	if err != nil {
		return nil, err
	}
	if cfg.TenantID != tenant {
		return nil, pg.ErrNotFound
	}
	return dispatch.OpenSecrets(s.Box, sealed)
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

// testSendsPerMinute caps test sends per workspace: they skip consent, so they mustn't become a way
// to mail anyone in bulk.
const testSendsPerMinute = 30

// testSend sends a template to one contact now, through the normal dispatch path.
func (s *Server) testSend(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		TemplateID         int64  `json:"template_id"`
		FallbackTemplateID int64  `json:"fallback_template_id"`
		ExternalID         string `json:"external_id"`
		// TestContactID sends to a saved test contact instead: its contact, or its address for the channel.
		TestContactID int64 `json:"test_contact_id"`
		// ProviderID sends through this provider of the template's channel; 0 is the default.
		ProviderID int64 `json:"provider_id"`
		// Event: sample trigger event properties for {{event.x}} (as a journey would carry them).
		Event map[string]any `json:"event"`
	}
	if !decode(w, r, &req) || !eventFits(w, req.Event) {
		return
	}
	if !s.allowTenant(r.Context(), "testsend", tenant, testSendsPerMinute) {
		writeErr(w, http.StatusTooManyRequests, "too many test sends; try again in a minute")
		return
	}
	t, err := s.uiStore().Template(r.Context(), tenant, req.TemplateID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if req.FallbackTemplateID != 0 && !channel.FallsBackToSMS(t.Channel) {
		writeErr(w, http.StatusBadRequest, "only WhatsApp and RCS messages take an SMS fallback")
		return
	}
	if req.ProviderID != 0 {
		if err := s.providerFor(r.Context(), tenant, req.ProviderID, t.Channel); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	m := model.Message{ID: "test:" + auth.NewToken(""), TenantID: tenant, Channel: t.Channel,
		TemplateID: t.ID, FallbackTemplateID: req.FallbackTemplateID, Campaign: "Test send", ProviderID: req.ProviderID}
	if req.TestContactID != 0 {
		tc, err := s.campStore().GetTestContact(r.Context(), tenant, req.TestContactID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if tc.ExternalID != "" {
			req.ExternalID = tc.ExternalID
		} else if addr := testAddress(tc, t.Channel); addr != "" {
			m.TestTo, m.TestName, m.FallbackTemplateID = addr, tc.Name, 0
		} else {
			writeErr(w, http.StatusBadRequest, tc.Name+" has no address for "+t.Channel)
			return
		}
	}
	if m.TestTo == "" {
		c, err := s.Store.ContactByExternalID(r.Context(), tenant, req.ExternalID)
		if err != nil {
			s.fail(w, err)
			return
		}
		m.ContactID = c.ID
	}
	if len(req.Event) > 0 {
		m.Event, _ = json.Marshal(req.Event)
	}
	rec, err := kafka.JSON(channel.Topic(t.Channel), event.ContactKey(tenant, m.ContactID), m)
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
	ok := rc.Status == "delivered" || rc.Status == "failed" || rc.Status == "read" || rc.Status == "complained" ||
		rc.Status == "bounced" && (rc.Kind == "hard" || rc.Kind == "soft")
	if rc.ProviderMessageID == "" || !ok || rc.Status == providers.Inbound {
		writeErr(w, http.StatusBadRequest, "provider_message_id and status (delivered|failed|read|complained, or bounced with kind hard|soft) are required")
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
	s.pushReachable(r.Context(), tenant, found[req.ExternalID].ID)
	w.WriteHeader(http.StatusNoContent)
}

// providerWebhook receives delivery reports. The adapter verifies the provider's signature with the
// stored secrets before anything is trusted.
func (s *Server) providerWebhook(w http.ResponseWriter, r *http.Request) {
	p, cfg, ok := s.webhookProvider(w, r)
	if !ok {
		return
	}
	s.receipts(w, r, p, cfg)
}

// receipts parses and publishes a provider's delivery reports and inbound messages.
func (s *Server) receipts(w http.ResponseWriter, r *http.Request, p providers.Provider, cfg model.Provider) {
	tenant := cfg.TenantID
	parser, ok := p.(providers.ReceiptParser)
	if !ok {
		writeErr(w, http.StatusNotFound, "this provider does not accept webhooks")
		return
	}
	if bc, ok := p.(providers.BodyChallenger); ok { // e.g. RBM's {clientToken, secret} URL check
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "unreadable body")
			return
		}
		if resp, isChallenge, valid := bc.BodyChallenge(body); isChallenge {
			if !valid {
				writeErr(w, http.StatusUnauthorized, "invalid client token")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(resp)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
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

// pushReachable marks a contact reachable by push (reachability bitmaps, package reach).
func (s *Server) pushReachable(ctx context.Context, tenant int64, contact uint32) {
	if s.RDB != nil {
		_ = reach.SetPush(ctx, s.RDB, tenant, contact, true)
	}
}
