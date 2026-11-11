package api

// The full contact page (M4, /contacts/:id in the app): who the person is and how to reach them,
// how active they are, what they bought, their journeys, every message with its outcome, and their
// events with filters. Everything needs contacts view (it is one person's data).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/reach"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// ContactAnalytics are the ClickHouse queries of the contact page.
type ContactAnalytics interface {
	ContactActivity(ctx context.Context, tenant int64, contact uint32, activeCond string, now time.Time) (ch.ActivityCounts, error)
	ContactMessages(ctx context.Context, tenant int64, contact uint32, before time.Time, limit int) ([]ch.ContactMessage, error)
	ContactPurchases(ctx context.Context, tenant int64, contact uint32, event, amountProp string, limit int) ([]ch.ContactPurchase, uint64, float64, error)
	ContactEventsFiltered(ctx context.Context, tenant int64, contact uint32, names []string, cond string, before time.Time, limit int) ([]ch.EventRow, error)
}

func (s *Server) contactPageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/contacts/{external_id}/profile", s.tenant(s.contactProfile))
	mux.HandleFunc("GET /v1/contacts/{external_id}/messages", s.tenant(s.contactMessages))
}

func (s *Server) contactAnalytics(w http.ResponseWriter) (ContactAnalytics, bool) {
	ca, ok := s.Analytics.(ContactAnalytics)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "the contact page needs the analytics database")
	}
	return ca, ok
}

// ChannelReach is whether a contact can get messages on one channel, and why not.
type ChannelReach struct {
	Channel   string `json:"channel"`
	Address   string `json:"address,omitempty"` // email, phone, or "2 devices"
	HasAddr   bool   `json:"has_address"`
	OptedOut  bool   `json:"opted_out"`
	Bounced   bool   `json:"bounced"`
	Reachable bool   `json:"reachable"`
	Why       string `json:"why,omitempty"`
}

// ContactReach is the per-channel matrix, with the same checks as the dispatcher and the
// reachability bitmaps (reach.Bits): an address that works, not bounced, not opted out.
func ContactReach(c model.Contact, devices []model.Device) []ChannelReach {
	attrs := map[string]any{}
	for k, v := range c.Attrs {
		attrs[k] = v
	}
	if c.Email != "" {
		attrs["email"] = c.Email
	}
	bits := reach.Bits(attrs)
	phone := attr.String(attrs["phone"])
	out := []ChannelReach{}
	for _, chn := range channel.All {
		r := ChannelReach{Channel: chn, OptedOut: consent.OptedOut(attrs, chn)}
		switch chn {
		case channel.Email:
			r.Address, r.HasAddr = c.Email, c.Email != ""
			r.Bounced = attr.String(attrs[consent.AttrEmailBounced]) == "true"
		case channel.SMS, channel.WhatsApp, channel.RCS:
			r.Address, r.HasAddr = phone, bits[reach.Phone]
			r.Bounced = chn == channel.SMS && attr.String(attrs[consent.AttrSMSBounced]) == "true"
		case channel.Push:
			r.HasAddr = len(devices) > 0
			if r.HasAddr {
				r.Address = strconv.Itoa(len(devices)) + " device"
				if len(devices) > 1 {
					r.Address += "s"
				}
			}
		case channel.Inbox:
			r.HasAddr = bits[reach.Known]
			r.Address = "app inbox"
		}
		merged := attr.String(attrs["merged_into"]) != ""
		r.Reachable = r.HasAddr && !r.OptedOut && !r.Bounced && !merged
		switch {
		case merged:
			r.Why = "merged into another contact"
		case !r.HasAddr && (chn == channel.SMS || chn == channel.WhatsApp || chn == channel.RCS) && phone != "":
			r.Why = "the phone number isn't in international format (+91…)"
		case !r.HasAddr:
			r.Why = "no " + addressWord(chn)
		case r.Bounced:
			r.Why = "bounced"
		case r.OptedOut:
			r.Why = "opted out"
		}
		if chn == channel.WhatsApp || chn == channel.RCS {
			if r.Reachable {
				r.Why = "has a phone number; the provider decides at send time"
			}
		}
		out = append(out, r)
	}
	return out
}

func addressWord(ch string) string {
	switch ch {
	case channel.Email:
		return "email address"
	case channel.Push:
		return "push device"
	case channel.Inbox:
		return "contact"
	}
	return "phone number"
}

