// Package onsite decides what a website visitor sees: which focus items (modal, bar, slide-in) are
// eligible on this page, and which content fills each Dynamic Web Content slot. The web SDK calls the
// decision API, then applies triggers (exit intent, scroll, delay) and frequency caps in the browser.
package onsite

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"pulse/internal/model"
	"pulse/internal/schedule"
)

var slotRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Decision is the answer for one page view.
type Decision struct {
	Focus   []model.FocusItem `json:"focus"`
	Content map[string]string `json:"content"`
}

// Decide picks the eligible focus items and the content for each requested slot.
// isMember reports whether the visitor is in a segment (always false for anonymous visitors).
func Decide(items []model.FocusItem, slots []model.DWCSlot, wanted []string, pageURL string, now time.Time, isMember func(int64) bool) Decision {
	d := Decision{Focus: []model.FocusItem{}, Content: map[string]string{}}
	any := func(ids []int64) bool {
		for _, id := range ids {
			if isMember(id) {
				return true
			}
		}
		return false
	}
	for _, it := range items {
		t := it.Targeting
		if it.Status != "active" || !(schedule.Window{Start: it.StartAt, End: it.EndAt}).Active(now) || !urlMatches(pageURL, t.URLContains) {
			continue
		}
		if len(t.SegmentIDs) > 0 && !any(t.SegmentIDs) {
			continue
		}
		if any(t.ExcludeSegmentIDs) {
			continue
		}
		d.Focus = append(d.Focus, it)
	}
	want := map[string]bool{}
	for _, w := range wanted {
		want[w] = true
	}
	for _, s := range slots {
		if s.Status != "active" || !want[s.Slot] || !(schedule.Window{Start: s.StartAt, End: s.EndAt}).Active(now) {
			continue
		}
		html := s.DefaultHTML
		for _, v := range s.Variants {
			if isMember(v.SegmentID) {
				html = v.HTML
				break
			}
		}
		d.Content[s.Slot] = html
	}
	return d
}

// urlMatches checks the page's path and query only: matching the whole URL would let "/shop" match
// the host of https://shop.example.com/about.
func urlMatches(page string, contains []string) bool {
	if len(contains) == 0 {
		return true
	}
	if u, err := url.Parse(page); err == nil && u.Host != "" {
		page = u.RequestURI()
	}
	for _, c := range contains {
		if c != "" && strings.Contains(page, c) {
			return true
		}
	}
	return false
}

// Segments lists every segment the items and slots refer to (what the decision needs to look up).
func Segments(items []model.FocusItem, slots []model.DWCSlot) []int64 {
	seen := map[int64]bool{}
	var out []int64
	add := func(ids ...int64) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	for _, it := range items {
		add(it.Targeting.SegmentIDs...)
		add(it.Targeting.ExcludeSegmentIDs...)
	}
	for _, s := range slots {
		for _, v := range s.Variants {
			add(v.SegmentID)
		}
	}
	return out
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// safeURL allows only http(s) links, so content can never carry javascript: URLs.
func safeURL(u string) bool {
	if u == "" {
		return true
	}
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
}

// ValidateFocus checks a focus item before it is saved.
func ValidateFocus(f model.FocusItem) error {
	t, c := f.Targeting, f.Content
	switch {
	case f.Name == "":
		return fmt.Errorf("name is required")
	case !oneOf(f.Kind, "modal", "bar", "slide"):
		return fmt.Errorf("kind must be modal, bar or slide")
	case !oneOf(f.Status, "draft", "active"):
		return fmt.Errorf("status must be draft or active")
	case c.Title == "" && c.BodyHTML == "":
		return fmt.Errorf("a title or body is required")
	case !safeURL(c.CTAURL) || !safeURL(c.ImageURL):
		return fmt.Errorf("links and images must be http(s) URLs")
	case !oneOf(t.Trigger, "load", "exit_intent", "scroll", "delay"):
		return fmt.Errorf("trigger must be load, exit_intent, scroll or delay")
	case t.Trigger == "scroll" && (t.TriggerValue < 1 || t.TriggerValue > 100):
		return fmt.Errorf("scroll trigger needs a percentage (1-100)")
	case t.Trigger == "delay" && (t.TriggerValue < 1 || t.TriggerValue > 600):
		return fmt.Errorf("delay trigger needs 1-600 seconds")
	case !oneOf(t.Frequency, "once", "session", "daily", "always"):
		return fmt.Errorf("frequency must be once, session, daily or always")
	}
	return (schedule.Window{Start: f.StartAt, End: f.EndAt}).Validate()
}

// ValidateDWC checks a content slot before it is saved.
func ValidateDWC(d model.DWCSlot) error {
	switch {
	case !slotRe.MatchString(d.Slot):
		return fmt.Errorf("slot must be lowercase letters, digits, - or _ (e.g. home-hero)")
	case d.Name == "":
		return fmt.Errorf("name is required")
	case !oneOf(d.Status, "draft", "active"):
		return fmt.Errorf("status must be draft or active")
	}
	for i, v := range d.Variants {
		if v.SegmentID <= 0 {
			return fmt.Errorf("variant %d needs a segment", i+1)
		}
	}
	return (schedule.Window{Start: d.StartAt, End: d.EndAt}).Validate()
}
