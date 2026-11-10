// Package consent holds a contact's per-channel opt-outs. They live in reserved profile attributes
// (optout_email, optout_sms, ..., optout_all), so segments can filter on them and every change is an
// event in the contact's history. They change only through "unsubscribed" and "resubscribed" events,
// which the profile service turns into those attributes.
package consent

import (
	"strings"

	"pulse/internal/attr"
	"pulse/internal/channel"
)

const (
	Unsubscribed = "unsubscribed" // props: {channel: email|sms|whatsapp|rcs|push|all, source: ...}
	Resubscribed = "resubscribed"
	All          = "all"

	// AttrEmailBounced marks an address that hard-bounced: email to it is suppressed. Set by bounce
	// webhooks; a server can clear it (false) once the address is fixed.
	AttrEmailBounced = "email_bounced"
)

// Key is the attribute that records an opt-out from ch (or from everything, for All).
func Key(ch string) string { return "optout_" + ch }

// IsReserved reports whether an attribute may only be changed through consent events. Browsers
// (site key) can't set these directly, so a page script can't opt someone back in.
// Tags, points, merged_into, the lifecycle stage and the company attributes (company_id,
// company_name, company_<key>) are reserved the same way: they change only through their events.
func IsReserved(attrName string) bool {
	switch attrName {
	case Key(All), "tags", "points", "merged_into", AttrEmailBounced, "first_seen", "last_seen", "stage", "stage_changed_at":
		return true
	}
	if strings.HasPrefix(attrName, "company_") {
		return true
	}
	for _, ch := range channel.All {
		if attrName == Key(ch) {
			return true
		}
	}
	return false
}

// OptedOut reports whether the contact must not receive messages on ch.
func OptedOut(attrs map[string]any, ch string) bool {
	return truthy(attrs[Key(All)]) || truthy(attrs[Key(ch)])
}

func truthy(v any) bool {
	s := attr.String(v)
	return s == "true" || s == "1" || s == "yes"
}

// Updates returns the attribute changes a consent event makes, or nil for other events.
// Resubscribing to one channel also lifts a global opt-out, so it takes effect.
func Updates(eventName string, props map[string]any) map[string]any {
	ch := attr.String(props["channel"])
	if ch == "" {
		ch = All
	}
	if ch != All && !channel.Valid(ch) {
		return nil
	}
	switch eventName {
	case Unsubscribed:
		return map[string]any{Key(ch): true}
	case Resubscribed:
		u := map[string]any{Key(ch): false}
		if ch != All {
			u[Key(All)] = false
		} else {
			for _, c := range channel.All {
				u[Key(c)] = false
			}
		}
		return u
	}
	return nil
}
