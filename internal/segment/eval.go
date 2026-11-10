package segment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
	case "eq":
		return s == attr.String(value)
	case "ne":
		return s != attr.String(value)
	case "contains":
		return strings.Contains(s, attr.String(value))
	case "in":
		for _, v := range value.([]any) {
			if s == attr.String(v) {
				return true
			}
		}
		return false
	default:
		f, ok := attr.Number(s)
		return ok && cmpFloat(op, f, value.(float64))
	}
}
