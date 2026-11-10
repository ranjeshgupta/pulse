package segment

import (
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
	if err := r.Validate(); err != nil {
		return Query{}, err
	}
	b := &sqlBuilder{tenant: uint64(tenantID), asOf: asOf.UTC()}
	where := b.rule(r)
	return Query{
		// Contacts merged into another profile keep their row but never match anything.
		SQL:  "SELECT id FROM contacts FINAL WHERE tenant_id = ? AND NOT mapContains(attrs, 'merged_into') AND " + where + " ORDER BY id",
		Args: append([]any{uint64(tenantID)}, b.args...),
	}, nil
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
	case "eq", "ne":
		b.args = append(b.args, attr.String(value))
		return expr + " " + cmpOps[op] + " ?"
	case "contains":
		b.args = append(b.args, attr.String(value))
		return "position(" + expr + ", ?) > 0"
	case "in":
		list := value.([]any)
		marks := make([]string, len(list))
		for i, v := range list {
			marks[i] = "?"
			b.args = append(b.args, attr.String(v))
		}
		return expr + " IN (" + strings.Join(marks, ", ") + ")"
	case "before", "after":
		b.args = append(b.args, value.(string))
		return "ifNull(toDateOrNull(substring(" + expr + ", 1, 10)) " + map[string]string{"before": "<", "after": ">"}[op] + " toDate(?), 0)"
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

// propExpr reads one event property as the canonical string attr.String gives the decoded JSON
// value: strings as-is, numbers in shortest form, booleans as true/false, missing or null as "".
const propExpr = "multiIf(JSONType(props, ?) = 'String', JSONExtractString(props, ?)," +
	" JSONType(props, ?) IN ('Int64', 'UInt64', 'Double'), toString(JSONExtractFloat(props, ?))," +
	" JSONType(props, ?) = 'Null', '', JSONExtractRaw(props, ?))"

func (b *sqlBuilder) props(where []PropFilter) string {
	var sb strings.Builder
	for _, f := range where {
		k := f.Prop
		if f.Op == "exists" {
			b.args = append(b.args, k)
			sb.WriteString(" AND JSONHas(props, ?)")
			continue
		}
		b.args = append(b.args, k, k, k, k, k, k)
		sb.WriteString(" AND " + b.compare(propExpr, f.Op, f.Value, ""))
	}
	return sb.String()
}

// event compiles a count rule. The subquery only sees contacts with at least one matching event,
// so if a contact with zero events satisfies the rule ("login = 0", "purchase < 2"), the query is
// flipped: NOT IN the contacts whose count fails it.
func (b *sqlBuilder) event(r Rule) string {
	start := b.asOf.AddDate(0, 0, -(r.Days - 1)).Format(time.DateOnly)
	end := b.asOf.Format(time.DateOnly)
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
