package api

// The app inbox API for the SDKs (site key): list, mark read, delete, record clicks. Inboxes are
// private, so a known user always needs their user hash (from the customer's server), whether or
// not the workspace requires identity verification for events; anonymous visitors (random anon-
// ids from the SDK) don't. Reads and clicks become inbox_opened and
// inbox_clicked events, counted in the sending journey's or broadcast's report.

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"pulse/internal/event"
	"pulse/internal/model"
)

const inboxPage = 50

type InboxStore interface {
	Inbox(ctx context.Context, tenant int64, contact uint32, before int64, limit int) ([]model.InboxMessage, int, error)
	InboxUpdate(ctx context.Context, tenant int64, contact uint32, action string, ids []int64) ([]model.InboxMessage, error)
	InboxMessage(ctx context.Context, tenant int64, contact uint32, id int64) (model.InboxMessage, error)
}

func (s *Server) inboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/web/inbox", s.webInbox)
	mux.HandleFunc("OPTIONS /v1/web/inbox", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/inbox", s.webInboxAction)
	mux.HandleFunc("GET /v1/contacts/{external_id}/inbox", s.tenant(s.contactInbox))
}

func (s *Server) inbox() InboxStore { return s.Store.(InboxStore) }

// inboxContact resolves the SDK caller to a contact. ok=false has already answered.
func (s *Server) inboxContact(w http.ResponseWriter, r *http.Request, t model.Tenant, id, hash string) (model.Contact, bool) {
	if id == "" || !strings.HasPrefix(id, "anon-") && !validUserHash(t.IdentitySecret, id, hash) {
		writeErr(w, http.StatusForbidden, "the inbox of a signed-in user needs their user_hash (identity verification)")
		return model.Contact{}, false
	}
	if !s.allowWeb(r.Context(), t.ID, r, 1) {
		writeErr(w, http.StatusTooManyRequests, "too many requests from this browser")
		return model.Contact{}, false
	}
	c, err := s.Store.ContactByExternalID(r.Context(), t.ID, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"messages": []model.InboxMessage{}, "unread": 0}) // not seen yet: empty inbox
		return c, false
	}
	return c, true
}

func (s *Server) webInbox(w http.ResponseWriter, r *http.Request) {
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	c, ok := s.inboxContact(w, r, t, q.Get("id"), q.Get("user_hash"))
	if !ok {
		return
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	ms, unread, err := s.inbox().Inbox(r.Context(), t.ID, c.ID, before, inboxPage)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ms == nil {
		ms = []model.InboxMessage{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"messages": ms, "unread": unread})
}

// webInboxAction: {id, user_hash, action: read|delete|click, message_ids: [] (empty = all; click: one)}.
func (s *Server) webInboxAction(w http.ResponseWriter, r *http.Request) {
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	var req struct {
		ID         string  `json:"id"`
		UserHash   string  `json:"user_hash"`
		Action     string  `json:"action"`
		MessageIDs []int64 `json:"message_ids"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, ok := s.inboxContact(w, r, t, req.ID, req.UserHash)
	if !ok {
		return
	}
	var changed []model.InboxMessage
	var name string
	switch req.Action {
	case "read", "delete":
		var err error
		if changed, err = s.inbox().InboxUpdate(r.Context(), t.ID, c.ID, req.Action, req.MessageIDs); err != nil {
			s.fail(w, err)
			return
		}
		if req.Action == "read" {
			name = "inbox_opened"
		}
	case "click":
		if len(req.MessageIDs) != 1 {
			writeErr(w, http.StatusBadRequest, "click one message")
			return
		}
		m, err := s.inbox().InboxMessage(r.Context(), t.ID, c.ID, req.MessageIDs[0])
		if err != nil {
			s.fail(w, err)
			return
		}
		changed, name = []model.InboxMessage{m}, "inbox_clicked"
	default:
		writeErr(w, http.StatusBadRequest, "action must be read, delete or click")
		return
	}
	if name != "" && len(changed) > 0 {
		evs := make([]event.Event, 0, len(changed))
		for _, m := range changed {
			props := map[string]any{"message_id": m.MessageID}
			for k, v := range m.Meta {
				props[k] = v
			}
			if name == "inbox_clicked" && m.URL != "" {
				props["url"] = m.URL
			}
			// One event per message and kind: repeated reads or clicks count once.
			evs = append(evs, event.Event{ID: name + ":" + m.MessageID, ExternalID: c.ExternalID, Name: name, Props: props})
		}
		if err := s.publishEvents(r.Context(), t.ID, evs); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "retry")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": len(changed)})
}

// contactInbox shows a contact's inbox in the app (contact drawer).
func (s *Server) contactInbox(w http.ResponseWriter, r *http.Request, tenant int64) {
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ms, unread, err := s.inbox().Inbox(r.Context(), tenant, c.ID, 0, inboxPage)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ms == nil {
		ms = []model.InboxMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": ms, "unread": unread})
}
