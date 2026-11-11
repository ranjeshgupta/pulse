package providers

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"pulse/internal/httpauth"
)

// Endpoints lists where a provider's settings send credentials, as scheme://host[:port]: every
// URL setting (url, base_url, …; not status_callback_url, which is Pulse's own), an SMTP host,
// the custom request's URL and the authentication's login and token URLs.
func Endpoints(config map[string]string) []string {
	set := map[string]bool{}
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			set[strings.ToLower(u.Scheme+"://"+u.Host)] = true
			return
		}
		// A templated URL ({{…}} in its path) still has a fixed origin.
		if i := strings.Index(raw, "://"); i > 0 {
			rest := raw[i+3:]
			if j := strings.IndexAny(rest, "/?#"); j >= 0 {
				rest = rest[:j]
			}
			set[strings.ToLower(raw[:i]+"://"+rest)] = true
			return
		}
		set["?"+raw] = true
	}
	for k, v := range config {
		switch {
		case k == "status_callback_url":
		case k == "url" || strings.HasSuffix(k, "_url"):
			add(v)
		case k == "host":
			if h := strings.TrimSpace(v); h != "" {
				set["smtp://"+strings.ToLower(h)] = true
			}
		}
	}
	if a, err := httpauth.Parse(config["auth"]); err == nil {
		for _, u := range a.URLs() {
			add(u)
		}
	}
	if strings.TrimSpace(config["request"]) != "" {
		if cr, err := ParseCustomRequest(config["request"]); err == nil {
			add(cr.URL)
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// EndpointsChanged reports whether a settings change sends credentials to a different host:
// the stored secrets must then be entered again (the integrations rule).
func EndpointsChanged(before, after map[string]string) bool {
	return strings.Join(Endpoints(before), " ") != strings.Join(Endpoints(after), " ")
}

// CheckConfig validates settings beyond required fields: the custom HTTP authentication (with
// its secrets). allowPrivate (local development) permits plain-http endpoints.
func CheckConfig(kind string, config, secrets map[string]string, allowPrivate bool) error {
	k, ok := Lookup(kind)
	if !ok {
		return fmt.Errorf("unknown provider %q", kind)
	}
	if k.Auth {
		a, err := httpauth.Parse(config["auth"])
		if err != nil {
			return err
		}
		if err := a.Validate(secrets, true, allowPrivate); err != nil {
			return fmt.Errorf("authentication: %w", err)
		}
	}
	return nil
}
