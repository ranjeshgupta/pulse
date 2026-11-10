// Package profile resolves identities and maintains contact profiles. It consumes events.raw and
// produces events.enriched, so every later stage sees a dense contact id and the merged profile.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
)

const (
	dedupeTTL = 24 * time.Hour
	// A contact this young is still treated as new, so a batch retried after a partial failure
	// still evaluates all of its segments. Re-evaluating is idempotent; only changes are published.
	newContactWindow = time.Minute
	scoringTTL       = 5 * time.Minute // full reload even if a version bump was missed
	scoringCheck     = time.Second     // how often the version key is read
	// ScoringVersionKey is bumped by the API whenever point rules change, so new rules apply to
	// the next events instead of after a cache expiry.
	ScoringVersionKey = "scoring:v"
	maxTags           = 100
)

type Store interface {
	ResolveContacts(ctx context.Context, tenant int64, externalIDs []string) (map[string]model.Contact, map[string]bool, error)
	LookupContacts(ctx context.Context, tenant int64, externalIDs []string) (map[string]model.Contact, error)
	SaveContactsAttrs(ctx context.Context, updates []pg.AttrUpdate) error
	PointRules(ctx context.Context) (map[int64]map[string]int, error)
}

type Service struct {
	store Store
	rdb   redis.Cmdable
	pub   kafka.Publisher
	now   func() time.Time

	mu        sync.Mutex
	points    map[int64]map[string]int // tenant -> event -> points
	pointsAt  time.Time
	pointsVer string
	checkedAt time.Time
}

func New(store Store, rdb redis.Cmdable, pub kafka.Publisher) *Service {
	return &Service{store: store, rdb: rdb, pub: pub, now: time.Now}
}

// scoring returns the tenants' point rules. They are reloaded when the version key changes
// (checked at most once a second) or every scoringTTL. If a reload fails the previous rules stay.
func (s *Service) scoring(ctx context.Context) map[int64]map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	stale := s.points == nil || now.Sub(s.pointsAt) > scoringTTL
	if !stale && now.Sub(s.checkedAt) >= scoringCheck {
		s.checkedAt = now
		if v, err := s.rdb.Get(ctx, ScoringVersionKey).Result(); err == nil || errors.Is(err, redis.Nil) {
			stale = v != s.pointsVer
		}
	}
	if stale {
		v, _ := s.rdb.Get(ctx, ScoringVersionKey).Result() // read first: a bump during the load reloads again
		if rules, err := s.store.PointRules(ctx); err == nil {
			s.points, s.pointsVer = rules, v
		}
		s.pointsAt, s.checkedAt = now, now
	}
	return s.points
}

// Merge applies an event's attributes (and email) to the current profile. It returns the merged
// profile and the keys whose canonical value changed; cur is not modified.
// Consent events (unsubscribed, resubscribed) also set the contact's opt-out attributes, tag events
// change tags, and points_changed adjusts points.
func Merge(cur map[string]any, e *event.Event) (map[string]any, []string) {
	return MergeScored(cur, e, 0)
}

// MergeScored is Merge plus points from the tenant's scoring rules for this event.
func MergeScored(cur map[string]any, e *event.Event, points int) (map[string]any, []string) {
	optouts := consent.Updates(e.Name, e.Props)
	updates := make(map[string]any, len(e.Attrs)+len(optouts)+2)
	for k, v := range e.Attrs {
		if k != event.AttrMergedInto {
			updates[k] = v
		}
	}
	if e.Email != "" {
		updates["email"] = e.Email
	}
	for k, v := range optouts {
		updates[k] = v
	}
	if v, ok := updates[event.AttrTags]; ok {
		updates[event.AttrTags] = NormalizeTags(v)
	}
	if e.Name == event.TagAdded || e.Name == event.TagRemoved {
		base, ok := updates[event.AttrTags]
		if !ok {
			base = cur[event.AttrTags]
		}
		updates[event.AttrTags] = applyTags(NormalizeTags(base), NormalizeTags(eventTags(e.Props)), e.Name == event.TagAdded)
	}
	if e.Name == event.PointsChanged {
		if d, ok := attr.Number(attr.String(e.Props["delta"])); ok {
			points += int(d)
		}
	}
	if points != 0 {
		base, ok := updates[event.AttrPoints]
		if !ok {
			base = cur[event.AttrPoints]
		}
		n, _ := attr.Number(attr.String(base))
		updates[event.AttrPoints] = float64(int64(n) + int64(points))
	}
	merged := make(map[string]any, len(cur)+len(updates))
	for k, v := range cur {
		merged[k] = v
	}
	var changed []string
	for k, v := range updates {
		old, had := cur[k]
		if !had || attr.String(old) != attr.String(v) {
			changed = append(changed, k)
		}
		merged[k] = v
	}
	sort.Strings(changed)
	return merged, changed
}

