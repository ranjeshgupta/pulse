package api

// The bounce mailbox (Channels → Bounces): an IMAP mailbox that Pulse reads for delivery status
// notifications and feedback-loop complaints (package bounces). The password is sealed with the
// server key and only shown masked; changing the server, port, encryption or user needs the
// password again, so a saved password never goes to a different server.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"pulse/internal/bounces"
	"pulse/internal/integrations"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

type MailboxStore interface {
	BounceMailbox(ctx context.Context, tenant int64) (bounces.Mailbox, []byte, error)
	SaveBounceMailbox(ctx context.Context, m bounces.Mailbox, sealed []byte) error
	DeleteBounceMailbox(ctx context.Context, tenant int64) error
	CheckBounceMailboxNow(ctx context.Context, tenant int64) error
}

func (s *Server) mailboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/settings/bounce-mailbox", s.authed(s.getMailbox))
	mux.HandleFunc("PUT /v1/settings/bounce-mailbox", s.authed(s.putMailbox))
	mux.HandleFunc("DELETE /v1/settings/bounce-mailbox", s.authed(s.deleteMailbox))
	mux.HandleFunc("POST /v1/settings/bounce-mailbox/test", s.authed(s.testMailbox))
	mux.HandleFunc("POST /v1/settings/bounce-mailbox/check", s.authed(s.checkMailbox))
}

func (s *Server) mailboxStore() MailboxStore { return s.Store.(MailboxStore) }

func (s *Server) mailboxOpener() bounces.Opener {
	if s.MailboxOpener != nil {
		return s.MailboxOpener
	}
	return bounces.IMAPOpener(s.AllowPrivateWebhooks)
}

func mailboxView(m bounces.Mailbox) map[string]any {
	pw := ""
	if m.HasPassword {
		pw = integrations.Mask("password")
	}
	return map[string]any{"mailbox": m, "password": pw}
}

func (s *Server) getMailbox(w http.ResponseWriter, r *http.Request, p model.Principal) {
	m, _, err := s.mailboxStore().BounceMailbox(r.Context(), p.TenantID)
	if errors.Is(err, pg.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"mailbox": nil})
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mailboxView(m))
}

type mailboxRequest struct {
	bounces.Mailbox
	Password string `json:"password"`
}

// mailboxSettings validates a request and works out the password to use: the one sent, else the
// stored one when the server and user are unchanged. sealed is what to store.
func (s *Server) mailboxSettings(ctx context.Context, tenant int64, req mailboxRequest) (m bounces.Mailbox, password string, sealed []byte, status int, err error) {
	m = req.Mailbox
	m.TenantID = tenant
	if err := m.Validate(s.AllowPrivateWebhooks); err != nil {
		return m, "", nil, http.StatusBadRequest, err
	}
	if s.Box == nil {
		return m, "", nil, http.StatusServiceUnavailable, errors.New("the bounce mailbox needs the server's secret key (PULSE_SECRET_KEY)")
	}
	pw := strings.TrimSpace(req.Password)
	if pw != "" && !strings.HasPrefix(pw, "••••") {
		if len(pw) > 1000 {
			return m, "", nil, http.StatusBadRequest, errors.New("password is too long")
		}
		return m, pw, s.Box.Seal([]byte(pw)), 0, nil
	}
	old, oldSealed, err := s.mailboxStore().BounceMailbox(ctx, tenant)
	if errors.Is(err, pg.ErrNotFound) || (err == nil && len(oldSealed) == 0) {
		return m, "", nil, http.StatusBadRequest, errors.New("enter the mailbox password")
	} else if err != nil {
		return m, "", nil, 0, err
	}
	if !old.SameServer(m) {
		return m, "", nil, http.StatusBadRequest, errors.New("the server, port, encryption or user changed: enter the password again")
	}
	plain, err := s.Box.Open(oldSealed)
	if err != nil {
		return m, "", nil, http.StatusBadRequest, errors.New("the saved password can't be read: enter it again")
	}
	return m, string(plain), oldSealed, 0, nil
}

func (s *Server) putMailbox(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req mailboxRequest
	if !decode(w, r, &req) {
		return
	}
	m, _, sealed, status, err := s.mailboxSettings(r.Context(), p.TenantID, req)
	if err != nil {
		if status == 0 {
			s.fail(w, err)
		} else {
			writeErr(w, status, err.Error())
		}
		return
	}
	if err := s.mailboxStore().SaveBounceMailbox(r.Context(), m, sealed); err != nil {
		s.fail(w, err)
		return
	}
	saved, _, err := s.mailboxStore().BounceMailbox(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mailboxView(saved))
}

func (s *Server) deleteMailbox(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if err := s.mailboxStore().DeleteBounceMailbox(r.Context(), p.TenantID); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testMailbox connects with the settings in the body (unsaved), signs in and opens the folder.
func (s *Server) testMailbox(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req mailboxRequest
	if !decode(w, r, &req) {
		return
	}
	m, pw, _, status, err := s.mailboxSettings(r.Context(), p.TenantID, req)
	if err != nil {
		if status == 0 {
			s.fail(w, err)
		} else {
			writeErr(w, status, err.Error())
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sess, err := s.mailboxOpener()(ctx, m, pw)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = sess.Close()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Connected, signed in and opened " + m.Folder + "."})
}

func (s *Server) checkMailbox(w http.ResponseWriter, r *http.Request, p model.Principal) {
	err := s.mailboxStore().CheckBounceMailboxNow(r.Context(), p.TenantID)
	if errors.Is(err, pg.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "turn the bounce mailbox on first")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
