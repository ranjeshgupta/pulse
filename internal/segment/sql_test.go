package segment

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var asOf = time.Date(2026, 11, 10, 15, 4, 0, 0, time.UTC)

func TestToSQLAttr(t *testing.T) {
	q, err := ToSQL(mustParse(t, `{"all":[
		{"attr":"city","op":"in","value":["Pune","Delhi"]},
		{"attr":"age","op":"gte","value":18}
	]}`), 7, asOf)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL := "SELECT id FROM contacts FINAL WHERE tenant_id = ? AND NOT mapContains(attrs, 'merged_into') AND " +
		"(attrs[?] IN (?, ?) AND ifNull(toFloat64OrNull(attrs[?]) >= ?, 0)) ORDER BY id"
	if q.SQL != wantSQL {
		t.Errorf("SQL =\n%s\nwant\n%s", q.SQL, wantSQL)
	}
	wantArgs := []any{uint64(7), "city", "Pune", "Delhi", "age", 18.0}
	if !reflect.DeepEqual(q.Args, wantArgs) {
		t.Errorf("args = %#v, want %#v", q.Args, wantArgs)
	}
}

func TestToSQLEventWindow(t *testing.T) {
	q, err := ToSQL(mustParse(t, `{"event":"purchase","op":"gte","count":3,"days":7}`), 7, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, "id IN (SELECT contact_id FROM events") || !strings.Contains(q.SQL, "HAVING uniqExact(id) >= ?") {
		t.Errorf("unexpected SQL: %s", q.SQL)
	}
	// 7 days = Nov 10 plus the 6 days before it.
	wantArgs := []any{uint64(7), uint64(7), "purchase", "2026-11-04", "2026-11-10", 3}
	if !reflect.DeepEqual(q.Args, wantArgs) {
		t.Errorf("args = %#v, want %#v", q.Args, wantArgs)
	}
}

// A rule a zero-event contact satisfies must use NOT IN, or contacts with no events would be missed.
func TestToSQLZeroMatchingRulesFlip(t *testing.T) {
	cases := map[string]bool{
		`{"event":"login","op":"eq","count":0,"days":30}`:  true,
		`{"event":"login","op":"lt","count":2,"days":30}`:  true,
		`{"event":"login","op":"lte","count":0,"days":30}`: true,
		`{"event":"login","op":"ne","count":5,"days":30}`:  true,
		`{"event":"login","op":"gte","count":0,"days":30}`: true,
		`{"event":"login","op":"gte","count":1,"days":30}`: false,
		`{"event":"login","op":"eq","count":2,"days":30}`:  false,
		`{"event":"login","op":"gt","count":0,"days":30}`:  false,
	}
	for rule, wantNotIn := range cases {
		q, err := ToSQL(mustParse(t, rule), 1, asOf)
		if err != nil {
			t.Fatal(err)
		}
		gotNotIn := strings.Contains(q.SQL, "id NOT IN") && strings.Contains(q.SQL, "HAVING NOT (")
		if gotNotIn != wantNotIn {
			t.Errorf("%s: NOT IN = %v, want %v\n%s", rule, gotNotIn, wantNotIn, q.SQL)
		}
	}
}

func TestToSQLPlaceholdersMatchArgs(t *testing.T) {
	q, err := ToSQL(mustParse(t, `{"any":[
		{"attr":"a","op":"exists"},
		{"attr":"b","op":"contains","value":"x"},
		{"attr":"c","op":"ne","value":true},
		{"event":"e","op":"eq","count":0,"days":1},
		{"event":"e","op":"gte","count":1,"days":3,"where":[{"prop":"sku","op":"in","value":["a","b"]},{"prop":"amt","op":"gt","value":5},{"prop":"x","op":"exists"}]},
		{"tag":"vip","op":"not_has"},
		{"segment":12,"op":"in"}
	]}`), 3, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(q.SQL, "?"); n != len(q.Args) {
		t.Errorf("%d placeholders, %d args", n, len(q.Args))
	}
}

func TestToSQLRejectsInvalid(t *testing.T) {
	if _, err := ToSQL(Rule{Attr: "x'--", Op: "exists"}, 1, asOf); err == nil {
		t.Fatal("want validation error")
	}
}

func TestToSQLSegmentAndTag(t *testing.T) {
	q, err := ToSQL(mustParse(t, `{"all":[{"segment":9,"op":"not_in"},{"tag":"vip","op":"has"}]}`), 4, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, "id NOT IN (SELECT contact_id FROM segment_members FINAL WHERE tenant_id = ? AND segment_id = ? AND member = 1)") ||
		!strings.Contains(q.SQL, "has(JSONExtract(attrs[?], 'Array(String)'), ?)") {
		t.Errorf("unexpected SQL: %s", q.SQL)
	}
	wantArgs := []any{uint64(4), uint64(4), uint64(9), "tags", "vip"}
	if !reflect.DeepEqual(q.Args, wantArgs) {
		t.Errorf("args = %#v, want %#v", q.Args, wantArgs)
	}
}
