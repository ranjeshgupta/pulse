package api

// Integrations (Settings → Integrations): connectors to CRMs, Shopify, Zapier-style REST hooks
// and ad platforms, with their sync log, a connection test, previews and audience links.
// Secrets are sealed with the server key and only shown masked.

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/auth"
	"pulse/internal/consent"
	"pulse/internal/integrations"
	"pulse/internal/model"
	"pulse/internal/safehttp"
	"pulse/internal/store/pg"
)

// IntegrationStore is what the integration routes read and write (Postgres).
type IntegrationStore interface {
	integrations.Store
	ListIntegrations(ctx context.Context, tenant int64) ([]integrations.Connector, error)
	IntegrationByID(ctx context.Context, id int64) (integrations.Connector, error)
	IntegrationByKeyHash(ctx context.Context, hash string) (integrations.Connector, error)
	SaveIntegration(ctx context.Context, c integrations.Connector, sealed []byte) (int64, error)
	DeleteIntegration(ctx context.Context, tenant, id int64) error
	SetIntegrationKey(ctx context.Context, tenant, id int64, hash, hint string) error
	PullNow(ctx context.Context, tenant, id int64, restart bool) error
	IntegrationLog(ctx context.Context, tenant, id int64, limit int) ([]integrations.LogEntry, error)

	ListAudienceSyncs(ctx context.Context, tenant, integration int64) ([]integrations.AudienceSync, error)
	GetAudienceSync(ctx context.Context, tenant, integration, id int64) (integrations.AudienceSync, error)
	CreateAudienceSync(ctx context.Context, a integrations.AudienceSync) (int64, error)
	SetAudienceSync(ctx context.Context, tenant, integration, id int64, active bool) error
	ReconcileNow(ctx context.Context, tenant, integration, id int64) error
	DeleteAudienceSync(ctx context.Context, tenant, integration, id int64) error
	ContactsByPhone(ctx context.Context, tenant int64, phone string) ([]model.Contact, error)

	HookSubscriptions(ctx context.Context, tenant, integration int64) ([]model.Webhook, error)
	SubscribeHook(ctx context.Context, h model.Webhook) (int64, error)
	UnsubscribeHook(ctx context.Context, tenant, integration, id int64) error
	HookDeliveries(ctx context.Context, tenant, integration int64, limit int) ([]model.WebhookDelivery, error)
}

func (s *Server) integrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/integrations/catalog", s.authed(s.integrationCatalog))
	mux.HandleFunc("GET /v1/integrations", s.authed(s.listIntegrations))
	mux.HandleFunc("POST /v1/integrations", s.authed(s.saveIntegration))
	mux.HandleFunc("GET /v1/integrations/{id}", s.authed(s.getIntegration))
	mux.HandleFunc("PUT /v1/integrations/{id}", s.authed(s.saveIntegration))
	mux.HandleFunc("DELETE /v1/integrations/{id}", s.authed(s.deleteIntegration))
	mux.HandleFunc("POST /v1/integrations/{id}/test", s.authed(s.testIntegration))
	mux.HandleFunc("GET /v1/integrations/{id}/fields", s.authed(s.integrationFields))
	mux.HandleFunc("POST /v1/integrations/{id}/pull", s.authed(s.pullIntegration))
	mux.HandleFunc("POST /v1/integrations/{id}/push", s.authed(s.pushIntegration))
	mux.HandleFunc("POST /v1/integrations/{id}/preview", s.authed(s.previewIntegration))
	mux.HandleFunc("POST /v1/integrations/{id}/key", s.authed(s.rotateIntegrationKey))
	mux.HandleFunc("DELETE /v1/integrations/{id}/subscriptions/{sub}", s.authed(s.deleteSubscription))
	mux.HandleFunc("GET /v1/integrations/{id}/audiences", s.authed(s.listAudienceSyncs))
	mux.HandleFunc("POST /v1/integrations/{id}/audiences", s.authed(s.linkAudience))
	mux.HandleFunc("PUT /v1/integrations/{id}/audiences/{sync}", s.authed(s.updateAudienceSync))
	mux.HandleFunc("DELETE /v1/integrations/{id}/audiences/{sync}", s.authed(s.unlinkAudience))
	mux.HandleFunc("POST /v1/integrations/{id}/audiences/{sync}/reconcile", s.authed(s.reconcileAudience))

	// Inbound: Shopify webhooks (HMAC-verified) and REST hooks (the connector's own API key).
	mux.HandleFunc("POST /v1/webhooks/shopify/{id}", s.shopifyWebhook)
	s.hookRoutes(mux)
}

