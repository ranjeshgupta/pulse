// Package segment implements segment rules: a JSON AST compiled two ways.
//
//   - Eval runs the rule for one contact in Go. The streaming path calls it for every event.
//   - ToSQL turns the rule into a ClickHouse query over all contacts. Backfills and sweeps use it.
//
// The two compilers must agree on every input. The integration test in agreement_integration_test.go checks this.
package segment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"pulse/internal/attr"
	"pulse/internal/event"
)

const (
	maxDepth = 8
	maxNodes = 200
	maxDays  = 365
	maxIn    = 1000
)

// Rule is one node of the segment AST. Exactly one of All, Any, None, Attr, Event, Segment or Tag
// is set. None matches when none of its rules do (an exclusion group).
type Rule struct {
	All     []Rule `json:"all,omitempty"`
	Any     []Rule `json:"any,omitempty"`
	None    []Rule `json:"none,omitempty"`
	Attr    string `json:"attr,omitempty"`
	Event   string `json:"event,omitempty"`
	Segment int64  `json:"segment,omitempty"` // op in | not_in: membership of another segment or list
	Tag     string `json:"tag,omitempty"`     // op has | not_has
	Op      string `json:"op,omitempty"`
	Value   any    `json:"value,omitempty"`
	Count   *int   `json:"count,omitempty"` // event rules: compared with the number of events
	Days    int    `json:"days,omitempty"`  // event rules: today plus the previous Days-1 UTC days
	// From and To (event rules, instead of Days): an absolute range of UTC days, both included;
	// an empty To means up to today. Days after today never count.
	From  string       `json:"from,omitempty"`
	To    string       `json:"to,omitempty"`
	Where []PropFilter `json:"where,omitempty"` // event rules: only count events whose properties match all of these
}

