package api

// One contact's data for privacy requests: delete a contact (right to erasure) and download
// everything Pulse keeps about them as JSON (right of access). Both are audited.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/model"
)

// maxExportEvents caps the event history in a contact's data download (newest first).
const maxExportEvents = 100000

// ContactDeleter permanently deletes contacts: ClickHouse events and memberships first, then the
// Postgres rows (maintenance.Cleaner, the same path as retention cleanup and the journey step).
type ContactDeleter interface {
	DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error
}

func (s *Server) contactDataRoutes(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /v1/contacts/{external_id}", s.tenant(s.deleteContact))
	// A GET, so the generic audit (changes only) doesn't see it: the handler records it itself.
	mux.HandleFunc("GET /v1/contacts/{external_id}/data", s.authed(func(w http.ResponseWriter, r *http.Request, p model.Principal) {
		s.audited(w, r, p, func(w http.ResponseWriter, r *http.Request) { s.exportContactData(w, r, p.TenantID) })
	}))
}

func (s *Server) deleteContact(w http.ResponseWriter, r *http.Request, tenant int64) {
	if s.Deleter == nil {
		writeErr(w, http.StatusServiceUnavailable, "deleting contacts isn't available")
		return
	}
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Deleter.DeleteContacts(r.Context(), tenant, []uint32{c.ID}); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": c.ExternalID})
}

// exportContactData answers a data access request: the profile, consent per channel, segment
// memberships and the event history, as a JSON download.
func (s *Server) exportContactData(w http.ResponseWriter, r *http.Request, tenant int64) {
	ctx := r.Context()
	c, err := s.Store.ContactByExternalID(ctx, tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	segs, err := s.Store.ListSegments(ctx, tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	type segRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	in := []segRef{}
	for _, seg := range segs {
		ok, err := s.Members.IsMember(ctx, tenant, seg.ID, c.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if ok {
			in = append(in, segRef{seg.ID, seg.Name, seg.Kind})
		}
	}
	type chanConsent struct {
		Channel    string `json:"channel"`
		Subscribed bool   `json:"subscribed"`
	}
	cons := []chanConsent{}
	for _, ch := range channel.All {
		cons = append(cons, chanConsent{ch, !consent.OptedOut(c.Attrs, ch)})
	}
	type event struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		At    time.Time       `json:"at"`
		Props json.RawMessage `json:"props,omitempty"`
	}
	events := []event{}
	if s.Analytics != nil {
		rows, err := s.Analytics.ContactEvents(ctx, tenant, c.ID, maxExportEvents)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, e := range rows {
			var props json.RawMessage
			if json.Valid([]byte(e.Props)) && e.Props != "" && e.Props != "{}" {
				props = json.RawMessage(e.Props)
			}
			events = append(events, event{e.ID, e.Name, e.Ts, props})
		}
	}
	out := map[string]any{
		"exported_at": s.now().UTC(),
		"contact": map[string]any{"external_id": c.ExternalID, "email": c.Email, "attributes": c.Attrs,
			"created_at": c.CreatedAt, "updated_at": c.UpdatedAt},
		"consent":  map[string]any{"subscribed_to_all": !consent.OptedOut(c.Attrs, consent.All), "channels": cons},
		"segments": in,
		"events":   events,
		"events_note": fmt.Sprintf("Newest first, at most %d events. Events removed by the workspace's retention settings are not included.",
			maxExportEvents),
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="contact-%s.json"`, safeFileName(c.ExternalID)))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeFileName keeps a download name to plain characters.
func safeFileName(s string) string {
	s = unsafeFileChars.ReplaceAllString(s, "_")
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		return "contact"
	}
	return s
}
