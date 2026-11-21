package profile

// Identity keys (Settings → Contacts → Identity, package identity): an event whose email, phone or
// other key value is already held by another contact either merges that contact into its own or,
// when the other contact ranks higher, goes to it.
//
// The ranking follows the workspace's key order. The event's own contact counts as matched by the
// external id, unless it is new (made for this event): then the highest-ranked contact sharing a
// merging key wins. So with the order external id → email, a new id carrying a known email joins
// the contact that has the email, and a known id carrying another contact's email takes that
// contact in.
//
// Merging reuses merge_requested (POST /v1/contacts/merge): the loser's attributes fill the
// winner's gaps, its history and counters move, and its later events follow merged_into. When the
// winner is another contact, the event and a merge_requested for the winner are sent back to
// events.raw under the winner's key, so the winner's profile is only ever written by its own
// partition; they're marked Rerouted and never go round again.

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"pulse/internal/automation"
	"pulse/internal/event"
	"pulse/internal/identity"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/store/pg"

	"github.com/twmb/franz-go/pkg/kgo"
)

// IdentityStore reads and writes identity key values (contact_keys).
type IdentityStore interface {
	MatchIdentityKeys(ctx context.Context, tenant int64, kvs []pg.KeyValue) (map[pg.KeyValue][]model.Contact, error)
	ReplaceContactKeys(ctx context.Context, tenant int64, fields []string, cs []pg.ContactKeys) error
}

// identityKeys returns a workspace's keys (only the external id without an automation store).
func (s *Service) identityKeys(ctx context.Context, tenant int64) []identity.Key {
	if _, ok := s.store.(AutomationStore); !ok {
		return identity.Defaults()
	}
	keys := s.settings(ctx, tenant).Identity.Keys
	if len(keys) == 0 {
		return identity.Defaults()
	}
	return keys
}

// settings reads a workspace's automation settings through a 30-second cache; the defaults when
// there is no automation store or the read fails.
func (s *Service) settings(ctx context.Context, tenant int64) automation.Settings {
	as, ok := s.store.(AutomationStore)
	if !ok {
		return automation.Defaults()
	}
	now := s.now()
	if c, hit := s.bounces.Load(tenant); hit && now.Before(c.(cachedBounces).exp) {
		return c.(cachedBounces).s
	}
	set, err := as.AutomationSettings(ctx, tenant)
	if err != nil {
		set = automation.Defaults()
	}
	s.bounces.Store(tenant, cachedBounces{s: set, exp: now.Add(bounceRulesTTL)})
	return set
}

// isNew reports whether a contact was made for this batch (or moments ago, by a batch that failed
// before it finished): it has no standing of its own in the ranking.
func isNew(c model.Contact, created bool, now time.Time) bool {
	return created || now.Sub(c.CreatedAt) < newContactWindow
}