func (s *Server) intStore() IntegrationStore { return s.Store.(IntegrationStore) }

// integrationSvc is the integrations service with this server's store, key and SSRF-safe client.
func (s *Server) integrationSvc() *integrations.Service {
	var open func([]byte) (map[string]string, error)
	if s.Box != nil {
		open = func(sealed []byte) (map[string]string, error) {
			b, err := s.Box.Open(sealed)
			if err != nil {
				return nil, err
			}
			var m map[string]string
			return m, json.Unmarshal(b, &m)
		}
	}
	// Integration requests carry the workspace's credentials: redirects stay on the same site.
	return &integrations.Service{Store: s.intStore(), Open: open, HTTP: safehttp.SameOrigin(safehttp.Client(20*time.Second, s.AllowPrivateWebhooks)),
		Pub: s.Pub, Log: s.Log, Now: s.now}
}

func (s *Server) integrationCatalog(w http.ResponseWriter, _ *http.Request, _ model.Principal) {
	writeJSON(w, http.StatusOK, map[string]any{"kinds": integrations.Catalog(), "shopify_topics": integrations.ShopifyTopics,
		"hook_events": hookEvents})
}

// masked prepares a connector for the API: secrets masked, never in full.
func (s *Server) masked(c *integrations.Connector) {
	c.Masked = map[string]string{}
	if err := s.integrationSvc().OpenSecrets(c); err == nil {
		c.Masked = integrations.MaskAll(c.Secrets)
	} else if len(c.Sealed) > 0 {
		k, _ := integrations.Lookup(c.Kind)
		for _, f := range k.Secrets {
			c.Masked[f.Key] = "•••••••• (can't be opened with this server's key)"
		}
	}
	c.Secrets = nil
}

