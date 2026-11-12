package api

// M6 web push: the no-code opt-in prompt, the install check and the browser self-check.
//
//	GET  /v1/settings/web-push             settings and defaults (channels view)
//	PUT  /v1/settings/web-push             save them (channels edit)
//	POST /v1/settings/web-push/check       fetch the site and its service worker from the server (channels edit)
//	POST /v1/settings/web-push/check-token a link that makes the SDK report Pulse.diagnose() (channels edit)
//	GET  /v1/settings/web-push/diagnosis   what the browser reported for that link (channels view)
//	POST /v1/web/diagnose                  the SDK's report (public: site key + check token)

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/webcheck"
)

// WebPushStore keeps web push settings (pg.Store).
type WebPushStore interface {
	WebPushSettings(ctx context.Context, tenant int64) (webcheck.Settings, error)
	PutWebPushSettings(ctx context.Context, tenant int64, s webcheck.Settings) error
}

const (
	installChecksPerMinute = 10
	diagnoseTTL            = 30 * time.Minute
	diagnoseMaxBody        = 8 << 10
)

func (s *Server) webPushM6Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/settings/web-push", s.tenant(s.getWebPush))
	mux.HandleFunc("PUT /v1/settings/web-push", s.tenant(s.putWebPush))
	mux.HandleFunc("POST /v1/settings/web-push/check", s.tenant(s.installCheck))
	mux.HandleFunc("POST /v1/settings/web-push/check-token", s.tenant(s.checkToken))
	mux.HandleFunc("GET /v1/settings/web-push/diagnosis", s.tenant(s.diagnosis))
	mux.HandleFunc("OPTIONS /v1/web/diagnose", s.corsPreflight)
	mux.HandleFunc("POST /v1/web/diagnose", s.webDiagnose)
}

func (s *Server) webPushStore(w http.ResponseWriter) (WebPushStore, bool) {
	ws, ok := s.Store.(WebPushStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "web push settings are not available")
	}
	return ws, ok
}

// pushSettings reads the settings, defaults on any error (the SDK must keep working).
func (s *Server) pushSettings(ctx context.Context, tenant int64) webcheck.Settings {
	if ws, ok := s.Store.(WebPushStore); ok {
		if v, err := ws.WebPushSettings(ctx, tenant); err == nil {
			return v
		}
	}
	return webcheck.Defaults()
}

func (s *Server) getWebPush(w http.ResponseWriter, r *http.Request, tenant int64) {
	ws, ok := s.webPushStore(w)
	if !ok {
		return
	}
	v, err := ws.WebPushSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": v, "defaults": webcheck.Defaults(), "pulse_url": s.sdkBase()})
}

func (s *Server) putWebPush(w http.ResponseWriter, r *http.Request, tenant int64) {
	ws, ok := s.webPushStore(w)
	if !ok {
		return
	}
	var v webcheck.Settings
	if !decode(w, r, &v) {
		return
	}
	if v.ServiceWorker == "" {
		v.ServiceWorker = webcheck.DefaultServiceWorker
	}
	v.SiteURL = strings.TrimSpace(v.SiteURL)
	if err := v.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ws.PutWebPushSettings(r.Context(), tenant, v); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": v})
}

// sdkBase is where the web SDK and service worker are served from (the API's public URL).
func (s *Server) sdkBase() string {
	if s.Tracking != nil {
		return s.Tracking.Base()
	}
	return strings.TrimRight(s.PublicURL, "/")
}

// installCheck fetches the site and its service worker from the server (webcheck.Fetcher: public
// https addresses only, unless PULSE_ALLOW_PRIVATE_WEBHOOKS; same-site redirects; 1 MB, 10 s).
func (s *Server) installCheck(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		SiteURL       string `json:"site_url"`
		ServiceWorker string `json:"service_worker"`
	}
	if !decode(w, r, &req) {
		return
	}
	cur := s.pushSettings(r.Context(), tenant)
	if req.SiteURL == "" {
		req.SiteURL = cur.SiteURL
	}
	if req.ServiceWorker == "" {
		req.ServiceWorker = cur.ServiceWorker
	}
	check := webcheck.Settings{SiteURL: strings.TrimSpace(req.SiteURL), ServiceWorker: req.ServiceWorker, Prompt: webcheck.Defaults().Prompt}
	if check.SiteURL == "" {
		writeErr(w, http.StatusBadRequest, "enter your site's address")
		return
	}
	if err := check.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allowTenant(r.Context(), "pushcheck", tenant, installChecksPerMinute) {
		writeErr(w, http.StatusTooManyRequests, "too many checks; try again in a minute")
		return
	}
	t, err := s.Store.Tenant(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	vapid := false
	if s.Box != nil {
		pub, err := s.vapidPublic(r.Context(), tenant)
		vapid = err == nil && pub != ""
	}
	f := s.siteFetcher
	if f == nil {
		f = webcheck.NewFetcher(s.AllowPrivateWebhooks)
	}
	rep := f.Check(r.Context(), webcheck.Input{SiteURL: check.SiteURL, ServiceWorker: check.ServiceWorker, SiteKey: t.SiteKey,
		PulseURL: s.sdkBase(), VAPID: vapid})
	writeJSON(w, http.StatusOK, rep)
}

