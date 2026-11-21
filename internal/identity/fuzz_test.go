package identity

import (
	"regexp"
	"testing"

	"pulse/internal/event"
)

var phoneOut = regexp.MustCompile(`^\+?[0-9]+$`)

// FuzzValue checks key normalisation: no panics, phones come out as digits with an optional +,
// and a normalised value normalises to itself.
func FuzzValue(f *testing.F) {
	for _, s := range []string{" Asha@Example.COM ", "+91 98765-43210", "(022) 2345 6789", "+ -", " x\v", "İSTANBUL", "Ⱥ"} {
		f.Add(Email, s)
		f.Add(Phone, s)
		f.Add("username", s)
	}
	f.Fuzz(func(t *testing.T, field, raw string) {
		v := Value(field, raw)
		if v == "" {
			return
		}
		if field == Phone && !phoneOut.MatchString(v) {
			t.Fatalf("Value(phone, %q) = %q", raw, v)
		}
		if len(v) <= MaxValue {
			if again := Value(field, v); again != v {
				t.Fatalf("Value(%q, %q) = %q, but normalising that again gives %q", field, raw, v, again)
			}
		}
		e := &event.Event{ExternalID: raw, Email: raw, Attrs: map[string]any{field: raw}}
		FromEvent(field, e)
		FromContact(field, raw, e.Attrs)
		Fill(map[string]any{}, e)
		keys := []Key{{Field: ExternalID}, {Field: field, Mode: Merge}}
		if Validate(keys) == nil {
			Values(keys, raw, e.Attrs)
		}
	})
}
