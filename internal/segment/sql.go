package segment

import (
	"fmt"
	"strings"
	"time"

	"pulse/internal/attr"
)

// Query is ClickHouse SQL with positional (?) arguments. Every value, including attribute keys
// and event names, is bound as an argument, so no rule can inject SQL.
type Query struct {
	SQL  string
	Args []any
}

// ToSQL compiles the rule into a query returning the ids of every matching contact of the tenant.
// asOf fixes "today" for event windows, so results are reproducible and match Eval with the same clock.
func ToSQL(r Rule, tenantID int64, asOf time.Time) (Query, error) {
	where, args, err := WhereSQL(r, tenantID, asOf)
	if err != nil {
		return Query{}, err
	}
	return Query{
		// Contacts merged into another profile keep their row but never match anything.
		SQL:  "SELECT id FROM contacts FINAL WHERE tenant_id = ? AND NOT mapContains(attrs, 'merged_into') AND " + where + " ORDER BY id",
		Args: append([]any{uint64(tenantID)}, args...),
	}, nil
}

// WhereSQL compiles the rule into the condition ToSQL uses, over one row of the contacts table,
// with its arguments. Other queries over contacts (the Contacts list's filtered views) reuse it, so
// they match exactly what a segment with this rule would.
func WhereSQL(r Rule, tenantID int64, asOf time.Time) (string, []any, error) {
	if err := r.Validate(); err != nil {
		return "", nil, err
	}
	b := &sqlBuilder{tenant: uint64(tenantID), asOf: asOf.UTC()}
	where := b.rule(r)
	return where, b.args, nil
}

type sqlBuilder struct {
	tenant uint64
	asOf   time.Time
	args   []any
}

func (b *sqlBuilder) rule(r Rule) string {
	switch {
	case r.All != nil:
		return b.group(r.All, " AND ")
	case r.Any != nil:
		return b.group(r.Any, " OR ")
	case r.None != nil:
		// Every leaf compiles to a non-NULL condition (ifNull, IN, Map and JSON lookups), so NOT
		// is exact.
		return "NOT " + b.group(r.None, " OR ")
	case r.Attr != "":
		b.args = append(b.args, r.Attr)
		return b.compare("attrs[?]", r.Op, r.Value, "mapContains(attrs, ?)")
	case r.Tag != "":
		b.args = append(b.args, "tags", r.Tag)
		has := "has(JSONExtract(attrs[?], 'Array(String)'), ?)"
		if r.Op == "not_has" {
			return "NOT " + has
		}
		return has
	case r.Segment != 0:
		b.args = append(b.args, b.tenant, uint64(r.Segment))
		in := "id IN"
		if r.Op == "not_in" {
			in = "id NOT IN"
		}
		return in + " (SELECT contact_id FROM segment_members FINAL WHERE tenant_id = ? AND segment_id = ? AND member = 1)"
	default:
		return b.event(r)
	}
}

func (b *sqlBuilder) group(children []Rule, sep string) string {
	parts := make([]string, len(children))
	for i, c := range children {
		parts[i] = b.rule(c)
	}
	return "(" + strings.Join(parts, sep) + ")"
}

// compare compiles an attribute-style comparison of expr. expr appears exactly once in the
// result, so the caller appends its arguments first; exists returns existsExpr, which must take
// the same arguments.
func (b *sqlBuilder) compare(expr string, op string, value any, existsExpr string) string {
	switch op {
	case "exists":
		return existsExpr
	case "not_exists":
		return "NOT " + existsExpr
	case "eq", "ne":
		b.args = append(b.args, attr.String(value))
		return expr + " " + cmpOps[op] + " ?"
	case "contains":
		b.args = append(b.args, attr.String(value))
		return "position(" + expr + ", ?) > 0"
	case "not_contains":
		b.args = append(b.args, attr.String(value))
		return "position(" + expr + ", ?) = 0"
	case "starts_with":
		b.args = append(b.args, attr.String(value))
		return "startsWith(" + expr + ", ?)"
	case "ends_with":
		b.args = append(b.args, attr.String(value))
		return "endsWith(" + expr + ", ?)"
	case "in", "not_in":
		list := value.([]any)
		marks := make([]string, len(list))
		for i, v := range list {
			marks[i] = "?"
			b.args = append(b.args, attr.String(v))
		}
		in := " IN ("
		if op == "not_in" {
			in = " NOT IN ("
		}
		return expr + in + strings.Join(marks, ", ") + ")"
	case "has", "not_has":
		b.args = append(b.args, attr.String(value))
		not := ""
		if op == "not_has" {
			not = "NOT "
		}
		return not + "has(JSONExtract(" + expr + ", 'Array(String)'), ?)"
	case "between":
		list := value.([]any)
		if from, ok := list[0].(string); ok {
			b.args = append(b.args, from, list[1].(string))
			return "ifNull(toDateOrNull(substring(" + expr + ", 1, 10)) BETWEEN toDate(?) AND toDate(?), 0)"
		}
		b.args = append(b.args, list[0].(float64), list[1].(float64))
		return "ifNull(toFloat64OrNull(" + expr + ") BETWEEN ? AND ?, 0)"
	case "on", "before", "after":
		b.args = append(b.args, value.(string))
		return "ifNull(toDateOrNull(substring(" + expr + ", 1, 10)) " + map[string]string{"on": "=", "before": "<", "after": ">"}[op] + " toDate(?), 0)"
	case "within_days":
		from := b.asOf.AddDate(0, 0, -int(value.(float64))).Format(time.DateOnly)
		b.args = append(b.args, from, b.asOf.Format(time.DateOnly))
		return "ifNull(toDateOrNull(substring(" + expr + ", 1, 10)) BETWEEN toDate(?) AND toDate(?), 0)"
	case "older_than_days":
		b.args = append(b.args, b.asOf.AddDate(0, 0, -int(value.(float64))).Format(time.DateOnly))
		return "ifNull(toDateOrNull(substring(" + expr + ", 1, 10)) < toDate(?), 0)"
	default: // gt gte lt lte
		b.args = append(b.args, value.(float64))
		return "ifNull(toFloat64OrNull(" + expr + ") " + cmpOps[op] + " ?, 0)"
	}
}

