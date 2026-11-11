package eventschema

import (
	"encoding/json"
	"sort"

	"pulse/internal/segment"
)

// MaxMessageProps caps the event properties a journey keeps for its messages ({{event.x}}).
const MaxMessageProps = 16 << 10

// MessageProps is what a journey keeps of the event that started it, for its messages: the
// properties without those the schema (s, may be nil) marks personal, as JSON of at most
// MaxMessageProps bytes. When the properties are larger, the biggest top-level properties are
// left out until the rest fits. It returns nil when nothing is left.
func MessageProps(s *Schema, props map[string]any) json.RawMessage {
	if len(props) == 0 {
		return nil
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return nil
	}
	var cp map[string]any // a copy: the event itself is left alone
	if json.Unmarshal(raw, &cp) != nil {
		return nil
	}
	if s != nil {
		for _, p := range s.PersonalPaths() {
			remove(cp, p)
		}
	}
	raw, _ = json.Marshal(cp)
	if len(raw) > MaxMessageProps {
		type sized struct {
			key  string
			size int
		}
		var keys []sized
		for k, v := range cp {
			b, _ := json.Marshal(v)
			keys = append(keys, sized{k, len(b) + len(k) + 4})
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].size != keys[j].size {
				return keys[i].size > keys[j].size
			}
			return keys[i].key < keys[j].key
		})
		total := len(raw)
		for _, k := range keys {
			if total <= MaxMessageProps {
				break
			}
			delete(cp, k.key)
			total -= k.size
		}
		raw, _ = json.Marshal(cp)
		for len(raw) > MaxMessageProps && len(cp) > 0 { // separators miscounted: drop more
			for _, k := range keys {
				if _, ok := cp[k.key]; ok {
					delete(cp, k.key)
					break
				}
			}
			raw, _ = json.Marshal(cp)
		}
	}
	if len(cp) == 0 {
		return nil
	}
	return raw
}

// Example builds sample properties from a schema, for previews when a contact has no such event:
// enum properties take their first value, others a value of their type. Personal properties are
// left out.
func Example(s *Schema) map[string]any {
	out := map[string]any{}
	if s == nil {
		return out
	}
	for _, p := range s.Props {
		if p.Personal {
			continue
		}
		var v any
		switch p.Type {
		case TypeNumber:
			v = 1299.0
		case TypeBool:
			v = true
		case TypeDate:
			v = "2026-11-11T10:30:00Z"
		case TypeEnum:
			v = "value"
			if len(p.Values) > 0 {
				v = p.Values[0].Value
			}
		case TypeList:
			v = []any{"item 1", "item 2"}
		case TypeObject:
			v = map[string]any{}
		default:
			v = "Sample " + p.Key
			if p.Label != "" {
				v = "Sample " + p.Label
			}
		}
		setPath(out, p.PathOf(), v)
	}
	return out
}

// setPath writes v at a schema path (items[].x writes one item).
func setPath(m map[string]any, path string, v any) {
	p, ok := segment.ParsePath(path)
	if !ok {
		return
	}
	cur := m
	for i, k := range p.Keys {
		last := i == len(p.Keys)-1
		if last && !p.List {
			if _, exists := cur[k]; !exists {
				cur[k] = v
			}
			return
		}
		if last && p.List {
			items, _ := cur[k].([]any)
			if len(items) == 0 {
				items = []any{map[string]any{}}
			}
			item, _ := items[0].(map[string]any)
			if item == nil {
				return
			}
			inner := item
			for j, ik := range p.InItems {
				if j == len(p.InItems)-1 {
					inner[ik] = v
					break
				}
				next, _ := inner[ik].(map[string]any)
				if next == nil {
					next = map[string]any{}
					inner[ik] = next
				}
				inner = next
			}
			cur[k] = items
			return
		}
		next, _ := cur[k].(map[string]any)
		if next == nil {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
}
