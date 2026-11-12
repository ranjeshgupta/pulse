// Package onsite decides what a website visitor sees: which focus items (modal, bar, slide-in) are
// eligible on this page, and which content fills each Dynamic Web Content slot. The web SDK calls the
// decision API, then applies triggers (exit intent, scroll, delay) and frequency caps in the browser.
package onsite

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/segment"
)

var slotRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Decision is the answer for one page view. Focus items come in the order the SDK should try them
// (priority); Variants names the variant each slot shows ("" for the default).
type Decision struct {
	Focus    []model.FocusItem `json:"focus"`
	Content  map[string]string `json:"content"`
	Variants map[string]string `json:"variants,omitempty"`
}

// Visitor is what a decision knows about the person on the page.
type Visitor struct {
	PageURL string
	Device  string // desktop | mobile | tablet ("" unknown: device rules don't match)
	// IsMember reports segment membership (always false for anonymous visitors).
	IsMember func(int64) bool
	// Attrs are the contact's fields, only for a verified identity (nil otherwise: rules with
	// conditions on contact fields never match).
	Attrs map[string]any
}

// Decide picks the eligible focus items and the content for each requested slot.
// isMember reports whether the visitor is in a segment (always false for anonymous visitors).
func Decide(items []model.FocusItem, slots []model.DWCSlot, wanted []string, pageURL string, now time.Time, isMember func(int64) bool) Decision {
	return DecideFor(items, slots, wanted, now, Visitor{PageURL: pageURL, IsMember: isMember})
}

// Matches reports whether the visitor is in an audience.
func Matches(a model.OnsiteAudience, v Visitor) bool {
	in := func(ids []int64) bool {
		for _, id := range ids {
			if v.IsMember != nil && v.IsMember(id) {
				return true
			}
		}
		return false
	}
	switch {
	case len(a.SegmentIDs) > 0 && !in(a.SegmentIDs), in(a.ExcludeSegmentIDs):
		return false
	case !urlMatches(v.PageURL, a.URLContains), len(a.URLNotContains) > 0 && urlMatches(v.PageURL, a.URLNotContains):
		return false
	case len(a.Devices) > 0 && !slices.Contains(a.Devices, v.Device):
		return false
	case len(a.Conditions) > 0 && (v.Attrs == nil || !segment.MatchProps(a.Conditions, v.Attrs)):
		return false
	}
	return true
}

// DecideFor is Decide with everything known about the visitor.
func DecideFor(items []model.FocusItem, slots []model.DWCSlot, wanted []string, now time.Time, v Visitor) Decision {
	d := Decision{Focus: []model.FocusItem{}, Content: map[string]string{}, Variants: map[string]string{}}
	for _, it := range items {
		if it.Status != "active" || !(schedule.Window{Start: it.StartAt, End: it.EndAt}).Active(now) || !Matches(it.Targeting.Audience(), v) {
			continue
		}
		d.Focus = append(d.Focus, it)
	}
	SortFocus(d.Focus)
	want := map[string]bool{}
	for _, w := range wanted {
		want[w] = true
	}
	for _, s := range slots {
		if s.Status != "active" || !want[s.Slot] || !(schedule.Window{Start: s.StartAt, End: s.EndAt}).Active(now) {
			continue
		}
		html, which := s.DefaultHTML, ""
		for i, vr := range s.Variants {
			if Matches(vr.Rules(), v) {
				html, which = vr.HTML, vr.ID
				if which == "" {
					which = fmt.Sprintf("%d", i+1)
				}
				break
			}
		}
		d.Content[s.Slot] = html
		if which != "" {
			d.Variants[s.Slot] = which
		}
	}
	return d
}

// SortFocus orders focus items by priority (lower first), keeping the given order for ties.
func SortFocus(items []model.FocusItem) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Targeting.Priority < items[j].Targeting.Priority })
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
			r := v.Rules()
			add(r.SegmentIDs...)
			add(r.ExcludeSegmentIDs...)
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
	case t.Priority < 0 || t.Priority > 10000:
		return fmt.Errorf("priority must be 0 to 10000")
	}
	if err := ValidateAudience("audience", t.Audience()); err != nil {
		return err
	}
	return (schedule.Window{Start: f.StartAt, End: f.EndAt}).Validate()
}

// Limits of on-site audiences.
const (
	MaxVariants    = 20
	maxAudienceSeg = 20
	maxURLRules    = 20
)

var devices = []string{"desktop", "mobile", "tablet"}

// ValidateAudience checks one audience's rules (what says the where is the caller's label).
func ValidateAudience(what string, a model.OnsiteAudience) error {
	if len(a.SegmentIDs)+len(a.ExcludeSegmentIDs) > maxAudienceSeg {
		return fmt.Errorf("%s: at most %d segments", what, maxAudienceSeg)
	}
	for _, id := range append(slices.Clone(a.SegmentIDs), a.ExcludeSegmentIDs...) {
		if id <= 0 {
			return fmt.Errorf("%s: choose a segment", what)
		}
	}
	for _, id := range a.SegmentIDs {
		if slices.Contains(a.ExcludeSegmentIDs, id) {
			return fmt.Errorf("%s: a segment can't be both shown to and left out", what)
		}
	}
	if len(a.URLContains) > maxURLRules || len(a.URLNotContains) > maxURLRules {
		return fmt.Errorf("%s: at most %d page rules of each kind", what, maxURLRules)
	}
	for _, u := range append(slices.Clone(a.URLContains), a.URLNotContains...) {
		if strings.TrimSpace(u) == "" || len(u) > 300 {
			return fmt.Errorf("%s: page rules must be 1 to 300 characters", what)
		}
	}
	for _, d := range a.Devices {
		if !slices.Contains(devices, d) {
			return fmt.Errorf("%s: device must be desktop, mobile or tablet", what)
		}
	}
	return segment.ValidatePropFilters(what+" contact fields", a.Conditions)
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
	case len(d.Variants) > MaxVariants:
		return fmt.Errorf("at most %d variants", MaxVariants)
	}
	ids := map[string]bool{}
	for i, v := range d.Variants {
		what := fmt.Sprintf("variant %d", i+1)
		if v.Name != "" {
			what = fmt.Sprintf("variant %d (%s)", i+1, v.Name)
		}
		if v.ID != "" {
			if !variantIDRe.MatchString(v.ID) || ids[v.ID] {
				return fmt.Errorf("%s: its id must be unique letters and digits", what)
			}
			ids[v.ID] = true
		}
		if len(v.Name) > 80 {
			return fmt.Errorf("%s: the name is longer than 80 characters", what)
		}
		if err := ValidateAudience(what, v.Rules()); err != nil {
			return err
		}
	}
	return (schedule.Window{Start: d.StartAt, End: d.EndAt}).Validate()
}

var variantIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
