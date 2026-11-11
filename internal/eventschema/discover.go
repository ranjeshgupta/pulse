package eventschema

import (
	"sort"

	"pulse/internal/attr"
	"pulse/internal/segment"
)

// Discovered is a property seen in a sample of events.
type Discovered struct {
	Path     string   `json:"path"`     // e.g. product_code, product.code, items[].code
	Type     string   `json:"type"`     // string | number | bool | date | list | object | mixed
	Share    float64  `json:"share"`    // fraction of sampled events that had it
	Examples []string `json:"examples"` // up to 3 distinct values (none for personal properties)
}

const (
	maxDiscovered = 200
	maxDepth      = 3
)

// Discover lists the properties of sampled events (decoded props), most common first. Paths of
// lists of objects use [] (items[].code). Examples are left out for hidden paths.
func Discover(samples []map[string]any, hidden func(path string) bool) []Discovered {
	type acc struct {
		types    map[string]bool
		events   int
		examples []string
	}
	seen := map[string]*acc{}
	var order []string
	for _, props := range samples {
		inEvent := map[string]bool{}
		var walk func(prefix string, v any, depth int)
		walk = func(prefix string, v any, depth int) {
			m, ok := v.(map[string]any)
			if !ok {
				return
			}
			for k, x := range m {
				if !attr.ValidKey(k) {
					continue
				}
				path := k
				if prefix != "" {
					path = prefix + "." + k
				}
				a := seen[path]
				if a == nil {
					if len(seen) >= maxDiscovered {
						continue
					}
					a = &acc{types: map[string]bool{}}
					seen[path] = a
					order = append(order, path)
				}
				if !inEvent[path] {
					inEvent[path] = true
					a.events++
				}
				t := typeOf(x)
				a.types[t] = true
				if (t == TypeString || t == TypeNumber || t == TypeBool || t == TypeDate) && len(a.examples) < 3 {
					s := attr.String(x)
					if s != "" && len(s) <= 80 && !containsStr(a.examples, s) {
						a.examples = append(a.examples, s)
					}
				}
				if depth >= maxDepth {
					continue
				}
				switch y := x.(type) {
				case map[string]any:
					walk(path, y, depth+1)
				case []any:
					for _, it := range y {
						if _, isObj := it.(map[string]any); isObj && !segment.HasList(path) {
							walk(path+"[]", it, depth+1)
						}
					}
				}
			}
		}
		walk("", props, 1)
	}
	out := make([]Discovered, 0, len(order))
	for _, p := range order {
		a := seen[p]
		t := "mixed"
		if len(a.types) == 1 {
			for x := range a.types {
				t = x
			}
		}
		d := Discovered{Path: p, Type: t, Share: float64(a.events) / float64(max(len(samples), 1)), Examples: a.examples}
		if d.Examples == nil || (hidden != nil && hidden(p)) {
			d.Examples = []string{}
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Share != out[j].Share {
			return out[i].Share > out[j].Share
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func typeOf(v any) string {
	switch x := v.(type) {
	case string:
		if _, ok := segment.DatePart(x); ok && len(x) <= 35 {
			return TypeDate
		}
		return TypeString
	case float64:
		return TypeNumber
	case bool:
		return TypeBool
	case []any:
		return TypeList
	case map[string]any:
		return TypeObject
	}
	return "null"
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
