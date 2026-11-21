package eventschema

import (
	"encoding/json"
	"testing"
)

// FuzzSchema decodes arbitrary schemas and event properties: Normalize must not panic, and for a
// schema it accepts, checking, list adds, redaction, message props, examples and discovery must
// not either. Message props never exceed their cap.
func FuzzSchema(f *testing.F) {
	f.Add(`{"name":"purchase","props":[{"key":"amount","type":"number","required":true},{"key":"method","type":"enum","values":[{"value":"card"},{"value":"social"}]},{"key":"provider","type":"string","depends_on":{"prop":"method","values":["social"]}},{"key":"code","path":"items[].code","type":"string","add_to_list":"products"},{"key":"email","type":"string","personal":true}]}`,
		`{"amount":"x","method":"social","items":[{"code":"A"},{"code":3},"x"],"email":"a@b.co"}`)
	f.Add(`{"name":"signup","mode":"enforce","props":[{"key":"d","type":"date"},{"key":"o","path":"a.b.c","type":"object"},{"key":"l","type":"list"},{"key":"b","type":"bool"}]}`,
		`{"d":"2026-11-30","a":{"b":{"c":{}}},"l":[1],"b":true}`)
	f.Add(`{"name":"x","props":[{"key":"a","type":"enum","values":[]}]}`, `{}`)
	f.Fuzz(func(t *testing.T, schemaJSON, propsJSON string) {
		var props map[string]any
		_ = json.Unmarshal([]byte(propsJSON), &props)
		Discover([]map[string]any{props, props}, func(p string) bool { return len(p)%2 == 0 })
		if raw := MessageProps(nil, props); len(raw) > MaxMessageProps {
			t.Fatalf("message props of %d bytes", len(raw))
		}
		var s Schema
		if json.Unmarshal([]byte(schemaJSON), &s) != nil || s.Normalize() != nil {
			return
		}
		s.Check(props)
		s.ListAdds(props)
		s.PersonalPaths()
		redact(&s, props)
		if raw := MessageProps(&s, props); len(raw) > MaxMessageProps {
			t.Fatalf("message props of %d bytes", len(raw))
		}
		if ex := Example(&s); len(ex) > 0 {
			s.Check(ex)
		}
	})
}