// resolveIdentity matches the batch's events against the identity keys. Events whose contact loses
// to another are sent to that contact (published to events.raw) and left out of the returned
// batch; merges into an event's own contact are added as merge_requested events just before it.
// The contacts found are added to contacts.
func (s *Service) resolveIdentity(ctx context.Context, evs []event.Event, contacts map[string]model.Contact, created map[string]bool, now time.Time) ([]event.Event, error) {
	is, ok := s.store.(IdentityStore)
	if !ok {
		return evs, nil
	}
	keysOf := map[int64][]identity.Key{}
	want := map[int64][]pg.KeyValue{}
	for i := range evs {
		e := &evs[i]
		keys, seen := keysOf[e.TenantID]
		if !seen {
			keys = s.identityKeys(ctx, e.TenantID)
			if !identity.Merging(keys) {
				keys = nil
			}
			keysOf[e.TenantID] = keys
		}
		if keys == nil || !resolvable(e) {
			continue
		}
		for _, kv := range carried(keys, e, contacts[ckey(e.TenantID, e.ExternalID)], created[ckey(e.TenantID, e.ExternalID)], now) {
			want[e.TenantID] = append(want[e.TenantID], kv)
		}
	}
	if len(want) == 0 {
		return evs, nil
	}
	matches := map[int64]map[pg.KeyValue][]model.Contact{}
	for tenant, kvs := range want {
		m, err := is.MatchIdentityKeys(ctx, tenant, kvs)
		if err != nil {
			return nil, fmt.Errorf("match identity keys: %w", err)
		}
		matches[tenant] = m
		for _, cs := range m {
			for _, c := range cs {
				if _, have := contacts[ckey(tenant, c.ExternalID)]; !have {
					contacts[ckey(tenant, c.ExternalID)] = c
				}
			}
		}
	}

	out := make([]event.Event, 0, len(evs))
	var reroute []*kgo.Record
	movedTo := map[string]model.Contact{} // contact key -> the contact its events went to
	for i := range evs {
		e := evs[i]
		k := ckey(e.TenantID, e.ExternalID)
		if to, gone := movedTo[k]; gone {
			// The rest of a moved contact's events in this batch follow it, in order.
			recs, err := rerouted(e, to, nil)
			if err != nil {
				return nil, err
			}
			reroute = append(reroute, recs...)
			continue
		}
		keys := keysOf[e.TenantID]
		if keys == nil || !resolvable(&e) {
			out = append(out, e)
			continue
		}
		own := contacts[k]
		ranked := rank(keys, &e, own, isNew(own, created[k], now), matches[e.TenantID])
		if len(ranked) == 0 || ranked[0].c.ID == own.ID || e.Rerouted {
			// Merge every other matching contact into the event's own one.
			for _, r := range ranked {
				if r.c.ID == own.ID {
					continue
				}
				out = append(out, event.Event{
					ID: e.ID + ":id:" + r.c.ExternalID, TenantID: e.TenantID, ExternalID: e.ExternalID, Name: event.MergeRequested, Ts: e.Ts,
					Props: map[string]any{"loser": r.c.ExternalID, "source": "identity", "key": r.field},
				})
			}
			out = append(out, e)
			continue
		}
		win := ranked[0]
		movedTo[k] = win.c
		// What the event's own external id says (an email or phone id) goes along with it.
		if fill := identity.Fill(own.Attrs, &e); fill != nil {
			attrs := maps.Clone(e.Attrs)
			if attrs == nil {
				attrs = map[string]any{}
			}
			for f, v := range fill {
				if _, set := attrs[f]; !set {
					attrs[f] = v
				}
			}
			e.Attrs = attrs
		}
		recs, err := rerouted(e, win.c, &event.Event{
			ID: e.ID + ":id", TenantID: e.TenantID, ExternalID: win.c.ExternalID, Name: event.MergeRequested, Ts: e.Ts,
			Props: map[string]any{"loser": e.ExternalID, "source": "identity", "key": win.field}, Rerouted: true,
		})
		if err != nil {
			return nil, err
		}
		reroute = append(reroute, recs...)
	}
	if len(reroute) > 0 {
		if err := s.pub.Publish(ctx, reroute...); err != nil {
			return nil, fmt.Errorf("publish rerouted events: %w", err)
		}
	}
	return out, nil
}

// resolvable: events that take part in identity matching. Merges themselves don't, and neither do
// browser / app SDK events: whatever email a page sends is unproven, and a browser may never merge
// a known contact (the same rule as identify's anonymous_id). Anonymous visitors (anon-…, e.g. a
// public form) don't either: their merge into a known contact is the identify's job.
func resolvable(e *event.Event) bool {
	return !e.FromBrowser && !strings.HasPrefix(e.ExternalID, "anon-") && e.Name != event.MergeRequested && e.Name != event.ContactMerged && e.Name != event.ContactMergedInto
}

