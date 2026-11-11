package eventschema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessagePropsRemovesPersonalAndCaps(t *testing.T) {
	s := &Schema{Name: "cart_abandoned", Props: []Prop{
		{Key: "email_hint", Type: TypeString, Personal: true},
		{Key: "addr", Path: "shipping.address", Type: TypeString, Personal: true},
		{Key: "buyer", Path: "items[].buyer_phone", Type: TypeString, Personal: true},
	}}
	props := map[string]any{"cart_id": "c1", "email_hint": "a@x.test", "shipping": map[string]any{"address": "1 Main St", "city": "Pune"},
		"items": []any{map[string]any{"name": "Mat", "buyer_phone": "+91"}}}
	raw := MessageProps(s, props)
	got := string(raw)
	for _, leak := range []string{"a@x.test", "1 Main St", "+91"} {
		if strings.Contains(got, leak) {
			t.Errorf("personal value %q kept: %s", leak, got)
		}
	}
	if !strings.Contains(got, `"cart_id":"c1"`) || !strings.Contains(got, `"city":"Pune"`) || !strings.Contains(got, `"name":"Mat"`) {
		t.Errorf("kept = %s", got)
	}
	if props["email_hint"] != "a@x.test" {
		t.Error("the event itself was changed")
	}

	// Over 16 KB: the biggest properties go first, the rest stay.
	big := map[string]any{"product": "Yoga mat", "total": 2499.0, "blob": strings.Repeat("x", 20<<10), "notes": strings.Repeat("y", 3<<10)}
	raw = MessageProps(nil, big)
	if len(raw) > MaxMessageProps {
		t.Fatalf("%d bytes kept", len(raw))
	}
	var kept map[string]any
	_ = json.Unmarshal(raw, &kept)
	if kept["blob"] != nil || kept["product"] != "Yoga mat" || kept["notes"] == nil {
		t.Errorf("kept keys: %v", keys(kept))
	}
	// Many small properties over the cap: still capped.
	many := map[string]any{}
	for i := 0; i < 2000; i++ {
		many[strings.Repeat("k", 5)+string(rune('a'+i%26))+strings.Repeat("z", i%7)+itoa(i)] = "value"
	}
	if raw = MessageProps(nil, many); len(raw) > MaxMessageProps || len(raw) == 0 {
		t.Errorf("many: %d bytes", len(raw))
	}
	if MessageProps(s, map[string]any{"email_hint": "x"}) != nil || MessageProps(nil, nil) != nil {
		t.Error("nothing left should be nil")
	}
}

func TestExample(t *testing.T) {
	s := &Schema{Name: "cart_abandoned", Props: []Prop{
		{Key: "total", Type: TypeNumber}, {Key: "method", Type: TypeEnum, Values: []EnumValue{{Value: "upi"}}},
		{Key: "item_name", Path: "items[].name", Type: TypeString, Label: "Item"}, {Key: "secret", Type: TypeString, Personal: true},
		{Key: "code", Path: "product.code", Type: TypeString},
	}}
	ex := Example(s)
	b, _ := json.Marshal(ex)
	if string(b) != `{"items":[{"name":"Sample Item"}],"method":"upi","product":{"code":"Sample code"},"total":1299}` {
		t.Errorf("example = %s", b)
	}
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }
