package segment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"pulse/internal/attr"
)

// Counter returns how many times the contact did an event in the last `days` UTC calendar days.
type Counter interface {
	Count(ctx context.Context, event string, days int) (int, error)
}

// MembershipLookup answers "is this contact in segment id?". Counters that also implement it
// let rules reference other segments and lists.
type MembershipLookup interface {
	InSegment(ctx context.Context, segment int64) (bool, error)
}

var errNoLookup = errors.New("segment rules need a membership lookup")

// Eval reports whether one contact matches the rule. attrs is the contact's profile.
func Eval(ctx context.Context, r Rule, attrs map[string]any, c Counter) (bool, error) {
	switch {
	case r.All != nil:
		for _, child := range r.All {
			ok, err := Eval(ctx, child, attrs, c)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	case r.Any != nil:
		for _, child := range r.Any {
			ok, err := Eval(ctx, child, attrs, c)
			if err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	case r.Attr != "":
		raw, present := attrs[r.Attr]
		return compare(r.Op, raw, present, r.Value), nil
	case r.Tag != "":
		return HasTag(attrs, r.Tag) == (r.Op == "has"), nil
	case r.Segment != 0:
		l, ok := c.(MembershipLookup)
		if !ok {
			return false, errNoLookup
		}
		in, err := l.InSegment(ctx, r.Segment)
		if err != nil {
			return false, err
		}
		return in == (r.Op == "in"), nil
	default:
		n, err := c.Count(ctx, CounterName(r.Event, r.Where), r.Days)
		if err != nil {
			return false, err
		}
		return cmpInt(r.Op, n, *r.Count), nil
	}
}

// MatchProps reports whether an event's properties pass every filter. The profile service uses it
// to decide which filtered counters an event increments.
func MatchProps(where []PropFilter, props map[string]any) bool {
	for _, f := range where {
		raw, present := props[f.Prop]
		if !compare(f.Op, raw, present, f.Value) {
			return false
		}
	}
	return true
}

// Tags returns the contact's tags. They are stored as a JSON array under the "tags" attribute;
// in ClickHouse the same array arrives as its JSON text.
func Tags(attrs map[string]any) []string {
	var list []any
	switch v := attrs["tags"].(type) {
	case []any:
		list = v
	case []string:
		return v
	case string:
		_ = json.Unmarshal([]byte(v), &list)
	}
	out := make([]string, 0, len(list))
	for _, t := range list {
		if s, ok := t.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// HasTag reports whether the contact carries tag t.
func HasTag(attrs map[string]any, t string) bool {
	for _, x := range Tags(attrs) {
		if x == t {
			return true
		}
	}
	return false
}

// compare mirrors the SQL in sql.go: a missing key reads as "" (like a ClickHouse Map lookup),
// and a non-numeric value fails every numeric comparison (like ifNull(toFloat64OrNull(...) > x, 0)).
func compare(op string, raw any, present bool, value any) bool {
	s := attr.String(raw)
	switch op {
	case "exists":
		return present
	case "not_exists":
		return !present
	case "eq":
		return s == attr.String(value)
	case "ne":
		return s != attr.String(value)
	case "contains":
		return strings.Contains(s, attr.String(value))
	case "not_contains":
		return !strings.Contains(s, attr.String(value))
	case "starts_with":
		return strings.HasPrefix(s, attr.String(value))
	case "ends_with":
		return strings.HasSuffix(s, attr.String(value))
	case "in", "not_in":
		found := false
		for _, v := range value.([]any) {
			if s == attr.String(v) {
				found = true
				break
			}
		}
		return found == (op == "in")
	case "between":
		f, ok := attr.Number(s)
		list := value.([]any)
		return ok && f >= list[0].(float64) && f <= list[1].(float64)
	case "on", "before", "after", "within_days", "older_than_days":
		d, ok := DatePart(s)
		if !ok {
			return false
		}
		switch op {
		case "on":
			return d == value.(string)
		case "before":
			return d < value.(string)
		case "after":
			return d > value.(string)
		}
		today := Today()
		from := today.AddDate(0, 0, -int(value.(float64))).Format(time.DateOnly)
		if op == "within_days" {
			return d >= from && d <= today.Format(time.DateOnly)
		}
		return d < from
	default:
		f, ok := attr.Number(s)
		return ok && cmpFloat(op, f, value.(float64))
	}
}

// Today is the UTC date relative date rules compare with (a variable for tests).
var Today = func() time.Time { return time.Now().UTC() }

// DatePart returns the YYYY-MM-DD at the start of an attribute value (a date or an RFC 3339
// timestamp), the same way the SQL compiler reads it.
func DatePart(s string) (string, bool) {
	if len(s) < 10 {
		return "", false
	}
	d := s[:10]
	if _, err := time.Parse(time.DateOnly, d); err != nil {
		return "", false
	}
	return d, true
}
