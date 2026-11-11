package api

// Provider tools for the settings form (M5): "Test connection" runs a provider's custom HTTP
// authentication (login or token request) and shows the token masked with its expiry; "Preview"
// shows the exact request a provider would send for a message, secrets masked. Both work on the
// settings being edited: secrets left empty come from the saved provider, unless an endpoint's
// host changed (then they must be entered again, as when saving).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/httpauth"
	"pulse/internal/model"
	"pulse/internal/personalize"
	"pulse/internal/providers"
)

func (s *Server) providerToolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/channels/providers/check-auth", s.admin(s.checkProviderAuth))
	mux.HandleFunc("POST /v1/channels/providers/preview", s.admin(s.previewProvider))
}

// providerDeps are the adapter dependencies for one saved provider: its token cache scope, and
// whether private addresses are allowed.
func (s *Server) providerDeps(tenant, id int64) providers.Deps {
	d := providers.Deps{AllowPrivate: s.AllowPrivateWebhooks}
	if s.RDB != nil && id != 0 {
		d.Tokens = httpauth.RedisCache{RDB: s.RDB, Box: boxOrNil(s)}
		d.Scope = providers.TokenScope(tenant, id)
	}
	return d
}

func boxOrNil(s *Server) httpauth.Sealer {
	if s.Box == nil {
		return nil
	}
	return s.Box
}

type providerDraft struct {
	ProviderID int64             `json:"provider_id"`
	Kind       string            `json:"provider"`
	Config     map[string]string `json:"config"`
	Secrets    map[string]string `json:"secrets"`
	// preview only
	To         string         `json:"to"`
	TemplateID int64          `json:"template_id"`
	ExternalID string         `json:"external_id"`
	Event      map[string]any `json:"event"`
}

// draftProvider builds an adapter from settings being edited (no shared token cache: a test
// always logs in).
func (s *Server) draftProvider(ctx context.Context, tenant int64, d providerDraft) (providers.Provider, providers.Kind, map[string]string, int, error) {
	if d.Config == nil {
		d.Config = map[string]string{}
	}
	stored := map[string]string{}
	if d.ProviderID != 0 {
		cur, err := s.chStore().GetProvider(ctx, tenant, d.ProviderID)
		if err != nil {
			return nil, providers.Kind{}, nil, http.StatusNotFound, errors.New("provider not found")
		}
		d.Kind = cur.Kind
		if !providers.EndpointsChanged(cur.Config, d.Config) {
			if stored, err = s.storedSecrets(ctx, tenant, d.ProviderID); err != nil {
				return nil, providers.Kind{}, nil, http.StatusInternalServerError, err
			}
		}
	}
	kind, ok := providers.Lookup(d.Kind)
	if !ok {
		return nil, kind, nil, http.StatusBadRequest, errors.New("unknown provider")
	}
	secrets := mergeSecrets(stored, d.Secrets)
	if err := providers.CheckConfig(d.Kind, d.Config, secrets, s.AllowPrivateWebhooks); err != nil {
		return nil, kind, nil, http.StatusBadRequest, err
	}
	p, err := providers.New(d.Kind, providers.Deps{AllowPrivate: s.AllowPrivateWebhooks}, d.Config, secrets)
	if err != nil {
		return nil, kind, nil, http.StatusBadRequest, errors.New("these settings don't work: " + err.Error())
	}
	return p, kind, secrets, 0, nil
}

// testAuthsPerMinute caps test logins per workspace: each one calls the provider's login.
const testAuthsPerMinute = 20

func (s *Server) checkProviderAuth(w http.ResponseWriter, r *http.Request, pr model.Principal) {
	var d providerDraft
	if !decode(w, r, &d) {
		return
	}
	if !s.allowTenant(r.Context(), "authtest", pr.TenantID, testAuthsPerMinute) {
		writeErr(w, http.StatusTooManyRequests, "too many connection tests; try again in a minute")
		return
	}
	p, _, _, code, err := s.draftProvider(r.Context(), pr.TenantID, d)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}
	t, ok := p.(providers.AuthTester)
	if !ok {
		writeErr(w, http.StatusBadRequest, "this provider has no custom authentication to test; use Send test")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := t.TestAuth(ctx)
	if err != nil {
		meaning := "the login request failed: retry, or check the URL"
		if errors.Is(err, httpauth.ErrRejected) {
			meaning = "the provider refused these credentials"
		} else if errors.Is(err, httpauth.ErrConfig) {
			meaning = "the answer didn't have the token where the settings say"
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error(), "meaning": meaning})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func (s *Server) previewProvider(w http.ResponseWriter, r *http.Request, pr model.Principal) {
	var d providerDraft
	if !decode(w, r, &d) || !eventFits(w, d.Event) {
		return
	}
	p, kind, _, code, err := s.draftProvider(r.Context(), pr.TenantID, d)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}
	pv, ok := p.(providers.Previewer)
	if !ok {
		writeErr(w, http.StatusBadRequest, "this provider has no request preview")
		return
	}
	o, err := s.previewOutbound(r.Context(), pr, kind.Channel, d)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	method, target, headers, body := pv.PreviewRequest(o)
	if strings.HasPrefix(body, "error: ") && method == "" {
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(body, "error: "))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"method": method, "url": target, "headers": headers, "body": body})
}

// previewOutbound is the message a preview shows: a template rendered for a contact (when
// given; contact data needs contacts view), or Pulse's fixed test message.
func (s *Server) previewOutbound(ctx context.Context, pr model.Principal, ch string, d providerDraft) (providers.Outbound, error) {
	to := strings.TrimSpace(d.To)
	if to == "" {
		to = map[string]string{channel.Email: "asha@example.com", channel.Push: "device-token"}[ch]
		if to == "" {
			to = "+919812345678"
		}
	}
	o := providers.Outbound{TenantID: pr.TenantID, MessageID: "preview-1", Channel: ch, To: to, Platform: "android",
		From: "Pulse <noreply@pulse.local>", Ref: "p1preview"}
	const text = "Test message from Pulse. If you can read this, the channel is connected."
	o.Rendered = channel.Rendered{Subject: "Pulse test message", HTML: "<p>" + text + "</p>",
		Content: channel.Content{Text: text, Title: "Pulse test", Body: text, TemplateName: "hello_world", Language: "en_US"}}
	contact := model.Contact{ExternalID: "preview", Attrs: map[string]any{"first_name": "Asha"}}
	if d.ExternalID != "" {
		if !pr.Perms.Allows(auth.AreaContacts, auth.View) {
			return o, errors.New("previewing with a contact's data needs contacts view access")
		}
		c, err := s.Store.ContactByExternalID(ctx, pr.TenantID, d.ExternalID)
		if err != nil {
			return o, errors.New("no contact " + d.ExternalID)
		}
		contact = c
	}
	v := channel.Vars{Event: d.Event}
	v.Campaign.Name, v.Campaign.Type, v.Campaign.Channel = "Preview", "test", ch
	if d.TemplateID != 0 {
		t, err := s.uiStore().Template(ctx, pr.TenantID, d.TemplateID)
		if err != nil {
			return o, errors.New("template not found")
		}
		if t.Channel != ch {
			return o, errors.New("that template is for " + t.Channel)
		}
		rendered, err := channel.RenderWith(t, contact, v)
		if err != nil {
			return o, err
		}
		o.Rendered = rendered
	}
	o.Vars = &personalize.Vars{Contact: contact, Event: v.Event, Campaign: v.Campaign}
	return o, nil
}
