package identity

import (
	"strings"
	"testing"

	"pulse/internal/event"
)

func TestValue(t *testing.T) {
	for _, c := range []struct{ field, in, want string }{
		{Email, "  Asha@Example.COM ", "asha@example.com"},
		{Phone, "+91 98765-43210", "+919876543210"},
		{Phone, "(022) 2345 6789", "02223456789"},
		{Phone, "+ -", ""},
		{"username", " Asha_K ", "asha_k"},
		{Email, strings.Repeat("a", 256), ""},
		{Email, "", ""},
	} {
		if got := Value(c.field, c.in); got != c.want {
			t.Errorf("Value(%s, %q) = %q, want %q", c.field, c.in, got, c.want)
		}
	}
}

func TestFromExternalID(t *testing.T) {
	e := &event.Event{ExternalID: "Ravi@Y.co"}
	if v := FromEvent(Email, e); v != "ravi@y.co" {
		t.Errorf("email from id = %q", v)
	}
	if v := FromEvent(Phone, &event.Event{ExternalID: "+919876543210"}); v != "+919876543210" {
		t.Errorf("phone from id = %q", v)
	}
	if v := FromEvent(Email, &event.Event{ExternalID: "u-1"}); v != "" {
		t.Errorf("email from a plain id = %q", v)
	}
	// The event's own email wins over the id.
	if v := FromEvent(Email, &event.Event{ExternalID: "a@x.co", Email: "b@x.co"}); v != "b@x.co" {
		t.Errorf("event email = %q", v)
	}
}

func TestFill(t *testing.T) {
	if f := Fill(map[string]any{}, &event.Event{ExternalID: "a@x.co"}); f["email"] != "a@x.co" {
		t.Errorf("fill email = %v", f)
	}
	if f := Fill(map[string]any{"email": "b@x.co"}, &event.Event{ExternalID: "a@x.co"}); f != nil {
		t.Errorf("an email already set is kept: %v", f)
	}
	if f := Fill(map[string]any{}, &event.Event{ExternalID: "a@x.co", Email: "c@x.co"}); f != nil {
		t.Errorf("the event's own email wins: %v", f)
	}
	if f := Fill(map[string]any{}, &event.Event{ExternalID: "+919876543210"}); f["phone"] != "+919876543210" {
		t.Errorf("fill phone = %v", f)
	}
	if f := Fill(map[string]any{}, &event.Event{ExternalID: "anon-12"}); f != nil {
		t.Errorf("plain id filled %v", f)
	}
}

func TestValidate(t *testing.T) {
	ok := []Key{{Field: "email", Mode: Merge}, {Field: ExternalID, Mode: "x"}, {Field: "username", Mode: Suggest}}
	if err := Validate(ok); err != nil || ok[1].Mode != Merge {
		t.Errorf("valid keys: %v %+v", err, ok)
	}
	for _, bad := range [][]Key{
		{{Field: "email", Mode: Merge}},
		{{Field: ExternalID}, {Field: "email", Mode: Merge}, {Field: "email", Mode: Suggest}},
		{{Field: ExternalID}, {Field: "email", Mode: "sometimes"}},
		{{Field: ExternalID}, {Field: "bad field", Mode: Merge}},
		{{Field: ExternalID}, {Field: "merged_into", Mode: Merge}},
	} {
		if err := Validate(bad); err == nil {
			t.Errorf("Validate(%+v) passed", bad)
		}
	}
}