// NormalizeTags turns a tags value (a JSON list, or text separated by commas or semicolons) into a
// sorted list of distinct valid tags. Invalid entries are dropped.
func NormalizeTags(v any) []any {
	var raw []string
	switch x := v.(type) {
	case []any:
		for _, t := range x {
			raw = append(raw, attr.String(t))
		}
	case []string:
		raw = x
	case string:
		if strings.HasPrefix(strings.TrimSpace(x), "[") {
			return NormalizeTags(segment.Tags(map[string]any{event.AttrTags: x}))
		}
		raw = strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == ';' })
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if segment.ValidTag(t) && !seen[t] && len(out) < maxTags {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	list := make([]any, len(out))
	for i, t := range out {
		list[i] = t
	}
	return list
}

func eventTags(props map[string]any) any {
	if v, ok := props["tags"]; ok {
		return v
	}
	return props["tag"]
}

func applyTags(cur, change []any, add bool) []any {
	set := map[string]bool{}
	for _, t := range cur {
		set[t.(string)] = true
	}
	for _, t := range change {
		set[t.(string)] = add
	}
	var out []any
	for _, t := range cur {
		if set[t.(string)] {
			out = append(out, t)
			delete(set, t.(string))
		}
	}
	for _, t := range change {
		if set[t.(string)] {
			out = append(out, t)
			delete(set, t.(string))
		}
	}
	return NormalizeTags(out)
}

// MergeIdentity folds an anonymous visitor's profile into a known contact's. The known contact's
// values win, tags are combined and points are added.
func MergeIdentity(anon, known map[string]any) map[string]any {
	out := make(map[string]any, len(anon)+len(known))
	for k, v := range anon {
		if k != event.AttrMergedInto {
			out[k] = v
		}
	}
	for k, v := range known {
		out[k] = v
	}
	if anon[event.AttrTags] != nil || known[event.AttrTags] != nil {
		out[event.AttrTags] = applyTags(NormalizeTags(known[event.AttrTags]), NormalizeTags(anon[event.AttrTags]), true)
	}
	a, aok := attr.Number(attr.String(anon[event.AttrPoints]))
	k, kok := attr.Number(attr.String(known[event.AttrPoints]))
	if aok || kok {
		out[event.AttrPoints] = a + k
	}
	return out
}

// merge is one identity merge found in a batch.
type merge struct {
	anonKey, knownKey string
	source            *event.Event // the identify event
	anonAttrs         []string     // keys the anonymous profile brought
}

// Handle processes a single record (kept for callers and tests that work record by record).
func (s *Service) Handle(ctx context.Context, r *kgo.Record) error {
	return s.HandleBatch(ctx, []*kgo.Record{r})
}

