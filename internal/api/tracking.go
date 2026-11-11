package api

// Public endpoints that email links call: the open pixel, click redirects and unsubscribe.
// They are authenticated by the sealed token in the link, not by a session or key.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/tracking"
)

func (s *Server) trackingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /t/o/{file}", s.trackOpen)
	mux.HandleFunc("GET /t/c/{token}", s.trackClick)
	mux.HandleFunc("GET /t/s/{code}", s.trackShortClick)
	mux.HandleFunc("POST /t/p/{token}", s.trackPush)
	mux.HandleFunc("OPTIONS /t/p/{token}", s.trackPushPreflight)
	mux.HandleFunc("GET /u/{token}", s.unsubscribePage)
	mux.HandleFunc("POST /u/{token}", s.unsubscribe)
}

// trackEvent records an engagement event for the message's contact. The id makes repeats of the same
// open or click count once.
func (s *Server) trackEvent(ctx context.Context, t tracking.Token, name, id string, props map[string]any) {
	if props == nil {
		props = map[string]any{}
	}
	for k, v := range t.Props() {
		props[k] = v
	}
	ev := event.Event{ID: id, TenantID: t.TenantID, ExternalID: t.ExternalID, Name: name, Ts: s.now().UTC(), Props: props}
	rec, err := kafka.JSON(event.TopicRaw, ev.RawKey(), ev)
	if err == nil {
		err = s.Pub.Publish(ctx, rec)
	}
	if err != nil {
		s.Log.Error("record engagement", "event", name, "err", err)
	}
}

func (s *Server) trackOpen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	if s.Tracking != nil {
		if t, err := s.Tracking.Decode(strings.TrimSuffix(r.PathValue("file"), ".gif")); err == nil {
			s.trackEvent(r.Context(), t, t.Channel+"_opened", "open:"+t.MessageID, nil)
		}
	}
	_, _ = w.Write(tracking.Pixel) // always answer with the pixel, so broken tokens don't show as broken images
}

