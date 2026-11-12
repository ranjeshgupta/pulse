// Package webcheck holds the web push settings of a workspace (the no-code opt-in prompt the web
// SDK shows before the browser's own permission prompt) and the install check, which fetches the
// customer's site from the server, through safehttp, to verify the snippet and service worker.
package webcheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Prompt is the soft opt-in prompt. The SDK shows it when Enabled, the browser can take push,
// permission hasn't been decided, the triggers are met and the frequency allows; "Allow" then
// opens the browser's permission prompt (a click, as browsers require).
type Prompt struct {
	Enabled  bool   `json:"enabled"`
	Style    string `json:"style"`              // bell | bar | modal
	Position string `json:"position,omitempty"` // bell: bottom-right | bottom-left; bar: top | bottom
	Title    string `json:"title"`
	Text     string `json:"text"`
	Allow    string `json:"allow"` // button labels
	Later    string `json:"later"`
	// Colours (#rrggbb).
	Background string `json:"background,omitempty"`
	TextColor  string `json:"text_color,omitempty"`
	Button     string `json:"button,omitempty"`
	// Triggers: all must be met (0 = no condition).
	DelaySeconds  int `json:"delay_seconds"`  // after the page loads
	ScrollPercent int `json:"scroll_percent"` // of the page
	PageViews     int `json:"page_views"`     // pages seen in this visit, including this one
	// After "Not now", ask again after RepeatDays days (0 = never again on this browser).
	RepeatDays int `json:"repeat_days"`
	// Pages: show only where the URL contains any of ShowOn (empty: everywhere), never where it
	// contains any of HideOn.
	ShowOn []string `json:"show_on,omitempty"`
	HideOn []string `json:"hide_on,omitempty"`
}

// Settings are a workspace's web push settings.
type Settings struct {
	Prompt Prompt `json:"prompt"`
	// SiteURL is the site the install check opens (https://www.example.com/).
	SiteURL string `json:"site_url,omitempty"`
	// ServiceWorker is the service worker's path on the site (default /pulse-sw.js).
	ServiceWorker string `json:"service_worker,omitempty"`
}

// DefaultServiceWorker is where the SDK registers the service worker unless told otherwise.
const DefaultServiceWorker = "/pulse-sw.js"

// Defaults: prompt off, a friendly bell after 5 seconds, asked again after 7 days.
func Defaults() Settings {
	return Settings{ServiceWorker: DefaultServiceWorker, Prompt: Prompt{Style: "bell", Position: "bottom-right",
		Title: "Get updates", Text: "We'll let you know about offers and news. You can turn this off at any time.",
		Allow: "Allow", Later: "Not now", Background: "#ffffff", TextColor: "#152238", Button: "#2747d6",
		DelaySeconds: 5, RepeatDays: 7}}
}

// Load reads stored settings over the defaults.
func Load(raw []byte) (Settings, error) {
	s := Defaults()
	if len(raw) == 0 || string(raw) == "{}" {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Defaults(), err
	}
	if s.ServiceWorker == "" {
		s.ServiceWorker = DefaultServiceWorker
	}
	return s, nil
}

var (
	colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	swRe    = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}\.js$`)
)

// Validate checks settings before they're saved, with messages for marketers.
func (s Settings) Validate() error {
	p := s.Prompt
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	switch p.Style {
	case "bell":
		if p.Position != "" && p.Position != "bottom-right" && p.Position != "bottom-left" {
			bad("the bell sits bottom-right or bottom-left")
		}
	case "bar":
		if p.Position != "" && p.Position != "top" && p.Position != "bottom" {
			bad("the bar sits at the top or bottom")
		}
	case "modal":
	default:
		bad("choose a bell, a bar or a pop-up")
	}
	if p.Enabled && (strings.TrimSpace(p.Text) == "" || strings.TrimSpace(p.Allow) == "") {
		bad("write the message and the Allow button's label")
	}
	for name, v := range map[string]string{"title": p.Title, "allow": p.Allow, "later": p.Later} {
		if len(v) > 60 {
			bad("the %s is longer than 60 characters", name)
		}
	}
	if len(p.Text) > 300 {
		bad("the message is longer than 300 characters")
	}
	for _, c := range []string{p.Background, p.TextColor, p.Button} {
		if c != "" && !colorRe.MatchString(c) {
			bad("colours look like #1a2b3c")
		}
	}
	if p.DelaySeconds < 0 || p.DelaySeconds > 600 {
		bad("the delay is 0 to 600 seconds")
	}
	if p.ScrollPercent < 0 || p.ScrollPercent > 100 {
		bad("scrolling is 0 to 100%%")
	}
	if p.PageViews < 0 || p.PageViews > 50 {
		bad("pages seen is 0 to 50")
	}
	if p.RepeatDays < 0 || p.RepeatDays > 365 {
		bad("ask again after 0 to 365 days")
	}
	if len(p.ShowOn) > 20 || len(p.HideOn) > 20 {
		bad("at most 20 page rules of each kind")
	}
	for _, u := range slices.Concat(p.ShowOn, p.HideOn) {
		if strings.TrimSpace(u) == "" || len(u) > 300 {
			bad("page rules are 1 to 300 characters")
			break
		}
	}
	if s.SiteURL != "" {
		u, err := url.Parse(s.SiteURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			bad("the site address must look like https://www.example.com/")
		}
	}
	if s.ServiceWorker != "" && (!swRe.MatchString(s.ServiceWorker) || strings.Contains(s.ServiceWorker, "..")) {
		bad("the service worker path must look like /pulse-sw.js")
	}
	return errors.Join(errs...)
}