// HandleBatch processes one partition's records in order with a fixed number of round trips:
// one Redis read, one contact lookup per tenant (plus one insert batch), one profile write batch,
// two Redis writes for counters, one Kafka publish and one Redis write to mark events done.
//
// Redelivery is safe: "seen" skips events already published, and the counter has its own guard,
// so a retry after a partial failure never double counts.
func (s *Service) HandleBatch(ctx context.Context, recs []*kgo.Record) error {
	evs := make([]event.Event, 0, len(recs))
	for _, r := range recs {
		var e event.Event
		if err := json.Unmarshal(r.Value, &e); err != nil {
			return fmt.Errorf("decode event: %w", err)
		}
		evs = append(evs, e)
	}

	// 1. Drop events that were already published.
	pipe := s.rdb.Pipeline()
	seen := make([]*redis.IntCmd, len(evs))
	for i, e := range evs {
		seen[i] = pipe.Exists(ctx, seenKey(e))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	fresh := evs[:0]
	for i, e := range evs {
		if seen[i].Val() == 0 {
			fresh = append(fresh, e)
		}
	}
	evs = fresh
	if len(evs) == 0 {
		return nil
	}

	// 2. Resolve every contact in the batch, one query per tenant.
	byTenant := map[int64][]string{}
	want := map[string]bool{}
	for _, e := range evs {
		if k := ckey(e.TenantID, e.ExternalID); !want[k] {
			want[k] = true
			byTenant[e.TenantID] = append(byTenant[e.TenantID], e.ExternalID)
		}
	}
	contacts := map[string]model.Contact{}
	created := map[string]bool{}
	for tenant, exts := range byTenant {
		found, made, err := s.store.ResolveContacts(ctx, tenant, exts)
		if err != nil {
			return fmt.Errorf("resolve contacts: %w", err)
		}
		for ext, c := range found {
			contacts[ckey(tenant, ext)] = c
		}
		for ext := range made {
			created[ckey(tenant, ext)] = true
		}
	}
	if err := s.redirectMerged(ctx, evs, contacts); err != nil {
		return err
	}
	if err := s.lookupAnonymous(ctx, evs, contacts); err != nil {
		return err
	}

	// 3. Fold the events in order, keeping each contact's profile in memory.
	now := s.now()
	scoring := s.scoring(ctx)
	profiles := map[string]map[string]any{}
	profile := func(k string) map[string]any {
		if p, ok := profiles[k]; ok {
			return p
		}
		return contacts[k].Attrs
	}
	dirty := map[string]bool{}
	firstSeen := map[string]bool{}
	var merges []merge
	for i := range evs {
		e := &evs[i]
		k := ckey(e.TenantID, e.ExternalID)
		c := contacts[k]
		cur := profile(k)
		var fromAnon []string
		if ak, ok := anonymousKey(e); ok {
			if a, found := contacts[ak]; found && a.ID != c.ID && profile(ak)[event.AttrMergedInto] == nil {
				anon := profile(ak)
				cur = MergeIdentity(anon, cur)
				for key := range anon {
					fromAnon = append(fromAnon, key)
				}
				marked := make(map[string]any, len(anon)+1)
				for key, v := range anon {
					marked[key] = v
				}
				marked[event.AttrMergedInto] = e.ExternalID
				profiles[ak], dirty[ak] = marked, true
				merges = append(merges, merge{anonKey: ak, knownKey: k, source: e, anonAttrs: fromAnon})
			}
		}
		merged, changed := MergeScored(cur, e, scoring[e.TenantID][e.Name])
		profiles[k] = merged
		if len(changed) > 0 || len(fromAnon) > 0 {
			dirty[k] = true
		}
		e.ContactID, e.ChangedAttrs, e.Profile = c.ID, changed, merged
		if !firstSeen[k] {
			firstSeen[k] = true
			e.NewContact = created[k] || now.Sub(c.CreatedAt) < newContactWindow
		}
	}

	// 3b. Identity merges: move the visitor's counters first (a retry finds nothing left to move),
	// then record both sides so the sink copies the history and segments re-evaluate.
	for _, m := range merges {
		a, kc := contacts[m.anonKey], contacts[m.knownKey]
		if err := segment.MoveCounters(ctx, s.rdb, a.TenantID, a.ID, kc.ID); err != nil {
			return fmt.Errorf("move counters: %w", err)
		}
		changed := append([]string(nil), m.anonAttrs...)
		sort.Strings(changed)
		evs = append(evs,
			event.Event{
				ID: m.source.ID + ":merged", TenantID: kc.TenantID, ExternalID: kc.ExternalID, Name: event.ContactMerged, Ts: m.source.Ts,
				Props:     map[string]any{"from_contact_id": a.ID, "from_external_id": a.ExternalID},
				ContactID: kc.ID, NewContact: true, ChangedAttrs: changed, Profile: profiles[m.knownKey],
			},
			event.Event{
				ID: m.source.ID + ":merged_into", TenantID: a.TenantID, ExternalID: a.ExternalID, Name: event.ContactMergedInto, Ts: m.source.Ts,
				Props:     map[string]any{"into_external_id": kc.ExternalID},
				ContactID: a.ID, ChangedAttrs: []string{event.AttrMergedInto}, Profile: profiles[m.anonKey],
			})
	}

	// 4. Persist changed profiles in one round trip.
	updates := make([]pg.AttrUpdate, 0, len(dirty))
	for k := range dirty {
		c, attrs := contacts[k], profile(k)
		updates = append(updates, pg.AttrUpdate{TenantID: c.TenantID, ID: c.ID, Email: attr.String(attrs["email"]), Attrs: attrs})
	}
	if err := s.store.SaveContactsAttrs(ctx, updates); err != nil {
		return fmt.Errorf("save attrs: %w", err)
	}

	// 5. Count each event exactly once: claim the guard, then increment for the claims we won.
	pipe = s.rdb.Pipeline()
	claims := make([]*redis.BoolCmd, len(evs))
	for i, e := range evs {
		claims[i] = pipe.SetNX(ctx, countedKey(e), 1, dedupeTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	pipe = s.rdb.Pipeline()
	var won []string
	for i, e := range evs {
		if claims[i].Val() {
			segment.QueueIncr(ctx, pipe, e.TenantID, e.ContactID, e.Name, e.Ts)
			won = append(won, countedKey(e))
		}
	}
	if len(won) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			s.rdb.Del(ctx, won...) // let the retry count them
			return fmt.Errorf("count events: %w", err)
		}
	}

	// 6. Publish the whole batch at once.
	out := make([]*kgo.Record, len(evs))
	for i, e := range evs {
		rec, err := kafka.JSON(event.TopicEnriched, event.ContactKey(e.TenantID, e.ContactID), e)
		if err != nil {
			return err
		}
		out[i] = rec
	}
	if err := s.pub.Publish(ctx, out...); err != nil {
		return fmt.Errorf("publish enriched: %w", err)
	}

	// 7. Mark everything done.
	pipe = s.rdb.Pipeline()
	for _, e := range evs {
		pipe.Set(ctx, seenKey(e), 1, dedupeTTL)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// redirectMerged points events for a visitor who was already merged at the known contact.
func (s *Service) redirectMerged(ctx context.Context, evs []event.Event, contacts map[string]model.Contact) error {
	targets := map[int64][]string{}
	for i := range evs {
		into := attr.String(contacts[ckey(evs[i].TenantID, evs[i].ExternalID)].Attrs[event.AttrMergedInto])
		if into != "" {
			if _, ok := contacts[ckey(evs[i].TenantID, into)]; !ok {
				targets[evs[i].TenantID] = append(targets[evs[i].TenantID], into)
			}
		}
	}
	for tenant, exts := range targets {
		found, _, err := s.store.ResolveContacts(ctx, tenant, exts)
		if err != nil {
			return fmt.Errorf("resolve merged contacts: %w", err)
		}
		for ext, c := range found {
			contacts[ckey(tenant, ext)] = c
		}
	}
	for i := range evs {
		e := &evs[i]
		if into := attr.String(contacts[ckey(e.TenantID, e.ExternalID)].Attrs[event.AttrMergedInto]); into != "" {
			e.ExternalID = into
		}
	}
	return nil
}

// lookupAnonymous loads the visitors named by identify events (without creating any).
func (s *Service) lookupAnonymous(ctx context.Context, evs []event.Event, contacts map[string]model.Contact) error {
	want := map[int64][]string{}
	for i := range evs {
		if k, ok := anonymousKey(&evs[i]); ok {
			if _, have := contacts[k]; !have {
				want[evs[i].TenantID] = append(want[evs[i].TenantID], attr.String(evs[i].Props["anonymous_id"]))
			}
		}
	}
	for tenant, exts := range want {
		found, err := s.store.LookupContacts(ctx, tenant, exts)
		if err != nil {
			return fmt.Errorf("lookup visitors: %w", err)
		}
		for ext, c := range found {
			contacts[ckey(tenant, ext)] = c
		}
	}
	return nil
}

// anonymousKey returns the visitor an identify event merges, if any.
func anonymousKey(e *event.Event) (string, bool) {
	if e.Name != event.Identify {
		return "", false
	}
	aid := attr.String(e.Props["anonymous_id"])
	if aid == "" || aid == e.ExternalID {
		return "", false
	}
	return ckey(e.TenantID, aid), true
}

func ckey(tenant int64, ext string) string { return strconv.FormatInt(tenant, 10) + ":" + ext }
func seenKey(e event.Event) string         { return fmt.Sprintf("evt:%d:%s", e.TenantID, e.ID) }
func countedKey(e event.Event) string      { return fmt.Sprintf("evc:%d:%s", e.TenantID, e.ID) }
