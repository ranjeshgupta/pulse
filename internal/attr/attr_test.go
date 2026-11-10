package attr

import (
	"encoding/json"
	"testing"
)

func TestString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"Delhi", "Delhi"},
		{true, "true"},
		{false, "false"},
		{34.0, "34"},
		{12.5, "12.5"},
		{1e21, "1000000000000000000000"},
		{int64(-3), "-3"},
		{json.Number("7"), "7"},
		{[]any{"a", 1.0}, `["a",1]`},
		{map[string]any{"k": "v"}, `{"k":"v"}`},
	}
	for _, tc := range cases {
		if got := String(tc.in); got != tc.want {
			t.Errorf("String(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNumber(t *testing.T) {
	for in, want := range map[string]bool{"12": true, "-1.5": true, "1e3": true, "": false, "abc": false, "inf": false, "NaN": false} {
		if _, ok := Number(in); ok != want {
			t.Errorf("Number(%q) ok = %v, want %v", in, ok, want)
		}
	}
}

func TestValidKey(t *testing.T) {
	for k, want := range map[string]bool{"city": true, "_x1": true, "first_name": true, "1a": false, "a-b": false, "a.b": false, "": false} {
		if got := ValidKey(k); got != want {
			t.Errorf("ValidKey(%q) = %v, want %v", k, got, want)
		}
	}
}
