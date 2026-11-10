package segment

import (
	"context"
	"testing"
)

// mapCounter is an in-memory Counter: event -> count (window is ignored; tests set counts per case).
type mapCounter map[string]int

func (m mapCounter) Count(_ context.Context, ev string, _ int) (int, error) { return m[ev], nil }

func mustParse(t *testing.T, s string) Rule {
	t.Helper()
	r, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return r
}

func TestEvalAttr(t *testing.T) {
	attrs := map[string]any{"city": "Delhi", "age": 34.0, "vip": true, "score": "12.5", "bio": "loves mutual funds", "nil": nil}
	cases := []struct {
		rule string
		want bool
	}{
		{`{"attr":"city","op":"eq","value":"Delhi"}`, true},
		{`{"attr":"city","op":"eq","value":"delhi"}`, false},
		{`{"attr":"city","op":"ne","value":"Pune"}`, true},
		{`{"attr":"missing","op":"ne","value":"x"}`, true},
		{`{"attr":"missing","op":"eq","value":""}`, true},
		{`{"attr":"age","op":"eq","value":34}`, true},
		{`{"attr":"age","op":"gte","value":34}`, true},
		{`{"attr":"age","op":"gt","value":34}`, false},
		{`{"attr":"score","op":"lt","value":13}`, true},
		{`{"attr":"city","op":"gt","value":0}`, false},
		{`{"attr":"missing","op":"lt","value":100}`, false},
		{`{"attr":"vip","op":"eq","value":true}`, true},
		{`{"attr":"vip","op":"eq","value":"true"}`, true},
		{`{"attr":"bio","op":"contains","value":"mutual"}`, true},
		{`{"attr":"city","op":"in","value":["Pune","Delhi"]}`, true},
		{`{"attr":"age","op":"in","value":[33,34]}`, true},
		{`{"attr":"city","op":"exists"}`, true},
		{`{"attr":"nil","op":"exists"}`, true},
		{`{"attr":"missing","op":"exists"}`, false},
	}
	for _, tc := range cases {
		got, err := Eval(context.Background(), mustParse(t, tc.rule), attrs, mapCounter{})
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.rule, got, tc.want)
		}
	}
}

func TestEvalEventCounts(t *testing.T) {
	counts := mapCounter{"purchase": 3}
	cases := []struct {
		rule string
		want bool
	}{
		{`{"event":"purchase","op":"gte","count":3,"days":7}`, true},
		{`{"event":"purchase","op":"gt","count":3,"days":7}`, false},
		{`{"event":"purchase","op":"eq","count":3,"days":7}`, true},
		{`{"event":"login","op":"eq","count":0,"days":30}`, true},
		{`{"event":"login","op":"gte","count":1,"days":30}`, false},
		{`{"event":"login","op":"lt","count":2,"days":30}`, true},
	}
	for _, tc := range cases {
		got, err := Eval(context.Background(), mustParse(t, tc.rule), nil, counts)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.rule, got, tc.want)
		}
	}
}

func TestEvalGroups(t *testing.T) {
	attrs := map[string]any{"city": "Delhi", "plan": "free"}
	counts := mapCounter{"purchase": 1}
	rule := mustParse(t, `{"all":[
		{"attr":"city","op":"eq","value":"Delhi"},
		{"any":[{"attr":"plan","op":"eq","value":"pro"},{"event":"purchase","op":"gte","count":1,"days":7}]}
	]}`)
	if ok, _ := Eval(context.Background(), rule, attrs, counts); !ok {
		t.Error("want match via any.purchase")
	}
	if ok, _ := Eval(context.Background(), rule, attrs, mapCounter{}); ok {
		t.Error("want no match without purchase")
	}
}

type lookupCounter struct {
	mapCounter
	in map[int64]bool
}

func (l lookupCounter) InSegment(_ context.Context, id int64) (bool, error) { return l.in[id], nil }

func TestEvalTagsSegmentsAndFilters(t *testing.T) {
	attrs := map[string]any{"tags": []any{"vip", "beta"}}
	where := []PropFilter{{Prop: "sku", Op: "eq", Value: "A1"}}
	c := lookupCounter{mapCounter{CounterName("purchase", where): 2, "purchase": 5}, map[int64]bool{7: true}}
	cases := []struct {
		rule string
		want bool
	}{
		{`{"tag":"vip","op":"has"}`, true},
		{`{"tag":"gold","op":"has"}`, false},
		{`{"tag":"gold","op":"not_has"}`, true},
		{`{"segment":7,"op":"in"}`, true},
		{`{"segment":8,"op":"in"}`, false},
		{`{"segment":8,"op":"not_in"}`, true},
		{`{"event":"purchase","op":"eq","count":2,"days":7,"where":[{"prop":"sku","op":"eq","value":"A1"}]}`, true},
		{`{"event":"purchase","op":"eq","count":5,"days":7}`, true},
	}
	for _, tc := range cases {
		got, err := Eval(context.Background(), mustParse(t, tc.rule), attrs, c)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.rule, got, tc.want)
		}
	}
	if _, err := Eval(context.Background(), mustParse(t, `{"segment":7,"op":"in"}`), attrs, mapCounter{}); err == nil {
		t.Error("segment rule without a lookup should fail")
	}
	// Tags read from ClickHouse arrive as JSON text.
	if !HasTag(map[string]any{"tags": `["vip"]`}, "vip") {
		t.Error("HasTag should parse JSON text")
	}
}

func TestMatchProps(t *testing.T) {
	props := map[string]any{"sku": "A1", "amount": 499.0, "gift": true}
	cases := []struct {
		where []PropFilter
		want  bool
	}{
		{[]PropFilter{{Prop: "sku", Op: "eq", Value: "A1"}}, true},
		{[]PropFilter{{Prop: "amount", Op: "gte", Value: 100.0}, {Prop: "gift", Op: "eq", Value: true}}, true},
		{[]PropFilter{{Prop: "amount", Op: "gte", Value: 500.0}}, false},
		{[]PropFilter{{Prop: "coupon", Op: "exists"}}, false},
		{nil, true},
	}
	for i, tc := range cases {
		if got := MatchProps(tc.where, props); got != tc.want {
			t.Errorf("case %d = %v, want %v", i, got, tc.want)
		}
	}
	a := CounterName("purchase", []PropFilter{{Prop: "sku", Op: "eq", Value: "A1"}})
	b := CounterName("purchase", []PropFilter{{Prop: "sku", Op: "eq", Value: "A2"}})
	if a == b || a == "purchase" || CounterName("purchase", nil) != "purchase" {
		t.Errorf("counter names: %q %q", a, b)
	}
}
