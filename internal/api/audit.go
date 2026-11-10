package api

// The audit log records every change made through the app or the API (who, what, which object,
// the outcome and the request, with secrets removed), and every sign-in.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/model"
)

const maxAuditDetail = 8 << 10

type AuditStore interface {
	InsertAudit(ctx context.Context, tenant int64, e model.AuditEntry) error
	ListAudit(ctx context.Context, tenant int64, f model.AuditFilter) ([]model.AuditEntry, error)
}

func (s *Server) auditRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/audit", s.authed(s.listAudit))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// audited runs a state-changing request and records it.
func (s *Server) audited(w http.ResponseWriter, r *http.Request, p model.Principal, h http.HandlerFunc) {
	as, ok := s.Store.(AuditStore)
	if !ok {
		h(w, r)
		return
	}
	var body []byte
	if ct := r.Header.Get("Content-Type"); (strings.HasPrefix(ct, "application/json") || ct == "") && r.URL.Path != "/v1/imports" {
		body, _ = io.ReadAll(io.LimitReader(r.Body, maxBody))
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	rec := &statusRecorder{ResponseWriter: w}
	h(rec, r)
	path := r.Pattern[strings.IndexByte(r.Pattern, ' ')+1:]
	objType, _, _ := strings.Cut(strings.TrimPrefix(path, "/v1/"), "/")
	objID := r.PathValue("id")
	for _, k := range []string{"external_id", "key"} {
		if objID == "" {
			objID = r.PathValue(k)
		}
	}
	action := r.Method + " " + path
	if a := r.PathValue("action"); a != "" { // e.g. POST /v1/journeys/{id}/activate
		action = r.Method + " " + strings.Replace(path, "{action}", a, 1)
	}
	e := model.AuditEntry{UserID: p.UserID, Actor: p.Actor, Action: action, ObjectType: objType, ObjectID: objID,
		Status: rec.status, IP: clientIP(r, s.TrustProxy), Detail: redactJSON(body)}
	s.writeAudit(as, p.TenantID, e)
}

// auditSignIn records a sign-in (or a failed one, for an existing workspace).
func (s *Server) auditSignIn(r *http.Request, tenant, user int64, actor, how string, ok bool) {
	as, isAudit := s.Store.(AuditStore)
	if !isAudit || tenant == 0 {
		return
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusUnauthorized
	}
	s.writeAudit(as, tenant, model.AuditEntry{UserID: user, Actor: actor, Action: how, ObjectType: "auth", Status: status, IP: clientIP(r, s.TrustProxy)})
}

func (s *Server) writeAudit(as AuditStore, tenant int64, e model.AuditEntry) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := as.InsertAudit(ctx, tenant, e); err != nil {
			s.Log.Error("audit log", "action", e.Action, "err", err)
		}
	}()
}

// secretKey reports whether a JSON key holds something that must never be logged.
func secretKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"password", "secret", "token", "private_key", "api_key", "authorization"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if secretKey(k) {
				x[k] = "[redacted]"
			} else {
				x[k] = redact(val)
			}
		}
	case []any:
		for i := range x {
			x[i] = redact(x[i])
		}
	}
	return v
}

// redactJSON returns the request body with secrets replaced, capped in size; nil for non-JSON.
func redactJSON(body []byte) json.RawMessage {
	var v any
	if len(body) == 0 || json.Unmarshal(body, &v) != nil {
		return nil
	}
	out, _ := json.Marshal(redact(v))
	if len(out) > maxAuditDetail {
		out, _ = json.Marshal(map[string]any{"truncated": true, "bytes": len(body)})
	}
	return out
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, p model.Principal) {
	q := r.URL.Query()
	f := model.AuditFilter{ObjectType: q.Get("object_type"), Limit: 100}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	f.UserID, _ = strconv.ParseInt(q.Get("user_id"), 10, 64)
	es, err := s.Store.(AuditStore).ListAudit(r.Context(), p.TenantID, f)
	if err != nil {
		s.fail(w, err)
		return
	}
	if es == nil {
		es = []model.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": es})
}