// carried lists the merging key values an event brings that need a lookup: all of them for a new
// contact, otherwise only values the contact doesn't already hold (a value it holds was matched
// when it got it).
func carried(keys []identity.Key, e *event.Event, own model.Contact, created bool, now time.Time) []pg.KeyValue {
	fresh := isNew(own, created, now)
	var out []pg.KeyValue
	for _, k := range keys {
		if k.Field == identity.ExternalID || k.Mode != identity.Merge {
			continue
		}
		v := identity.FromEvent(k.Field, e)
		if v == "" || !fresh && identity.FromContact(k.Field, own.ExternalID, own.Attrs) == v {
			continue
		}
		out = append(out, pg.KeyValue{Field: k.Field, Value: v})
	}
	return out
}

type ranked struct {
	c     model.Contact
	field string // the key it was matched by
}

// rank lists the contacts an event matches, best first, each once: its own contact at the external
// id's place (unless new), and for every merging key the contacts holding the event's value, oldest
// first.
func rank(keys []identity.Key, e *event.Event, own model.Contact, fresh bool, matches map[pg.KeyValue][]model.Contact) []ranked {
	var out []ranked
	seen := map[uint32]bool{}
	for _, k := range keys {
		if k.Field == identity.ExternalID {
			if !fresh && !seen[own.ID] {
				seen[own.ID] = true
				out = append(out, ranked{c: own, field: identity.ExternalID})
			}
			continue
		}
		if k.Mode != identity.Merge {
			continue
		}
		v := identity.FromEvent(k.Field, e)
		if v == "" {
			continue
		}
		for _, c := range matches[pg.KeyValue{Field: k.Field, Value: v}] {
			// Anonymous visitors are never matched: their values come from browsers, so anyone
			// could plant someone else's email on one.
			if !seen[c.ID] && c.Attrs[event.AttrMergedInto] == nil && !strings.HasPrefix(c.ExternalID, "anon-") {
				seen[c.ID] = true
				out = append(out, ranked{c: c, field: k.Field})
			}
		}
	}
	return out
}

// rerouted are the records that send an event (after an optional merge) to another contact.
func rerouted(e event.Event, to model.Contact, first *event.Event) ([]*kgo.Record, error) {
	var out []*kgo.Record
	if first != nil {
		r, err := kafka.JSON(event.TopicRaw, first.RawKey(), first)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	e.ExternalID, e.Rerouted = to.ExternalID, true
	e.ContactID, e.NewContact, e.ChangedAttrs, e.Profile = 0, false, nil, nil
	r, err := kafka.JSON(event.TopicRaw, e.RawKey(), &e)
	if err != nil {
		return nil, err
	}
	return append(out, r), nil
}

// saveKeys stores the key values of the batch's contacts whose values changed (or that were merged
// away: they hold none).
func (s *Service) saveKeys(ctx context.Context, dirty map[string]bool, contacts map[string]model.Contact, profile func(string) map[string]any) error {
	is, ok := s.store.(IdentityStore)
	if !ok {
		return nil
	}
	byTenant := map[int64][]pg.ContactKeys{}
	fields := map[int64][]string{}
	for k := range dirty {
		c := contacts[k]
		keys := s.identityKeys(ctx, c.TenantID)
		if len(keys) <= 1 {
			continue
		}
		if _, ok := fields[c.TenantID]; !ok {
			for _, key := range keys {
				if key.Field != identity.ExternalID {
					fields[c.TenantID] = append(fields[c.TenantID], key.Field)
				}
			}
		}
		after := profile(k)
		var now map[string]string
		if after[event.AttrMergedInto] == nil {
			now = identity.Values(keys, c.ExternalID, after)
		}
		var before map[string]string
		if c.Attrs[event.AttrMergedInto] == nil {
			before = identity.Values(keys, c.ExternalID, c.Attrs)
		}
		// A contact made in this batch has no rows yet even when its values look unchanged.
		if identity.SameValues(before, now) && len(c.Attrs) > 0 {
			continue
		}
		byTenant[c.TenantID] = append(byTenant[c.TenantID], pg.ContactKeys{ContactID: c.ID, Values: now})
	}
	for tenant, cs := range byTenant {
		if err := is.ReplaceContactKeys(ctx, tenant, fields[tenant], cs); err != nil {
			return err
		}
	}
	return nil
}
