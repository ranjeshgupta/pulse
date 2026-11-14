package api

// Inbound integration endpoints:
//   - POST /v1/webhooks/shopify/{id}: Shopify store webhooks, verified with the connector's signing
//     secret and turned into events on identified contacts.
//   - /v1/hooks/*: REST hooks for Zapier, Make and the like, authenticated by a Zapier / Make
//     connector's own API key ("Authorization: Bearer pzk_…" or "X-Pulse-Key"). Subscriptions are
//     outbound webhooks owned by the connector (signed and retried like any webhook); actions
//     create or update contacts, track events and add or remove tags.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/integrations"
	"pulse/internal/model"
	"pulse/internal/store/pg"
	"pulse/internal/webhooks"
)

// hookEvents are the triggers offered to Zapier / Make (any other valid event name works too).
var hookEvents = []string{webhooks.ContactCreated, "segment_entered", "segment_exited", "form_submitted"}

const maxHookSubscriptions = 100

// ---- Shopify ----

func (s *Server) shopifyWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	ctx := r.Context()
	c, err := s.intStore().IntegrationByID(ctx, id)
	if err != nil || c.Kind != integrations.KindShopify {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	svc := s.integrationSvc()
	if err := svc.OpenSecrets(&c); err != nil {
		s.fail(w, err)
		return
	}
	if !integrations.VerifyShopify(c.Secrets["webhook_secret"], body, r.Header.Get("X-Shopify-Hmac-Sha256")) {
		writeErr(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	if d := r.Header.Get("X-Shopify-Shop-Domain"); d != "" && !strings.EqualFold(d, c.Setting("shop")) {
		writeErr(w, http.StatusForbidden, "this webhook is for another shop")
		return
	}
	logEntry := func(ok bool, msg string) {
		_ = s.intStore().LogIntegration(context.WithoutCancel(ctx), c.ID, integrations.LogEntry{Action: "webhook", OK: ok, Count: 1, Message: msg})
	}
	if !c.Active {
		logEntry(false, "ignored: the integration is turned off")
		writeJSON(w, http.StatusOK, map[string]any{"ignored": true})
		return
	}
	topic := r.Header.Get("X-Shopify-Topic")
	in, err := integrations.ParseShopify(topic, body)
	if errors.Is(err, integrations.ErrSkip) {
		logEntry(true, topic+": "+err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"ignored": true, "reason": err.Error()})
		return
	} else if err != nil {
		logEntry(false, topic+": "+err.Error())
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ext, err := s.inboundExternalID(ctx, c.TenantID, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	evID := r.Header.Get("X-Shopify-Webhook-Id")
	if evID == "" || len(evID) > 50 {
		evID = integrations.Sha256Hex(topic + "|" + string(body))[:40]
	}
	e := event.Event{ID: "shopify:" + evID, ExternalID: ext, Name: in.Name, Props: in.Props, Attrs: in.Attrs}
	if integrations.NormalizeEmail(in.Email) != "" {
		e.Email = in.Email
	}
	e.Props["integration_id"] = c.ID
	if err := s.publishEvents(ctx, c.TenantID, []event.Event{e}); err != nil {
		logEntry(false, topic+": "+err.Error())
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	_ = s.intStore().SetIntegrationStatus(ctx, c.ID, "ok", "")
	logEntry(true, fmt.Sprintf("%s → %s", topic, in.Name))
	writeJSON(w, http.StatusOK, map[string]any{"recorded": in.Name})
}

// inboundExternalID picks the contact an inbound record is about: the contact with its email,
// else with its phone, else a new contact named by the email, phone or Shopify customer id.
func (s *Server) inboundExternalID(ctx context.Context, tenant int64, in integrations.Inbound) (string, error) {
	st := s.intStore()
	if e := integrations.NormalizeEmail(in.Email); e != "" {
		ext, err := st.ExternalIDByEmail(ctx, tenant, e)
		if err != nil || ext != "" {
			return ext, err
		}
		return e, nil
	}
	if in.Phone != "" {
		cs, err := st.ContactsByPhone(ctx, tenant, in.Phone)
		if err != nil {
			return "", err
		}
		if len(cs) > 0 {
			return cs[0].ExternalID, nil
		}
		return in.Phone, nil
	}
	return integrations.ShopifyCustomerExternal(in.CustomerID), nil
}

// ---- REST hooks ----

type hookHandler func(w http.ResponseWriter, r *http.Request, c integrations.Connector)

func (s *Server) hookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/hooks/me", s.hookAuth(s.hookMe))
	mux.HandleFunc("GET /v1/hooks/segments", s.hookAuth(s.hookSegments))
	mux.HandleFunc("GET /v1/hooks/samples", s.hookAuth(s.hookSamples))
	mux.HandleFunc("POST /v1/hooks/subscriptions", s.hookAuth(s.hookSubscribe))
	mux.HandleFunc("DELETE /v1/hooks/subscriptions/{id}", s.hookAuth(s.hookUnsubscribe))
	mux.HandleFunc("POST /v1/hooks/contacts", s.hookAuth(s.hookContact))
	mux.HandleFunc("POST /v1/hooks/events", s.hookAuth(s.hookEvent))
	mux.HandleFunc("POST /v1/hooks/tags", s.hookAuth(s.hookTags))
}

// hookAuth resolves the Zapier / Make connector from its API key. Changes are audited as the
// connector.
func (s *Server) hookAuth(h hookHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" {
			key = r.Header.Get("X-Pulse-Key")
		}
		if !strings.HasPrefix(key, "pzk_") {
			writeErr(w, http.StatusUnauthorized, "send the integration's API key (Authorization: Bearer pzk_…)")
			return
		}
		c, err := s.intStore().IntegrationByKeyHash(r.Context(), auth.HashToken(key))
		if errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusUnauthorized, "this API key isn't valid (rotated, or the integration was deleted)")
			return
		} else if err != nil {
			s.fail(w, err)
			return
		}
		if !c.Active {
			writeErr(w, http.StatusForbidden, "this integration is turned off in Pulse")
			return
		}
		if r.Method == http.MethodGet {
			h(w, r, c)
			return
		}
		p := model.Principal{TenantID: c.TenantID, Actor: "Integration: " + c.Name}
		s.audited(w, r, p, func(w http.ResponseWriter, r *http.Request) { h(w, r, c) })
	}
}

