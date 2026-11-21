package onsite

// App content slots (batch O3): dynamic content for named places in a mobile app, the app twin of
// a website's data-pulse-slot. The SDKs ask POST /v1/web/content for the slots on screen and draw
// the card (or HTML) they get; targeting works as on the web, except page-address rules, which
// an app has no use for.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"pulse/internal/model"
	"pulse/internal/schedule"
)

// Platforms of a slot.
const (
	PlatformWeb = "web"
	PlatformApp = "app"
)

// AppContent is what an app slot shows: a card, or HTML for a web view.
type AppContent struct {
	Card    *model.AppCard `json:"card,omitempty"`
	HTML    string         `json:"html,omitempty"`
	Variant string         `json:"variant,omitempty"` // the variant shown ("" for the default)
}

// IsApp reports whether a slot is an app slot.
func IsApp(s model.DWCSlot) bool { return s.Platform == PlatformApp }

// DecideApp picks the content of each requested app slot for the visitor (first matching variant,
// else the default). Slots with no content at all are left out.
func DecideApp(slots []model.DWCSlot, wanted []string, now time.Time, v Visitor) map[string]AppContent {
	want := map[string]bool{}
	for _, w := range wanted {
		want[w] = true
	}
	out := map[string]AppContent{}
	for _, s := range slots {
		if !IsApp(s) || s.Status != "active" || !want[s.Slot] || !(schedule.Window{Start: s.StartAt, End: s.EndAt}).Active(now) {
			continue
		}
		c := AppContent{Card: s.DefaultCard, HTML: s.DefaultHTML}
		for i, vr := range s.Variants {
			if Matches(vr.Rules(), v) {
				c = AppContent{Card: vr.Card, HTML: vr.HTML, Variant: vr.ID}
				if c.Variant == "" {
					c.Variant = fmt.Sprintf("%d", i+1)
				}
				break
			}
		}
		if c.Card == nil && c.HTML == "" {
			continue
		}
		out[s.Slot] = c
	}
	return out
}

var (
	schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]{0,31}$`)
	dataKey  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`)
)

// SafeLink allows https links and app deep links (scheme://…), never javascript:, data:, file: or
// plain http.
func SafeLink(u string) bool {
	i := strings.Index(u, "://")
	if i <= 0 {
		return false
	}
	scheme := strings.ToLower(u[:i])
	return scheme != "javascript" && scheme != "data" && scheme != "http" && scheme != "file" && schemeRe.MatchString(scheme)
}

// MaxCardData caps a card's custom key-values.
const MaxCardData = 30

// ValidateCard checks an app card.
func ValidateCard(what string, c *model.AppCard) error {
	if c == nil {
		return nil
	}
	switch {
	case len(c.Title) > 200:
		return fmt.Errorf("%s: the title is longer than 200 characters", what)
	case len(c.Body) > 2000:
		return fmt.Errorf("%s: the text is longer than 2,000 characters", what)
	case len(c.ButtonText) > 60:
		return fmt.Errorf("%s: the button text is longer than 60 characters", what)
	case c.ImageURL != "" && !strings.HasPrefix(c.ImageURL, "https://"):
		return fmt.Errorf("%s: the image must be an https:// address", what)
	case c.DeepLink != "" && !SafeLink(c.DeepLink):
		return fmt.Errorf("%s: the link must be https:// or an app link like myapp://offers", what)
	case len(c.Data) > MaxCardData:
		return fmt.Errorf("%s: at most %d custom values", what, MaxCardData)
	}
	for k, v := range c.Data {
		if !dataKey.MatchString(k) {
			return fmt.Errorf("%s: custom value name %q: use letters, digits, _ . or -", what, k)
		}
		if len(v) > 1000 {
			return fmt.Errorf("%s: custom value %s is longer than 1,000 characters", what, k)
		}
	}
	return nil
}
