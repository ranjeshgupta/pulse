package eventschema

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/event"
)

func props(t *testing.T, js string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func suggested(t *testing.T, name string) *Schema {
	t.Helper()
	for _, s := range Suggested() {
		if s.Name == name {
			if err := s.Normalize(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return &s
		}
	}
	t.Fatalf("no suggested schema %s", name)
	return nil
}

func codes(vs []Violation) string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Code+":"+v.Prop)
	}
	return strings.Join(out, ",")
}

func TestSuggestedSchemasAreValid(t *testing.T) {
	for _, s := range Suggested() {
		if err := s.Normalize(); err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
		if s.Mode != ModeWarn {
			t.Errorf("%s starts in %s", s.Name, s.Mode)
		}
	}
}

func TestCheckDependentEnum(t *testing.T) {
	s := suggested(t, "signup")
	for _, c := range []struct{ props, want string }{
		{`{"method":"social","social_provider":"google"}`, ""},
		{`{"method":"otp"}`, ""},
		{`{"method":"social"}`, "missing:social_provider"},
		{`{"method":"social","social_provider":"myspace"}`, "enum:social_provider"},
		{`{"method":"password","social_provider":"google"}`, "not_applicable:social_provider"},
		{`{}`, "missing:method"},
		{`{"method":"sms"}`, "enum:method"},
	} {
		if got := codes(s.Check(props(t, c.props))); got != c.want {
			t.Errorf("%s: got %q, want %q", c.props, got, c.want)
		}
	}
}

func TestCheckTypesAndItems(t *testing.T) {
	s := suggested(t, "purchase")
	ok := `{"order_id":"o1","value":499,"currency":"INR","product_code":"SKU1","items":[{"product_code":"A","quantity":1,"price":10}]}`
	if vs := s.Check(props(t, ok)); vs != nil {
		t.Fatalf("valid purchase: %v", vs)
	}
	for _, c := range []struct{ props, want string }{
		{`{"order_id":"o1","value":"499"}`, "type:value"},
		{`{"value":1}`, "missing:order_id"},
		{`{"order_id":"o1","value":1,"items":[{"product_code":"A","quantity":"two"}]}`, "type:item_quantity"},
		{`{"order_id":"o1","value":1,"items":"A"}`, "type:items"},
	} {
		if got := codes(s.Check(props(t, c.props))); got != c.want {
			t.Errorf("%s: got %q, want %q", c.props, got, c.want)
		}
	}
	adds := s.ListAdds(props(t, `{"product_code":"SKU1","items":[{"product_code":"A"},{"product_code":"SKU1"},{"product_code":7}]}`))
	if got := strings.Join(adds["products"], ","); got != "SKU1,A,7" {
		t.Errorf("list adds %q", got)
	}
}

func TestNormalizeRejects(t *testing.T) {
	for _, js := range []string{
		`{"name":"x","props":[{"key":"a","type":"enum"}]}`,
		`{"name":"x","props":[{"key":"a","type":"string"},{"key":"a","type":"string"}]}`,
		`{"name":"x","props":[{"key":"a","type":"string","depends_on":{"prop":"b","values":["1"]}}]}`,
		`{"name":"x","props":[{"key":"a","path":"items[].b[].c","type":"string"}]}`,
		`{"name":"x","props":[{"key":"a","type":"uuid"}]}`,
		`{"name":"x","mode":"strict"}`,
		`{"name":"contact_merged"}`,
		`{"name":"x","props":[{"key":"a","type":"string","add_to_list":"stage"}]}`,
	} {
		var s Schema
		if err := json.Unmarshal([]byte(js), &s); err != nil {
			t.Fatal(err)
		}
		if err := s.Normalize(); err == nil {
			t.Errorf("%s: accepted", js)
		}
	}
}

func TestBatchAndSummary(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	s := suggested(t, "signup")
	s.Props[0].Personal = false
	s.Props = append(s.Props, Prop{Key: "email_hint", Type: TypeString, Personal: true})
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	var b Batch
	for i, p := range []string{`{"method":"otp"}`, `{"method":"social","email_hint":"a@b.c"}`, `{"method":"social","social_provider":"x"}`} {
		e := &event.Event{ID: string(rune('a' + i)), TenantID: 1, ExternalID: "u1", Name: "signup", Ts: now, Props: props(t, p)}
		b.Add(s, e, s.Check(e.Props))
	}
	if err := b.Flush(ctx, rdb); err != nil {
		t.Fatal(err)
	}
	sum, err := Summarize(ctx, rdb, 1, "signup", 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Checked != 3 || sum.Bad != 2 || len(sum.Samples) != 2 || len(sum.Days) != 7 || sum.Days[6].Bad != 2 {
		t.Fatalf("summary %+v", sum)
	}
	for _, smp := range sum.Samples {
		if strings.Contains(smp.Props, "a@b.c") {
			t.Errorf("personal property kept in a sample: %s", smp.Props)
		}
	}
	tot, err := Totals(ctx, rdb, 1, []string{"signup", "login"}, 30, now)
	if err != nil || tot["signup"] != [2]int64{2, 3} || tot["login"] != [2]int64{0, 0} {
		t.Fatalf("totals %v %v", tot, err)
	}
}
