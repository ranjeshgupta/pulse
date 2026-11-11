// Package segengine keeps segment membership current.
//
// Streaming: every enriched event re-evaluates only the segments that reference its event name or
// a changed attribute (all of the tenant's segments for a brand-new contact), for that one contact.
// Cost per event is independent of how many contacts exist, unlike Mautic's full rebuild.
//
// Batch: new segments are backfilled from ClickHouse, and segments with event windows are swept on
// an interval so "in the last N days" membership decays. Both diff against the stored bitmap and
// publish only the contacts that entered or exited.
//
// Segments can reference other segments and static lists. The index orders segments so a segment
// is evaluated after every segment it reads, and an event re-evaluates the dependents of its
// candidates in the same pass. Changes that do not come from that pass (backfills, sweeps, list
// edits) arrive on segment.membership, and HandleMembership re-evaluates their dependents.
//
// Event rules with property filters read their own counters, which this engine increments (the
// filters live here, in the index) and seeds from ClickHouse when it backfills or sweeps.
//
// Two kinds of rule segments stay out of the streaming index: daily segments (rebuilt once a day
// at their time, source "daily") and rules counting events further back than the counters keep
// (an absolute range starting more than 365 days ago), which the sweep evaluates.
package segengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/reach"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
)

const (
	refreshEvery = 2 * time.Second
	publishChunk = 5000
	// catchUpAfter: a backfill is re-run once this long after it, to pick up events that were
	// in flight to ClickHouse while the segment was joining the streaming index.
	catchUpAfter = 5 * time.Second
	dedupeTTL    = 24 * time.Hour
	// verifyLimit: rebuild diffs up to this size are checked contact by contact (see Rebuild).
	verifyLimit = 20000

	// Sources of membership changes this engine makes while following dependencies. Changes with
	// these sources already had their dependents evaluated.
	SourceStream    = "stream"
	SourceDependent = "dependent"
)

type Store interface {
	AllSegments(ctx context.Context) ([]model.Segment, error)
	ClaimSegmentWork(ctx context.Context, sweepEvery time.Duration) (model.Segment, string, bool, error)
	MarkSegmentReady(ctx context.Context, id int64) error
	SetNextRefresh(ctx context.Context, id int64, at time.Time) error
	ContactsByIDs(ctx context.Context, tenant int64, ids []uint32) ([]model.Contact, error)
}

type IDQuerier interface {
	SegmentIDs(ctx context.Context, q segment.Query) ([]uint32, error)
	DayCounts(ctx context.Context, q segment.Query) ([]ch.ContactDayCount, error)
}

type compiled struct {
	model.Segment
	rule     segment.Rule
	counters []string // counter names the rule reads, for prefetch
	refs     []int64  // segments the rule reads
	rank     int      // evaluated after every segment of lower rank
}

type tenantIndex struct {
	all        []*compiled // rule segments in evaluation order
	static     []int64     // static lists (no rule; cleared when a contact is merged away)
	byEvent    map[string][]*compiled
	byAttr     map[string][]*compiled
	dependents map[int64][]*compiled // segment -> rule segments that reference it
	filters    map[string][]segment.Filter
}

type Engine struct {
	store      Store
	ids        IDQuerier
	rdb        redis.Cmdable
	mem        segment.Membership
	pub        kafka.Publisher
	log        *slog.Logger
	now        func() time.Time
	sweepEvery time.Duration

	idx       atomic.Pointer[map[int64]*tenantIndex]
	refreshed atomic.Int64      // unix nanos of the last Refresh
	catchUps  map[int64]catchUp // maintenance loop only
}

type catchUp struct {
	seg model.Segment
	due time.Time
}

func New(store Store, ids IDQuerier, rdb redis.Cmdable, pub kafka.Publisher, log *slog.Logger, sweepEvery time.Duration) *Engine {
	e := &Engine{store: store, ids: ids, rdb: rdb, mem: segment.Membership{RDB: rdb}, pub: pub,
		log: log, now: time.Now, sweepEvery: sweepEvery, catchUps: map[int64]catchUp{}}
	empty := map[int64]*tenantIndex{}
	e.idx.Store(&empty)
	return e
}

