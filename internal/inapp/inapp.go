// Package inapp validates in-app campaigns and picks the ones a contact may see.
//
// The SDK asks for its campaigns once per session (and after identify); Pulse answers with every
// active campaign whose platform and segments match the contact. The SDK then shows a campaign
// when its trigger event (or screen) happens and its frequency allows, and reports inapp_shown,
// inapp_clicked and inapp_dismissed events.
package inapp

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/schedule"
)

var (
	colour   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
)

// Validate checks a campaign.
func Validate(m model.InAppMessage) error {
	var errs []error
	if strings.TrimSpace(m.Name) == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if !slices.Contains([]string{"modal", "banner", "fullscreen"}, m.Layout) {
		errs = append(errs, errors.New("layout must be modal, banner or fullscreen"))
	}
	c := m.Content
	if strings.TrimSpace(c.Title) == "" && strings.TrimSpace(c.Body) == "" {
		errs = append(errs, errors.New("add a title or a message"))
	}
	if len(c.Buttons) > 2 {
		errs = append(errs, errors.New("at most 2 buttons"))
	}
	for i, b := range c.Buttons {
		switch {
		case strings.TrimSpace(b.Text) == "":
			errs = append(errs, fmt.Errorf("button %d needs text", i+1))
		case b.Action == "url" && !safeURL(b.Value):
			errs = append(errs, fmt.Errorf("button %d: the link must be https:// or an app deep link (myapp://...)", i+1))
		case b.Action != "url" && b.Action != "dismiss":
			errs = append(errs, fmt.Errorf("button %d: action must be url or dismiss", i+1))
		}
	}
	if c.ImageURL != "" && !strings.HasPrefix(c.ImageURL, "https://") {
		errs = append(errs, errors.New("the image must be an https URL"))
	}
	for _, col := range []string{c.Background, c.TextColor, c.ButtonColor} {
		if col != "" && !colour.MatchString(col) {
			errs = append(errs, errors.New("colors must look like #2747d6"))
			break
		}
	}
	if m.Layout == "banner" && c.Position != "" && c.Position != "top" && c.Position != "bottom" {
		errs = append(errs, errors.New("a banner goes at the top or bottom"))
	}
	t := m.Targeting
	for _, p := range t.Platforms {
		if p != "android" && p != "ios" {
			errs = append(errs, fmt.Errorf("platform %q: in-app messages are for android and ios apps (use focus items on websites)", p))
		}
	}
	if !event.ValidName(t.TriggerEvent) {
		errs = append(errs, errors.New("choose the event that shows it, e.g. app_open"))
	}
	if t.Screen != "" && t.TriggerEvent != "screen_view" {
		errs = append(errs, errors.New("a screen only applies to the screen_view trigger"))
	}
	if t.DelaySeconds < 0 || t.DelaySeconds > 600 {
		errs = append(errs, errors.New("delay must be 0 to 600 seconds"))
	}
	if !slices.Contains([]string{"once", "session", "daily", "always"}, t.Frequency) {
		errs = append(errs, errors.New("frequency must be once, session, daily or always"))
	}
	errs = append(errs, (schedule.Window{Start: m.StartAt, End: m.EndAt}).Validate())
	return errors.Join(errs...)
}

// safeURL allows https links and app deep links (scheme://...), never javascript: or data:.
func safeURL(u string) bool {
	i := strings.Index(u, "://")
	if i <= 0 {
		return false
	}
	scheme := strings.ToLower(u[:i])
	return scheme != "javascript" && scheme != "data" && scheme != "http" && scheme != "file" && schemeRe.MatchString(scheme)
}

// Segments lists every segment the campaigns target or exclude.
func Segments(ms []model.InAppMessage) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, m := range ms {
		for _, id := range append(append([]int64{}, m.Targeting.SegmentIDs...), m.Targeting.ExcludeSegmentIDs...) {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// For returns the active campaigns this contact may see on platform now, highest priority first.
func For(ms []model.InAppMessage, platform string, now time.Time, isMember func(int64) bool) []model.InAppMessage {
	out := []model.InAppMessage{}
	for _, m := range ms {
		w := schedule.Window{Start: m.StartAt, End: m.EndAt}
		if m.Status != "active" || !w.Active(now) {
			continue
		}
		t := m.Targeting
		if len(t.Platforms) > 0 && !slices.Contains(t.Platforms, platform) {
			continue
		}
		in := len(t.SegmentIDs) == 0
		for _, s := range t.SegmentIDs {
			in = in || isMember(s)
		}
		for _, s := range t.ExcludeSegmentIDs {
			in = in && !isMember(s)
		}
		if in {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	return out
}
