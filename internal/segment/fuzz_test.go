package segment

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// FuzzParse feeds arbitrary JSON to Parse. Nothing may panic; a rule Parse accepts must compile to
// SQL with one argument per placeholder, evaluate, explain and survive a JSON round trip.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		`{"attr":"city","op":"eq","value":"Delhi"}`,
		`{"attr":"age","op":"between","value":[1,5]}`,
		`{"attr":"signup","op":"between","value":["2026-01-01","2026-02-01"]}`,
		`{"attr":"signup","op":"within_days","value":7}`,
		`{"attr":"c","op":"in","value":["a",1,true]}`,
		`{"event":"purchase","op":"gte","count":3,"days":7}`,
		`{"event":"e","op":"eq","count":0,"from":"2025-01-01","to":"2026-01-01"}`,
		`{"event":"e","op":"gte","count":1,"days":3,"where":[{"prop":"items[].sku","op":"in","value":["a","b"]},{"prop":"amt","op":"gt","value":5}]}`,
		`{"all":[{"segment":9,"op":"not_in"},{"tag":"vip","op":"has"}]}`,
		`{"none":[{"any":[{"attr":"a","op":"exists"},{"attr":"b","op":"starts_with","value":"x"}]}]}`,
		`{"all":[{"all":[{"all":[{"all":[{"all":[{"all":[{"all":[{"all":[{"attr":"a","op":"exists"}]}]}]}]}]}]}]}]}`,
		`{"attr":"x'--","op":"exists"}`,
		`{}`, `[]`, `null`, ``,
	} {
		f.Add([]byte(s))
	}
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := Parse(b)
		if err != nil {
			return
		}
		q, err := ToSQL(r, 7, asOf)
		if err != nil {
			t.Fatalf("Parse accepted %s but ToSQL failed: %v", b, err)
		}
		if n := strings.Count(q.SQL, "?"); n != len(q.Args) {
			t.Fatalf("%s: %d placeholders, %d args\n%s", b, n, len(q.Args), q.SQL)
		}
		attrs := map[string]any{"city": "Delhi", "age": 34.0, "tags": []any{"vip"}, "signup": "2026-01-02"}
		c := lookupCounter{mapCounter{}, map[int64]bool{9: true}}
		_, _ = Eval(ctx, r, attrs, c)
		_, _ = Explain(ctx, r, attrs, c)
		r.Refs()
		r.SegmentRefs()
		r.Counters()
		r.Filters()
		r.HasWindow()
		r.SweepOnly(asOf)
		out, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := Parse(out); err != nil {
			t.Fatalf("re-parse of %s failed: %v (from %s)", out, err, b)
		}
	})
}

// FuzzFunnel decodes arbitrary funnels: a funnel ValidateFunnel accepts must compile, with one
// argument per placeholder, and both its queries must agree on that.
func FuzzFunnel(f *testing.F) {
	f.Add(`[{"event":"view"},{"event":"cart","where":[{"prop":"items[].sku","op":"in","value":["a"]}]},{"event":"purchase","where":[{"prop":"amount","op":"gt","value":5}]}]`, 7, "plan")
	f.Add(`[{"event":"a","where":[{"prop":"d","op":"within_days","value":3}]},{"event":"b"}]`, 1, "")
	f.Fuzz(func(t *testing.T, stepsJSON string, window int, breakdown string) {
		var steps []FunnelStep
		if json.Unmarshal([]byte(stepsJSON), &steps) != nil || ValidateFunnel(steps, window) != nil {
			return
		}
		from, to := asOf.AddDate(0, 0, -30), asOf
		for _, seg := range []int64{0, 9} {
			q, err := FunnelSQL(7, steps, window, from, to, seg)
			if err != nil {
				t.Fatalf("validated funnel %s did not compile: %v", stepsJSON, err)
			}
			if n := strings.Count(q.SQL, "?"); n != len(q.Args) {
				t.Fatalf("%s: %d placeholders, %d args\n%s", stepsJSON, n, len(q.Args), q.SQL)
			}
			if q, err := FunnelBreakdownSQL(7, steps, window, from, to, seg, breakdown); err == nil {
				if n := strings.Count(q.SQL, "?"); n != len(q.Args) {
					t.Fatalf("breakdown %s: %d placeholders, %d args\n%s", stepsJSON, n, len(q.Args), q.SQL)
				}
			}
		}
	})
}
