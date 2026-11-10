// Package attr defines the canonical string form of contact attributes.
//
// Attributes are compared in two places: the Go segment evaluator (streaming path) and ClickHouse,
// where they are stored as Map(String, String) (batch path). Both sides must see the same string
// for the same value, or a contact could be "in" a segment on one path and "out" on the other.
package attr

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

var keyRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

// ValidKey reports whether k can be used as an attribute name.
func ValidKey(k string) bool { return keyRe.MatchString(k) }

// String converts a JSON-decoded value to its canonical string. Missing and null are "".
func String(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case json.Number:
		return x.String()
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

// Number parses s the way ClickHouse toFloat64OrNull does for ordinary decimal input.
// Infinities and NaN are rejected so comparisons stay well-defined on both paths.
func Number(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// Strings converts a whole attribute map, as written to ClickHouse.
func Strings(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = String(v)
	}
	return out
}