// Refresh reloads segment definitions and rebuilds the inverted index. Segments in a reference
// cycle (which the API refuses, but the store could still hold) are left out and logged.
func (e *Engine) Refresh(ctx context.Context) error {
	segs, err := e.store.AllSegments(ctx)
	if err != nil {
		return err
	}
	byTenant := map[int64]map[int64]*compiled{}
	idx := map[int64]*tenantIndex{}
	now := e.now()
	for _, s := range segs {
		ti := idx[s.TenantID]
		if ti == nil {
			ti = &tenantIndex{byEvent: map[string][]*compiled{}, byAttr: map[string][]*compiled{},
				dependents: map[int64][]*compiled{}, filters: map[string][]segment.Filter{}}
			idx[s.TenantID], byTenant[s.TenantID] = ti, map[int64]*compiled{}
		}
		if s.Kind == "static" {
			ti.static = append(ti.static, s.ID)
			continue
		}
		rule, err := segment.Parse(s.Rule)
		if err != nil {
			e.log.Error("skipping invalid segment", "segment", s.ID, "err", err)
			continue
		}
		if s.Refresh.Daily() || rule.SweepOnly(now) {
			continue // batch only; segments that read it see its bit like a static list's
		}
		byTenant[s.TenantID][s.ID] = &compiled{Segment: s, rule: rule, counters: rule.Counters(), refs: rule.SegmentRefs()}
	}
	for tenant, segs := range byTenant {
		ti := idx[tenant]
		for _, c := range ranked(segs, e.log) {
			ti.all = append(ti.all, c)
			events, attrs := c.rule.Refs()
			for _, ev := range events {
				ti.byEvent[ev] = append(ti.byEvent[ev], c)
			}
			for _, a := range attrs {
				ti.byAttr[a] = append(ti.byAttr[a], c)
			}
			for _, ref := range c.refs {
				ti.dependents[ref] = append(ti.dependents[ref], c)
			}
			for _, f := range c.rule.Filters() {
				if !hasFilter(ti.filters[f.Event], f.Key) {
					ti.filters[f.Event] = append(ti.filters[f.Event], f)
				}
			}
		}
	}
	e.idx.Store(&idx)
	e.refreshed.Store(e.now().UnixNano())
	return nil
}

// refreshForLists reloads the index when an event names a list it doesn't know yet: a list
// created a moment ago, before the periodic refresh. Unknown ids refresh at most once a second.
func (e *Engine) refreshForLists(ctx context.Context, evs []event.Event) error {
	idx := *e.idx.Load()
	for i := range evs {
		ev := &evs[i]
		if ev.Name != event.ListJoined && ev.Name != event.ListLeft {
			continue
		}
		if ti := idx[ev.TenantID]; ti != nil {
			if list, _ := listChange(ev, ti); list != 0 {
				continue
			}
		}
		// Refreshes are throttled to one a second; wait out the rest of that second rather than
		// skip it, or the event would miss a list created moments ago.
		if wait := time.Second - time.Duration(e.now().UnixNano()-e.refreshed.Load()); wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		return e.Refresh(ctx)
	}
	return nil
}

// indexed reports whether the streaming index has segment s.
func (e *Engine) indexed(s model.Segment) bool {
	ti := (*e.idx.Load())[s.TenantID]
	if ti == nil {
		return false
	}
	for _, c := range ti.all {
		if c.ID == s.ID {
			return true
		}
	}
	return false
}

func hasFilter(fs []segment.Filter, key string) bool {
	for _, f := range fs {
		if f.Key == key {
			return true
		}
	}
	return false
}

// ranked orders a tenant's rule segments so each comes after the segments it references (a static
// list or unknown id ranks 0), dropping any segment on or behind a cycle.
func ranked(segs map[int64]*compiled, log *slog.Logger) []*compiled {
	const visiting = -1
	state := map[int64]int{} // 0 unvisited, -1 visiting, else rank+1
	var visit func(id int64) (int, bool)
	visit = func(id int64) (int, bool) {
		c, ok := segs[id]
		if !ok {
			return 0, true // a static list, or a deleted segment (always "not in")
		}
		switch st := state[id]; {
		case st == visiting:
			return 0, false
		case st > 0:
			return st - 1, true
		case st < -1:
			return 0, false // known bad
		}
		state[id] = visiting
		rank := 0
		for _, ref := range c.refs {
			r, ok := visit(ref)
			if !ok {
				state[id] = -2
				return 0, false
			}
			rank = max(rank, r+1)
		}
		c.rank = rank
		state[id] = rank + 1
		return rank, true
	}
	var out []*compiled
	for id, c := range segs {
		if _, ok := visit(id); ok {
			out = append(out, c)
		} else {
			log.Error("skipping segment in a reference cycle", "segment", id)
		}
	}
	sortByRank(out)
	return out
}