// contactProfile: GET /v1/contacts/{external_id}/profile — the contact page's header and panels.
func (s *Server) contactProfile(w http.ResponseWriter, r *http.Request, tenant int64) {
	ctx := r.Context()
	c, err := s.Store.ContactByExternalID(ctx, tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"contact": c}
	var devices []model.Device
	if ds, ok := s.Store.(interface {
		ContactDevices(ctx context.Context, tenant int64, contact uint32) ([]model.Device, error)
	}); ok {
		if devices, err = ds.ContactDevices(ctx, tenant, c.ID); err != nil {
			s.fail(w, err)
			return
		}
	}
	type deviceView struct {
		Platform string `json:"platform"`
		Token    string `json:"token"` // shortened: enough to tell devices apart
	}
	dv := []deviceView{}
	for _, d := range devices {
		dv = append(dv, deviceView{Platform: d.Platform, Token: shortToken(d.Token)})
	}
	out["devices"] = dv
	out["reach"] = ContactReach(c, devices)
	out["products"] = c.Attrs["products"]

	journeys := []map[string]any{}
	if js, ok := s.Store.(interface {
		ContactJourneys(ctx context.Context, tenant int64, contact uint32, limit int) ([]pg.ContactJourney, error)
	}); ok {
		list, err := js.ContactJourneys(ctx, tenant, c.ID, 50)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, j := range list {
			stepType := ""
			var g journey.Graph
			if json.Unmarshal(j.Graph, &g) == nil {
				if n, ok := g.Nodes[j.Node]; ok {
					stepType = n.Type
				}
			}
			journeys = append(journeys, map[string]any{"journey_id": j.JourneyID, "name": j.Name, "status": j.Status, "step": j.Node,
				"step_type": stepType, "current": j.Status == "active" || j.Status == "waiting", "entered_at": j.EnteredAt, "updated_at": j.UpdatedAt})
		}
	}
	out["journeys"] = journeys

	if ca, ok := s.Analytics.(ContactAnalytics); ok {
		act, err := ca.ContactActivity(ctx, tenant, c.ID, analytics.ActiveCondition, s.now())
		if err != nil {
			s.fail(w, err)
			return
		}
		out["activity"] = act
		rs := analytics.DefaultRevenue()
		if is, ok := s.Store.(InsightsStore); ok {
			if got, err := is.RevenueSettings(ctx, tenant); err == nil {
				rs = got
			}
		}
		items, n, total, err := ca.ContactPurchases(ctx, tenant, c.ID, rs.Event, rs.AmountProp, 20)
		if err != nil {
			s.fail(w, err)
			return
		}
		out["purchases"] = map[string]any{"items": items, "count": n, "total": total, "event": rs.Event, "currency": rs.Currency}
	}
	writeJSON(w, http.StatusOK, out)
}

func shortToken(t string) string {
	if len(t) <= 12 {
		return t
	}
	return t[:6] + "…" + t[len(t)-4:]
}

// contactMessages: GET /v1/contacts/{external_id}/messages?before=&limit= — every message sent (or
// not) to the contact, newest first, with its outcome, reason, campaign and engagement.
func (s *Server) contactMessages(w http.ResponseWriter, r *http.Request, tenant int64) {
	ca, ok := s.contactAnalytics(w)
	if !ok {
		return
	}
	ctx := r.Context()
	c, err := s.Store.ContactByExternalID(ctx, tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	before, limit, err := pageParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	msgs, err := ca.ContactMessages(ctx, tenant, c.ID, before, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	names := s.campaignNames(ctx, tenant, msgs)
	type row struct {
		ch.ContactMessage
		Campaign string `json:"campaign,omitempty"`
	}
	out := make([]row, len(msgs))
	for i, m := range msgs {
		out[i] = row{ContactMessage: m, Campaign: names[m.Source+":"+strconv.FormatUint(m.SourceID, 10)]}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

// campaignNames names the broadcasts and journeys of a contact's messages.
func (s *Server) campaignNames(ctx context.Context, tenant int64, msgs []ch.ContactMessage) map[string]string {
	out := map[string]string{}
	need := func(src string) bool {
		return slices.ContainsFunc(msgs, func(m ch.ContactMessage) bool { return m.Source == src })
	}
	if need("broadcast") {
		if bs, err := s.bcStore().ListBroadcasts(ctx, tenant); err == nil {
			for _, b := range bs {
				out["broadcast:"+strconv.FormatInt(b.ID, 10)] = b.Name
			}
		}
	}
	if need("journey") {
		if js, err := s.uiStore().ListJourneys(ctx, tenant); err == nil {
			for _, j := range js {
				out["journey:"+strconv.FormatInt(j.ID, 10)] = j.Name
			}
		}
	}
	return out
}

// pageParams reads ?before=<RFC 3339>&limit=N (default 50, at most 200).
func pageParams(r *http.Request) (time.Time, int, error) {
	q := r.URL.Query()
	var before time.Time
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return before, 0, errors.New("before must be a time like 2026-11-11T10:00:00Z")
		}
		before = t
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return before, limit, nil
}

// eventFilter reads the timeline filters: ?name=a,b (at most 20 names) and ?kind=activity|pulse
// (the contact's own events, or the ones Pulse records).
func eventFilter(r *http.Request) ([]string, string, error) {
	q := r.URL.Query()
	var names []string
	if v := strings.TrimSpace(q.Get("name")); v != "" {
		for _, n := range strings.Split(v, ",") {
			n = strings.TrimSpace(n)
			if !event.ValidName(n) {
				return nil, "", errors.New("invalid event name " + strconv.Quote(n))
			}
			names = append(names, n)
		}
		if len(names) > 20 {
			return nil, "", errors.New("at most 20 event names")
		}
	}
	switch q.Get("kind") {
	case "":
		return names, "", nil
	case "activity":
		return names, analytics.ActiveCondition, nil
	case "pulse":
		return names, "NOT (" + analytics.ActiveCondition + ")", nil
	}
	return nil, "", errors.New("kind must be activity or pulse")
}
