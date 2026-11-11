// Package profile resolves identities and maintains contact profiles. It consumes events.raw and
// produces events.enriched, so every later stage sees a dense contact id and the merged profile.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/eventschema"
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
	// ScoringVersionKey is bumped by the API whenever point rules or lifecycle stages change, so
	// they apply to the next events instead of after a cache expiry.
	ScoringVersionKey = "scoring:v"
	maxTags           = 100
	maxListValues     = 200 // values an event schema keeps in a contact list field
)

type Store interface {
	ResolveContacts(ctx context.Context, tenant int64, externalIDs []string) (map[string]model.Contact, map[string]bool, error)
	LookupContacts(ctx context.Context, tenant int64, externalIDs []string) (map[string]model.Contact, error)
	SaveContactsAttrs(ctx context.Context, updates []pg.AttrUpdate) error
	PointRules(ctx context.Context) (map[int64]map[string]int, error)
	// StageRules returns every tenant's stages in order (first stage first).
	StageRules(ctx context.Context) (map[int64][]pg.Stage, error)
}

type Service struct {
	store Store
	rdb   redis.Cmdable
	pub   kafka.Publisher
	now   func() time.Time

	// Schemas, when set, checks events against their event schemas (counting problems) and
	// applies their "add to list field" properties.
	Schemas *eventschema.Cache
	Log     *slog.Logger

	mu        sync.Mutex
	points    map[int64]map[string]int // tenant -> event -> points
	stages    map[int64][]pg.Stage     // tenant -> stages in order
	pointsAt  time.Time
	pointsVer string
	checkedAt time.Time
}

func New(store Store, rdb redis.Cmdable, pub kafka.Publisher) *Service {
	return &Service{store: store, rdb: rdb, pub: pub, now: time.Now}
}

// scoring returns the tenants' point rules.
func (s *Service) scoring(ctx context.Context) map[int64]map[string]int {
	points, _ := s.rules(ctx)
	return points
}

// rules returns the tenants' point rules and stages. They are reloaded when the version key
// changes (checked at most once a second) or every scoringTTL. If a reload fails the previous
// rules stay.
func (s *Service) rules(ctx context.Context) (map[int64]map[string]int, map[int64][]pg.Stage) {
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
		rules, err := s.store.PointRules(ctx)
		if err == nil {
			var stages map[int64][]pg.Stage
			if stages, err = s.store.StageRules(ctx); err == nil {
				s.points, s.stages, s.pointsVer = rules, stages, v
			}
		}
		s.pointsAt, s.checkedAt = now, now
	}
	return s.points, s.stages
}

// Merge applies an event's attributes (and email) to the current profile. It returns the merged
// profile and the keys whose canonical value changed; cur is not modified.
// Consent events (unsubscribed, resubscribed) also set the contact's opt-out attributes, tag events
// change tags, and points_changed adjusts points.
func Merge(cur map[string]any, e *event.Event) (map[string]any, []string) {
	return MergeScored(cur, e, 0)
}