func sortByRank(cs []*compiled) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].rank != cs[j].rank {
			return cs[i].rank < cs[j].rank
		}
		return cs[i].ID < cs[j].ID
	})
}

// candidates returns the segments an event can affect, plus every segment that depends on them,
// without duplicates and in evaluation order.
func (e *Engine) candidates(ev *event.Event) []*compiled {
	ti := (*e.idx.Load())[ev.TenantID]
	if ti == nil {
		return nil
	}
	if ev.NewContact || mergedAway(ev.Profile) {
		return ti.all
	}
	seen := map[int64]bool{}
	var out []*compiled
	var add func(cs []*compiled)
	add = func(cs []*compiled) {
		for _, c := range cs {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, c)
				add(ti.dependents[c.ID])
			}
		}
	}
	add(ti.byEvent[ev.Name])
	for _, a := range ev.ChangedAttrs {
		add(ti.byAttr[a])
	}
	if list, _ := listChange(ev, ti); list != 0 {
		add(ti.dependents[list])
	}
	sortByRank(out)
	return out
}

// listChange returns the static list a list_joined or list_left event changes (0 for other
// events, or a list of another workspace) and whether the contact joins it.
func listChange(ev *event.Event, ti *tenantIndex) (int64, bool) {
	if ti == nil || ev.Name != event.ListJoined && ev.Name != event.ListLeft {
		return 0, false // ti is nil for a workspace with no segments or lists indexed yet
	}
	id, ok := attr.Number(attr.String(ev.Props["list_id"]))
	if !ok {
		return 0, false
	}
	for _, s := range ti.static {
		if s == int64(id) {
			return s, ev.Name == event.ListJoined
		}
	}
	return 0, false
}

// mergedAway reports whether the profile was merged into another contact. Such contacts are in
// no segment.
func mergedAway(profile map[string]any) bool {
	_, ok := profile[event.AttrMergedInto]
	return ok
}

// lookup is the Counter (and segment lookup) for one contact in a batch: prefetched counters and
// bits, overridden by results computed earlier in the same pass.
type lookup struct {
	segment.BucketCounter
	bits map[int64]bool
}

func (l lookup) InSegment(_ context.Context, seg int64) (bool, error) { return l.bits[seg], nil }