// valueExpr reads the value at keys inside the JSON text doc as the canonical string attr.String
// gives the decoded JSON value: strings as-is, numbers in shortest form, booleans as true/false,
// missing or null as "". It appends its arguments (the keys, six times) to args.
func valueExpr(doc string, keys []string, args *[]any) string {
	ks := ""
	for range keys {
		ks += ", ?"
	}
	for i := 0; i < 6; i++ {
		for _, k := range keys {
			*args = append(*args, k)
		}
	}
	return "multiIf(JSONType(" + doc + ks + ") = 'String', JSONExtractString(" + doc + ks + ")," +
		" JSONType(" + doc + ks + ") IN ('Int64', 'UInt64', 'Double'), toString(JSONExtractFloat(" + doc + ks + "))," +
		" JSONType(" + doc + ks + ") = 'Null', '', JSONExtractRaw(" + doc + ks + "))"
}

// hasExpr is true when keys exist in doc (null values count as there).
func hasExpr(doc string, keys []string, args *[]any) string {
	if len(keys) == 0 {
		return "1" // a list item is always there
	}
	ks := ""
	for _, k := range keys {
		ks += ", ?"
		*args = append(*args, k)
	}
	return "JSONHas(" + doc + ks + ")"
}

// propFilter compiles one property filter over an events row's props. A path through a list is
// true when any item passes; an empty or missing list is one missing value, whose result is known
// in Go (compare with present=false), so it is spliced in as a constant.
func (b *sqlBuilder) propFilter(f PropFilter) string {
	p, _ := ParsePath(f.Prop)
	doc, keys := "props", p.Keys
	if p.List {
		doc, keys = "x", p.InItems
	}
	mark := len(b.args)
	var cond string
	if f.Op == "exists" || f.Op == "not_exists" {
		cond = hasExpr(doc, keys, &b.args)
		if f.Op == "not_exists" {
			cond = "NOT " + cond
		}
	} else {
		cond = b.compare(valueExpr(doc, keys, &b.args), f.Op, f.Value, "")
	}
	if !p.List {
		return cond
	}
	// The list's keys are bound twice, before and after the item condition's own arguments.
	var listArgs []any
	listKeys := ""
	for _, k := range p.Keys {
		listKeys += ", ?"
		listArgs = append(listArgs, k)
	}
	condArgs := append([]any(nil), b.args[mark:]...)
	b.args = append(b.args[:mark], listArgs...)
	b.args = append(b.args, condArgs...)
	b.args = append(b.args, listArgs...)
	missing := "0"
	if compare(f.Op, nil, false, f.Value) {
		missing = "1"
	}
	arr := "JSONExtractArrayRaw(props" + listKeys + ")"
	return "if(empty(" + arr + "), " + missing + ", arrayExists(x -> " + cond + ", " + arr + "))"
}

func (b *sqlBuilder) props(where []PropFilter) string {
	var sb strings.Builder
	for _, f := range where {
		sb.WriteString(" AND " + b.propFilter(f))
	}
	return sb.String()
}

// event compiles a count rule. The subquery only sees contacts with at least one matching event,
// so if a contact with zero events satisfies the rule ("login = 0", "purchase < 2"), the query is
// flipped: NOT IN the contacts whose count fails it.
func (b *sqlBuilder) event(r Rule) string {
	from, to := r.Window().Bounds(b.asOf)
	start, end := from.Format(time.DateOnly), to.Format(time.DateOnly)
	matchesZero := cmpInt(r.Op, 0, *r.Count)

	having := "uniqExact(id) " + cmpOps[r.Op] + " ?"
	in := "id IN"
	if matchesZero {
		having = "NOT (" + having + ")"
		in = "id NOT IN"
	}
	b.args = append(b.args, b.tenant, r.Event, start, end)
	filters := b.props(r.Where)
	b.args = append(b.args, *r.Count)
	return in + " (SELECT contact_id FROM events WHERE tenant_id = ? AND name = ?" +
		" AND toDate(ts, 'UTC') BETWEEN toDate(?) AND toDate(?)" + filters +
		" GROUP BY contact_id HAVING " + having + ")"
}