// checkToken makes a link to the site that turns on the SDK's self-check: opened in the admin's
// browser, Pulse.diagnose() runs there and is reported back for 30 minutes.
func (s *Server) checkToken(w http.ResponseWriter, r *http.Request, tenant int64) {
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "the browser check needs Redis")
		return
	}
	cur := s.pushSettings(r.Context(), tenant)
	tok := "pc" + auth.NewToken("")[:30]
	if err := s.RDB.Set(r.Context(), "pchk:"+tok, strconv.FormatInt(tenant, 10), diagnoseTTL).Err(); err != nil {
		s.fail(w, err)
		return
	}
	link := ""
	if u, err := url.Parse(cur.SiteURL); err == nil && u.Host != "" {
		q := u.Query()
		q.Set("pulse_check", tok)
		u.RawQuery = q.Encode()
		link = u.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "url": link, "expires_at": s.now().Add(diagnoseTTL)})
}

func (s *Server) diagnosis(w http.ResponseWriter, r *http.Request, tenant int64) {
	tok := r.URL.Query().Get("token")
	if s.RDB == nil || !strings.HasPrefix(tok, "pc") || len(tok) > 64 {
		writeErr(w, http.StatusBadRequest, "unknown check")
		return
	}
	owner, err := s.RDB.Get(r.Context(), "pchk:"+tok).Result()
	if err != nil || owner != strconv.FormatInt(tenant, 10) {
		writeErr(w, http.StatusNotFound, "this check has expired; start a new one")
		return
	}
	raw, err := s.RDB.Get(r.Context(), "pchkr:"+tok).Bytes()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"report": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": json.RawMessage(raw)})
}

// diagnoseReport is what Pulse.diagnose() reports; every field is checked and capped.
type diagnoseReport struct {
	SDKVersion     string   `json:"sdk_version"`
	URL            string   `json:"url"`
	HTTPS          bool     `json:"https"`
	PushSupported  bool     `json:"push_supported"`
	Permission     string   `json:"permission"` // default | granted | denied | unsupported
	ServiceWorker  string   `json:"service_worker"`
	WorkerFound    bool     `json:"worker_found"`
	WorkerPulse    bool     `json:"worker_pulse"`
	Subscribed     bool     `json:"subscribed"`
	PromptEnabled  bool     `json:"prompt_enabled"`
	Identified     bool     `json:"identified"`
	Problems       []string `json:"problems"`
	ReportedAt     string   `json:"reported_at"`
	SiteKeyMatches bool     `json:"site_key_matches"`
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// webDiagnose stores the SDK's self-check for the workspace whose check token it carries; the
// site key must belong to that workspace too.
func (s *Server) webDiagnose(w http.ResponseWriter, r *http.Request) {
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if !s.allowRate(r.Context(), "rldiag", t.ID, r, 1, 20) {
		writeErr(w, http.StatusTooManyRequests, "too many reports")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, diagnoseMaxBody)
	var req struct {
		Token  string         `json:"token"`
		Report diagnoseReport `json:"report"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !strings.HasPrefix(req.Token, "pc") || len(req.Token) > 64 {
		writeErr(w, http.StatusBadRequest, "unknown check")
		return
	}
	owner, err := s.RDB.Get(r.Context(), "pchk:"+req.Token).Result()
	if err != nil || owner != strconv.FormatInt(t.ID, 10) {
		writeErr(w, http.StatusForbidden, "this check isn't for this site key, or has expired")
		return
	}
	d := req.Report
	d.SDKVersion, d.URL, d.Permission, d.ServiceWorker = clip(d.SDKVersion, 40), clip(d.URL, 500), clip(d.Permission, 20), clip(d.ServiceWorker, 300)
	if len(d.Problems) > 10 {
		d.Problems = d.Problems[:10]
	}
	for i := range d.Problems {
		d.Problems[i] = clip(d.Problems[i], 300)
	}
	d.SiteKeyMatches = true // the SDK reported with this workspace's site key
	d.ReportedAt = s.now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(d)
	if err := s.RDB.Set(r.Context(), "pchkr:"+req.Token, b, diagnoseTTL).Err(); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
