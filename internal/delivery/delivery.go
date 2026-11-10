// Package delivery holds a workspace's delivery rules: frequency caps (at most N marketing messages
// per channel per window, per contact) and quiet hours (marketing messages wait until morning in
// the contact's time zone). Templates marked transactional (OTPs, receipts) are exempt from both.
package delivery

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pulse/internal/channel"
)

type Cap struct {
	Max   int `json:"max"`   // messages
	Hours int `json:"hours"` // per this many hours
}

type QuietHours struct {
	Enabled  bool     `json:"enabled"`
	Start    string   `json:"start"`    // "21:00"
	End      string   `json:"end"`      // "09:00"
	Timezone string   `json:"timezone"` // used when the contact has no "timezone" attribute
	Channels []string `json:"channels"`
}

type Rules struct {
	Caps     map[string]Cap `json:"caps"`
	Quiet    QuietHours     `json:"quiet"`
	Keywords Keywords       `json:"keywords"`
}

// Keywords are replies (SMS, WhatsApp) that opt a contact out of, or back in to, that channel.
// A reply matches when its whole text is the keyword, ignoring case, spaces and punctuation.
type Keywords struct {
	OptOut []string `json:"opt_out"`
	OptIn  []string `json:"opt_in"`
}

// DefaultKeywords are the carrier-standard words (US CTIA, India TRAI practice).
var DefaultKeywords = Keywords{
	OptOut: []string{"STOP", "STOPALL", "UNSUBSCRIBE", "CANCEL", "END", "QUIT", "OPTOUT"},
	OptIn:  []string{"START", "UNSTOP", "SUBSCRIBE", "OPTIN"},
}

// Default rules: no caps, quiet hours off (a sensible 21:00-09:00 IST is prefilled for when they're
// turned on), standard opt-out keywords.
func Default() Rules {
	return Rules{Caps: map[string]Cap{}, Quiet: QuietHours{Start: "21:00", End: "09:00", Timezone: "Asia/Kolkata",
		Channels: []string{channel.SMS, channel.WhatsApp, channel.RCS, channel.Push}}, Keywords: DefaultKeywords}
}

var keywordRe = regexp.MustCompile(`^[A-Z0-9]{2,20}$`)

// normalizeKeyword upper-cases a reply and drops everything but letters and digits.
func normalizeKeyword(s string) string {
	var b []rune
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b = append(b, r)
		}
	}
	return string(b)
}

// Match returns "opt_out", "opt_in" or "" for a reply's text.
func (k Keywords) Match(text string) (string, string) {
	w := normalizeKeyword(text)
	if w == "" || len(w) > 20 {
		return "", ""
	}
	for _, o := range k.OptOut {
		if normalizeKeyword(o) == w {
			return "opt_out", w
		}
	}
	for _, o := range k.OptIn {
		if normalizeKeyword(o) == w {
			return "opt_in", w
		}
	}
	return "", ""
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func (r Rules) Validate() error {
	for ch, c := range r.Caps {
		if !channel.Valid(ch) {
			return fmt.Errorf("unknown channel %q", ch)
		}
		if c.Max < 1 || c.Hours < 1 || c.Hours > 24*31 {
			return fmt.Errorf("%s cap: at least 1 message, per 1 hour to 31 days", ch)
		}
	}
	seen := map[string]bool{}
	for _, list := range [][]string{r.Keywords.OptOut, r.Keywords.OptIn} {
		for _, k := range list {
			n := normalizeKeyword(k)
			if !keywordRe.MatchString(n) || n != strings.ToUpper(strings.TrimSpace(k)) {
				return fmt.Errorf("keyword %q: use 2-20 letters or digits", k)
			}
			if seen[n] {
				return fmt.Errorf("keyword %q is listed twice", k)
			}
			seen[n] = true
		}
	}
	if len(r.Keywords.OptOut) == 0 {
		return errors.New("keep at least one opt-out keyword such as STOP: carriers require it")
	}
	q := r.Quiet
	if !q.Enabled {
		return nil
	}
	if !hhmm.MatchString(q.Start) || !hhmm.MatchString(q.End) || q.Start == q.End {
		return errors.New("quiet hours need different start and end times like 21:00 and 09:00")
	}
	if _, err := time.LoadLocation(q.Timezone); err != nil || q.Timezone == "" {
		return fmt.Errorf("unknown time zone %q", q.Timezone)
	}
	for _, ch := range q.Channels {
		if !channel.Valid(ch) {
			return fmt.Errorf("unknown channel %q", ch)
		}
	}
	return nil
}

func minutes(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}

// QuietUntil reports whether now is inside quiet hours for this contact and channel, and if so when
// they end. tz is the contact's "timezone" attribute ("" to use the workspace's).
func (r Rules) QuietUntil(ch, tz string, now time.Time) (time.Time, bool) {
	q := r.Quiet
	if !q.Enabled {
		return time.Time{}, false
	}
	applies := false
	for _, c := range q.Channels {
		applies = applies || c == ch
	}
	if !applies {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(tz)
	if tz == "" || err != nil {
		if loc, err = time.LoadLocation(q.Timezone); err != nil {
			return time.Time{}, false
		}
	}
	local := now.In(loc)
	cur := local.Hour()*60 + local.Minute()
	start, end := minutes(q.Start), minutes(q.End)
	var inside bool
	if start < end { // e.g. 13:00-15:00
		inside = cur >= start && cur < end
	} else { // wraps midnight, e.g. 21:00-09:00
		inside = cur >= start || cur < end
	}
	if !inside {
		return time.Time{}, false
	}
	until := time.Date(local.Year(), local.Month(), local.Day(), end/60, end%60, 0, 0, loc)
	if !until.After(now) {
		until = until.AddDate(0, 0, 1)
	}
	return until, true
}
