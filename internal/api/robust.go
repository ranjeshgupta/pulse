package api

// Input hardening shared by every route: request bodies are read and checked the same way, text
// that can't be stored is refused up front with a clear 400, database errors caused by the input
// become 4xx, and a panic never drops the connection.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"runtime/debug"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

const errNUL = "text can't contain NUL characters (\\u0000)"

// decode reads a JSON request body into v. On failure it writes a 4xx with a message that says
// what was wrong (and where), and returns false.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeBodyErr(w, err)
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		writeErr(w, http.StatusBadRequest, "the request body is empty; send a JSON object")
		return false
	}
	if hasNULEscape(raw) {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+errNUL)
		return false
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, jsonErrMessage(err))
		return false
	}
	return true
}

// writeBodyErr answers a request whose body couldn't be read.
func writeBodyErr(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the request body is too large (the limit is %s)", byteSize(tooBig.Limit)))
		return
	}
	writeErr(w, http.StatusBadRequest, "the request body couldn't be read")
}

func byteSize(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d KB", n>>10)
}

// jsonErrMessage turns a decoding error into a message for the person who sent the request.
func jsonErrMessage(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return fmt.Sprintf("the request body is too large (the limit is %s)", byteSize(tooBig.Limit))
	case errors.Is(err, io.EOF):
		return "the request body is empty; send a JSON object"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "invalid JSON: the body ends too early"
	case errors.As(err, &syn):
		return fmt.Sprintf("invalid JSON at byte %d: %s", syn.Offset, strings.TrimPrefix(syn.Error(), "json: "))
	case errors.As(err, &typ):
		want := jsonKind(typ.Type)
		if typ.Field == "" {
			return fmt.Sprintf("invalid JSON: expected %s, got %s", want, typ.Value)
		}
		return fmt.Sprintf("invalid JSON: %q should be %s, not %s", typ.Field, want, typ.Value)
	}
	return "invalid JSON: " + strings.TrimPrefix(err.Error(), "json: ")
}

func jsonKind(t reflect.Type) string {
	if t == nil {
		return "a value"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "text"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "a whole number"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number of 0 or more"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return "text"
		}
		return "a list"
	case reflect.Map, reflect.Struct:
		if t.String() == "time.Time" {
			return "a date and time (RFC 3339)"
		}
		return "an object"
	}
	return "a different type"
}

// hasNULEscape reports whether JSON text contains a \u0000 escape (a NUL character): Postgres
// can store it neither in text nor in jsonb, and it is never meaningful in Pulse's input.
func hasNULEscape(raw []byte) bool {
	for i := 0; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		if raw[i+1] == 'u' && i+6 <= len(raw) && string(raw[i+2:i+6]) == "0000" {
			return true
		}
		i++ // skip the escaped character (\\ included)
	}
	return false
}

// inputGuard refuses addresses with NUL characters (no route can use them and the database can't
// store them) and turns a panic into a 500 that is logged, so a request never drops the connection.
func (s *Server) inputGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic serving request", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		if strings.ContainsRune(r.URL.Path, 0) || strings.Contains(r.URL.RawQuery, "%00") || strings.ContainsRune(r.URL.RawQuery, 0) {
			writeErr(w, http.StatusBadRequest, "the address can't contain NUL characters (%00)")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// inputProblem explains a database error that was caused by the request's values (Postgres
// classes 22, data exceptions, and 23, integrity violations); "" for anything else.
func inputProblem(err error) (int, string) {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return 0, ""
	}
	switch pe.Code {
	case "22021", "22P05":
		return http.StatusBadRequest, errNUL + " or invalid characters"
	case "22001":
		return http.StatusBadRequest, "a value is too long"
	case "22003":
		return http.StatusBadRequest, "a number is out of range"
	case "22007", "22008":
		return http.StatusBadRequest, "a date or time is out of range"
	case "23505":
		return http.StatusConflict, "already exists"
	case "23503":
		return http.StatusBadRequest, "it refers to something that doesn't exist"
	case "23502", "23514":
		return http.StatusBadRequest, "a value is missing or not allowed"
	}
	if strings.HasPrefix(pe.Code, "22") {
		return http.StatusBadRequest, "a value is invalid"
	}
	return 0, ""
}

// Limits on one event's props and attrs. The request body limit alone (4 MB) would let one event
// carry a single huge or deeply nested value, which every consumer then parses and stores.
const (
	maxEventKeys     = 500      // top-level props, and top-level attrs
	maxEventValues   = 10000    // values in all, nested ones included
	maxEventDepth    = 20       // nesting of objects and lists
	maxEventStrBytes = 64 << 10 // one text value or key
)

// eventSizeProblem explains why an event's props or attrs are too large ("" when they're fine).
func eventSizeProblem(props, attrs map[string]any) string {
	for _, part := range []struct {
		name string
		m    map[string]any
	}{{"props", props}, {"attrs", attrs}} {
		if len(part.m) > maxEventKeys {
			return fmt.Sprintf("%s has %d keys; the limit is %d", part.name, len(part.m), maxEventKeys)
		}
		n := 0
		if msg := valueProblem(part.name, part.m, 0, &n); msg != "" {
			return msg
		}
	}
	return ""
}

func valueProblem(path string, v any, depth int, n *int) string {
	*n++
	if *n > maxEventValues {
		return fmt.Sprintf("an event can hold at most %d values (props and attrs, nested ones included)", maxEventValues)
	}
	if depth > maxEventDepth {
		return fmt.Sprintf("%s is nested more than %d levels deep", path, maxEventDepth)
	}
	switch x := v.(type) {
	case string:
		if len(x) > maxEventStrBytes {
			return fmt.Sprintf("%s is %d KB; a text value can be at most %d KB", path, len(x)>>10, maxEventStrBytes>>10)
		}
	case map[string]any:
		for k, val := range x {
			if len(k) > maxEventStrBytes {
				return fmt.Sprintf("%s has a key longer than %d KB", path, maxEventStrBytes>>10)
			}
			if msg := valueProblem(path+"."+k, val, depth+1, n); msg != "" {
				return msg
			}
		}
	case []any:
		for i, val := range x {
			if msg := valueProblem(fmt.Sprintf("%s[%d]", path, i), val, depth+1, n); msg != "" {
				return msg
			}
		}
	}
	return ""
}
