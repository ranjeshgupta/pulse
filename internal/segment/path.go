package segment

import (
	"strings"

	"pulse/internal/attr"
)

// Event property filters name a property by path: a top-level key ("amount"), a dotted path into
// nested objects ("product.code"), or a path through one list with [] ("items[].code"), which
// matches when any item of the list matches. A missing, empty or non-list value behaves like a
// single missing value, so "items[].code is not set" holds for an order without items.

const (
	maxPathParts = 4
	maxPathLen   = 128
)

// Path is a parsed property path: the keys before the list (or all of them), and for a path
// with [] the keys inside each item.
type Path struct {
	Keys    []string
	List    bool
	InItems []string
}

// ParsePath parses and validates a property path.
func ParsePath(p string) (Path, bool) {
	if p == "" || len(p) > maxPathLen {
		return Path{}, false
	}
	parts := strings.Split(p, ".")
	if len(parts) > maxPathParts {
		return Path{}, false
	}
	var out Path
	for _, part := range parts {
		key, list := strings.CutSuffix(part, "[]")
		if !attr.ValidKey(key) || (list && out.List) {
			return Path{}, false
		}
		if out.List {
			out.InItems = append(out.InItems, key)
		} else {
			out.Keys = append(out.Keys, key)
		}
		out.List = out.List || list
	}
	return out, true
}

// ValidPath reports whether p can name an event property in a filter.
func ValidPath(p string) bool {
	_, ok := ParsePath(p)
	return ok
}

// candidate is one value a path reads: the value and whether it is there at all.
type candidate struct {
	v       any
	present bool
}

// lookup follows keys through nested objects.
func lookup(v any, keys []string) (any, bool) {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// values returns what the path reads in props: one candidate, or one per list item.
func (p Path) values(props map[string]any) []candidate {
	v, ok := lookup(map[string]any(props), p.Keys)
	if !p.List {
		return []candidate{{v, ok}}
	}
	items, _ := v.([]any)
	if len(items) == 0 {
		return []candidate{{nil, false}}
	}
	out := make([]candidate, len(items))
	for i, it := range items {
		x, ok := lookup(it, p.InItems)
		out[i] = candidate{x, ok}
	}
	return out
}

// Value returns the value at a path without [] (for display and schema checks).
func Value(props map[string]any, path string) (any, bool) {
	p, ok := ParsePath(path)
	if !ok || p.List {
		return nil, false
	}
	return lookup(map[string]any(props), p.Keys)
}

// HasList reports whether a path already goes through a list ([]); paths go through one at most.
func HasList(path string) bool {
	for i := 0; i+1 < len(path); i++ {
		if path[i] == '[' && path[i+1] == ']' {
			return true
		}
	}
	return false
}
