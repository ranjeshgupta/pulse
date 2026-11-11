package segment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"pulse/internal/attr"
)

// Counter returns how many times the contact did an event in a window of UTC days. A counter
// that can't see that far back (see Window.InCounters) returns ErrNeedsSweep.
type Counter interface {
	Count(ctx context.Context, event string, w Window) (int, error)
}

// ErrNeedsSweep is returned for rules that read more history than the per-contact counters keep;
// the batch path (backfill and sweep) evaluates them instead.
var ErrNeedsSweep = errors.New("this rule counts events from more than 365 days ago, which only the periodic sweep can check")

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
	case r.None != nil:
		for _, child := range r.None {
			ok, err := Eval(ctx, child, attrs, c)
			if err != nil || ok {
				return false, err
			}
		}
		return true, nil
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
		n, err := c.Count(ctx, CounterName(r.Event, r.Where), r.Window())
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
		if !matchProp(f, props) {
			return false
		}
	}
	return true
}

// matchProp reports whether one filter passes: for a path through a list, whether any item does.
func matchProp(f PropFilter, props map[string]any) bool {
	p, ok := ParsePath(f.Prop)
	if !ok {
		return false
	}
	for _, c := range p.values(props) {
		if compare(f.Op, c.v, c.present, f.Value) {
			return true
		}
	}
	return false
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

// ListValues reads a list value (a contact's list field, or a list property) the way ClickHouse's
// JSONExtract(x, 'Array(String)') does: a JSON array, or a string holding one; scalar items
// become their canonical strings. Anything else is an empty list.
func ListValues(v any) []string {
	var list []any
	switch x := v.(type) {
	case []any:
		list = x
	case []string:
		return x
	case string:
		if json.Unmarshal([]byte(x), &list) != nil {
			return nil
		}
	}
	out := make([]string, 0, len(list))
	for _, it := range list {
		switch it.(type) {
		case string, float64, bool:
			out = append(out, attr.String(it))
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
	case "has", "not_has":
		found := false
		for _, x := range ListValues(raw) {
			if x == value.(string) {
				found = true
				break
			}
		}
		return found == (op == "has")
	case "between":
		list := value.([]any)
		if from, ok := list[0].(string); ok {
			d, ok := DatePart(s)
			return ok && d >= from && d <= list[1].(string)
		}
		f, ok := attr.Number(s)
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