// countFiltered increments the filtered counters of the events that pass them, once per event.
func (e *Engine) countFiltered(ctx context.Context, evs []event.Event) error {
	idx := *e.idx.Load()
	type hit struct {
		ev   *event.Event
		keys []string
	}
	var hits []hit
	for i := range evs {
		ev := &evs[i]
		ti := idx[ev.TenantID]
		if ti == nil {
			continue
		}
		var keys []string
		for _, f := range ti.filters[ev.Name] {
			if segment.MatchProps(f.Where, ev.Props) {
				keys = append(keys, f.Key)
			}
		}
		if len(keys) > 0 {
			hits = append(hits, hit{ev, keys})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	pipe := e.rdb.Pipeline()
	claims := make([]*redis.BoolCmd, len(hits))
	for i, h := range hits {
		claims[i] = pipe.SetNX(ctx, fmt.Sprintf("evf:%d:%s", h.ev.TenantID, h.ev.ID), 1, dedupeTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	pipe = e.rdb.Pipeline()
	var won []string
	for i, h := range hits {
		if !claims[i].Val() {
			continue
		}
		won = append(won, fmt.Sprintf("evf:%d:%s", h.ev.TenantID, h.ev.ID))
		for _, k := range h.keys {
			segment.QueueIncr(ctx, pipe, h.ev.TenantID, h.ev.ContactID, k, h.ev.Ts)
		}
	}
	if len(won) == 0 {
		return nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		e.rdb.Del(ctx, won...)
		return fmt.Errorf("count filtered events: %w", err)
	}
	return nil
}

// HandleEvent is the streaming path for one events.enriched record.
func (e *Engine) HandleEvent(ctx context.Context, r *kgo.Record) error {
	return e.HandleBatch(ctx, []*kgo.Record{r})
}

// HandleBatch is the streaming path for one partition's records, in three round trips:
// fetch every counter the candidate segments need, write every membership bit (SETBIT returns the
// old bit, so changes are detected in order, even for several events of one contact), publish.
func (e *Engine) HandleBatch(ctx context.Context, recs []*kgo.Record) error {
	type job struct {
		ev    event.Event
		cands []*compiled
	}
	evs := make([]event.Event, 0, len(recs))
	for _, r := range recs {
		var ev event.Event
		if err := json.Unmarshal(r.Value, &ev); err != nil {
			return fmt.Errorf("decode event: %w", err)
		}
		evs = append(evs, ev)
	}
	if err := e.refreshForLists(ctx, evs); err != nil {
		return err
	}
	if err := e.countFiltered(ctx, evs); err != nil {
		return err
	}
	if err := e.updateReach(ctx, evs); err != nil {
		return err
	}
	jobs := make([]job, 0, len(evs))
	idx := *e.idx.Load()
	for _, ev := range evs {
		cands := e.candidates(&ev)
		if list, _ := listChange(&ev, idx[ev.TenantID]); len(cands) > 0 || list != 0 {
			jobs = append(jobs, job{ev, cands})
		}
	}
	if len(jobs) == 0 {
		return nil
	}

	// 1. Prefetch the event counters and segment bits the candidate rules read.
	type ckey struct {
		tenant  int64
		contact uint32
		event   string
	}
	type bkey struct {
		tenant, segment int64
		contact         uint32
	}
	fetch := map[ckey]*redis.MapStringStringCmd{}
	bits := map[bkey]*redis.IntCmd{}
	pipe := e.rdb.Pipeline()
	for _, j := range jobs {
		for _, c := range j.cands {
			for _, name := range c.counters {
				k := ckey{j.ev.TenantID, j.ev.ContactID, name}
				if fetch[k] == nil {
					fetch[k] = pipe.HGetAll(ctx, segment.CounterKey(k.tenant, k.contact, name))
				}
			}
			for _, ref := range c.refs {
				k := bkey{j.ev.TenantID, ref, j.ev.ContactID}
				if bits[k] == nil {
					bits[k] = pipe.GetBit(ctx, segment.MemberKey(k.tenant, ref), int64(k.contact))
				}
			}
		}
	}
	if len(fetch)+len(bits) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("fetch counters: %w", err)
		}
	}

	// 2. Evaluate and write every bit in one pipeline.
	now := e.now()
	type flip struct {
		change model.MembershipChange
		old    *redis.IntCmd
	}
	var flips []flip
	pipe = e.rdb.Pipeline()
	// Later events of a contact in this batch see the bits earlier ones wrote.
	written := map[bkey]bool{}
	for _, j := range jobs {
		l := lookup{segment.BucketCounter{Buckets: map[string]map[string]string{}, AsOf: now}, map[int64]bool{}}
		for _, c := range j.cands {
			for _, name := range c.counters {
				l.Buckets[name] = fetch[ckey{j.ev.TenantID, j.ev.ContactID, name}].Val()
			}
			for _, ref := range c.refs {
				k := bkey{j.ev.TenantID, ref, j.ev.ContactID}
				if v, ok := written[k]; ok {
					l.bits[ref] = v
				} else {
					l.bits[ref] = bits[k].Val() == 1
				}
			}
		}
		gone := mergedAway(j.ev.Profile)
		if list, joined := listChange(&j.ev, idx[j.ev.TenantID]); list != 0 {
			joined = joined && !gone
			l.bits[list] = joined
			written[bkey{j.ev.TenantID, list, j.ev.ContactID}] = joined
			flips = append(flips, flip{
				change: model.MembershipChange{TenantID: j.ev.TenantID, SegmentID: list, ContactID: j.ev.ContactID, Entered: joined, Source: SourceStream, At: now},
				old:    pipe.SetBit(ctx, segment.MemberKey(j.ev.TenantID, list), int64(j.ev.ContactID), boolBit(joined)),
			})
		}
		for _, c := range j.cands {
			in := false
			if !gone {
				var err error
				if in, err = segment.Eval(ctx, c.rule, j.ev.Profile, l); err != nil {
					return fmt.Errorf("eval segment %d: %w", c.ID, err)
				}
			}
			l.bits[c.ID] = in
			written[bkey{j.ev.TenantID, c.ID, j.ev.ContactID}] = in
			flips = append(flips, flip{
				change: model.MembershipChange{TenantID: j.ev.TenantID, SegmentID: c.ID, ContactID: j.ev.ContactID, Entered: in, Source: SourceStream, At: now},
				old:    pipe.SetBit(ctx, segment.MemberKey(j.ev.TenantID, c.ID), int64(j.ev.ContactID), boolBit(in)),
			})
		}
		if gone {
			for _, id := range idx[j.ev.TenantID].static {
				flips = append(flips, flip{
					change: model.MembershipChange{TenantID: j.ev.TenantID, SegmentID: id, ContactID: j.ev.ContactID, Source: SourceStream, At: now},
					old:    pipe.SetBit(ctx, segment.MemberKey(j.ev.TenantID, id), int64(j.ev.ContactID), 0),
				})
			}
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("write membership: %w", err)
	}

	// 3. Publish only real changes.
	var out []*kgo.Record
	for _, f := range flips {
		wasIn := f.old.Val() == 1
		if wasIn == f.change.Entered {
			continue
		}
		rec, err := changeRecord(f.change)
		if err != nil {
			return err
		}
		out = append(out, rec)
	}
	return e.pub.Publish(ctx, out...)
}

// updateReach sets the reachability bits (package reach) of contacts that were created, or whose
// address, consent or bounce attributes changed, from their profile after the event.
func (e *Engine) updateReach(ctx context.Context, evs []event.Event) error {
	var p redis.Pipeliner
	for i := range evs {
		ev := &evs[i]
		if ev.Profile == nil || ev.ContactID == 0 || !(ev.NewContact || reach.Relevant(ev.ChangedAttrs)) {
			continue
		}
		if p == nil {
			p = e.rdb.Pipeline()
		}
		reach.Queue(ctx, p, ev.TenantID, ev.ContactID, reach.Bits(ev.Profile))
	}
	if p == nil {
		return nil
	}
	if _, err := p.Exec(ctx); err != nil {
		return fmt.Errorf("write reachability: %w", err)
	}
	return nil
}

func boolBit(in bool) int {
	if in {
		return 1
	}
	return 0
}

// HandleMembership re-evaluates the dependents of segments whose membership changed outside the
// streaming pass (backfills, sweeps, list edits), for the contacts that changed. Its own changes
// carry SourceDependent and already include their dependents, so they are skipped here.
func (e *Engine) HandleMembership(ctx context.Context, recs []*kgo.Record) error {
	idx := *e.idx.Load()
	type tc struct {
		tenant  int64
		contact uint32
	}
	changed := map[tc][]int64{}
	var order []tc
	for _, r := range recs {
		var c model.MembershipChange
		if err := json.Unmarshal(r.Value, &c); err != nil {
			e.log.Error("skipping undecodable membership change", "offset", r.Offset, "err", err)
			continue
		}
		if c.Source == SourceStream || c.Source == SourceDependent {
			continue
		}
		if ti := idx[c.TenantID]; ti == nil || len(ti.dependents[c.SegmentID]) == 0 {
			continue
		}
		k := tc{c.TenantID, c.ContactID}
		if _, ok := changed[k]; !ok {
			order = append(order, k)
		}
		changed[k] = append(changed[k], c.SegmentID)
	}
	if len(order) == 0 {
		return nil
	}
	byTenant := map[int64][]uint32{}
	for _, k := range order {
		byTenant[k.tenant] = append(byTenant[k.tenant], k.contact)
	}
	profiles := map[tc]map[string]any{}
	for tenant, ids := range byTenant {
		cs, err := e.store.ContactsByIDs(ctx, tenant, ids)
		if err != nil {
			return fmt.Errorf("load contacts: %w", err)
		}
		for _, c := range cs {
			profiles[tc{tenant, c.ID}] = c.Attrs
		}
	}
	now := e.now()
	var out []*kgo.Record
	for _, k := range order {
		attrs, ok := profiles[k]
		if !ok || mergedAway(attrs) {
			continue
		}
		ti := idx[k.tenant]
		seen := map[int64]bool{}
		var deps []*compiled
		var add func(cs []*compiled)
		add = func(cs []*compiled) {
			for _, c := range cs {
				if !seen[c.ID] {
					seen[c.ID] = true
					deps = append(deps, c)
					add(ti.dependents[c.ID])
				}
			}
		}
		for _, seg := range changed[k] {
			add(ti.dependents[seg])
		}
		sortByRank(deps)
		counter := segment.NewRedisCounter(e.rdb, k.tenant, k.contact, now)
		for _, c := range deps {
			in, err := segment.Eval(ctx, c.rule, attrs, counter)
			if err != nil {
				return fmt.Errorf("eval segment %d: %w", c.ID, err)
			}
			counter.Set(c.ID, in)
			flipped, err := e.mem.SetMember(ctx, k.tenant, c.ID, k.contact, in)
			if err != nil {
				return err
			}
			if !flipped {
				continue
			}
			rec, err := changeRecord(model.MembershipChange{TenantID: k.tenant, SegmentID: c.ID, ContactID: k.contact, Entered: in, Source: SourceDependent, At: now})
			if err != nil {
				return err
			}
			out = append(out, rec)
		}
	}
	return e.pub.Publish(ctx, out...)
}

// seedFilters overwrites a rule's filtered counters with the counts in ClickHouse, so a filter
// that was just added (or a counter that drifted) matches history before the rule is evaluated.
func (e *Engine) seedFilters(ctx context.Context, tenant int64, rule segment.Rule, now time.Time) error {
	for _, f := range rule.Filters() {
		rows, err := e.ids.DayCounts(ctx, segment.FilterCountsSQL(tenant, f, now))
		if err != nil {
			return fmt.Errorf("seed %s: %w", f.Key, err)
		}
		for start := 0; start < len(rows); start += publishChunk {
			pipe := e.rdb.Pipeline()
			for _, r := range rows[start:min(start+publishChunk, len(rows))] {
				segment.QueueSet(ctx, pipe, tenant, r.ContactID, f.Key, r.Day, r.Count)
			}
			if _, err := pipe.Exec(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// Rebuild recomputes a segment from ClickHouse, swaps the bitmap and publishes the diff.
func (e *Engine) Rebuild(ctx context.Context, s model.Segment, source string) (entered, exited int, err error) {
	if s.Kind == "static" {
		return 0, 0, nil // a list's members are its bitmap; there is nothing to recompute
	}
	rule, err := segment.Parse(s.Rule)
	if err != nil {
		return 0, 0, err
	}
	now := e.now()
	if err := e.seedFilters(ctx, s.TenantID, rule, now); err != nil {
		return 0, 0, err
	}
	q, err := segment.ToSQL(rule, s.TenantID, now)
	if err != nil {
		return 0, 0, err
	}
	ids, err := e.ids.SegmentIDs(ctx, q)
	if err != nil {
		return 0, 0, fmt.Errorf("query segment %d: %w", s.ID, err)
	}
	old, err := e.mem.Load(ctx, s.TenantID, s.ID)
	if err != nil {
		return 0, 0, err
	}
	cur := segment.Build(ids)
	in, out := segment.Diff(old, cur)
	// ClickHouse lags the stream: a contact whose newer event the stream already evaluated may
	// still look like its old self there. For a segment the stream keeps current, a small diff is
	// checked against the contact's current profile (Postgres) and counters (Redis), exactly as the
	// stream would evaluate it, and only the changes that still hold are applied, bit by bit, so a
	// catch-up rebuild never undoes a newer streaming result. Large diffs (a first backfill of a
	// big segment) replace the bitmap as before.
	if n := len(in) + len(out); n > 0 && n <= verifyLimit && e.indexed(s) && !s.Refresh.Daily() && !rule.SweepOnly(now) {
		if in, out, err = e.verify(ctx, s.TenantID, rule, in, out, now); err != nil {
			return 0, 0, err
		}
		pipe := e.rdb.Pipeline()
		for _, id := range in {
			pipe.SetBit(ctx, segment.MemberKey(s.TenantID, s.ID), int64(id), 1)
		}
		for _, id := range out {
			pipe.SetBit(ctx, segment.MemberKey(s.TenantID, s.ID), int64(id), 0)
		}
		if len(in)+len(out) > 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				return 0, 0, err
			}
		}
	} else if err := e.mem.Replace(ctx, s.TenantID, s.ID, cur); err != nil {
		return 0, 0, err
	}

	recs := make([]*kgo.Record, 0, min(publishChunk, len(in)+len(out)))
	flush := func() error {
		err := e.pub.Publish(ctx, recs...)
		recs = recs[:0]
		return err
	}
	for _, group := range []struct {
		ids     []uint32
		entered bool
	}{{in, true}, {out, false}} {
		for _, id := range group.ids {
			rec, err := changeRecord(model.MembershipChange{
				TenantID: s.TenantID, SegmentID: s.ID, ContactID: id, Entered: group.entered, Source: source, At: now,
			})
			if err != nil {
				return 0, 0, err
			}
			if recs = append(recs, rec); len(recs) == publishChunk {
				if err := flush(); err != nil {
					return 0, 0, err
				}
			}
		}
	}
	return len(in), len(out), flush()
}

// verify keeps the entries and exits that the contacts' current profiles and counters confirm.
// A contact without a profile (deleted) or merged away is in no segment.
func (e *Engine) verify(ctx context.Context, tenant int64, rule segment.Rule, in, out []uint32, now time.Time) ([]uint32, []uint32, error) {
	all := append(append([]uint32{}, in...), out...)
	profiles := make(map[uint32]map[string]any, len(all))
	for start := 0; start < len(all); start += publishChunk {
		cs, err := e.store.ContactsByIDs(ctx, tenant, all[start:min(start+publishChunk, len(all))])
		if err != nil {
			return nil, nil, fmt.Errorf("load contacts: %w", err)
		}
		for _, c := range cs {
			profiles[c.ID] = c.Attrs
		}
	}
	member := func(id uint32) (bool, error) {
		attrs, ok := profiles[id]
		if !ok || mergedAway(attrs) {
			return false, nil
		}
		return segment.Eval(ctx, rule, attrs, segment.NewRedisCounter(e.rdb, tenant, id, now))
	}
	var keepIn, keepOut []uint32
	for _, id := range in {
		ok, err := member(id)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			keepIn = append(keepIn, id)
		}
	}
	for _, id := range out {
		ok, err := member(id)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			keepOut = append(keepOut, id)
		}
	}
	return keepIn, keepOut, nil
}

// RunMaintenance refreshes the index and processes backfills and sweeps until ctx is done.
func (e *Engine) RunMaintenance(ctx context.Context) error {
	tick := time.NewTicker(refreshEvery)
	defer tick.Stop()
	for {
		if err := e.Refresh(ctx); err != nil && ctx.Err() == nil {
			e.log.Error("refresh segments", "err", err)
		}
		for ctx.Err() == nil {
			s, source, ok, err := e.store.ClaimSegmentWork(ctx, e.sweepEvery)
			if err != nil || !ok {
				if err != nil && ctx.Err() == nil {
					e.log.Error("claim segment work", "err", err)
				}
				break
			}
			// The stream must know a segment before its backfill, or events between the two
			// would be missed by both.
			if source == "backfill" && !e.indexed(s) {
				if err := e.Refresh(ctx); err != nil {
					e.log.Error("refresh segments", "err", err)
					continue
				}
			}
			if !e.rebuild(ctx, s, source) {
				continue // status stays 'building'; the claim expires and another attempt follows
			}
			if err := e.store.MarkSegmentReady(ctx, s.ID); err != nil {
				e.log.Error("mark segment ready", "segment", s.ID, "err", err)
			}
			if s.Refresh.Daily() {
				if err := e.store.SetNextRefresh(ctx, s.ID, s.Refresh.Next(e.now())); err != nil {
					e.log.Error("schedule daily refresh", "segment", s.ID, "err", err)
				}
			}
			if source == "backfill" && !s.Refresh.Daily() { // a daily segment changes only at its time
				e.catchUps[s.ID] = catchUp{s, e.now().Add(catchUpAfter)}
			}
		}
		for id, c := range e.catchUps {
			if ctx.Err() == nil && !e.now().Before(c.due) {
				delete(e.catchUps, id)
				e.rebuild(ctx, c.seg, "backfill")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (e *Engine) rebuild(ctx context.Context, s model.Segment, source string) bool {
	start := time.Now()
	in, out, err := e.Rebuild(ctx, s, source)
	if err != nil {
		e.log.Error("rebuild segment", "segment", s.ID, "source", source, "err", err)
		return false
	}
	e.log.Info("segment rebuilt", "segment", s.ID, "source", source, "entered", in, "exited", out, "took", time.Since(start))
	return true
}

func changeRecord(c model.MembershipChange) (*kgo.Record, error) {
	return kafka.JSON(event.TopicMembership, event.ContactKey(c.TenantID, c.ContactID), c)
}
