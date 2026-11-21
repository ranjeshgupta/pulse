package api

// Double opt-in (O6): a form with double opt-in records optin_requested with a confirmation link
// (the journeys role sends the form's confirmation email). GET /f/confirm/{token} only shows a
// Confirm button (mail scanners open links); the button's POST to the same address confirms: the
// form's opt-ins, lists and tags apply, and optin_confirmed is recorded. Links last 7 days.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pulse/internal/forms"
	"pulse/internal/model"
	"pulse/internal/tracking"
)

const optinLinkTTL = 7 * 24 * time.Hour

func (s *Server) optinRoutes(mux *http.ServeMux) {
	// GET only shows the question: mail scanners open links, and must not confirm for anyone.
	mux.HandleFunc("GET /f/confirm/{token}", s.confirmOptin)
	mux.HandleFunc("POST /f/confirm/{token}", s.confirmOptin)
}

// optinURL is a submitter's confirmation link ("" when links can't be signed).
func (s *Server) optinURL(t model.Tenant, f model.Form, externalID, email string) string {
	if s.Tracking == nil {
		return ""
	}
	tok := s.Tracking.EncodeOptin(tracking.OptinToken{TenantID: t.ID, FormID: f.ID, ExternalID: externalID, Email: email, Issued: s.now().Unix()})
	return s.publicBase() + "/f/confirm/" + url.PathEscape(tok)
}

var optinPage = template.Must(template.New("o").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>{{.Title}}</title>
<style>*{box-sizing:border-box}body{font:16px/1.5 system-ui,sans-serif;background:#f4f6f8;color:#152238;margin:0;display:grid;place-items:center;min-height:100vh}
main{background:#fff;border:1px solid #dce1e8;border-radius:10px;padding:32px;max-width:480px;width:100%;margin:16px;text-align:center}
h1{font-size:22px;margin:8px 0}p{color:#5b6474;margin:0}.big{font-size:42px}</style></head>
<body><main><div class="big" aria-hidden="true">{{.Icon}}</div><h1>{{.Title}}</h1><p>{{.Text}}</p>
{{if .Ask}}<form method="post"><button style="font:inherit;font-weight:600;margin-top:18px;padding:12px 22px;border-radius:8px;border:0;background:#2747d6;color:#fff;cursor:pointer;width:100%">Yes, confirm my subscription</button></form>{{end}}
</main></body></html>`))

const optinCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'"

func renderOptin(w http.ResponseWriter, code int, icon, title, text string) {
	renderOptinPage(w, code, map[string]any{"Icon": icon, "Title": title, "Text": text})
}

func renderOptinPage(w http.ResponseWriter, code int, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer") // the link is a secret
	// No scripts at all, and never framed (a framed Confirm button could be clickjacked). Form
	// posts aren't restricted: after confirming, the form may redirect to the workspace's site.
	w.Header().Set("Content-Security-Policy", optinCSP)
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(code)
	_ = optinPage.Execute(w, data)
}

func (s *Server) confirmOptin(w http.ResponseWriter, r *http.Request) {
	if s.Tracking == nil {
		renderOptin(w, http.StatusServiceUnavailable, "⚠️", "Confirmation isn't available", "Please try again later.")
		return
	}
	raw := r.PathValue("token")
	tok, err := s.Tracking.DecodeOptin(raw)
	if err != nil {
		renderOptin(w, http.StatusNotFound, "🔗", "This link doesn't work", "It may be incomplete. Use the link in the email exactly as it came.")
		return
	}
	if s.now().Sub(time.Unix(tok.Issued, 0)) > optinLinkTTL {
		renderOptin(w, http.StatusGone, "⌛", "This link has expired", "Fill in the form again and we'll send a new one.")
		return
	}
	fs, ok := s.Store.(FormStore)
	if !ok {
		renderOptin(w, http.StatusServiceUnavailable, "⚠️", "Confirmation isn't available", "Please try again later.")
		return
	}
	f, err := fs.GetForm(r.Context(), tok.TenantID, tok.FormID)
	if err != nil {
		renderOptin(w, http.StatusNotFound, "🔗", "This link doesn't work any more", "The form it came from was removed.")
		return
	}
	if r.Method != http.MethodPost {
		renderOptinPage(w, http.StatusOK, map[string]any{"Icon": "✉️", "Title": "Confirm your subscription",
			"Text": "One click and you're in: " + tok.Email + ".", "Ask": true})
		return
	}
	// The same link always records the same event ids: clicking twice changes nothing.
	sum := sha256.Sum256([]byte(raw))
	id := "optin-" + hex.EncodeToString(sum[:10])
	ctx := context.WithoutCancel(r.Context())
	if err := s.publishEvents(ctx, tok.TenantID, forms.ConfirmEvents(f, tok.ExternalID, id)); err != nil {
		renderOptin(w, http.StatusServiceUnavailable, "⚠️", "We couldn't confirm that just now", "Please open the link again in a minute.")
		return
	}
	if u := f.Settings.ConfirmedRedirect; u != "" && (strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
		http.Redirect(w, r, u, http.StatusSeeOther)
		return
	}
	text := f.Settings.ConfirmedMessage
	if strings.TrimSpace(text) == "" {
		text = "Thanks — you're subscribed."
	}
	renderOptin(w, http.StatusOK, "✅", "You're confirmed", text)
}
