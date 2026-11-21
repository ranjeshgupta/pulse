// Package identity decides which contact an incoming event belongs to when a workspace identifies
// people by more than their external id (Settings → Contacts → Identity).
//
// A workspace lists its identity keys in priority order: the external id (always present) and any
// of email, phone or a contact field (user name, customer number…). Each key other than the
// external id either merges contacts that share its value ("merge") or only lists them under
// Duplicates ("suggest").
//
// Values are compared normalised (Value): emails and custom fields trimmed and in lower case,
// phones as digits with the leading "+" kept. The Postgres side (contact_keys, its backfill) must
// normalise exactly the same way; see pg.keyExpr.
package identity

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"pulse/internal/attr"
	"pulse/internal/event"
)

// Fields with a meaning of their own; any other key is a contact field.
const (
	ExternalID = "external_id"
	Email      = "email"
	Phone      = "phone"
)

// Modes of a key.
const (
	Merge   = "merge"   // contacts sharing the value are merged into the higher-priority one
	Suggest = "suggest" // contacts sharing the value are only listed under Duplicates
)

// MaxKeys caps the keys of a workspace (the external id included).
const MaxKeys = 8

// MaxValue is the longest value kept as a key; longer values aren't identifiers.
const MaxValue = 255

// Key is one identity key.
type Key struct {
	Field string `json:"field"`
	Mode  string `json:"mode"`
}

var (
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	e164Re  = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
)

// LooksLikeEmail reports whether s is shaped like an email address.
func LooksLikeEmail(s string) bool { return len(s) <= MaxValue && emailRe.MatchString(s) }

// LooksLikePhone reports whether s is an international (E.164) phone number.
func LooksLikePhone(s string) bool { return e164Re.MatchString(s) }

// asciiSpace is what Value trims: exactly what Postgres' btrim in pg.keyExpr trims, so both sides
// agree on values padded with other (Unicode) spaces.
const asciiSpace = " \t\n\r\v\f"

// Value normalises a raw value of a field; "" means no usable value. MaxValue counts bytes (as
// octet_length does in pg.keyExpr).
func Value(field, raw string) string {
	raw = strings.Trim(raw, asciiSpace)
	if raw == "" || len(raw) > MaxValue {
		return ""
	}
	if field == Phone {
		var b strings.Builder
		if strings.HasPrefix(raw, "+") {
			b.WriteByte('+')
		}
		for _, r := range raw {
			if r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		out := b.String()
		if strings.TrimPrefix(out, "+") == "" {
			return ""
		}
		return out
	}
	return strings.Map(unicode.ToLower, raw)
}

// FromContact is a contact's value of a field: its attribute, or for email and phone its external
// id when that is an email address or an international phone number and the attribute is empty.
func FromContact(field, externalID string, attrs map[string]any) string {
	if v := Value(field, attr.String(attrs[field])); v != "" {
		return v
	}
	return fromExternalID(field, externalID)
}

// FromEvent is the value of a field an event carries (its attributes or email), or for email and
// phone the event's external id as above. "" when the event says nothing about the field.
func FromEvent(field string, e *event.Event) string {
	if field == Email && e.Email != "" {
		if v := Value(field, e.Email); v != "" {
			return v
		}
	}
	if v := Value(field, attr.String(e.Attrs[field])); v != "" {
		return v
	}
	return fromExternalID(field, e.ExternalID)
}

func fromExternalID(field, ext string) string {
	switch {
	case field == Email && LooksLikeEmail(ext):
		return Value(Email, ext)
	case field == Phone && LooksLikePhone(ext):
		return ext
	}
	return ""
}

// Values are a contact's key values by field (stored keys only: the external id is not one).
func Values(keys []Key, externalID string, attrs map[string]any) map[string]string {
	var out map[string]string
	for _, k := range keys {
		if k.Field == ExternalID {
			continue
		}
		if v := FromContact(k.Field, externalID, attrs); v != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[k.Field] = v
		}
	}
	return out
}

// SameValues reports whether two Values results are equal.
func SameValues(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Defaults are the keys of a workspace that never set them: the external id only (no automatic
// merging, as Pulse always did).
func Defaults() []Key { return []Key{{Field: ExternalID, Mode: Merge}} }

// Merging reports whether any key other than the external id merges contacts.
func Merging(keys []Key) bool {
	for _, k := range keys {
		if k.Field != ExternalID && k.Mode == Merge {
			return true
		}
	}
	return false
}

// Fill returns the attributes an event should also set from its external id: the email when the
// id is an email address and the profile has none, the phone likewise. nil when there's nothing.
func Fill(cur map[string]any, e *event.Event) map[string]any {
	var out map[string]any
	if LooksLikeEmail(e.ExternalID) && attr.String(cur[Email]) == "" && e.Email == "" && attr.String(e.Attrs[Email]) == "" {
		out = map[string]any{Email: e.ExternalID}
	}
	if LooksLikePhone(e.ExternalID) && attr.String(cur[Phone]) == "" && attr.String(e.Attrs[Phone]) == "" {
		if out == nil {
			out = map[string]any{}
		}
		out[Phone] = e.ExternalID
	}
	return out
}

// Validate checks a workspace's keys: the external id exactly once, no field twice, known modes,
// contact field names only. The external id's mode is always Merge (it is the contact itself).
func Validate(keys []Key) error {
	if len(keys) > MaxKeys {
		return fmt.Errorf("identity: at most %d keys", MaxKeys)
	}
	seen := map[string]bool{}
	for i := range keys {
		k := &keys[i]
		k.Field = strings.TrimSpace(k.Field)
		switch {
		case k.Field == "":
			return errors.New("identity: a key without a field")
		case seen[k.Field]:
			return fmt.Errorf("identity: %q is listed twice", k.Field)
		case k.Field != ExternalID && (!attr.ValidKey(k.Field) || event.EventOnlyAttr(k.Field) || k.Field == event.AttrMergedInto):
			return fmt.Errorf("identity: %q is not a contact field that can identify people", k.Field)
		}
		seen[k.Field] = true
		if k.Field == ExternalID {
			k.Mode = Merge
		} else if k.Mode != Merge && k.Mode != Suggest {
			return fmt.Errorf("identity: %s: mode must be merge or suggest", k.Field)
		}
	}
	if !seen[ExternalID] {
		return errors.New("identity: the external id must be one of the keys")
	}
	return nil
}
