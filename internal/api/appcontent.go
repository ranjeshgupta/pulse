package api

// App content slots (batch O3): POST /v1/web/content is the app SDKs' twin of /v1/web/decide for
// dynamic content. Body: {id, user_hash, platform, slots: [...]}; answer: {content: {slot: {card |
// html, variant}}}. Like the in-app endpoint it needs only the site key; a contact's segments
// count only when the id is an anonymous visitor or carries a valid user hash, and contact-field
// rules and personalised text need a verified identity.

import (
	"context"
	"net/http"
	"strings"

	"pulse/internal/event"
	"pulse/internal/inapp"
	"pulse/internal/model"
	"pulse/internal/onsite"
)

func (s *Server) appContentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("OPTIONS /v1/web/content", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/content", s.appContent)
}

func (s *Server) appContent(w http.ResponseWriter, r *http.Request) {
	cors(w)
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	var req struct {
		ID       string   `json:"id"`
		UserHash string   `json:"user_hash"`
		Platform string   `json:"platform"`
		Slots    []string `json:"slots"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Platform != "android" && req.Platform != "ios" {
		writeErr(w, http.StatusBadRequest, "platform must be android or ios")
		return
	}
	if len(req.Slots) == 0 || len(req.Slots) > 50 {
		writeErr(w, http.StatusBadRequest, "ask for 1 to 50 slots")
		return
	}
	for _, sl := range req.Slots {
		if !slotKey.MatchString(sl) {
			writeErr(w, http.StatusBadRequest, "slot ids are lowercase letters, digits, - or _")
			return
		}
	}
	req.ID = strings.TrimSpace(req.ID)
	if !strings.HasPrefix(req.ID, "anon-") && !validUserHash(t.IdentitySecret, req.ID, req.UserHash) {
		req.ID = ""
	}
	if !s.allowWeb(r.Context(), t.ID, r, 1) {
		writeErr(w, http.StatusTooManyRequests, "too many requests from this app")
		return
	}
	set, err := s.activeOnsite(r.Context(), t.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	verified := verifiedIdentity(t, req.ID, req.UserHash)
	member := map[int64]bool{}
	var known *model.Contact
	if req.ID != "" {
		if c, err := s.Store.ContactByExternalID(r.Context(), t.ID, req.ID); err == nil {
			known = &c
			for _, seg := range onsite.Segments(nil, set.slots) {
				if in, err := s.Members.IsMember(r.Context(), t.ID, seg, c.ID); err == nil && in {
					member[seg] = true
				}
			}
		}
	}
	v := onsite.Visitor{Device: "mobile", IsMember: func(id int64) bool { return member[id] }}
	who := model.Contact{Attrs: map[string]any{}}
	if known != nil && verified {
		v.Attrs, who = known.Attrs, *known
	}
	content := onsite.DecideApp(set.slots, req.Slots, s.now(), v)
	for k, c := range content {
		if c.Card != nil {
			card := personalizeCard(*c.Card, who)
			c.Card = &card
			content[k] = c
		}
	}
	if known != nil {
		s.appJourneyContent(r.Context(), t.ID, *known, req.Slots, verified, content)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"content": content})
}

// personalizeCard fills {{…}} variables in a card's texts and link (defaults for anyone not verified).
func personalizeCard(c model.AppCard, who model.Contact) model.AppCard {
	p := inapp.Personalize(model.InAppContent{Title: c.Title, Body: c.Body, Buttons: []model.InAppButton{{Text: c.ButtonText, Value: c.DeepLink}}}, who)
	c.Title, c.Body = p.Title, p.Body
	if len(p.Buttons) == 1 {
		c.ButtonText = p.Buttons[0].Text
		if onsite.SafeLink(p.Buttons[0].Value) {
			c.DeepLink = p.Buttons[0].Value
		}
	}
	return c
}

// appJourneyContent applies content that "push dynamic content" journey steps set for the contact
// (it wins over the slot's own) and records dwc_requested for each slot of an app slot, which
// "requests dynamic content" decisions wait for.
func (s *Server) appJourneyContent(ctx context.Context, tenant int64, c model.Contact, slots []string, verified bool, content map[string]onsite.AppContent) {
	if s.RDB == nil {
		return
	}
	p := onsite.Pushes{RDB: s.RDB}
	if pushed, err := p.DWC(ctx, tenant, c.ID, slots); err == nil {
		for k, html := range pushed {
			content[k] = onsite.AppContent{HTML: onsite.ResolveDWC(html, verified), Variant: "journey"}
		}
	} else {
		s.Log.Error("journey app content", "err", err)
	}
	evs := make([]event.Event, len(slots))
	for i, sl := range slots {
		evs[i] = event.Event{ExternalID: c.ExternalID, Name: event.DWCRequested, Props: map[string]any{"slot": sl, "platform": "app"}}
	}
	if err := s.publishEvents(ctx, tenant, evs); err != nil {
		s.Log.Error("record dwc_requested", "err", err)
	}
}