func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request, p model.Principal) {
	cs, err := s.intStore().ListIntegrations(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range cs {
		s.masked(&cs[i])
	}
	if cs == nil {
		cs = []integrations.Connector{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": cs})
}

// loadIntegration reads the {id} connector of the caller's workspace.
func (s *Server) loadIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) (integrations.Connector, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return integrations.Connector{}, false
	}
	c, err := s.intStore().GetIntegration(r.Context(), p.TenantID, id)
	if err != nil {
		s.fail(w, err)
		return c, false
	}
	return c, true
}

func (s *Server) shopifyURL(id int64) string {
	return s.PublicURL + "/v1/webhooks/shopify/" + strconv.FormatInt(id, 10)
}

func (s *Server) getIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.loadIntegration(w, r, p)
	if !ok {
		return
	}
	ctx := r.Context()
	s.masked(&c)
	k, _ := integrations.Lookup(c.Kind)
	log, err := s.intStore().IntegrationLog(ctx, p.TenantID, c.ID, 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	if log == nil {
		log = []integrations.LogEntry{}
	}
	out := map[string]any{"integration": c, "kind": k, "log": log}
	switch {
	case c.Kind == integrations.KindShopify:
		out["webhook_url"] = s.shopifyURL(c.ID)
	case k.Hooks:
		subs, err := s.intStore().HookSubscriptions(ctx, p.TenantID, c.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		ds, err := s.intStore().HookDeliveries(ctx, p.TenantID, c.ID, 50)
		if err != nil {
			s.fail(w, err)
			return
		}
		for i := range subs {
			subs[i].Secret = ""
		}
		if subs == nil {
			subs = []model.Webhook{}
		}
		if ds == nil {
			ds = []model.WebhookDelivery{}
		}
		out["hooks_url"], out["subscriptions"], out["deliveries"] = s.PublicURL+"/v1/hooks", subs, ds
	case k.Audiences:
		as, err := s.intStore().ListAudienceSyncs(ctx, p.TenantID, c.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if as == nil {
			as = []integrations.AudienceSync{}
		}
		out["audiences"] = as
	}
	writeJSON(w, http.StatusOK, out)
}

type integrationReq struct {
	Kind        string                  `json:"kind"`
	Name        string                  `json:"name"`
	Config      map[string]string       `json:"config"`
	Secrets     map[string]string       `json:"secrets"`
	Mapping     []integrations.FieldMap `json:"mapping"`
	Active      *bool                   `json:"active"`
	SyncMinutes *int                    `json:"sync_minutes"`
}

// saveIntegration creates (POST) or updates (PUT) a connector. Secrets left empty, or sent back
// masked, keep the stored ones. A new Zapier / Make connector answers with its API key, once.
func (s *Server) saveIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req integrationReq
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	svc := s.integrationSvc()
	var c integrations.Connector
	if r.Method == http.MethodPut {
		cur, ok := s.loadIntegration(w, r, p)
		if !ok {
			return
		}
		if err := svc.OpenSecrets(&cur); err != nil {
			cur.Secrets = map[string]string{} // sealed with another key: the credentials must be entered again
		}
		c = cur
	} else {
		k, ok := integrations.Lookup(req.Kind)
		if !ok {
			writeErr(w, http.StatusBadRequest, "unknown integration type")
			return
		}
		c = integrations.Connector{TenantID: p.TenantID, Kind: k.ID, Active: true, Mapping: k.Mapping, Secrets: map[string]string{}}
		if k.Pull {
			c.SyncMinutes = 60
		}
	}
	k, _ := integrations.Lookup(c.Kind)
	prevConfig := maps.Clone(c.Config)
	if req.Name != "" || r.Method == http.MethodPost {
		c.Name = strings.TrimSpace(req.Name)
	}
	if c.Name == "" && r.Method == http.MethodPost {
		c.Name = k.Name
	}
	if req.Config != nil {
		c.Config = map[string]string{}
		for key, v := range req.Config {
			if v = strings.TrimSpace(v); v != "" {
				c.Config[key] = v
			}
		}
	}
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if req.Mapping != nil {
		c.Mapping = req.Mapping
	}
	if req.Active != nil {
		c.Active = *req.Active
	}
	if req.SyncMinutes != nil {
		c.SyncMinutes = *req.SyncMinutes
	}
	if !k.Pull {
		c.SyncMinutes = 0
	}
	if r.Method == http.MethodPut && integrations.EndpointsChanged(prevConfig, c.Config) {
		// Never send stored credentials to a new address: whoever changes it enters them again.
		c.Secrets = map[string]string{}
	}
	if err := integrations.CheckHosts(c, s.AllowPrivateWebhooks); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	merged := integrations.MergeSecrets(c.Secrets, req.Secrets)
	if err := integrations.Validate(c, merged); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var sealed []byte
	if len(merged) > 0 && !sameSecrets(c.Secrets, merged) {
		if s.Box == nil {
			writeErr(w, http.StatusServiceUnavailable, "integrations need the server's secret key (PULSE_SECRET_KEY)")
			return
		}
		b, _ := json.Marshal(merged)
		sealed = s.Box.Seal(b)
	}
	id, err := s.intStore().SaveIntegration(ctx, c, sealed)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"id": id}
	if k.Hooks && r.Method == http.MethodPost {
		key, err := s.newHookKey(ctx, p.TenantID, id)
		if err != nil {
			s.fail(w, err)
			return
		}
		out["api_key"] = key
	}
	if c.Kind == integrations.KindShopify {
		out["webhook_url"] = s.shopifyURL(id)
	}
	writeJSON(w, http.StatusOK, out)
}

func sameSecrets(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (s *Server) newHookKey(ctx context.Context, tenant, id int64) (string, error) {
	key := auth.NewToken("pzk_")
	return key, s.intStore().SetIntegrationKey(ctx, tenant, id, auth.HashToken(key), key[len(key)-4:])
}

func (s *Server) rotateIntegrationKey(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.loadIntegration(w, r, p)
	if !ok {
		return
	}
	if k, _ := integrations.Lookup(c.Kind); !k.Hooks {
		writeErr(w, http.StatusBadRequest, "only Zapier / Make integrations have an API key")
		return
	}
	key, err := s.newHookKey(r.Context(), p.TenantID, c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_key": key})
}

func (s *Server) deleteIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.intStore().DeleteIntegration(r.Context(), p.TenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testIntegration checks the saved credentials against the platform.
func (s *Server) testIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.loadIntegration(w, r, p)
	if !ok {
		return
	}
	svc := s.integrationSvc()
	start := time.Now()
	err := svc.OpenSecrets(&c)
	if err == nil {
		err = svc.Test(r.Context(), c)
	}
	out := map[string]any{"ok": err == nil, "duration_ms": time.Since(start).Milliseconds()}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// integrationFields lists the CRM's contact fields for the mapping picker.
func (s *Server) integrationFields(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.loadIntegration(w, r, p)
	if !ok {
		return
	}
	svc := s.integrationSvc()
	if err := svc.OpenSecrets(&c); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	crm, err := integrations.NewCRM(c, svc.HTTP)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	fs, err := crm.Fields(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "couldn't read the fields: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": fs})
}

// pullIntegration makes the next pull due now ("restart": true pulls everything again).
func (s *Server) pullIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.loadIntegration(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Restart bool `json:"restart"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if k, _ := integrations.Lookup(c.Kind); !k.Pull {
		writeErr(w, http.StatusBadRequest, "this integration doesn't pull contacts")
		return
	}
	if err := s.intStore().PullNow(r.Context(), p.TenantID, c.ID, req.Restart); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (s *Server) contactFor(w http.ResponseWriter, r *http.Request, tenant int64, ext string) (model.Contact, bool) {
	if ext == "" {
		writeErr(w, http.StatusBadRequest, "choose a contact (external_id)")
		return model.Contact{}, false
	}
	ct, err := s.Store.ContactByExternalID(r.Context(), tenant, ext)
	if errors.Is(err, pg.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "there is no contact "+strconv.Quote(ext))
		return ct, false
	} else if err != nil {
		s.fail(w, err)
		return ct, false
	}
	return ct, true
}

// pushIntegration sends one contact to a CRM now.
func (s *Server) pushIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req struct {
		ExternalID string `json:"external_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ct, ok := s.contactFor(w, r, p.TenantID, req.ExternalID)
	if !ok {
		return
	}
	remote, err := s.integrationSvc().PushContact(r.Context(), p.TenantID, id, ct, "")
	if errors.Is(err, integrations.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	out := map[string]any{"ok": err == nil, "remote_id": remote}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// previewIntegration shows what Pulse would send or record, without calling out: a contact's
// mapped fields (CRM), the event a Shopify delivery becomes, or a contact's hashed identifiers and
// whether an audience would include them.
func (s *Server) previewIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.loadIntegration(w, r, p)
	if !ok {
		return
	}
	var req struct {
		ExternalID string `json:"external_id"`
		Topic      string `json:"topic"`
		Body       string `json:"body"`
		SegmentID  int64  `json:"segment_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	k, _ := integrations.Lookup(c.Kind)
	switch {
	case c.Kind == integrations.KindShopify:
		topic, body := req.Topic, []byte(req.Body)
		if topic == "" {
			topic = "orders/create"
		}
		if len(body) == 0 {
			body = integrations.SampleShopifyOrder("sample@example.com")
		}
		in, err := integrations.ParseShopify(topic, body)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"skipped": true, "reason": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"event": in.Name, "email": in.Email, "phone": in.Phone, "props": in.Props, "attrs": in.Attrs})
	case k.Push:
		ct, ok := s.contactFor(w, r, p.TenantID, req.ExternalID)
		if !ok {
			return
		}
		fields := integrations.PushFields(c.Mapping, ct.Email, ct.Attrs)
		writeJSON(w, http.StatusOK, map[string]any{"fields": fields, "remote_id": ct.Attrs[k.RemoteID]})
	case k.Audiences:
		ct, ok := s.contactFor(w, r, p.TenantID, req.ExternalID)
		if !ok {
			return
		}
		in := true
		if req.SegmentID > 0 {
			var err error
			if in, err = s.Members.IsMember(r.Context(), p.TenantID, req.SegmentID, ct.ID); err != nil {
				s.fail(w, err)
				return
			}
		}
		id, ok := integrations.Wanted(c.Kind, in, &ct, c.Setting("default_country"))
		reason := ""
		switch {
		case ok:
		case !in:
			reason = "not in the segment"
		case id.Empty() && !consent.OptedOut(ct.Attrs, consent.All):
			reason = "no email address or phone number that can be normalised"
		default:
			reason = "unsubscribed from everything, or merged into another contact"
		}
		if !ok {
			id = integrations.HashFor(c.Kind, ct.Email, attr.String(ct.Attrs["phone"]), c.Setting("default_country"))
		}
		writeJSON(w, http.StatusOK, map[string]any{"included": ok, "reason": reason, "email_sha256": id.EmailHash, "phone_sha256": id.PhoneHash})
	default:
		writeErr(w, http.StatusBadRequest, "nothing to preview for this integration")
	}
}

func (s *Server) deleteSubscription(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sub, err := strconv.ParseInt(r.PathValue("sub"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid subscription id")
		return
	}
	if err := s.intStore().UnsubscribeHook(r.Context(), p.TenantID, id, sub); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- audience links ----

// segmentsEditOK: linking a segment to an ad audience sends its members out, so besides settings
// edit (the route's need) it needs edit access to segments.
func segmentsEditOK(w http.ResponseWriter, p model.Principal) bool {
	if !p.Perms.Allows(auth.AreaSegments, auth.Edit) {
		writeErr(w, http.StatusForbidden, "linking segments to audiences also needs edit access to segments")
		return false
	}
	return true
}

func (s *Server) audienceIntegration(w http.ResponseWriter, r *http.Request, p model.Principal) (integrations.Connector, bool) {
	c, ok := s.loadIntegration(w, r, p)
	if !ok {
		return c, false
	}
	if k, _ := integrations.Lookup(c.Kind); !k.Audiences {
		writeErr(w, http.StatusBadRequest, "this integration has no audiences")
		return c, false
	}
	return c, true
}

func (s *Server) listAudienceSyncs(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.audienceIntegration(w, r, p)
	if !ok {
		return
	}
	as, err := s.intStore().ListAudienceSyncs(r.Context(), p.TenantID, c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if as == nil {
		as = []integrations.AudienceSync{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"audiences": as})
}

// linkAudience links a segment to an existing audience (audience_id) or a new one (audience_name).
// The first full upload starts within seconds.
func (s *Server) linkAudience(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if !segmentsEditOK(w, p) {
		return
	}
	c, ok := s.audienceIntegration(w, r, p)
	if !ok {
		return
	}
	var req struct {
		SegmentID    int64  `json:"segment_id"`
		AudienceID   string `json:"audience_id"`
		AudienceName string `json:"audience_name"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.SegmentID <= 0 {
		writeErr(w, http.StatusBadRequest, "choose a segment")
		return
	}
	seg, err := s.Store.GetSegment(ctx, p.TenantID, req.SegmentID)
	if errors.Is(err, pg.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "that segment doesn't exist")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	req.AudienceID, req.AudienceName = strings.TrimSpace(req.AudienceID), strings.TrimSpace(req.AudienceName)
	if req.AudienceID == "" {
		if req.AudienceName == "" {
			req.AudienceName = seg.Name + " (Pulse)"
		}
		svc := s.integrationSvc()
		if err := svc.OpenSecrets(&c); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		plat, err := integrations.NewPlatform(c, svc.HTTP)
		if err == nil {
			req.AudienceID, err = plat.Create(ctx, req.AudienceName)
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, "couldn't create the audience: "+err.Error())
			return
		}
	} else if c.Kind == integrations.KindGoogleAds {
		req.AudienceID = integrations.GoogleList(c.Setting("customer_id"), req.AudienceID)
	}
	if len(req.AudienceID) > 200 || len(req.AudienceName) > 200 {
		writeErr(w, http.StatusBadRequest, "the audience id and name are up to 200 characters")
		return
	}
	id, err := s.intStore().CreateAudienceSync(ctx, integrations.AudienceSync{TenantID: p.TenantID, IntegrationID: c.ID,
		SegmentID: seg.ID, AudienceID: req.AudienceID, AudienceName: req.AudienceName})
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, "this segment is already linked to that audience")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "audience_id": req.AudienceID})
}

func syncID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("sync"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid audience link id")
		return 0, false
	}
	return id, true
}

func (s *Server) updateAudienceSync(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if !segmentsEditOK(w, p) {
		return
	}
	c, ok := s.audienceIntegration(w, r, p)
	if !ok {
		return
	}
	sid, ok := syncID(w, r)
	if !ok {
		return
	}
	var req struct {
		Active bool `json:"active"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.intStore().SetAudienceSync(r.Context(), p.TenantID, c.ID, sid, req.Active); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": sid, "active": req.Active})
}

// unlinkAudience stops syncing; the audience keeps the contacts already uploaded.
func (s *Server) unlinkAudience(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if !segmentsEditOK(w, p) {
		return
	}
	c, ok := s.audienceIntegration(w, r, p)
	if !ok {
		return
	}
	sid, ok := syncID(w, r)
	if !ok {
		return
	}
	if err := s.intStore().DeleteAudienceSync(r.Context(), p.TenantID, c.ID, sid); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reconcileAudience(w http.ResponseWriter, r *http.Request, p model.Principal) {
	c, ok := s.audienceIntegration(w, r, p)
	if !ok {
		return
	}
	sid, ok := syncID(w, r)
	if !ok {
		return
	}
	if err := s.intStore().ReconcileNow(r.Context(), p.TenantID, c.ID, sid); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}
