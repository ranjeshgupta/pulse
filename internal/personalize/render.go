package personalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"
	"unicode"
	"unicode/utf8"

	"pulse/internal/model"
)

// Campaign describes what sends the message, for the campaign.* variables.
type Campaign struct {
	ID       int64  // journey or broadcast id
	Name     string // journey or broadcast name
	Type     string // journey | broadcast | test
	Step     string // journey step id
	Channel  string
	Template string // the template's name
}

// Vars is everything a message can be personalised with.
type Vars struct {
	Contact  model.Contact
	Event    map[string]any    // the triggering event's properties (nil for broadcasts)
	Campaign Campaign          //
	Links    map[string]string // unsubscribe, preferences
}

// Renderer fills templates for one message and remembers which variables had no value (and no
// default), for the missing-value policy.
type Renderer struct {
	vars    Vars
	loc     *time.Location
	missing map[string]bool
	// Extra functions for legacy actions (journey webhook bodies offer {{json .Attrs}}).
	Extra map[string]any
}

// New returns a renderer for one message.
func New(v Vars) *Renderer {
	r := &Renderer{vars: v, loc: time.UTC, missing: map[string]bool{}}
	for _, k := range []string{"timezone", "geo_timezone"} {
		if tz, _ := v.Contact.Attrs[k].(string); tz != "" {
			if l, err := time.LoadLocation(tz); err == nil {
				r.loc = l
				break
			}
		}
	}
	return r
}