// MergeScored is Merge plus points from the tenant's scoring rules for this event.
// Stage and company events set the stage and company attributes; attributes sent with an event
// can't (see event.EventOnlyAttr).
func MergeScored(cur map[string]any, e *event.Event, points int) (map[string]any, []string) {
	optouts := consent.Updates(e.Name, e.Props)
	updates := make(map[string]any, len(e.Attrs)+len(optouts)+2)
	for k, v := range e.Attrs {
		if event.EventOnlyAttr(k) || event.SetOnceAttr(k) && attr.String(cur[k]) != "" {
			continue
		}
		updates[k] = v
	}
	var removes []string
	switch e.Name {
	case event.StageChanged:
		stageUpdates(cur, e, updates)
	case event.CompanyLinked, event.CompanyUpdated, event.CompanyUnlinked:
		removes = companyUpdates(cur, e, updates)
	case event.OwnerChanged:
		removes = ownerUpdates(e, updates)
	}
	if event.CarriesDevice(e.Name) {
		removes = append(removes, deviceUpdates(e.Props, updates)...)
	}
	if e.Email != "" {
		updates["email"] = e.Email
	}
	if v, ok := updates["email"]; ok {
		if d := EmailDomain(attr.String(v)); d != "" {
			updates[event.AttrEmailDomain] = d
		} else {
			removes = append(removes, event.AttrEmailDomain)
		}
	}
	// The phone's country, when the number is written with its country code. A contact whose
	// phone predates this attribute gets it at its next event (only international numbers are
	// tried, so a national number costs nothing on later events).
	if v, ok := updates["phone"]; ok {
		if cc := PhoneCountry(attr.String(v)); cc != "" {
			updates[event.AttrPhoneCountry] = cc
		} else {
			removes = append(removes, event.AttrPhoneCountry)
		}
	} else if _, has := cur[event.AttrPhoneCountry]; !has && internationalPhone(attr.String(cur["phone"])) {
		if cc := PhoneCountry(attr.String(cur["phone"])); cc != "" {
			updates[event.AttrPhoneCountry] = cc
		}
	}
	// A new phone number hasn't bounced (unless this event says so itself).
	if v, ok := updates["phone"]; ok && attr.String(v) != attr.String(cur["phone"]) && truthy(cur[event.AttrSMSBounced]) {
		if _, set := updates[event.AttrSMSBounced]; !set {
			updates[event.AttrSMSBounced] = false
		}
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
	for _, k := range removes {
		if _, set := updates[k]; set {
			continue
		}
		if _, had := merged[k]; had {
			delete(merged, k)
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return merged, changed
}

// maxDeviceValue is the longest device property kept on a profile.
const maxDeviceValue = 100

// deviceUpdates copies the device properties of an SDK event to the last-seen device attributes.
// An event that names a device (device_type or os) replaces the whole device: attributes it
// doesn't carry are removed, so a model never sticks to the next device's profile. The locale
// property sets locale.
func deviceUpdates(props map[string]any, updates map[string]any) []string {
	if loc := deviceValue(props["locale"]); loc != "" {
		if _, set := updates[event.AttrLocale]; !set {
			updates[event.AttrLocale] = loc
		}
	}
	if deviceValue(props["device_type"]) == "" && deviceValue(props["os"]) == "" {
		return nil
	}
	var removes []string
	for prop, key := range event.DeviceProps {
		if v := deviceValue(props[prop]); v != "" {
			updates[key] = v
		} else {
			removes = append(removes, key)
		}
	}
	return removes
}

func deviceValue(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) > maxDeviceValue {
		s = s[:maxDeviceValue]
	}
	return s
}

// ownerUpdates applies owner_changed: owner_id and owner_name, or removes both when unassigned.
func ownerUpdates(e *event.Event, updates map[string]any) []string {
	id, ok := attr.Number(attr.String(e.Props["owner_id"]))
	if !ok || id <= 0 {
		return []string{event.AttrOwnerID, event.AttrOwnerName}
	}
	updates[event.AttrOwnerID] = id
	updates[event.AttrOwnerName] = attr.String(e.Props["owner_name"])
	return nil
}

// EmailDomain is the lower-case domain of an email address, or "" when it has none.
func EmailDomain(email string) string {
	i := strings.LastIndexByte(email, '@')
	if i < 0 || i == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[i+1:]))
}

// NormalizeConsent records how an unsubscribe happened (props.method: self or manual, see
// consent.Method), so segments can tell a contact's own unsubscribe from one made for them.
func NormalizeConsent(e *event.Event) {
	if e.Name != consent.Unsubscribed {
		return
	}
	if m := attr.String(e.Props["method"]); m == consent.MethodSelf || m == consent.MethodManual {
		return
	}
	if e.Props == nil {
		e.Props = map[string]any{}
	}
	e.Props["method"] = consent.Method(attr.String(e.Props["source"]))
}

// MaxStageName is the longest stage name.
const MaxStageName = 64

