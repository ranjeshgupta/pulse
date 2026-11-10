// Package event defines the tracked-event envelope that flows through Kafka.
package event

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"pulse/internal/attr"
)

// Reserved event names.
const (
	Identify    = "identify" // sets attributes only; props.anonymous_id merges that visitor into this contact
	EmailSent   = "email_sent"
	EmailFailed = "email_failed"

	TagAdded      = "tag_added"      // props: {tags: ["vip", ...]} or {tag: "vip"}
	TagRemoved    = "tag_removed"    // same props
	PointsChanged = "points_changed" // props: {delta: 10, reason: "..."}

	ListJoined = "list_joined" // props: {list_id: 12}; adds the contact to a static list
	ListLeft   = "list_left"

	// Written by the profile role when an anonymous visitor turns out to be a known contact.
	ContactMerged     = "contact_merged"      // on the known contact; props.from_contact_id, from_external_id
	ContactMergedInto = "contact_merged_into" // on the anonymous contact; props.into_external_id
)

// ServerOnly reports whether an event may only come from a server (API key) or the platform,
// never from a browser with the public site key.
func ServerOnly(name string) bool {
	switch name {
	case TagAdded, TagRemoved, PointsChanged, ListJoined, ListLeft, ContactMerged, ContactMergedInto:
		return true
	}
	return false
}

// Profile attributes the platform maintains. Tags and points change through their events (or a
// server import); merged_into is never accepted from outside.
const (
	AttrTags       = "tags"
	AttrPoints     = "points"
	AttrMergedInto = "merged_into"
	AttrFirstSeen  = "first_seen" // RFC 3339 time of the contact's first event
	AttrLastSeen   = "last_seen"  // UTC date (YYYY-MM-DD) of the latest event
)

// Kafka topics.
const (
	TopicRaw        = "events.raw"
	TopicEnriched   = "events.enriched"
	TopicMembership = "segment.membership"
)

const maxFutureSkew = 5 * time.Minute

var nameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`)

// ValidName reports whether n can be used as an event name.
func ValidName(n string) bool { return nameRe.MatchString(n) }

type Event struct {
	ID         string         `json:"id"`
	TenantID   int64          `json:"tenant_id"`
	ExternalID string         `json:"external_id"`
	Name       string         `json:"name"`
	Ts         time.Time      `json:"ts"`
	Props      map[string]any `json:"props,omitempty"`
	Attrs      map[string]any `json:"attrs,omitempty"` // attributes to merge into the profile
	Email      string         `json:"email,omitempty"` // shorthand for attrs.email

	// Set by the profile role before the event reaches events.enriched.
	ContactID    uint32         `json:"contact_id,omitempty"`
	NewContact   bool           `json:"new_contact,omitempty"`
	ChangedAttrs []string       `json:"changed_attrs,omitempty"`
	Profile      map[string]any `json:"profile,omitempty"` // full attribute snapshot after the merge
}

// Normalize validates an incoming event and fills defaults (id, timestamp).
func (e *Event) Normalize(now time.Time) error {
	switch {
	case e.ExternalID == "":
		return errors.New("external_id is required")
	case len(e.ExternalID) > 255:
		return errors.New("external_id is longer than 255 bytes")
	case !ValidName(e.Name):
		return fmt.Errorf("invalid event name %q", e.Name)
	case len(e.ID) > 64:
		return errors.New("id is longer than 64 bytes")
	}
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.Ts.IsZero() {
		e.Ts = now
	} else if e.Ts.After(now.Add(maxFutureSkew)) {
		return errors.New("ts is in the future")
	}
	e.Ts = e.Ts.UTC()
	for k := range e.Attrs {
		if !attr.ValidKey(k) {
			return fmt.Errorf("invalid attribute name %q", k)
		}
	}
	return nil
}

// RawKey partitions events.raw: one external id always lands on one partition.
func (e *Event) RawKey() []byte {
	return []byte(strconv.FormatInt(e.TenantID, 10) + ":" + e.ExternalID)
}

// ContactKey partitions every topic after identity resolution.
func ContactKey(tenant int64, contact uint32) []byte {
	return []byte(strconv.FormatInt(tenant, 10) + ":" + strconv.FormatUint(uint64(contact), 10))
}

// NewID returns a random 128-bit hex id.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