// Missing lists the variables (e.g. contact.first_name) that rendered empty without a default.
func (r *Renderer) Missing() []string {
	out := make([]string, 0, len(r.missing))
	for k := range r.missing {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Text renders a plain-text field (nothing is escaped). dot is the data older actions read
// ({{.Attrs.first_name}}).
func (r *Renderer) Text(name, src string, dot any) (string, error) {
	if !strings.Contains(src, "{{") {
		return src, nil
	}
	tr, _, err := Translate(src, false)
	if err != nil {
		return "", err
	}
	tpl, err := texttemplate.New(name).Option("missingkey=zero").Funcs(r.funcs(false)).Parse(tr)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tpl.Execute(&b, dot); err != nil {
		return "", err
	}
	return strings.ReplaceAll(b.String(), "<no value>", ""), nil
}

// HTML renders an HTML document: every value is escaped for where it lands (text, attribute, URL).
func (r *Renderer) HTML(name, src string, dot any) (string, error) {
	if !strings.Contains(src, "{{") {
		return src, nil
	}
	tr, _, err := Translate(src, true)
	if err != nil {
		return "", err
	}
	tpl, err := htmltemplate.New(name).Option("missingkey=zero").Funcs(r.funcs(true)).Parse(tr)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tpl.Execute(&b, dot); err != nil {
		return "", err
	}
	return strings.ReplaceAll(b.String(), "&lt;no value&gt;", ""), nil
}

// Check reports whether a field parses (standard variables, pipes and Go template syntax).
func Check(name, src string, html bool) error {
	r := New(Vars{})
	var err error
	if html {
		_, err = r.HTML(name, src, nil)
	} else {
		_, err = r.Text(name, src, nil)
	}
	return err
}

func (r *Renderer) funcs(html bool) map[string]any {
	f := map[string]any{
		"pulseVar": func(name string) any {
			v := r.Value(name)
			if Empty(v) {
				r.missing[name] = true
			}
			return v
		},
		"pulseVarOpt": func(name string) any { return r.Value(name) },
		"pulseDefault": func(def string, v any) any {
			if Empty(v) {
				return def
			}
			return v
		},
		"pulseUpper": func(v any) string { return strings.ToUpper(Format(v)) },
		"pulseLower": func(v any) string { return strings.ToLower(Format(v)) },
		"pulseTitle": func(v any) string { return titleCase(Format(v)) },
		"pulseDate":  func(layout string, v any) string { return formatDate(v, layout, r.loc) },
		"pulseMoney": func(cur string, v any) string { return formatMoney(v, cur) },
		"pulseNumber": func(dec string, v any) string {
			d := -1
			if dec != "" {
				d, _ = strconv.Atoi(dec)
			}
			return formatNumber(v, d)
		},
		"pulseTruncate": func(n string, v any) string {
			max, _ := strconv.Atoi(n)
			return truncate(Format(v), max)
		},
		"pulseJSON": func(v any) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
		"pulseOut": func(v any) any {
			if u, ok := v.(htmltemplate.URL); ok {
				return u
			}
			return Format(v)
		},
	}
	if html {
		// Already percent-encoded: mark it so html/template doesn't encode the % signs again in
		// a link. It can't start a javascript: link: the colon is encoded too.
		f["pulseURLEncode"] = func(v any) any { return htmltemplate.URL(url.QueryEscape(Format(v))) }
	} else {
		f["pulseURLEncode"] = func(v any) any { return url.QueryEscape(Format(v)) }
	}
	for k, v := range r.Extra {
		f[k] = v
	}
	return f
}

// Value returns a standard variable's value (nil when there is none).
func (r *Renderer) Value(name string) any {
	ns, path, _ := strings.Cut(name, ".")
	switch ns {
	case NSContact:
		return contactValue(r.vars.Contact, path)
	case NSEvent:
		v, _ := Lookup(r.vars.Event, path)
		return v
	case NSCampaign:
		c := r.vars.Campaign
		switch path {
		case "id":
			if c.ID == 0 {
				return nil
			}
			return c.ID
		case "name":
			return c.Name
		case "type":
			return c.Type
		case "step":
			return c.Step
		case "channel":
			return c.Channel
		case "template":
			return c.Template
		}
	case NSLinks:
		return r.vars.Links[path]
	}
	return nil
}

// contactValue reads a contact field: email and external_id from the contact, anything else from
// its attributes (dotted paths into object values).
func contactValue(c model.Contact, path string) any {
	switch path {
	case "email":
		if c.Email != "" {
			return c.Email
		}
	case "external_id":
		return c.ExternalID
	}
	v, _ := Lookup(c.Attrs, path)
	return v
}

// Lookup follows a dotted path through objects and lists: items.0.name reads one item,
// items[].name every item (a list of their values).
func Lookup(m map[string]any, path string) (any, bool) {
	if m == nil || path == "" {
		return nil, false
	}
	segs := strings.Split(path, ".")
	var cur any = m
	for i, s := range segs {
		key, list := strings.CutSuffix(s, "[]")
		next, ok := step(cur, key)
		if !ok {
			return nil, false
		}
		if !list {
			cur = next
			continue
		}
		items, _ := next.([]any)
		rest := strings.Join(segs[i+1:], ".")
		out := []any{}
		for _, it := range items {
			if rest == "" {
				out = append(out, it)
				continue
			}
			if im, ok := it.(map[string]any); ok {
				if v, ok := Lookup(im, rest); ok && !Empty(v) {
					out = append(out, v)
				}
			}
		}
		return out, true
	}
	return cur, true
}

func step(cur any, key string) (any, bool) {
	switch x := cur.(type) {
	case map[string]any:
		v, ok := x[key]
		return v, ok
	case []any:
		n, err := strconv.Atoi(key)
		if err != nil || n < 0 || n >= len(x) {
			return nil, false
		}
		return x[n], true
	}
	return nil, false
}

// Empty reports whether a value counts as missing: absent, null, blank text or an empty list.
func Empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case htmltemplate.URL:
		return x == ""
	}
	return false
}