// ValidStageName reports whether n can name a lifecycle stage: 1-64 characters, no control
// characters, no surrounding spaces.
func ValidStageName(n string) bool {
	if n == "" || len(n) > MaxStageName || strings.TrimSpace(n) != n {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// stageUpdates applies a stage_changed event: the new stage and the time it changed. Moving to the
// stage the contact is already in changes nothing. A rename changes the name only, and only for a
// contact still in the old stage.
func stageUpdates(cur map[string]any, e *event.Event, updates map[string]any) {
	st := attr.String(e.Props["stage"])
	now := attr.String(cur[event.AttrStage])
	if !ValidStageName(st) || st == now {
		return
	}
	if rename, _ := e.Props["rename"].(bool); rename {
		if now != "" && now == attr.String(e.Props["from"]) {
			updates[event.AttrStage] = st
		}
		return
	}
	updates[event.AttrStage] = st
	updates[event.AttrStageChangedAt] = e.Ts.UTC().Format(time.RFC3339)
}

// companyUpdates applies company_linked, company_updated and company_unlinked: it sets company_id,
// company_name and a company_<key> copy of each company attribute, and returns the company keys to
// remove (copies the new company doesn't have, or all of them when unlinking).
func companyUpdates(cur map[string]any, e *event.Event, updates map[string]any) []string {
	id, ok := attr.Number(attr.String(e.Props["company_id"]))
	linked := attr.String(cur[event.AttrCompanyID])
	var old []string
	for k := range cur {
		if strings.HasPrefix(k, event.CompanyAttrPrefix) {
			old = append(old, k)
		}
	}
	switch e.Name {
	case event.CompanyUnlinked:
		// Only the company named (if any) is unlinked: the contact may have moved on since.
		if e.Props["company_id"] != nil && (!ok || attr.String(id) != linked) {
			return nil
		}
		return old
	case event.CompanyUpdated:
		if !ok || attr.String(id) != linked {
			return nil
		}
	}
	if !ok || id <= 0 {
		return nil
	}
	updates[event.AttrCompanyID] = id
	updates[event.AttrCompanyName] = attr.String(e.Props["name"])
	if attrs, isMap := e.Props["attrs"].(map[string]any); isMap {
		for k, v := range attrs {
			if key := event.CompanyAttrPrefix + k; attr.ValidKey(key) && key != event.AttrCompanyID && key != event.AttrCompanyName {
				updates[key] = v
			}
		}
	}
	return old
}

// StageForPoints returns the stage a points change moves the contact to, or "" for none. When the
// change crosses a stage's min_points threshold upward, the contact goes to the furthest stage
// whose threshold they reach, but never back: only to a stage after their current one. stages are
// in order; a current stage that no longer exists counts as before every stage.
func StageForPoints(stages []pg.Stage, current string, before, after float64) string {
	if after <= before {
		return ""
	}
	crossed, best, at := false, -1, -1
	for i, st := range stages {
		if st.Name == current {
			at = i
		}
		if st.MinPoints == nil {
			continue
		}
		t := float64(*st.MinPoints)
		if before < t && t <= after {
			crossed = true
		}
		if t <= after {
			best = i
		}
	}
	if !crossed || best < 0 || best <= at {
		return ""
	}
	return stages[best].Name
}

func pointsOf(p map[string]any) float64 {
	n, _ := attr.Number(attr.String(p[event.AttrPoints]))
	return n
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

// MergeIdentity folds a profile (an anonymous visitor, or a duplicate) into a known contact's. The
// known contact's values win, tags are combined, points are added, first_seen is the earlier and
// last_seen the later of the two. A known contact with a company keeps only its own company's
// attributes.
func MergeIdentity(anon, known map[string]any) map[string]any {
	out := make(map[string]any, len(anon)+len(known))
	_, hasCompany := known[event.AttrCompanyID]
	for k, v := range anon {
		if k == event.AttrMergedInto || hasCompany && strings.HasPrefix(k, event.CompanyAttrPrefix) {
			continue
		}
		out[k] = v
	}
	for k, v := range known {
		out[k] = v
	}
	if a, k := attr.String(anon[event.AttrFirstSeen]), attr.String(known[event.AttrFirstSeen]); a != "" && (k == "" || a < k) {
		out[event.AttrFirstSeen] = a
	}
	if a, k := attr.String(anon[event.AttrLastSeen]), attr.String(known[event.AttrLastSeen]); a > k {
		out[event.AttrLastSeen] = a
	}
	if a, k := attr.String(anon[event.AttrDateAdded]), attr.String(known[event.AttrDateAdded]); a != "" && (k == "" || a < k) {
		out[event.AttrDateAdded] = a
	}
	if anon[event.AttrTags] != nil || known[event.AttrTags] != nil {
		out[event.AttrTags] = applyTags(NormalizeTags(known[event.AttrTags]), NormalizeTags(anon[event.AttrTags]), true)
	}
	a, aok := attr.Number(attr.String(anon[event.AttrPoints]))
	k, kok := attr.Number(attr.String(known[event.AttrPoints]))
	if aok || kok {
		out[event.AttrPoints] = a + k
	}
	// Opt-outs and hard bounces are sticky: if either profile has one, the merged contact keeps
	// it, so merging never starts messages to someone who unsubscribed or whose address bounced.
	for _, key := range stickyFlags() {
		if truthy(anon[key]) || truthy(known[key]) {
			out[key] = true
		}
	}
	return out
}

func stickyFlags() []string {
	keys := []string{consent.Key(consent.All), consent.AttrEmailBounced, consent.AttrSMSBounced}
	for _, ch := range channel.All {
		keys = append(keys, consent.Key(ch))
	}
	return keys
}

func truthy(v any) bool {
	s := attr.String(v)
	return s == "true" || s == "1" || s == "yes"
}

// merge is one identity merge found in a batch (anonymous visitor or duplicate contact).
type merge struct {
	anonKey, knownKey string
	source            *event.Event // the identify event
	anonAttrs         []string     // keys the anonymous profile brought
}

// Handle processes a single record (kept for callers and tests that work record by record).
// StampSeen keeps first_seen (the first event's time) and last_seen (the UTC date of the latest
// event, so it changes at most once a day per contact) on the profile, for "first seen" and
// "last active" segment rules.
func StampSeen(p map[string]any, changed []string, ts time.Time) (map[string]any, []string) {
	day := ts.UTC().Format(time.DateOnly)
	_, hasFirst := p[event.AttrFirstSeen]
	last, _ := p[event.AttrLastSeen].(string)
	if hasFirst && last >= day {
		return p, changed
	}
	out := make(map[string]any, len(p)+2)
	for k, v := range p {
		out[k] = v
	}
	if !hasFirst {
		out[event.AttrFirstSeen] = ts.UTC().Format(time.RFC3339)
		changed = append(changed, event.AttrFirstSeen)
	}
	if last < day {
		out[event.AttrLastSeen] = day
		changed = append(changed, event.AttrLastSeen)
	}
	return out, changed
}

// dateOnlyKeys never make date_modified move: they change on their own (every day, or once).
var dateOnlyKeys = map[string]bool{event.AttrFirstSeen: true, event.AttrLastSeen: true, event.AttrDateAdded: true,
	event.AttrDateIdentified: true, event.AttrDateModified: true}

// StampDates keeps date_added (when the contact was created: created, or ts if unknown),
// date_identified (the first event at which the contact had a known id or an email) and
// date_modified (the UTC day of the latest attribute change, so it is written at most once a day).
// changed are the keys this event changed so far.
func StampDates(p map[string]any, changed []string, ts, created time.Time, externalID string) (map[string]any, []string) {
	set := map[string]any{}
	if _, ok := p[event.AttrDateAdded]; !ok {
		if created.IsZero() {
			created = ts
		}
		set[event.AttrDateAdded] = created.UTC().Format(time.RFC3339)
	}
	if _, ok := p[event.AttrDateIdentified]; !ok && (!strings.HasPrefix(externalID, "anon-") || attr.String(p["email"]) != "") {
		set[event.AttrDateIdentified] = ts.UTC().Format(time.RFC3339)
	}
	day := ts.UTC().Format(time.DateOnly)
	if attr.String(p[event.AttrDateModified]) < day {
		for _, k := range changed {
			if !dateOnlyKeys[k] {
				set[event.AttrDateModified] = day
				break
			}
		}
	}
	if len(set) == 0 {
		return p, changed
	}
	out := make(map[string]any, len(p)+len(set))
	for k, v := range p {
		out[k] = v
	}
	for k, v := range set {
		out[k] = v
		changed = append(changed, k)
	}
	sort.Strings(changed)
	return out, changed
}

// internationalPhone reports whether a number is written with a country code.
func internationalPhone(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, "+") || strings.HasPrefix(p, "00")
}

// applyListAdds adds an event schema's values to the contact's list fields (deduplicated, the
// newest kept when a list passes maxListValues), through the event's attributes, so the change is
// ordered with the contact's other events like any attribute.
func applyListAdds(cur map[string]any, e *event.Event, adds map[string][]string) {
	for field, vals := range adds {
		if !eventschema.ListTargetAllowed(field) { // a schema saved before the rule tightened
			continue
		}
		base, ok := e.Attrs[field]
		if !ok {
			base = cur[field]
		}
		list := segment.ListValues(base)
		changed := false
		for _, v := range vals {
			if !slices.Contains(list, v) {
				list = append(list, v)
				changed = true
			}
		}
		if !changed {
			continue
		}
		if len(list) > maxListValues {
			list = list[len(list)-maxListValues:]
		}
		out := make([]any, len(list))
		for i, v := range list {
			out[i] = v
		}
		if e.Attrs == nil {
			e.Attrs = map[string]any{}
		}
		e.Attrs[field] = out
	}
}

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
	scoring, stages := s.rules(ctx)
	profiles := map[string]map[string]any{}
	profile := func(k string) map[string]any {
		if p, ok := profiles[k]; ok {
			return p
		}
		return contacts[k].Attrs
	}
	dirty := map[string]bool{}
	firstSeen := map[string]bool{}
	checks := eventschema.Batch{Now: now}
	schemas := map[int64]map[string]*eventschema.Schema{}
	var merges []merge
	after := map[int][]event.Event{} // events the profile role adds after evs[i] (stage moves)
	for i := range evs {
		e := &evs[i]
		k := ckey(e.TenantID, e.ExternalID)
		c := contacts[k]
		cur := profile(k)
		before := cur
		var fromAnon []string
		if ak, ok := mergeKey(e); ok {
			if a, found := contacts[ak]; found && a.ID != c.ID && profile(ak)[event.AttrMergedInto] == nil && cur[event.AttrMergedInto] == nil {
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
		NormalizeConsent(e)
		if s.Schemas != nil {
			ts, ok := schemas[e.TenantID]
			if !ok {
				ts = s.Schemas.Tenant(ctx, e.TenantID)
				schemas[e.TenantID] = ts
			}
			if sch := ts[e.Name]; sch != nil {
				if sch.Mode != eventschema.ModeOff {
					checks.Add(sch, e, sch.Check(e.Props))
				}
				if adds := sch.ListAdds(e.Props); adds != nil {
					applyListAdds(cur, e, adds)
				}
			}
		}
		merged, changed := MergeScored(cur, e, scoring[e.TenantID][e.Name])
		merged, changed = StampSeen(merged, changed, e.Ts)
		merged, changed = StampDates(merged, changed, e.Ts, c.CreatedAt, e.ExternalID)
		if st := StageForPoints(stages[e.TenantID], attr.String(merged[event.AttrStage]), pointsOf(before), pointsOf(merged)); st != "" {
			from := attr.String(merged[event.AttrStage])
			move := event.Event{ID: e.ID + ":stage", TenantID: e.TenantID, ExternalID: e.ExternalID, Name: event.StageChanged, Ts: e.Ts,
				Props: map[string]any{"stage": st, "from": from, "source": "points"}}
			var moved []string
			merged, moved = MergeScored(merged, &move, 0)
			changed = mergeKeys(changed, moved)
			move.ContactID, move.Profile = c.ID, merged
			after[i] = append(after[i], move)
		}
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

	if len(after) > 0 {
		woven := make([]event.Event, 0, len(evs)+len(after))
		for i := range evs {
			woven = append(woven, evs[i])
			woven = append(woven, after[i]...)
		}
		evs = woven
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
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	// 8. Event schema checks are counted best effort: losing them never holds up events.
	if err := checks.Flush(ctx, s.rdb); err != nil && s.Log != nil {
		s.Log.Warn("count event schema checks", "err", err)
	}
	return nil
}

// maxMergeHops bounds how far redirectMerged follows merged_into (a contact merged into one that
// was merged later).
const maxMergeHops = 5

// redirectMerged points events for a contact that was already merged at the contact it was merged
// into, following chains of merges.
func (s *Service) redirectMerged(ctx context.Context, evs []event.Event, contacts map[string]model.Contact) error {
	for hop := 0; hop < maxMergeHops; hop++ {
		moved, err := s.redirectOnce(ctx, evs, contacts)
		if err != nil || !moved {
			return err
		}
	}
	return nil
}

func (s *Service) redirectOnce(ctx context.Context, evs []event.Event, contacts map[string]model.Contact) (bool, error) {
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
			return false, fmt.Errorf("resolve merged contacts: %w", err)
		}
		for ext, c := range found {
			contacts[ckey(tenant, ext)] = c
		}
	}
	moved := false
	for i := range evs {
		e := &evs[i]
		if into := attr.String(contacts[ckey(e.TenantID, e.ExternalID)].Attrs[event.AttrMergedInto]); into != "" && into != e.ExternalID {
			e.ExternalID, moved = into, true
		}
	}
	return moved, nil
}

// lookupAnonymous loads the contacts named by identify and merge_requested events. An identify's
// anonymous visitor (anon-…) that doesn't exist yet is created, so the fold below marks it merged:
// the visitor's own events can reach another partition after the identify, and must then follow
// it to the known contact instead of starting a separate profile. A merge_requested loser is only
// looked up.
func (s *Service) lookupAnonymous(ctx context.Context, evs []event.Event, contacts map[string]model.Contact) error {
	want, create := map[int64][]string{}, map[int64][]string{}
	for i := range evs {
		if k, ok := mergeKey(&evs[i]); ok {
			if _, have := contacts[k]; !have {
				from := mergeSource(&evs[i])
				if evs[i].Name == event.Identify && strings.HasPrefix(from, "anon-") {
					create[evs[i].TenantID] = append(create[evs[i].TenantID], from)
				} else {
					want[evs[i].TenantID] = append(want[evs[i].TenantID], from)
				}
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
	for tenant, exts := range create {
		found, _, err := s.store.ResolveContacts(ctx, tenant, exts)
		if err != nil {
			return fmt.Errorf("resolve visitors: %w", err)
		}
		for ext, c := range found {
			contacts[ckey(tenant, ext)] = c
		}
	}
	return nil
}

// mergeSource returns the external id of the contact an event merges into its own: an identify's
// anonymous visitor (props.anonymous_id) or a merge_requested event's loser (props.loser).
func mergeSource(e *event.Event) string {
	switch e.Name {
	case event.Identify:
		return attr.String(e.Props["anonymous_id"])
	case event.MergeRequested:
		return attr.String(e.Props["loser"])
	}
	return ""
}

// mergeKey returns the contact an event merges, if any.
func mergeKey(e *event.Event) (string, bool) {
	from := mergeSource(e)
	if from == "" || from == e.ExternalID {
		return "", false
	}
	return ckey(e.TenantID, from), true
}

// mergeKeys returns the sorted union of two sorted key lists.
func mergeKeys(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, k := range b {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func ckey(tenant int64, ext string) string { return strconv.FormatInt(tenant, 10) + ":" + ext }
func seenKey(e event.Event) string         { return fmt.Sprintf("evt:%d:%s", e.TenantID, e.ID) }
func countedKey(e event.Event) string      { return fmt.Sprintf("evc:%d:%s", e.TenantID, e.ID) }