// hookMe answers Zapier's connection test.
func (s *Server) hookMe(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	t, err := s.Store.Tenant(r.Context(), c.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace": t.Name, "integration": c.Name, "integration_id": c.ID})
}

// hookSegments lists segments for a trigger's dropdown.
func (s *Server) hookSegments(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	segs, err := s.Store.ListSegments(r.Context(), c.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(segs))
	for _, sg := range segs {
		out = append(out, map[string]any{"id": sg.ID, "name": sg.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

// samplePayload is what a trigger delivers, with sample data (Zapier's "perform list").
func samplePayload(tenant int64, name string, segment, form int64) webhooks.Payload {
	p := webhooks.Payload{ID: "sample-" + name, Event: name, Ts: time.Now().UTC().Truncate(time.Second), Workspace: tenant,
		Contact: webhooks.PayloadContact{ID: 1, ExternalID: "sample-contact",
			Attrs: map[string]any{"email": "asha@example.com", "first_name": "Asha", "city": "Pune"}}}
	switch name {
	case "segment_entered", "segment_exited":
		p.SegmentID = segment
		if p.SegmentID == 0 {
			p.SegmentID = 1
		}
		p.Contact.Attrs = nil
	case "form_submitted":
		p.Props = map[string]any{"form_id": form, "fields": map[string]any{"email": "asha@example.com", "first_name": "Asha"}}
	case webhooks.ContactCreated:
	default:
		p.Props = map[string]any{"sample": true}
	}
	return p
}

func (s *Server) hookSamples(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	q := r.URL.Query()
	name := q.Get("event")
	if name == "" {
		name = webhooks.ContactCreated
	}
	if !event.ValidName(name) {
		writeErr(w, http.StatusBadRequest, "invalid event name")
		return
	}
	seg, _ := strconv.ParseInt(q.Get("segment_id"), 10, 64)
	form, _ := strconv.ParseInt(q.Get("form_id"), 10, 64)
	writeJSON(w, http.StatusOK, []webhooks.Payload{samplePayload(c.TenantID, name, seg, form)})
}

// hookSubscribe adds a REST hook: {target_url (or hookUrl), event, segment_id?, form_id?}.
func (s *Server) hookSubscribe(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	var req struct {
		TargetURL string `json:"target_url"`
		HookURL   string `json:"hookUrl"` // Zapier's name for it
		Event     string `json:"event"`
		SegmentID int64  `json:"segment_id"`
		FormID    int64  `json:"form_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	target := strings.TrimSpace(req.TargetURL)
	if target == "" {
		target = strings.TrimSpace(req.HookURL)
	}
	if u, err := url.Parse(target); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(target) > 2000 {
		writeErr(w, http.StatusBadRequest, "target_url must be an http(s) URL")
		return
	}
	if !event.ValidName(req.Event) {
		writeErr(w, http.StatusBadRequest, "choose the event: contact_created, segment_entered, segment_exited, form_submitted or any event name")
		return
	}
	if req.SegmentID != 0 {
		if req.Event != "segment_entered" && req.Event != "segment_exited" {
			writeErr(w, http.StatusBadRequest, "segment_id goes with segment_entered or segment_exited")
			return
		}
		if _, err := s.Store.GetSegment(ctx, c.TenantID, req.SegmentID); errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "that segment doesn't exist")
			return
		} else if err != nil {
			s.fail(w, err)
			return
		}
	}
	if req.FormID != 0 {
		if req.Event != "form_submitted" {
			writeErr(w, http.StatusBadRequest, "form_id goes with form_submitted")
			return
		}
		if fs, ok := s.Store.(FormStore); ok {
			if _, err := fs.GetForm(ctx, c.TenantID, req.FormID); errors.Is(err, pg.ErrNotFound) {
				writeErr(w, http.StatusBadRequest, "that form doesn't exist")
				return
			} else if err != nil {
				s.fail(w, err)
				return
			}
		}
	}
	subs, err := s.intStore().HookSubscriptions(ctx, c.TenantID, c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(subs) >= maxHookSubscriptions {
		writeErr(w, http.StatusConflict, fmt.Sprintf("this integration already has %d subscriptions; remove some first", maxHookSubscriptions))
		return
	}
	if s.Box == nil {
		writeErr(w, http.StatusServiceUnavailable, "subscriptions need the server's secret key")
		return
	}
	secret := auth.NewToken("whsec_")
	h := model.Webhook{TenantID: c.TenantID, Name: c.Name + ": " + req.Event, URL: target, Events: []string{req.Event},
		Sealed: s.Box.Seal([]byte(secret)), IntegrationID: c.ID, SegmentID: req.SegmentID, FormID: req.FormID}
	id, err := s.intStore().SubscribeHook(ctx, h)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "event": req.Event, "target_url": target, "secret": secret})
}

func (s *Server) hookUnsubscribe(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.intStore().UnsubscribeHook(r.Context(), c.TenantID, c.ID, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hookWho resolves the contact an action is about: external_id, else the contact with that email,
// else a new contact named by the email.
func (s *Server) hookWho(ctx context.Context, tenant int64, ext, email string) (string, string, error) {
	email = strings.TrimSpace(email)
	if email != "" && integrations.NormalizeEmail(email) == "" {
		return "", "", errors.New(strconv.Quote(email) + " isn't an email address")
	}
	if ext = strings.TrimSpace(ext); ext != "" {
		return ext, email, nil
	}
	if email == "" {
		return "", "", errors.New("send external_id or email")
	}
	found, err := s.intStore().ExternalIDByEmail(ctx, tenant, email)
	if err != nil || found != "" {
		return found, email, err
	}
	return integrations.NormalizeEmail(email), email, nil
}

func (s *Server) hookPublish(w http.ResponseWriter, r *http.Request, c integrations.Connector, e event.Event, extra map[string]any) {
	if e.Props == nil {
		e.Props = map[string]any{}
	}
	e.Props["integration_id"] = c.ID
	if err := s.publishEvents(r.Context(), c.TenantID, []event.Event{e}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := map[string]any{"external_id": e.ExternalID, "event_id": e.ID}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusAccepted, out)
}

// hookContact creates or updates a contact: {external_id?, email?, phone?, attrs?}. Reserved
// attributes (tags, points, consent, stage, company…) are left out and listed as ignored.
func (s *Server) hookContact(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	var req struct {
		ExternalID string         `json:"external_id"`
		Email      string         `json:"email"`
		Phone      string         `json:"phone"`
		Attrs      map[string]any `json:"attrs"`
	}
	if !decode(w, r, &req) {
		return
	}
	ext, email, err := s.hookWho(r.Context(), c.TenantID, req.ExternalID, req.Email)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	attrs := map[string]any{}
	ignored := []string{}
	for k, v := range req.Attrs {
		if integrations.MappableAttr(k) != "" {
			ignored = append(ignored, k)
			continue
		}
		attrs[k] = v
	}
	if p := strings.TrimSpace(req.Phone); p != "" {
		attrs["phone"] = p
	}
	e := event.Event{ExternalID: ext, Name: event.Identify, Email: email, Attrs: attrs}
	s.hookPublish(w, r, c, e, map[string]any{"ignored_attrs": ignored})
}

// hookEvent tracks an event: {external_id? | email?, name, props?}. Events Pulse records itself
// (tags, points, merges, stages…) are refused: use their own actions.
func (s *Server) hookEvent(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	var req struct {
		ExternalID string         `json:"external_id"`
		Email      string         `json:"email"`
		Name       string         `json:"name"`
		Props      map[string]any `json:"props"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !event.ValidName(req.Name) {
		writeErr(w, http.StatusBadRequest, "name the event: letters, digits, _ . : -")
		return
	}
	// Delivery and bounce reports count towards the bounce rules: only Pulse's bounce webhooks and
	// mailboxes, or a server API key, record them.
	if event.ServerOnly(req.Name) || event.PlatformOnly(req.Name) || event.BounceReport(req.Name) {
		writeErr(w, http.StatusBadRequest, req.Name+" is recorded by Pulse itself; use the tags action or the API")
		return
	}
	ext, email, err := s.hookWho(r.Context(), c.TenantID, req.ExternalID, req.Email)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.hookPublish(w, r, c, event.Event{ExternalID: ext, Name: req.Name, Email: email, Props: req.Props}, nil)
}

// hookTags adds (or with "remove": true removes) tags: {external_id? | email?, tags}.
func (s *Server) hookTags(w http.ResponseWriter, r *http.Request, c integrations.Connector) {
	var req struct {
		ExternalID string   `json:"external_id"`
		Email      string   `json:"email"`
		Tags       []string `json:"tags"`
		Remove     bool     `json:"remove"`
	}
	if !decode(w, r, &req) {
		return
	}
	var tags []string
	for _, t := range req.Tags {
		if t = strings.TrimSpace(t); t != "" && len(t) <= 100 {
			tags = append(tags, t)
		}
	}
	if len(tags) == 0 || len(tags) > 50 {
		writeErr(w, http.StatusBadRequest, "send 1 to 50 tags")
		return
	}
	ext, email, err := s.hookWho(r.Context(), c.TenantID, req.ExternalID, req.Email)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	name := event.TagAdded
	if req.Remove {
		name = event.TagRemoved
	}
	s.hookPublish(w, r, c, event.Event{ExternalID: ext, Name: name, Email: email, Props: map[string]any{"tags": tags}}, nil)
}