// Format is how a value prints: numbers without exponents, lists joined with ", ", objects as JSON.
func Format(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case htmltemplate.URL:
		return string(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x)
	case json.Number:
		return x.String()
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case []string:
		return strings.Join(x, ", ")
	case []any:
		parts := make([]string, 0, len(x))
		for _, it := range x {
			if m, ok := it.(map[string]any); ok {
				b, _ := json.Marshal(m)
				parts = append(parts, string(b))
				continue
			}
			parts = append(parts, Format(it))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		b, _ := json.Marshal(x)
		return string(b)
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Map || rv.Kind() == reflect.Slice {
		b, _ := json.Marshal(v)
		return string(b)
	}
	return fmt.Sprint(v)
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", ""), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

func titleCase(s string) string {
	var b strings.Builder
	start := true
	for _, r := range s {
		if unicode.IsSpace(r) || r == '-' {
			start = true
			b.WriteRune(r)
			continue
		}
		if start {
			b.WriteRune(unicode.ToUpper(r))
			start = false
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	rs := []rune(s)
	return strings.TrimRight(string(rs[:max-1]), " ") + "…"
}

// DefaultDateLayout is used by date without a layout.
const DefaultDateLayout = "2 Jan 2006"

var dateLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", time.DateOnly}

func parseTime(v any) (time.Time, bool) {
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		for _, l := range dateLayouts {
			if t, err := time.Parse(l, s); err == nil {
				return t, true
			}
		}
	}
	if t, ok := v.(time.Time); ok {
		return t, true
	}
	if f, ok := number(v); ok && f > 0 {
		if f > 1e11 { // milliseconds
			return time.UnixMilli(int64(f)), true
		}
		return time.Unix(int64(f), 0), true
	}
	return time.Time{}, false
}

func formatDate(v any, layout string, loc *time.Location) string {
	if Empty(v) {
		return ""
	}
	t, ok := parseTime(v)
	if !ok {
		return Format(v) // not a date: shown as it is
	}
	if layout == "" {
		layout = DefaultDateLayout
	}
	if s, isStr := v.(string); isStr && len(strings.TrimSpace(s)) == len(time.DateOnly) {
		return t.Format(layout) // a calendar date has no zone to convert from
	}
	return t.In(loc).Format(layout)
}

// group puts separators into the integer digits: 1,234,567, or Indian 12,34,567.
func group(digits string, indian bool) string {
	if len(digits) <= 3 {
		return digits
	}
	head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
	size := 3
	if indian {
		size = 2
	}
	var parts []string
	for len(head) > size {
		parts = append([]string{head[len(head)-size:]}, parts...)
		head = head[:len(head)-size]
	}
	parts = append([]string{head}, parts...)
	return strings.Join(parts, ",") + "," + tail
}

func formatFixed(f float64, dec int, indian bool) string {
	neg := f < 0
	p := math.Pow(10, float64(dec))
	s := strconv.FormatFloat(math.Round(math.Abs(f)*p)/p, 'f', dec, 64) // halves round up, as people expect
	ip, fp, _ := strings.Cut(s, ".")
	out := group(ip, indian)
	if fp != "" {
		out += "." + fp
	}
	if neg {
		out = "-" + out
	}
	return out
}

func formatNumber(v any, dec int) string {
	f, ok := number(v)
	if !ok {
		return Format(v)
	}
	if dec < 0 {
		dec = 0
		if f != math.Trunc(f) {
			dec = 2
		}
	}
	return formatFixed(f, dec, false)
}

var currencies = map[string]struct {
	symbol string
	dec    int
	indian bool
}{
	"INR": {"₹", 2, true}, "USD": {"$", 2, false}, "EUR": {"€", 2, false}, "GBP": {"£", 2, false},
	"JPY": {"¥", 0, false}, "AUD": {"A$", 2, false}, "CAD": {"C$", 2, false}, "SGD": {"S$", 2, false},
}

func formatMoney(v any, cur string) string {
	f, ok := number(v)
	if !ok {
		return Format(v)
	}
	cur = strings.ToUpper(strings.TrimSpace(cur))
	if cur == "" {
		cur = "INR"
	}
	c, known := currencies[cur]
	if !known {
		return cur + " " + formatFixed(f, 2, false)
	}
	s := formatFixed(f, c.dec, c.indian)
	if strings.HasPrefix(s, "-") {
		return "-" + c.symbol + s[1:]
	}
	return c.symbol + s
}
