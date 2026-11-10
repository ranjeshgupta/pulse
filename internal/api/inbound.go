package api

// Inbound messages (SMS and WhatsApp replies). Every reply is recorded as <channel>_received on the
// sender's contacts; a reply that is an opt-out keyword (STOP, ...) unsubscribes them from that
// channel, and an opt-in keyword (START, ...) subscribes them again.

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/providers"
)

const maxInboundText = 1600

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// handleInbound records a reply from rc.From on ch for every contact with that phone number.
// Replies from unknown numbers are dropped: there is nobody to unsubscribe.
func (s *Server) handleInbound(ctx context.Context, tenant int64, ch string, rc providers.Receipt) error {
	if !e164.MatchString(rc.From) {
		return nil
	}
	contacts, err := s.formStore().ContactsByPhone(ctx, tenant, rc.From)
	if err != nil || len(contacts) == 0 {
		return err
	}
	rules, err := s.Store.(DeliveryStore).DeliveryRules(ctx, tenant)
	if err != nil {
		return err
	}
	text := rc.Text
	if len(text) > maxInboundText {
		text = text[:maxInboundText]
	}
	action, word := rules.Keywords.Match(text)
	var evs []event.Event
	for _, c := range contacts {
		// The provider's message id makes a repeated webhook record the reply once.
		id := "in:" + ch + ":" + rc.ProviderMessageID
		if rc.ProviderMessageID == "" {
			id = ""
		}
		props := map[string]any{"text": text, "from": rc.From, "provider_message_id": rc.ProviderMessageID}
		if word != "" {
			props["keyword"] = word
		}
		evs = append(evs, event.Event{ID: idFor(id, c.ExternalID), ExternalID: c.ExternalID, Name: ch + "_received", Props: props})
		switch action {
		case "opt_out":
			evs = append(evs, event.Event{ID: idFor(id+":c", c.ExternalID), ExternalID: c.ExternalID, Name: consent.Unsubscribed,
				Props: map[string]any{"channel": ch, "source": ch + "_keyword", "keyword": word}})
		case "opt_in":
			evs = append(evs, event.Event{ID: idFor(id+":c", c.ExternalID), ExternalID: c.ExternalID, Name: consent.Resubscribed,
				Props: map[string]any{"channel": ch, "source": ch + "_keyword", "keyword": word}})
		}
	}
	return s.publishEvents(ctx, tenant, evs)
}

// idFor scopes a message-derived event id to one contact (several may share a number).
func idFor(base, externalID string) string {
	if base == "" {
		return ""
	}
	id := base + ":" + externalID
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

// sandboxInbound simulates a reply from a phone number, e.g. "STOP" on SMS.
func (s *Server) sandboxInbound(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Channel string `json:"channel"`
		From    string `json:"from"`
		Text    string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Channel != channel.SMS && req.Channel != channel.WhatsApp {
		writeErr(w, http.StatusBadRequest, "channel must be sms or whatsapp")
		return
	}
	req.From = strings.ReplaceAll(req.From, " ", "")
	if !e164.MatchString(req.From) || strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "from (E.164, like +919812345678) and text are required")
		return
	}
	rc := providers.Receipt{ProviderMessageID: "sbx-" + event.NewID()[:12], Status: providers.Inbound, From: req.From, Text: req.Text}
	if err := s.handleInbound(r.Context(), tenant, req.Channel, rc); err != nil {
		s.fail(w, err)
		return
	}
	rules, err := s.Store.(DeliveryStore).DeliveryRules(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	action, word := rules.Keywords.Match(req.Text)
	writeJSON(w, http.StatusAccepted, map[string]any{"keyword": word, "action": action})
}
