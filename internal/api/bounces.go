package api

// Email delivery reports, bounces and complaints, posted by the email service to a per-workspace
// URL (/v1/bounces/{token}). The token alone picks the workspace; the service's own signature (or
// basic auth) is checked too once the workspace saved its key (bounces.Verify). Reports quoting a
// message reference signed for this workspace are linked to the message and its campaign. A hard
// bounce marks the address (email_bounced) so no more email goes to it; a complaint unsubscribes the
// contact from email; a soft bounce is recorded only; a delivery report records email_delivered.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"pulse/internal/bounces"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/replies"
	"pulse/internal/safehttp"
	"pulse/internal/store/pg"
)

// AttrEmailBounced marks a contact whose address hard-bounced: email to them is suppressed.
const AttrEmailBounced = consent.AttrEmailBounced

type BounceStore interface {
	TenantByBounceToken(ctx context.Context, token string) (int64, error)
	BounceToken(ctx context.Context, tenant int64) (string, error)
	EmailWebhookKeys(ctx context.Context, tenant int64) ([]byte, error)
	SetEmailWebhookKeys(ctx context.Context, tenant int64, sealed []byte) error
}

func (s *Server) bounceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/bounces/{token}", s.bounceWebhook)
	mux.HandleFunc("POST /v1/replies/{token}", s.replyWebhook)
	mux.HandleFunc("GET /v1/settings/bounces", s.admin(s.bounceSettings))
	mux.HandleFunc("PUT /v1/settings/bounces/signing", s.admin(s.putBounceSigning))
	mux.HandleFunc("POST /v1/sandbox/bounce", s.tenant(s.sandboxBounce))
}

func (s *Server) bounceSettings(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tok, err := s.Store.(BounceStore).BounceToken(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	keys, err := s.webhookKeys(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": s.PublicURL + "/v1/bounces/" + tok, "reply_url": s.PublicURL + "/v1/replies/" + tok,
		"formats": []string{"sendgrid", "postmark", "mailgun", "ses", "sparkpost", "generic"}, "signing": keys.Status(),
		"linking": s.Tracking != nil})
}

// webhookKeys opens a workspace's saved email webhook keys (none saved: zero Keys).
func (s *Server) webhookKeys(ctx context.Context, tenant int64) (bounces.Keys, error) {
	var k bounces.Keys
	sealed, err := s.Store.(BounceStore).EmailWebhookKeys(ctx, tenant)
	if err != nil || len(sealed) == 0 || s.Box == nil {
		return k, err
	}
	plain, err := s.Box.Open(sealed)
	if err != nil {
		return k, errors.New("the saved webhook keys can't be read (was the server key changed?)")
	}
	return k, json.Unmarshal(plain, &k)
}

// putBounceSigning saves the keys the email services' webhooks are checked with. Empty fields keep
// what is saved; names listed in clear remove it (sendgrid, mailgun, basic).
func (s *Server) putBounceSigning(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var req struct {
		bounces.Keys
		Clear []string `json:"clear"`
	}
	if !decode(w, r, &req) {
		return
	}
	if s.Box == nil {
		writeErr(w, http.StatusServiceUnavailable, "saving keys needs the server's secret key (PULSE_SECRET_KEY)")
		return
	}
	k, err := s.webhookKeys(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if v := strings.TrimSpace(req.SendGridKey); v != "" {
		k.SendGridKey = v
	}
	if v := strings.TrimSpace(req.MailgunKey); v != "" {
		k.MailgunKey = v
	}
	if req.BasicPassword != "" || req.BasicUser != "" {
		k.BasicUser, k.BasicPassword = strings.TrimSpace(req.BasicUser), req.BasicPassword
	}
	for _, c := range req.Clear {
		switch c {
		case "sendgrid":
			k.SendGridKey = ""
		case "mailgun":
			k.MailgunKey = ""
		case "basic":
			k.BasicUser, k.BasicPassword = "", ""
		default:
			writeErr(w, http.StatusBadRequest, "clear takes sendgrid, mailgun or basic")
			return
		}
	}
	if len(k.SendGridKey) > 4000 || len(k.MailgunKey) > 500 || len(k.BasicPassword) > 500 || len(k.BasicUser) > 200 {
		writeErr(w, http.StatusBadRequest, "a key is too long")
		return
	}
	if err := k.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var sealed []byte
	if k != (bounces.Keys{}) {
		b, _ := json.Marshal(k)
		sealed = s.Box.Seal(b)
	}
	if err := s.Store.(BounceStore).SetEmailWebhookKeys(r.Context(), p.TenantID, sealed); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signing": k.Status()})
}

// snsCerts caches SNS signing certificates (a handful per region; they rotate rarely).
var snsCerts = struct {
	sync.Mutex
	m map[string][]byte
}{m: map[string][]byte{}}

func fetchSNSCert(u string) ([]byte, error) {
	snsCerts.Lock()
	b, ok := snsCerts.m[u]
	snsCerts.Unlock()
	if ok {
		return b, nil
	}
	resp, err := safehttp.Client(10*time.Second, false).Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("certificate download failed")
	}
	b, err = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	snsCerts.Lock()
	if len(snsCerts.m) > 100 {
		snsCerts.m = map[string][]byte{}
	}
	snsCerts.m[u] = b
	snsCerts.Unlock()
	return b, nil
}

