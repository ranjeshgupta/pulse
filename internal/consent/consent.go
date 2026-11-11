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
	// AttrSMSBounced is the same for SMS: a permanent failure of the phone number.
	AttrSMSBounced = "sms_bounced"
)

// Methods of an unsubscribe, recorded as props.method on unsubscribed events.
const (
	MethodSelf   = "self"   // the contact did it: a link, a keyword reply, the preference center, a form, a complaint
	MethodManual = "manual" // someone else did it: an admin in the app, an API call, an import
)

// Method classifies an unsubscribed event's source as self-service or manual.
func Method(source string) string {
	switch {
	case source == "email_link", source == "one_click", source == "preference_center", source == "form",
		source == "complaint", source == "sms_link", strings.HasSuffix(source, "_keyword"):
		return MethodSelf
	}
	return MethodManual
}

// Key is the attribute that records an opt-out from ch (or from everything, for All).
func Key(ch string) string { return "optout_" + ch }

// IsReserved reports whether an attribute may only be changed through consent events. Browsers
// (site key) can't set these directly, so a page script can't opt someone back in.
// Tags, points, merged_into, the lifecycle stage and the company attributes (company_id,
// company_name, company_<key>) are reserved the same way: they change only through their events.
func IsReserved(attrName string) bool {
	switch attrName {
	case Key(All), "tags", "points", "merged_into", AttrEmailBounced, AttrSMSBounced, "first_seen", "last_seen", "stage", "stage_changed_at",
		"date_added", "date_identified", "date_modified", "email_domain", "phone_country", "owner_id", "owner_name",
		"device_type", "device_os", "device_os_version", "device_brand", "device_model", "browser", "app_version":
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