func (s *Server) trackClick(w http.ResponseWriter, r *http.Request) {
	token, dest, sig := r.PathValue("token"), r.URL.Query().Get("u"), r.URL.Query().Get("s")
	if s.Tracking == nil || !s.Tracking.VerifyClick(token, dest, sig) || !(strings.HasPrefix(dest, "https://") || strings.HasPrefix(dest, "http://")) {
		http.Error(w, "This link is invalid or has been changed.", http.StatusBadRequest)
		return
	}
	if t, err := s.Tracking.Decode(token); err == nil {
		sum := sha256.Sum256([]byte(dest))
		s.trackEvent(r.Context(), t, t.Channel+"_clicked", "click:"+t.MessageID+":"+hex.EncodeToString(sum[:6]), map[string]any{"url": dest})
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// trackShortClick follows a short link from a text message (SMS, RCS) and records <channel>_clicked.
func (s *Server) trackShortClick(w http.ResponseWriter, r *http.Request) {
	if s.RDB == nil {
		http.Error(w, "This link is invalid or has expired.", http.StatusNotFound)
		return
	}
	t, dest, err := tracking.ResolveShort(r.Context(), s.RDB, r.PathValue("code"))
	if errors.Is(err, tracking.ErrNoShortLink) || err == nil && !(strings.HasPrefix(dest, "https://") || strings.HasPrefix(dest, "http://")) {
		http.Error(w, "This link is invalid or has expired.", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Try again in a moment.", http.StatusServiceUnavailable)
		return
	}
	sum := sha256.Sum256([]byte(dest))
	s.trackEvent(r.Context(), t, t.Channel+"_clicked", "click:"+t.MessageID+":"+hex.EncodeToString(sum[:6]), map[string]any{"url": dest})
	http.Redirect(w, r, dest, http.StatusFound)
}

// trackPush records a push notification's click (push_clicked) or dismissal (push_dismissed),
// reported by the web SDK's service worker or an app SDK: POST /t/p/{token}?a=clicked|dismissed,
// no body. The sealed token names the message; each counts once per message. Any origin may call
// it (the service worker runs on the customer's site) and nothing is returned.
func (s *Server) trackPush(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if s.Tracking == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	t, err := s.Tracking.Decode(r.PathValue("token"))
	if err != nil || t.Channel != channel.Push {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	action := r.URL.Query().Get("a")
	if action == "" {
		action = "clicked"
	}
	if action != "clicked" && action != "dismissed" {
		writeErr(w, http.StatusBadRequest, "a must be clicked or dismissed")
		return
	}
	props := map[string]any{}
	if p := r.URL.Query().Get("platform"); p == "web" || p == "android" || p == "ios" {
		props["platform"] = p
	}
	s.trackEvent(r.Context(), t, "push_"+action, "push:"+t.MessageID+":"+action, props)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) trackPushPreflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

var unsubPage = template.Must(template.New("u").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>Email preferences</title>
<style>body{font:16px/1.5 system-ui,sans-serif;background:#f4f6f8;color:#152238;margin:0;display:grid;place-items:center;min-height:100vh}
main{background:#fff;border:1px solid #dce1e8;border-radius:10px;padding:32px;max-width:440px;margin:16px}
h1{font-size:22px;margin:0 0 8px}p{color:#5b6474;margin:0 0 20px}form{margin:0 0 10px}
button{font:inherit;font-weight:600;border-radius:6px;padding:10px 16px;cursor:pointer;width:100%}
.primary{background:#2747d6;color:#fff;border:1px solid #2747d6}.secondary{background:#fff;color:#152238;border:1px solid #c3cbd6}</style></head>
<body><main>{{if .Done}}<h1>{{.Done}}</h1><p>{{.Detail}}</p>
{{if .CanUndo}}<form method="post"><input type="hidden" name="action" value="resubscribe"><button class="secondary">Subscribe again</button></form>{{end}}
{{else if .Invalid}}<h1>This link has expired or is invalid</h1><p>Use the unsubscribe link in a recent email from {{.Workspace}}.</p>
{{else}}<h1>Unsubscribe from {{.Workspace}}?</h1><p>We'll stop sending emails to this address.</p>
<form method="post"><input type="hidden" name="action" value="email"><button class="primary">Unsubscribe from emails</button></form>
<form method="post"><input type="hidden" name="action" value="all"><button class="secondary">Stop all messages (SMS, WhatsApp, push too)</button></form>
{{with .PrefsURL}}<p style="text-align:center;margin:14px 0 0"><a href="{{.}}" style="color:#2747d6">Choose what you get instead</a></p>{{end}}{{end}}
</main></body></html>`))

type unsubView struct {
	Workspace, Done, Detail, PrefsURL string
	Invalid, CanUndo                  bool
}

func (s *Server) unsubToken(r *http.Request) (tracking.Token, string, bool) {
	if s.Tracking == nil {
		return tracking.Token{}, "us", false
	}
	t, err := s.Tracking.Decode(r.PathValue("token"))
	if err != nil {
		return t, "us", false
	}
	name := "us"
	if ws, err := s.Store.Tenant(r.Context(), t.TenantID); err == nil {
		name = ws.Name
	}
	return t, name, true
}

func renderUnsub(w http.ResponseWriter, code int, v unsubView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.WriteHeader(code)
	_ = unsubPage.Execute(w, v)
}

func (s *Server) unsubscribePage(w http.ResponseWriter, r *http.Request) {
	t, name, ok := s.unsubToken(r)
	if !ok {
		renderUnsub(w, http.StatusNotFound, unsubView{Workspace: name, Invalid: true})
		return
	}
	if r.URL.Query().Get("view") == "preferences" {
		s.preferencesPage(w, r, t, name)
		return
	}
	renderUnsub(w, http.StatusOK, unsubView{Workspace: name, PrefsURL: "?view=preferences"})
}

// unsubscribe handles the page's buttons and RFC 8058 one-click requests from mailbox providers
// (body "List-Unsubscribe=One-Click"), which unsubscribe from email.
func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	t, name, ok := s.unsubToken(r)
	if !ok {
		renderUnsub(w, http.StatusNotFound, unsubView{Workspace: name, Invalid: true})
		return
	}
	_ = r.ParseForm()
	action := r.PostFormValue("action")
	oneClick := r.PostFormValue("List-Unsubscribe") == "One-Click"
	switch {
	case action == "preferences":
		s.savePreferences(w, r, t, name)
	case action == "resubscribe":
		s.trackEvent(r.Context(), t, consent.Resubscribed, "resub:"+t.MessageID+":"+s.now().Format("20060102150405"),
			map[string]any{"channel": consent.All, "source": "email_link"})
		renderUnsub(w, http.StatusOK, unsubView{Workspace: name, Done: "You're subscribed again", Detail: "Thanks for staying with " + name + "."})
	case action == "all":
		s.trackEvent(r.Context(), t, consent.Unsubscribed, "unsub:"+t.MessageID+":all", map[string]any{"channel": consent.All, "source": "email_link"})
		renderUnsub(w, http.StatusOK, unsubView{Workspace: name, Done: "You won't hear from us again", Detail: name + " will stop sending you emails, SMS, WhatsApp and push messages.", CanUndo: true})
	case action == "email" || oneClick:
		s.trackEvent(r.Context(), t, consent.Unsubscribed, "unsub:"+t.MessageID+":email", map[string]any{"channel": "email", "source": map[bool]string{true: "one_click", false: "email_link"}[oneClick]})
		if oneClick {
			w.WriteHeader(http.StatusOK)
			return
		}
		renderUnsub(w, http.StatusOK, unsubView{Workspace: name, Done: "You're unsubscribed", Detail: "You won't get emails from " + name + " anymore. It can take a minute for messages already on their way.", CanUndo: true})
	default:
		renderUnsub(w, http.StatusOK, unsubView{Workspace: name})
	}
}