// FilterCountsSQL returns, per contact and UTC day (YYYYMMDD), how many events pass a filter over
// the longest window a rule can use. It seeds a filtered counter from history.
func FilterCountsSQL(tenantID int64, f Filter, asOf time.Time) Query {
	b := &sqlBuilder{tenant: uint64(tenantID), asOf: asOf.UTC()}
	start := b.asOf.AddDate(0, 0, -(maxDays - 1)).Format(time.DateOnly)
	b.args = append(b.args, b.tenant, f.Event, start, b.asOf.Format(time.DateOnly))
	filters := b.props(f.Where)
	return Query{
		SQL: "SELECT contact_id, toString(toYYYYMMDD(toDate(ts, 'UTC'))) AS day, toInt64(uniqExact(id)) FROM events" +
			" WHERE tenant_id = ? AND name = ? AND toDate(ts, 'UTC') BETWEEN toDate(?) AND toDate(?)" + filters +
			" GROUP BY contact_id, day",
		Args: b.args,
	}
}

// ValidatePropFilters checks property filters the way event rules do (at most 5, valid names and
// comparisons, no relative-date ops), for analytics queries that reuse them.
func ValidatePropFilters(what string, where []PropFilter) error {
	if len(where) > maxWhere {
		return fmt.Errorf("%s: at most %d property filters", what, maxWhere)
	}
	for _, f := range where {
		if !ValidPath(f.Prop) {
			return fmt.Errorf("%s: invalid property %q", what, f.Prop)
		}
		if err := validCompare(fmt.Sprintf("%s property %q", what, f.Prop), f.Op, f.Value); err != nil {
			return err
		}
		if RelativeDateOp(f.Op) {
			return fmt.Errorf("%s property %q: use before or after for event properties", what, f.Prop)
		}
	}
	return nil
}

// ValidateAttrFilters checks attribute comparisons (as in attribute rules) used as filters.
func ValidateAttrFilters(what string, where []PropFilter) error {
	if len(where) > maxWhere {
		return fmt.Errorf("%s: at most %d attribute filters", what, maxWhere)
	}
	for _, f := range where {
		if !attr.ValidKey(f.Prop) {
			return fmt.Errorf("%s: invalid attribute name %q", what, f.Prop)
		}
		if err := validCompare(fmt.Sprintf("%s attribute %q", what, f.Prop), f.Op, f.Value); err != nil {
			return err
		}
	}
	return nil
}

// PropFilterSQL compiles validated property filters to " AND ..." conditions over an events row's
// props, with the same semantics as event-rule filters. Every value is a bound argument.
func PropFilterSQL(where []PropFilter) (string, []any) {
	b := &sqlBuilder{}
	return b.props(where), b.args
}

// AttrFilterSQL compiles validated attribute comparisons to " AND ..." conditions over a contacts
// row's attrs map, with the same semantics as attribute rules (asOf fixes "today" for date ops).
func AttrFilterSQL(where []PropFilter, asOf time.Time) (string, []any) {
	b := &sqlBuilder{asOf: asOf.UTC()}
	var sb strings.Builder
	for _, f := range where {
		b.args = append(b.args, f.Prop)
		sb.WriteString(" AND " + b.compare("attrs[?]", f.Op, f.Value, "mapContains(attrs, ?)"))
	}
	return sb.String(), b.args
}

// PropValueSQL is the canonical string value of one event property (see valueExpr), with its
// arguments: strings as-is, numbers in shortest form, missing as "". prop is a top-level name or
// a dotted path without [].
func PropValueSQL(prop string) (string, []any) {
	p, ok := ParsePath(prop)
	keys := p.Keys
	if !ok || p.List {
		keys = []string{prop}
	}
	var args []any
	return valueExpr("props", keys, &args), args
}

// ItemValueSQL is PropValueSQL for a path through a list (items[].code): an ARRAY JOIN source
// naming each item x, and the value of x to select, each with its arguments. ok is false for
// paths without [].
func ItemValueSQL(prop string) (join string, joinArgs []any, value string, valueArgs []any, ok bool) {
	p, valid := ParsePath(prop)
	if !valid || !p.List {
		return "", nil, "", nil, false
	}
	keys := ""
	for _, k := range p.Keys {
		keys += ", ?"
		joinArgs = append(joinArgs, k)
	}
	value = valueExpr("x", p.InItems, &valueArgs)
	return "JSONExtractArrayRaw(props" + keys + ")", joinArgs, value, valueArgs, true
}
