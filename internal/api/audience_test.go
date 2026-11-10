package api

import (
	"reflect"
	"testing"
)

func TestUserHash(t *testing.T) {
	// hex(HMAC-SHA256(secret, user id)) is what the customer's server computes.
	h := UserHash("secret", "u1")
	if len(h) != 64 {
		t.Fatalf("hash %q", h)
	}
	if !validUserHash("secret", "u1", h) || validUserHash("secret", "u2", h) || validUserHash("other", "u1", h) || validUserHash("secret", "u1", "") {
		t.Error("validUserHash accepts the wrong user, secret or an empty hash")
	}
}

func TestCoerceAttrs(t *testing.T) {
	attrs := map[string]any{"age": "42", "vip": "Yes", "city": "Pune"}
	if err := coerceAttrs(attrs, map[string]string{"age": "number", "vip": "boolean"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attrs, map[string]any{"age": 42.0, "vip": true, "city": "Pune"}) {
		t.Errorf("attrs = %v", attrs)
	}
	if err := coerceAttrs(map[string]any{"age": "old"}, map[string]string{"age": "number"}); err == nil {
		t.Error("a non-number should be reported")
	}
}
