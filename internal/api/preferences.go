package api

// The preference center: from an email's {{.PreferencesURL}} (or a link minted for the contact by
// the API), a contact turns each channel on or off and joins or leaves the workspace's public lists.
// Changes are consent and list events, like every other change to a profile.

import (
	"context"
	"html/template"
	"net/http"
	"sort"
	"strconv"

	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/tracking"
)

func (s *Server) preferenceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/contacts/{external_id}/preferences-link", s.tenant(s.preferencesLink))
}

// preferencesLink returns a personal preference-center URL for a contact, e.g. for an account page
// or an SMS. Anyone holding it can change that contact's subscriptions, so send it only to them.
func (s *Server) preferencesLink(w http.ResponseWriter, r *http.Request, tenant int64) {
	if s.Tracking == nil {
		writeErr(w, http.StatusServiceUnavailable, "links need the server's secret key")
		return
	}
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, r.PathValue("external_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	tok := s.Tracking.Encode(tracking.Token{TenantID: tenant, ExternalID: c.ExternalID, MessageID: "pref-" + auth.NewToken("")[:12], Channel: channel.Email})
	writeJSON(w, http.StatusOK, map[string]any{"url": s.Tracking.PreferencesURL(tok)})
}

var prefPage = template.Must(template.New("p").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>Your preferences</title>
<style>*{box-sizing:border-box}body{font:16px/1.5 system-ui,sans-serif;background:#f4f6f8;color:#152238;margin:0;display:grid;place-items:center;min-height:100vh}
main{background:#fff;border:1px solid #dce1e8;border-radius:10px;padding:28px;max-width:520px;width:100%;margin:16px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:15px;margin:22px 0 8px;color:#3d4757}p{color:#5b6474;margin:0 0 8px}
.row{display:flex;gap:12px;align-items:flex-start;padding:10px 0;border-top:1px solid #eef1f4}.row:first-of-type{border-top:0}
.row input{width:20px;height:20px;margin-top:2px;accent-color:#2747d6;flex:none}.row b{display:block;font-weight:600}.row small{color:#5b6474}
.saved{background:#e7f6ec;color:#14532d;border-radius:6px;padding:10px 12px;margin:12px 0}
button{font:inherit;font-weight:600;border-radius:6px;padding:10px 16px;cursor:pointer;width:100%;margin-top:14px}
.primary{background:#2747d6;color:#fff;border:1px solid #2747d6}.secondary{background:#fff;color:#152238;border:1px solid #c3cbd6}
.addr{font-size:14px;color:#5b6474}</style></head>
<body><main>{{if .Invalid}}<h1>This link has expired or is invalid</h1><p>Use the link in a recent message from {{.Workspace}}.</p>{{else}}
<h1>Your preferences</h1><p>Choose what {{.Workspace}} sends you.{{with .Address}} <span class="addr">({{.}})</span>{{end}}</p>
{{if .Saved}}<div class="saved" role="status">Saved. Changes apply within a minute.</div>{{end}}
<form method="post"><input type="hidden" name="action" value="preferences">
<h2>How we reach you</h2>
{{range .Channels}}<label class="row"><input type="checkbox" name="ch_{{.ID}}"{{if .On}} checked{{end}}><span><b>{{.Name}}</b><small>{{.Hint}}</small></span></label>{{end}}
{{if .Lists}}<h2>What you get</h2>
{{range .Lists}}<label class="row"><input type="checkbox" name="list_{{.ID}}"{{if .On}} checked{{end}}><span><b>{{.Name}}</b>{{with .Description}}<small>{{.}}</small>{{end}}</span></label>{{end}}{{end}}
<button class="primary">Save preferences</button></form>
<form method="post"><input type="hidden" name="action" value="all"><button class="secondary">Unsubscribe from everything</button></form>
{{end}}</main></body></html>`))

type deviceLister interface {
	ContactDevices(ctx context.Context, tenant int64, contact uint32) ([]model.Device, error)
}

type prefChannel struct {
	ID, Name, Hint string
	On             bool
}

type prefList struct {
	ID                int64
	Name, Description string
	On                bool
}

type prefView struct {
	Workspace, Address string
	Channels           []prefChannel
	Lists              []prefList
	Saved, Invalid     bool
}

var channelHints = map[string]string{
	channel.Email: "News, offers and updates by email", channel.SMS: "Text messages to your phone",
	channel.WhatsApp: "Messages on WhatsApp", channel.RCS: "Rich messages in your phone's messaging app",
	channel.Push: "Notifications on your devices",
}

// prefState is what the contact can change and its current values.
func (s *Server) prefState(ctx context.Context, tenant int64, c model.Contact) ([]prefChannel, []prefList, error) {
	phone, _ := c.Attrs["phone"].(string)
	var chans []prefChannel
	for _, ch := range channel.All {
		switch ch {
		case channel.Email:
			if c.Email == "" {
				continue
			}
		case channel.Push:
			devs, err := s.Store.(deviceLister).ContactDevices(ctx, tenant, c.ID)
			if err != nil {
				return nil, nil, err
			}
			if len(devs) == 0 {
				continue
			}
		default:
			if phone == "" {
				continue
			}
		}
		chans = append(chans, prefChannel{ID: ch, Name: channelNames[ch], Hint: channelHints[ch], On: !consent.OptedOut(c.Attrs, ch)})
	}
	all, err := s.audience().ListSegmentsOfKind(ctx, tenant, "static")
	if err != nil {
		return nil, nil, err
	}
	var lists []prefList
	for _, l := range all {
		if !l.Public {
			continue
		}
		in, err := s.Members.IsMember(ctx, tenant, l.ID, c.ID)
		if err != nil {
			return nil, nil, err
		}
		lists = append(lists, prefList{ID: l.ID, Name: l.Name, Description: l.Description, On: in})
	}
	sort.Slice(lists, func(i, j int) bool { return lists[i].Name < lists[j].Name })
	return chans, lists, nil
}

var channelNames = map[string]string{channel.Email: "Email", channel.SMS: "SMS", channel.WhatsApp: "WhatsApp", channel.RCS: "RCS", channel.Push: "Push notifications"}

func renderPrefs(w http.ResponseWriter, code int, v prefView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = prefPage.Execute(w, v)
}

func (s *Server) preferencesPage(w http.ResponseWriter, r *http.Request, t tracking.Token, workspace string) {
	c, err := s.Store.ContactByExternalID(r.Context(), t.TenantID, t.ExternalID)
	if err != nil {
		renderPrefs(w, http.StatusNotFound, prefView{Workspace: workspace, Invalid: true})
		return
	}
	chans, lists, err := s.prefState(r.Context(), t.TenantID, c)
	if err != nil {
		s.Log.Error("preference center", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	renderPrefs(w, http.StatusOK, prefView{Workspace: workspace, Address: c.Email, Channels: chans, Lists: lists})
}

// savePreferences turns the form into consent and list events for what changed.
func (s *Server) savePreferences(w http.ResponseWriter, r *http.Request, t tracking.Token, workspace string) {
	c, err := s.Store.ContactByExternalID(r.Context(), t.TenantID, t.ExternalID)
	if err != nil {
		renderPrefs(w, http.StatusNotFound, prefView{Workspace: workspace, Invalid: true})
		return
	}
	chans, lists, err := s.prefState(r.Context(), t.TenantID, c)
	if err != nil {
		s.Log.Error("preference center", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	src := map[string]any{"source": "preference_center"}
	var evs []event.Event
	add := func(name string, props map[string]any) {
		for k, v := range src {
			props[k] = v
		}
		for k, v := range t.Props() {
			props[k] = v
		}
		evs = append(evs, event.Event{ExternalID: c.ExternalID, Name: name, Props: props})
	}
	// Turning a channel on also lifts an "everything" opt-out (consent.Updates does that), which
	// would turn the other channels back on too; so after an "everything" opt-out, the channels left
	// off are opted out one by one, after the opt-ins.
	allOff := consent.OptedOut(c.Attrs, consent.All)
	var offs []prefChannel
	resubscribed := false
	for i, ch := range chans {
		on := r.PostFormValue("ch_"+ch.ID) == "on"
		switch {
		case on && !ch.On:
			add(consent.Resubscribed, map[string]any{"channel": ch.ID})
			resubscribed = true
		case !on && (ch.On || allOff):
			offs = append(offs, ch)
		}
		chans[i].On = on
	}
	for _, ch := range offs {
		if ch.On || resubscribed {
			add(consent.Unsubscribed, map[string]any{"channel": ch.ID})
		}
	}
	for i, l := range lists {
		on := r.PostFormValue("list_"+strconv.FormatInt(l.ID, 10)) == "on"
		if on != l.On {
			add(map[bool]string{true: event.ListJoined, false: event.ListLeft}[on], map[string]any{"list_id": l.ID})
			lists[i].On = on
		}
	}
	if len(evs) > 0 {
		if err := s.publishEvents(r.Context(), t.TenantID, evs); err != nil {
			http.Error(w, "We couldn't save that. Please try again.", http.StatusServiceUnavailable)
			return
		}
	}
	// Events apply asynchronously, so show the state the contact just chose.
	renderPrefs(w, http.StatusOK, prefView{Workspace: workspace, Address: c.Email, Channels: chans, Lists: lists, Saved: true})
}
