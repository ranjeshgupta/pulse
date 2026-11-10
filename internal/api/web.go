package api

// Browser endpoints for the JS SDK, authenticated by the public site key: events and push tokens.
// (The decision API for on-site content is in onsite.go.)
//
// A site key is public, so these endpoints accept less than the server API: small batches, and no
// consent attributes, so a script on a page can never opt someone back in.

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
)

const (
	maxWebBatch     = 50
	webEventsPerMin = 600 // per IP and workspace
)

func (s *Server) webRoutes(mux *http.ServeMux) {
	mux.HandleFunc("OPTIONS /v1/web/events", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/events", s.webEvents)
	mux.HandleFunc("OPTIONS /v1/web/devices", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/devices", s.webDevice)
}

func (s *Server) siteTenant(w http.ResponseWriter, r *http.Request) (model.Tenant, bool) {
	cors(w)
	key := r.Header.Get("X-Pulse-Site-Key")
	if key == "" {
		key = r.URL.Query().Get("site_key") // sendBeacon can't set headers
	}
	t, err := s.Store.TenantBySiteKey(r.Context(), key)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid site key")
		return t, false
	}
	return t, true
}

// allowWeb is a fixed-window rate limit per client IP and workspace. Without Redis it allows everything.
func (s *Server) allowWeb(ctx context.Context, tenant int64, r *http.Request, n int) bool {
	return s.allowRate(ctx, "rlweb", tenant, r, n, webEventsPerMin)
}

// allowRate counts n against limit per minute for (prefix, workspace, client IP).
func (s *Server) allowRate(ctx context.Context, prefix string, tenant int64, r *http.Request, n, limit int) bool {
	if s.RDB == nil {
		return true
	}
	key := prefix + ":" + strconv.FormatInt(tenant, 10) + ":" + clientIP(r, s.TrustProxy) + ":" + strconv.FormatInt(time.Now().Unix()/60, 10)
	c, err := s.RDB.IncrBy(ctx, key, int64(n)).Result()
	if err != nil {
		return true
	}
	if c == int64(n) {
		s.RDB.Expire(ctx, key, 2*time.Minute)
	}
	return c <= int64(limit)
}

// clientIP is the caller's address. Clients can write anything into X-Forwarded-For, so it's only
// used behind a trusted proxy, and then only its right-most entry (the one the proxy appended).
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		xff := r.Header.Get("X-Forwarded-For")
		if i := strings.LastIndexByte(xff, ','); i >= 0 {
			xff = xff[i+1:]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) webEvents(w http.ResponseWriter, r *http.Request) {
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	var req struct {
		Events []event.Event `json:"events"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Events) == 0 || len(req.Events) > maxWebBatch {
		writeErr(w, http.StatusBadRequest, "send 1-50 events")
		return
	}
	if !s.allowWeb(r.Context(), t.ID, r, len(req.Events)) {
		writeErr(w, http.StatusTooManyRequests, "too many events from this browser")
		return
	}
	now := s.now()
	recs := make([]*kgo.Record, 0, len(req.Events))
	for i := range req.Events {
		e := &req.Events[i]
		e.TenantID = t.ID
		e.ContactID, e.NewContact, e.ChangedAttrs, e.Profile = 0, false, nil, nil
		if e.Name == consent.Unsubscribed || e.Name == consent.Resubscribed {
			writeErr(w, http.StatusBadRequest, "consent changes can't be sent from the browser")
			return
		}
		if event.ServerOnly(e.Name) {
			writeErr(w, http.StatusBadRequest, e.Name+" can only be sent with an API key")
			return
		}
		// Identity verification: with it on, an event for a known user (anything but this
		// browser's anonymous id) must carry the user's hash from the customer's server.
		hash := attr.String(e.Props["user_hash"])
		delete(e.Props, "user_hash")
		if !webIdentityOK(t, e.ExternalID, hash) {
			writeErr(w, http.StatusForbidden, "event "+strconv.Itoa(i)+": user_hash is missing or wrong for this user id")
			return
		}
		for k := range e.Attrs {
			if consent.IsReserved(k) {
				delete(e.Attrs, k)
			}
		}
		if err := e.Normalize(now); err != nil {
			writeErr(w, http.StatusBadRequest, "event "+strconv.Itoa(i)+": "+err.Error())
			return
		}
		rec, err := kafka.JSON(event.TopicRaw, e.RawKey(), e)
		if err != nil {
			s.fail(w, err)
			return
		}
		recs = append(recs, rec)
	}
	if err := s.Pub.Publish(r.Context(), recs...); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "retry")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) webDevice(w http.ResponseWriter, r *http.Request) {
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	var req struct {
		ExternalID string `json:"external_id"`
		Token      string `json:"token"`
		UserHash   string `json:"user_hash"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !webIdentityOK(t, req.ExternalID, req.UserHash) {
		writeErr(w, http.StatusForbidden, "user_hash is missing or wrong for this user id")
		return
	}
	if req.ExternalID == "" || req.Token == "" || len(req.Token) > 4096 || len(req.ExternalID) > 255 {
		writeErr(w, http.StatusBadRequest, "external_id and token are required")
		return
	}
	found, _, err := s.chStore().ResolveContacts(r.Context(), t.ID, []string{req.ExternalID})
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.chStore().UpsertDevice(r.Context(), t.ID, found[req.ExternalID].ID, model.Device{Token: req.Token, Platform: "web"}); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