func (s *Server) bounceWebhook(w http.ResponseWriter, r *http.Request) {
	tenant, err := s.Store.(BounceStore).TenantByBounceToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	keys, err := s.webhookKeys(r.Context(), tenant)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "retry")
		return
	}
	certs := s.snsCerts
	if certs == nil {
		certs = fetchSNSCert
	}
	format := bounces.Detect(body)
	if err := bounces.Verify(format, r.Header, body, keys, certs, s.now()); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	if format == bounces.FormatMailgun && keys.MailgunKey != "" && s.RDB != nil {
		// Mailgun's signature doesn't cover the body: a signed token is accepted once (it is only
		// valid for a day anyway), so a captured request can't be replayed with another body.
		tok := bounces.MailgunToken(body)
		fresh, err := s.RDB.SetNX(r.Context(), "mgtok:"+strconv.FormatInt(tenant, 10)+":"+tok, 1, 25*time.Hour).Result()
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "retry")
			return
		}
		if !fresh {
			writeErr(w, http.StatusUnauthorized, "this webhook signature was already used")
			return
		}
	}
	bs, confirm, err := bounces.Parse(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if confirm != nil {
		// Amazon SNS: confirming the subscription is a GET to its (checked) SubscribeURL.
		resp, err := safehttp.Client(10*time.Second, false).Get(confirm.SubscribeURL)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "couldn't confirm the SNS subscription")
			return
		}
		resp.Body.Close()
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := s.applyBounces(r.Context(), tenant, bs); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "retry")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// bounceContacts looks contacts up for linking reports.
type bounceContacts struct{ s *Server }

func (b bounceContacts) ExternalIDByEmail(ctx context.Context, tenant int64, email string) (string, error) {
	cs, ok := b.s.Store.(interface {
		ContactByEmail(ctx context.Context, tenant int64, email string) (model.Contact, error)
	})
	if !ok {
		return "", pg.ErrNotFound
	}
	c, err := cs.ContactByEmail(ctx, tenant, email)
	return c.ExternalID, err
}

func (b bounceContacts) ContactByID(ctx context.Context, tenant int64, id uint32) (model.Contact, error) {
	cs, ok := b.s.Store.(interface {
		ContactByID(ctx context.Context, tenant int64, id uint32) (model.Contact, error)
	})
	if !ok {
		return model.Contact{}, pg.ErrNotFound
	}
	return cs.ContactByID(ctx, tenant, id)
}

// applyBounces records each report on its contact: the message's, when it quotes a reference
// signed for this workspace, else the contact with that address (unknown addresses are skipped).
func (s *Server) applyBounces(ctx context.Context, tenant int64, bs []bounces.Bounce) error {
	var refs bounces.RefParser
	if s.Tracking != nil {
		refs = s.Tracking
	}
	evs, err := bounces.Events(bs, bounces.Link(ctx, tenant, refs, bounceContacts{s}))
	if err != nil || len(evs) == 0 {
		return err
	}
	return s.publishEvents(ctx, tenant, evs)
}

// sandboxBounce simulates a bounce or complaint for an address: {email, type: hard|soft|complaint}.
func (s *Server) sandboxBounce(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Email string `json:"email"`
		Type  string `json:"type"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Type != bounces.Hard && req.Type != bounces.Soft && req.Type != bounces.Complaint || req.Email == "" {
		writeErr(w, http.StatusBadRequest, "email and type (hard, soft or complaint) are required")
		return
	}
	if _, err := s.formStore().ContactByEmail(r.Context(), tenant, req.Email); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.applyBounces(r.Context(), tenant, []bounces.Bounce{{Email: req.Email, Kind: req.Type, Reason: "simulated", Source: "sandbox"}}); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// replyWebhook receives inbound email from the email service's inbound parse (SendGrid, Mailgun,
// Postmark, or generic JSON) at the workspace's secret URL, and records email_replied for each
// message a reply code names (see package replies). Unknown codes are ignored.
func (s *Server) replyWebhook(w http.ResponseWriter, r *http.Request) {
	tenant, err := s.Store.(BounceStore).TenantByBounceToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	rep, err := replies.Parse(r.Header.Get("Content-Type"), body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.RDB == nil || s.Tracking == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	sum := sha256.Sum256(body)
	for _, code := range rep.Codes {
		tok, err := replies.Lookup(r.Context(), s.RDB, code)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "retry")
			return
		}
		t, err := s.Tracking.Decode(tok)
		if tok == "" || err != nil || t.TenantID != tenant {
			continue
		}
		s.trackEvent(r.Context(), t, event.EmailReplied, "reply:"+code+":"+hex.EncodeToString(sum[:6]),
			map[string]any{"from": rep.From, "subject": rep.Subject, "text": rep.Text})
	}
	w.WriteHeader(http.StatusOK)
}
