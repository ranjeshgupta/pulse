package segment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func decodeProps(t *testing.T, js string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParsePath(t *testing.T) {
	for p, ok := range map[string]bool{
		"amount": true, "product.code": true, "items[].code": true, "items[]": true, "a.b.items[].c": true,
		"a..b": false, "items[].tags[]": false, "a.b.c.d.e": false, "1a": false, "": false, "items[]x": false,
	} {
		if ValidPath(p) != ok {
			t.Errorf("ValidPath(%q) = %v", p, !ok)
		}
	}
}

func TestMatchPropsPaths(t *testing.T) {
	props := decodeProps(t, `{"product":{"code":"A1","price":10},"items":[{"code":"B2","qty":1},{"code":"C3","qty":5}],"tags":["x","y"],"empty":[]}`)
	for _, c := range []struct {
		f    PropFilter
		want bool
	}{
		{PropFilter{Prop: "product.code", Op: "eq", Value: "A1"}, true},
		{PropFilter{Prop: "product.price", Op: "gt", Value: 5.0}, true},
		{PropFilter{Prop: "product.missing", Op: "not_exists"}, true},
		{PropFilter{Prop: "items[].code", Op: "eq", Value: "C3"}, true},
		{PropFilter{Prop: "items[].code", Op: "eq", Value: "Z9"}, false},
		{PropFilter{Prop: "items[].qty", Op: "gte", Value: 5.0}, true},
		{PropFilter{Prop: "items[].code", Op: "ne", Value: "B2"}, true}, // any item: C3
		{PropFilter{Prop: "tags[]", Op: "eq", Value: "y"}, true},
		{PropFilter{Prop: "tags", Op: "has", Value: "x"}, true},
		{PropFilter{Prop: "empty[].code", Op: "not_exists"}, true},
		{PropFilter{Prop: "empty[].code", Op: "exists"}, false},
		{PropFilter{Prop: "nope[].code", Op: "eq", Value: ""}, true},
	} {
		if got := MatchProps([]PropFilter{c.f}, props); got != c.want {
			t.Errorf("%+v = %v, want %v", c.f, got, c.want)
		}
	}
}

func TestEvalNoneListAndDateBetween(t *testing.T) {
	attrs := map[string]any{"products": []any{"A1", "B2"}, "joined": "2026-03-05T10:00:00Z", "city": "Pune"}
	for _, c := range []struct {
		rule string
		want bool
	}{
		{`{"attr":"products","op":"has","value":"A1"}`, true},
		{`{"attr":"products","op":"not_has","value":"A1"}`, false},
		{`{"attr":"missing","op":"not_has","value":"A1"}`, true},
		{`{"attr":"joined","op":"between","value":["2026-03-01","2026-03-31"]}`, true},
		{`{"attr":"joined","op":"between","value":["2026-03-06","2026-03-31"]}`, false},
		{`{"none":[{"attr":"city","op":"eq","value":"Delhi"},{"attr":"products","op":"has","value":"Z"}]}`, true},
		{`{"none":[{"attr":"city","op":"eq","value":"Pune"}]}`, false},
	} {
		got, err := Eval(context.Background(), mustParse(t, c.rule), attrs, mapCounter{})
		if err != nil || got != c.want {
			t.Errorf("%s = %v (%v), want %v", c.rule, got, err, c.want)
		}
	}
	for _, bad := range []string{
		`{"attr":"joined","op":"between","value":["2026-03-31","2026-03-01"]}`,
		`{"attr":"products","op":"has","value":""}`,
		`{"none":[]}`,
		`{"event":"buy","op":"gte","count":1,"days":7,"from":"2026-01-01"}`,
		`{"event":"buy","op":"gte","count":1,"from":"2026-02-01","to":"2026-01-01"}`,
		`{"event":"buy","op":"gte","count":1,"to":"2026-01-01","days":3}`,
		`{"event":"buy","op":"gte","count":1,"from":"1999-12-31"}`,
		`{"attr":"x","op":"eq","value":"1","from":"2026-01-01"}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}

func TestWindowBounds(t *testing.T) {
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
	day := func(s string) string { return s }
	for _, c := range []struct {
		w          Window
		start, end string
		counters   bool
	}{
		{Window{Days: 7}, "2026-11-04", "2026-11-10", true},
		{Window{From: "2026-10-01", To: "2026-10-31"}, "2026-10-01", "2026-10-31", true},
		{Window{From: "2026-11-01", To: "2026-12-31"}, "2026-11-01", "2026-11-10", true},
		{Window{From: "2026-11-01"}, "2026-11-01", "2026-11-10", true},
		{Window{From: "2025-11-11"}, "2025-11-11", "2026-11-10", true},
		{Window{From: "2025-11-10", To: "2025-12-31"}, "2025-11-10", "2025-12-31", false},
	} {
		s, e := c.w.Bounds(now)
		if s.Format(time.DateOnly) != day(c.start) || e.Format(time.DateOnly) != c.end || c.w.InCounters(now) != c.counters {
			t.Errorf("%+v: %s..%s counters=%v", c.w, s.Format(time.DateOnly), e.Format(time.DateOnly), c.w.InCounters(now))
		}
	}
	buckets := map[string]string{"20261001": "2", "20261031": "1", "20261101": "4", "20261110": "1"}
	if n, err := (Window{From: "2026-10-01", To: "2026-10-31"}).Sum(buckets, now); n != 3 || err != nil {
		t.Errorf("sum %d %v", n, err)
	}
	if _, err := (Window{From: "2025-01-01"}).Sum(buckets, now); !errors.Is(err, ErrNeedsSweep) {
		t.Errorf("old range: %v", err)
	}
	old := mustParse(t, `{"all":[{"attr":"a","op":"exists"},{"event":"buy","op":"gte","count":1,"from":"2024-01-01","to":"2024-12-31"}]}`)
	if !old.SweepOnly(now) || mustParse(t, `{"event":"buy","op":"gte","count":1,"days":365}`).SweepOnly(now) {
		t.Error("SweepOnly")
	}
}

func TestToSQLNewConstructs(t *testing.T) {
	q, err := ToSQL(mustParse(t, `{"all":[
		{"none":[{"attr":"city","op":"eq","value":"Delhi"}]},
		{"attr":"products","op":"has","value":"A1"},
		{"event":"purchase","op":"gte","count":1,"from":"2026-01-01","to":"2026-03-31","where":[{"prop":"items[].code","op":"eq","value":"B2"}]}
	]}`), 7, asOf)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NOT (attrs[?] = ?)", "has(JSONExtract(attrs[?], 'Array(String)'), ?)", "if(empty(JSONExtractArrayRaw(props, ?)), 0, arrayExists(x -> ", "toDate(ts, 'UTC') BETWEEN toDate(?) AND toDate(?)"} {
		if !strings.Contains(q.SQL, want) {
			t.Errorf("missing %q in %s", want, q.SQL)
		}
	}
	if strings.Count(q.SQL, "?") != len(q.Args) {
		t.Errorf("%d placeholders, %d args", strings.Count(q.SQL, "?"), len(q.Args))
	}
	args := strings.Trim(strings.ReplaceAll(strings.Join(func() []string {
		var s []string
		for _, a := range q.Args {
			if v, ok := a.(string); ok {
				s = append(s, v)
			}
		}
		return s
	}(), ","), " ", ""), ",")
	if !strings.Contains(args, "purchase,2026-01-01,2026-03-31,items,code") || !strings.HasSuffix(args, "B2,items") {
		t.Errorf("args order: %s", args)
	}
}

func TestExplain(t *testing.T) {
	r := mustParse(t, `{"all":[{"attr":"city","op":"eq","value":"Pune"},{"any":[{"event":"buy","op":"gte","count":2,"days":7},{"tag":"vip","op":"has"}]},{"none":[{"attr":"plan","op":"eq","value":"free"}]}]}`)
	e, err := Explain(context.Background(), r, map[string]any{"city": "Pune", "plan": "pro"}, mapCounter{"buy": 3})
	if err != nil {
		t.Fatal(err)
	}
	if !e.Match || e.Kind != "all" || len(e.Children) != 3 || *e.Children[0].Value != "Pune" ||
		*e.Children[1].Children[0].Count != 3 || e.Children[1].Children[1].Match || e.Children[2].Kind != "none" || !e.Children[2].Match {
		b, _ := json.Marshal(e)
		t.Fatalf("explain %s", b)
	}
}

func TestRefreshNext(t *testing.T) {
	r := Refresh{Mode: "daily", At: "06:30", TZ: "Asia/Kolkata"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) // 05:30 in Kolkata
	if got := r.Next(now); !got.Equal(time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("next %v", got)
	}
	if got := r.Next(time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("next after the time %v", got)
	}
	for _, bad := range []Refresh{{Mode: "hourly"}, {Mode: "daily", At: "25:00"}, {Mode: "daily", At: "6:30"}, {Mode: "daily", At: "06:30", TZ: "Mars/Base"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	live := Refresh{}
	if live.Validate() != nil || live.Mode != "live" || live.Daily() {
		t.Error("live default")
	}
}
