package api

// The audit log records every change made through the app or the API (who, what, which object,
// the outcome and the request, with secrets removed), and every sign-in.

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"pulse/internal/model"
)

const maxAuditDetail = 8 << 10

type AuditStore interface {
	InsertAudit(ctx context.Context, tenant int64, e model.AuditEntry) error
	ListAudit(ctx context.Context, tenant int64, f model.AuditFilter) ([]model.AuditEntry, error)
}

func (s *Server) auditRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/audit", s.authed(s.listAudit))
	mux.HandleFunc("GET /v1/audit/export", s.authed(s.exportAudit))
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
	// Not recorded: import files, /v1/account/* (two-step sign-in codes and passwords) and the
	// settings that carry credentials (secretRoute).
	if ct := r.Header.Get("Content-Type"); (strings.HasPrefix(ct, "application/json") || ct == "") && r.URL.Path != "/v1/imports" &&
		!strings.HasPrefix(r.URL.Path, "/v1/account/") && !secretRoute(r.URL.Path) {
		body, _ = io.ReadAll(io.LimitReader(r.Body, maxBody))
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	rec := &statusRecorder{ResponseWriter: w}
	h(rec, r)
	path := r.Pattern[strings.IndexByte(r.Pattern, ' ')+1:]
	objType, _, _ := strings.Cut(strings.TrimPrefix(path, "/v1/"), "/")
	objID := r.PathValue("id")
	for _, k := range []string{"external_id", "key", "name"} {
		if objID == "" {
			objID = r.PathValue(k)
		}
	}
	action := r.Method + " " + path
	if a := r.PathValue("action"); a != "" { // e.g. POST /v1/journeys/{id}/activate
		action = r.Method + " " + strings.Replace(path, "{action}", a, 1)
	}
	e := model.AuditEntry{UserID: p.UserID, Actor: p.Actor, Action: action, ObjectType: objType, ObjectID: objID,
		Status: rec.status, IP: s.ip(r), Detail: redactJSON(body)}
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
	s.writeAudit(as, tenant, model.AuditEntry{UserID: user, Actor: actor, Action: how, ObjectType: "auth", Status: status, IP: s.ip(r)})
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

// secretRoutes are settings whose requests carry credentials (provider logins, signing keys,
// mailbox passwords, SSO and integration secrets): their bodies are never recorded, whatever the
// field names, so a secret in an unexpected field can't reach the log.
var secretRoutes = []string{"/v1/channels/providers", "/v1/settings/identity", "/v1/settings/bounces/signing",
	"/v1/settings/bounce-mailboxes", "/v1/settings/webhooks", "/v1/settings/web-push", "/v1/settings/sso",
	"/v1/settings/api-key", "/v1/integrations", "/v1/auth/"}

func secretRoute(path string) bool {
	for _, p := range secretRoutes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// secretKey reports whether a JSON key may hold something that must never be logged: any word of
// it (split at _, -, . and case changes) that mentions a secret, token, password, key, signing,
// credential, auth or an account SID.
func secretKey(k string) bool {
	for _, w := range keyWords(k) {
		for _, s := range []string{"secret", "token", "password", "passwd", "passphrase", "signing", "credential", "auth", "cookie", "session"} {
			if strings.Contains(w, s) {
				return true
			}
		}
		if strings.HasSuffix(w, "key") || strings.HasSuffix(w, "sid") || w == "pw" || w == "pwd" {
			return true
		}
	}
	return false
}

// keyWords splits a JSON key into lower-case words: mailgunSigningKey, mailgun_signing_key and
// Mailgun-Signing-Key all give mailgun, signing, key.
func keyWords(k string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(k)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush()
			continue
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	if len(words) == 0 {
		words = []string{strings.ToLower(k)}
	}
	return append(words, strings.ToLower(k)) // the whole key too: apikey, signingkey
}

func redact(v any) any {
	switch x := v.(type) {
	case string:
		return noNUL(x)
	case map[string]any:
		for k, val := range x {
			if strings.ContainsRune(k, 0) { // Postgres can't store NUL, and the entry would be lost
				delete(x, k)
				k = noNUL(k)
			}
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

// noNUL replaces NUL characters, which Postgres can't store, with U+FFFD.
func noNUL(s string) string { return strings.ReplaceAll(s, "\x00", "\uFFFD") }

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

// auditFilter reads the list and export filters: user_id, object_type, and from / to as dates
// (YYYY-MM-DD, to inclusive) or RFC 3339 times (to exclusive).
func auditFilter(q url.Values) (model.AuditFilter, error) {
	f := model.AuditFilter{ObjectType: q.Get("object_type"), Limit: 100}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	f.UserID, _ = strconv.ParseInt(q.Get("user_id"), 10, 64)
	for _, x := range []struct {
		name string
		dst  *time.Time
		end  bool
	}{{"from", &f.From, false}, {"to", &f.To, true}} {
		v := q.Get(x.name)
		if v == "" {
			continue
		}
		if d, err := time.Parse("2006-01-02", v); err == nil {
			if x.end {
				d = d.AddDate(0, 0, 1)
			}
			*x.dst = d
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("%s must be a date (YYYY-MM-DD) or an RFC 3339 time", x.name)
		}
		*x.dst = t
	}
	if !f.From.IsZero() && !f.To.IsZero() && !f.From.Before(f.To) {
		return f, errors.New("from must be before to")
	}
	return f, nil
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, p model.Principal) {
	f, err := auditFilter(r.URL.Query())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
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

// exportAudit streams the filtered log, newest first, as CSV (format=csv, the default) or a JSON
// array (format=json), a page of 1,000 entries at a time. It needs what viewing the log needs.
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request, p model.Principal) {
	q := r.URL.Query()
	f, err := auditFilter(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	format := q.Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		writeErr(w, http.StatusBadRequest, "format must be csv or json")
		return
	}
	st := s.Store.(AuditStore)
	f.Limit = 1000
	first, err := st.ListAudit(r.Context(), p.TenantID, f)
	if err != nil {
		s.fail(w, err)
		return
	}
	name := "audit-log-" + s.now().UTC().Format("2006-01-02") + "." + format
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	ae := newAuditEncoder(w, format)
	page := first
	for {
		for _, e := range page {
			ae.write(e)
		}
		ae.flush()
		if len(page) < f.Limit {
			break
		}
		f.Before = page[len(page)-1].ID
		if page, err = st.ListAudit(r.Context(), p.TenantID, f); err != nil {
			s.Log.Error("audit export", "err", err) // headers are sent: the file ends early
			break
		}
	}
	ae.close()
}

// auditEncoder writes entries as CSV rows or as the elements of one JSON array.
type auditEncoder struct {
	w     http.ResponseWriter
	csv   *csv.Writer
	n     int
	isCSV bool
}

var auditColumns = []string{"id", "at", "user_id", "actor", "action", "object_type", "object_id", "status", "ip", "detail"}

func newAuditEncoder(w http.ResponseWriter, format string) *auditEncoder {
	ae := &auditEncoder{w: w, isCSV: format == "csv"}
	if ae.isCSV {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		ae.csv = csv.NewWriter(w)
		_ = ae.csv.Write(auditColumns)
	} else {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "[")
	}
	return ae
}

func (ae *auditEncoder) write(e model.AuditEntry) {
	if ae.isCSV {
		_ = ae.csv.Write(auditRow(e))
		return
	}
	if ae.n > 0 {
		_, _ = io.WriteString(ae.w, ",\n")
	} else {
		_, _ = io.WriteString(ae.w, "\n")
	}
	b, _ := json.Marshal(e)
	_, _ = ae.w.Write(b)
	ae.n++
}

func (ae *auditEncoder) flush() {
	if ae.isCSV {
		ae.csv.Flush()
	}
	if fl, ok := ae.w.(http.Flusher); ok {
		fl.Flush()
	}
}

func (ae *auditEncoder) close() {
	if !ae.isCSV {
		_, _ = io.WriteString(ae.w, "\n]\n")
	}
	ae.flush()
}

// auditRow is one CSV row; every text a user or a request could influence goes through csvSafe.
func auditRow(e model.AuditEntry) []string {
	uid := ""
	if e.UserID != 0 {
		uid = strconv.FormatInt(e.UserID, 10)
	}
	return []string{strconv.FormatInt(e.ID, 10), e.At.UTC().Format(time.RFC3339), uid, csvSafe(e.Actor), csvSafe(e.Action),
		csvSafe(e.ObjectType), csvSafe(e.ObjectID), strconv.Itoa(e.Status), csvSafe(e.IP), csvSafe(string(e.Detail))}
}
