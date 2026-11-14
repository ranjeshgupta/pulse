// Package reach keeps, per workspace, Redis bitmaps of who can be reached on each channel, and
// counts audiences with them: segment bitmaps combined with these by BITOP, all inside Redis, so an
// exact estimate costs a few bitmap operations whatever the audience size.
//
// Bitmaps (bit N = contact N, like segment bitmaps), under rch:{tenant}:
//
//	known       the contact exists and isn't merged into another (the app inbox reaches everyone known)
//	email       has an email address that hasn't bounced
//	phone       has a valid E.164 phone number (WhatsApp, RCS)
//	sms         has a valid phone number that hasn't bounced for SMS
//	push        has a push device (app or browser)
//	out:{ch}    opted out of ch, or of everything (Do Not Contact)
//
// The segments role maintains them: every enriched event that creates a contact or changes one of
// the attributes they read sets the contact's bits from its profile (Bits), device registrations
// and dead tokens set push, and a full rebuild from Postgres runs when a workspace has none and
// then once a day (Rebuilder), correcting any drift.
package reach

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/redis/go-redis/v9"

	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/consent"
)

// Bitmap names.
const (
	Known = "known"
	Email = "email"
	Phone = "phone"
	SMS   = "sms"
	Push  = "push"
	// WhatsApp and RCS: a valid phone whose channel no bounce rule stopped (whatsapp_bounced,
	// rcs_bounced).
	WhatsApp = "whatsapp"
	RCS      = "rcs"
)

// Key is the Redis key of one of a workspace's bitmaps.
func Key(tenant int64, name string) string { return fmt.Sprintf("rch:%d:%s", tenant, name) }

// OptOut is the bitmap name of contacts opted out of ch (or of everything).
func OptOut(ch string) string { return "out:" + ch }

// BuiltKey exists while a workspace's bitmaps are fresh (set by a rebuild, for a day). v2: the
// WhatsApp and RCS bitmaps were added, so every workspace rebuilds once.
func BuiltKey(tenant int64) string { return fmt.Sprintf("rch:%d:built:v2", tenant) }

// AddressOf is the bitmap of contacts with a working address for ch.
func AddressOf(ch string) string {
	switch ch {
	case channel.Email:
		return Email
	case channel.SMS:
		return SMS
	case channel.WhatsApp:
		return WhatsApp
	case channel.RCS:
		return RCS
	case channel.Push:
		return Push
	}
	return Known // inbox: Pulse delivers it itself
}

// ProfileNames are the bitmaps set from a profile (push comes from devices).
func ProfileNames() []string {
	out := []string{Known, Email, Phone, SMS, WhatsApp, RCS}
	for _, ch := range channel.All {
		out = append(out, OptOut(ch))
	}
	return out
}

// AllNames are every bitmap of a workspace.
func AllNames() []string { return append(ProfileNames(), Push) }

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

func truthy(v any) bool {
	s := attr.String(v)
	return s == "true" || s == "1" || s == "yes"
}

// Bits are a contact's profile bits, from its attributes (the email is the "email" attribute, as
// on enriched events' profiles). The same checks the dispatcher makes before sending.
func Bits(attrs map[string]any) map[string]bool {
	b := map[string]bool{}
	merged := attr.String(attrs["merged_into"]) != ""
	b[Known] = !merged
	email := strings.TrimSpace(attr.String(attrs["email"]))
	b[Email] = !merged && email != "" && !truthy(attrs[consent.AttrEmailBounced])
	phone := e164.MatchString(attr.String(attrs["phone"]))
	b[Phone] = !merged && phone
	b[SMS] = !merged && phone && !truthy(attrs[consent.AttrSMSBounced])
	b[WhatsApp] = !merged && phone && !truthy(attrs[consent.AttrWhatsAppBounced])
	b[RCS] = !merged && phone && !truthy(attrs[consent.AttrRCSBounced])
	for _, ch := range channel.All {
		b[OptOut(ch)] = consent.OptedOut(attrs, ch)
	}
	return b
}

// Relevant reports whether a change to these attributes can change a contact's bits.
func Relevant(changed []string) bool {
	for _, a := range changed {
		switch {
		case a == "email", a == "phone", a == "merged_into", a == consent.AttrEmailBounced, a == consent.AttrSMSBounced,
			a == consent.AttrWhatsAppBounced, a == consent.AttrRCSBounced,
			strings.HasPrefix(a, "optout_"):
			return true
		}
	}
	return false
}

// Queue adds SETBITs for a contact's bits to a pipeline.
func Queue(ctx context.Context, p redis.Pipeliner, tenant int64, contact uint32, bits map[string]bool) {
	for name, on := range bits {
		v := 0
		if on {
			v = 1
		}
		p.SetBit(ctx, Key(tenant, name), int64(contact), v)
	}
}

// SetPush records whether a contact has a push device now.
func SetPush(ctx context.Context, rdb redis.Cmdable, tenant int64, contact uint32, has bool) error {
	v := 0
	if has {
		v = 1
	}
	return rdb.SetBit(ctx, Key(tenant, Push), int64(contact), v).Err()
}