// PropFilter compares one event property, named by a path (see ParsePath), with the same
// operators and semantics as attribute rules.
type PropFilter struct {
	Prop  string `json:"prop"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

const maxWhere = 5

// Parse decodes and validates a rule.
func Parse(b []byte) (Rule, error) {
	var r Rule
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Rule{}, fmt.Errorf("decode rule: %w", err)
	}
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	return r, nil
}

// Validate checks the shape of the whole tree.
func (r Rule) Validate() error {
	n := 0
	return r.validate(1, &n)
}

func (r Rule) validate(depth int, n *int) error {
	*n++
	if depth > maxDepth {
		return fmt.Errorf("rule is nested deeper than %d levels", maxDepth)
	}
	if *n > maxNodes {
		return fmt.Errorf("rule has more than %d nodes", maxNodes)
	}
	kinds := 0
	for _, set := range []bool{r.All != nil, r.Any != nil, r.None != nil, r.Attr != "", r.Event != "", r.Segment != 0, r.Tag != ""} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		return errors.New("each rule needs exactly one of all, any, none, attr, event, segment or tag")
	}
	if r.Where != nil && r.Event == "" {
		return errors.New("where applies to event rules only")
	}
	if (r.From != "" || r.To != "") && r.Event == "" {
		return errors.New("from and to apply to event rules only")
	}

	switch {
	case r.All != nil || r.Any != nil || r.None != nil:
		children := r.group()
		if len(children) == 0 {
			return errors.New("all/any/none needs at least one rule")
		}
		if r.Op != "" || r.Value != nil || r.Count != nil || r.Days != 0 {
			return errors.New("all/any/none cannot have op, value, count or days")
		}
		for _, c := range children {
			if err := c.validate(depth+1, n); err != nil {
				return err
			}
		}
		return nil

	case r.Segment != 0:
		if r.Segment < 0 || (r.Op != "in" && r.Op != "not_in") {
			return errors.New("segment rules need a segment id and op in or not_in")
		}
		if r.Value != nil || r.Count != nil || r.Days != 0 {
			return errors.New("segment rules take no value, count or days")
		}
		return nil

	case r.Tag != "":
		if !validTag(r.Tag) || (r.Op != "has" && r.Op != "not_has") {
			return fmt.Errorf("tag rules need a tag (letters, digits, - and _) and op has or not_has")
		}
		if r.Value != nil || r.Count != nil || r.Days != 0 {
			return errors.New("tag rules take no value, count or days")
		}
		return nil

	case r.Attr != "":
		if !attr.ValidKey(r.Attr) {
			return fmt.Errorf("invalid attribute name %q", r.Attr)
		}
		if r.Count != nil || r.Days != 0 {
			return fmt.Errorf("attr %q: count and days apply to event rules only", r.Attr)
		}
		return validCompare(fmt.Sprintf("attr %q", r.Attr), r.Op, r.Value)
	}
	return r.validateEvent()
}

func (r Rule) validateEvent() error {
	if !event.ValidName(r.Event) {
		return fmt.Errorf("invalid event name %q", r.Event)
	}
	if _, ok := cmpOps[r.Op]; !ok {
		return fmt.Errorf("event %q: unknown op %q", r.Event, r.Op)
	}
	if r.Count == nil || *r.Count < 0 {
		return fmt.Errorf("event %q: count must be 0 or more", r.Event)
	}
	if err := r.validateWindow(); err != nil {
		return fmt.Errorf("event %q: %w", r.Event, err)
	}
	if r.Value != nil {
		return fmt.Errorf("event %q: use count, not value", r.Event)
	}
	if len(r.Where) > maxWhere {
		return fmt.Errorf("event %q: at most %d property filters", r.Event, maxWhere)
	}
	for _, f := range r.Where {
		if !ValidPath(f.Prop) {
			return fmt.Errorf("event %q: invalid property %q (a name, a dotted path like product.code, or items[].code for any item)", r.Event, f.Prop)
		}
		if err := validCompare(fmt.Sprintf("event %q property %q", r.Event, f.Prop), f.Op, f.Value); err != nil {
			return err
		}
		if RelativeDateOp(f.Op) {
			return fmt.Errorf("event %q property %q: use before or after for event properties (the event's own window covers recency)", r.Event, f.Prop)
		}
	}
	return nil
}

// minDate is the earliest day a range may start: older history isn't kept anywhere.
const minDate = "2000-01-01"

// validateWindow checks an event rule's window: Days, or an absolute From (and optional To).
func (r Rule) validateWindow() error {
	if r.From == "" {
		if r.To != "" {
			return errors.New("to needs from")
		}
		if r.Days < 1 || r.Days > maxDays {
			return fmt.Errorf("days must be between 1 and %d", maxDays)
		}
		return nil
	}
	if r.Days != 0 {
		return errors.New("use days or a from-to range, not both")
	}
	if _, err := time.Parse(time.DateOnly, r.From); err != nil || r.From < minDate {
		return fmt.Errorf("from needs a date like 2026-01-31, from %s on", minDate)
	}
	if r.To != "" {
		if _, err := time.Parse(time.DateOnly, r.To); err != nil {
			return errors.New("to needs a date like 2026-03-31")
		}
		if r.To < r.From {
			return errors.New("the range ends before it starts")
		}
	}
	return nil
}

// Window is the span of days an event rule counts.
type Window struct {
	Days     int
	From, To string
}

// Window returns the rule's window.
func (r Rule) Window() Window { return Window{Days: r.Days, From: r.From, To: r.To} }

// Bounds returns the first and last UTC day the window counts on asOf's day. A range that ends
// after asOf stops at asOf; one that starts after it is empty (start after end).
func (w Window) Bounds(asOf time.Time) (start, end time.Time) {
	end = asOf.UTC().Truncate(24 * time.Hour)
	if w.From == "" {
		return end.AddDate(0, 0, -(w.Days - 1)), end
	}
	start, _ = time.Parse(time.DateOnly, w.From)
	if w.To != "" {
		if to, err := time.Parse(time.DateOnly, w.To); err == nil && to.Before(end) {
			end = to
		}
	}
	return start, end
}

// CounterDays is how many days of history the per-contact counters keep (today included).
const CounterDays = maxDays

// InCounters reports whether the counters still hold every day of the window on asOf's day.
func (w Window) InCounters(asOf time.Time) bool {
	start, _ := w.Bounds(asOf)
	return !start.Before(asOf.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(CounterDays - 1)))
}

// SweepOnly reports whether the rule reads more history than the counters keep (a range starting
// more than 365 days before asOf), so only the batch path can evaluate it.
func (r Rule) SweepOnly(asOf time.Time) bool {
	found := false
	r.walk(func(n Rule) { found = found || n.Event != "" && !n.Window().InCounters(asOf) })
	return found
}

var tagRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func validTag(t string) bool { return tagRe.MatchString(t) }

// ValidTag reports whether t can be used as a tag.
func ValidTag(t string) bool { return validTag(t) }

// validCompare checks an attribute-style comparison (attribute rules and property filters).
func validCompare(what, op string, value any) error {
	switch op {
	case "eq", "ne":
		if !isScalar(value) {
			return fmt.Errorf("%s: %s needs a string, number or boolean value", what, op)
		}
	case "contains", "not_contains", "starts_with", "ends_with":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: %s needs a string value", what, op)
		}
	case "gt", "gte", "lt", "lte":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s: %s needs a number value", what, op)
		}
	case "has", "not_has":
		if s, ok := value.(string); !ok || s == "" {
			return fmt.Errorf("%s: %s needs a value to look for in the list", what, op)
		}
	case "in", "not_in":
		list, ok := value.([]any)
		if !ok || len(list) == 0 || len(list) > maxIn {
			return fmt.Errorf("%s: %s needs a list of 1-%d values", what, op, maxIn)
		}
		for _, v := range list {
			if !isScalar(v) {
				return fmt.Errorf("%s: in values must be strings, numbers or booleans", what)
			}
		}
	case "exists", "not_exists":
		if value != nil {
			return fmt.Errorf("%s: %s takes no value", what, op)
		}
	case "between":
		list, ok := value.([]any)
		if !ok || len(list) != 2 {
			return fmt.Errorf("%s: between needs two numbers or two dates [low, high]", what)
		}
		if from, ok := list[0].(string); ok {
			to, _ := list[1].(string)
			_, err1 := time.Parse(time.DateOnly, from)
			_, err2 := time.Parse(time.DateOnly, to)
			if err1 != nil || err2 != nil || from > to {
				return fmt.Errorf("%s: between needs two dates like 2026-01-01, the earlier first", what)
			}
			return nil
		}
		lo, ok1 := list[0].(float64)
		hi, ok2 := list[1].(float64)
		if !ok1 || !ok2 || lo > hi {
			return fmt.Errorf("%s: between needs two numbers [low, high], low first", what)
		}
	case "on", "before", "after":
		s, _ := value.(string)
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return fmt.Errorf("%s: %s needs a date like 2026-11-30", what, op)
		}
	case "within_days", "older_than_days":
		n, ok := value.(float64)
		if !ok || n < 1 || n > maxDays*10 || n != float64(int(n)) {
			return fmt.Errorf("%s: %s needs a whole number of days (1-%d)", what, op, maxDays*10)
		}
	default:
		return fmt.Errorf("%s: unknown op %q", what, op)
	}
	return nil
}

// Refs returns the event names and attribute keys the rule reads, sorted and deduplicated.
// The streaming engine indexes segments by these so each event only re-evaluates segments it can affect.
func (r Rule) Refs() (events, attrs []string) {
	ev, at := map[string]bool{}, map[string]bool{}
	r.walk(func(n Rule) {
		if n.Event != "" {
			ev[n.Event] = true
		}
		if n.Attr != "" {
			at[n.Attr] = true
		}
		if n.Tag != "" {
			at["tags"] = true
		}
	})
	return sortedKeys(ev), sortedKeys(at)
}

// SegmentRefs lists the segments the rule depends on.
func (r Rule) SegmentRefs() []int64 {
	seen := map[int64]bool{}
	var out []int64
	r.walk(func(n Rule) {
		if n.Segment != 0 && !seen[n.Segment] {
			seen[n.Segment] = true
			out = append(out, n.Segment)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Counters lists the counter names the rule's event leaves read (see CounterName), deduplicated.
func (r Rule) Counters() []string {
	seen := map[string]bool{}
	var out []string
	r.walk(func(n Rule) {
		if n.Event != "" {
			if k := CounterName(n.Event, n.Where); !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	})
	sort.Strings(out)
	return out
}

// Filter is one event rule's property filter, which needs its own counter.
type Filter struct {
	Event string
	Where []PropFilter
	Key   string // counter name, see CounterName
}

// Filters lists the rule's filtered event rules (deduplicated by counter name).
func (r Rule) Filters() []Filter {
	seen := map[string]bool{}
	var out []Filter
	r.walk(func(n Rule) {
		if n.Event != "" && len(n.Where) > 0 {
			k := CounterName(n.Event, n.Where)
			if !seen[k] {
				seen[k] = true
				out = append(out, Filter{Event: n.Event, Where: n.Where, Key: k})
			}
		}
	})
	return out
}

// CounterName names the counter an event rule reads: the event itself, or event#<hash of its
// filters> when it only counts events with matching properties.
func CounterName(ev string, where []PropFilter) string {
	if len(where) == 0 {
		return ev
	}
	b, _ := json.Marshal(where)
	sum := sha256.Sum256(b)
	return ev + "#" + hex.EncodeToString(sum[:6])
}

// HasWindow reports whether membership can change just because time passes (an event rule, or a
// date compared with today). Such segments are re-checked by the periodic sweep.
func (r Rule) HasWindow() bool {
	found := false
	r.walk(func(n Rule) { found = found || n.Event != "" || RelativeDateOp(n.Op) })
	return found
}

// RelativeDateOp reports whether op compares a date with today.
func RelativeDateOp(op string) bool { return op == "within_days" || op == "older_than_days" }

func (r Rule) walk(fn func(Rule)) {
	fn(r)
	for _, c := range r.group() {
		c.walk(fn)
	}
}

// group returns a group's rules (nil for a leaf).
func (r Rule) group() []Rule {
	switch {
	case r.All != nil:
		return r.All
	case r.Any != nil:
		return r.Any
	}
	return r.None
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, float64, bool:
		return true
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cmpOps maps comparison ops to SQL operators.
var cmpOps = map[string]string{"eq": "=", "ne": "!=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}

func cmpInt(op string, a, b int) bool {
	switch op {
	case "eq":
		return a == b
	case "ne":
		return a != b
	case "gt":
		return a > b
	case "gte":
		return a >= b
	case "lt":
		return a < b
	case "lte":
		return a <= b
	}
	return false
}

func cmpFloat(op string, a, b float64) bool {
	switch op {
	case "gt":
		return a > b
	case "gte":
		return a >= b
	case "lt":
		return a < b
	case "lte":
		return a <= b
	}
	return false
}
