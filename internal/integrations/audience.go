package integrations

// Audience sync keeps an ad platform audience equal to a segment's members, as hashed emails and
// phone numbers:
//
//   - The worker reads segment.membership (entries and exits) and events.enriched (a changed
//     email, phone, global opt-out or merge) and queues the contact for each linked audience
//     (audience_queue, one row per contact).
//   - The flusher claims queued contacts in batches and decides each one from the current state:
//     a member with an email or phone who isn't unsubscribed from everything belongs in the
//     audience; anyone else who was uploaded is removed. audience_members keeps the hashes sent,
//     so a removal sends exactly what was added.
//   - A full reconcile (on linking, then every PULSE_AUDIENCE_RECONCILE_INTERVAL) walks the whole
//     segment and everyone uploaded, which also catches deleted contacts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/segment"
)

// AudienceSync links a segment to an audience of an ad platform connector.
type AudienceSync struct {
	ID            int64      `json:"id"`
	TenantID      int64      `json:"-"`
	IntegrationID int64      `json:"integration_id"`
	SegmentID     int64      `json:"segment_id"`
	AudienceID    string     `json:"audience_id"`
	AudienceName  string     `json:"audience_name"`
	Active        bool       `json:"active"`
	Status        string     `json:"status"` // new | syncing | ok | error
	LastError     string     `json:"last_error,omitempty"`
	Uploaded      int64      `json:"uploaded"`
	Added         int64      `json:"added"`
	Removed       int64      `json:"removed"`
	Skipped       int64      `json:"skipped"`
	Queued        int64      `json:"queued"`
	LastSyncAt    *time.Time `json:"last_sync_at,omitempty"`
	NextFullAt    time.Time  `json:"next_full_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Outcome is what one audience batch did.
type Outcome struct {
	Added, Removed, Skipped int
	// Uploaded is the members map after the batch: identities to keep (added or unchanged) and
	// contacts to forget.
	Keep   []Identity
	Forget []uint32
}

// AudienceStore is what audience sync reads and writes (Postgres in production).
type AudienceStore interface {
	GetIntegration(ctx context.Context, tenant, id int64) (Connector, error)
	ActiveAudienceSyncs(ctx context.Context) ([]AudienceSync, error)
	QueueAudience(ctx context.Context, sync int64, contacts []uint32) error
	QueuedAudienceSyncs(ctx context.Context) ([]int64, error)
	ClaimAudienceQueue(ctx context.Context, sync int64, limit int) ([]uint32, error)
	AudienceMembers(ctx context.Context, sync int64, contacts []uint32) (map[uint32]Identity, error)
	// UploadedAfter pages through every uploaded contact of a sync.
	UploadedAfter(ctx context.Context, sync int64, after uint32, limit int) ([]uint32, error)
	// ApplyAudience saves a batch's outcome and counts.
	ApplyAudience(ctx context.Context, sync int64, o Outcome) error
	AudienceError(ctx context.Context, sync int64, msg string) error
	// ClaimDueReconciles returns syncs whose full reconcile is due and moves it forward by every.
	ClaimDueReconciles(ctx context.Context, every time.Duration, limit int) ([]AudienceSync, error)
	FinishReconcile(ctx context.Context, sync int64, skipped int64) error
	ContactsByIDs(ctx context.Context, tenant int64, ids []uint32) ([]model.Contact, error)
	LogIntegration(ctx context.Context, id int64, e LogEntry) error
}

// MemberCheck tells which contacts are in a segment.
type MemberCheck interface {
	AreMembers(ctx context.Context, tenant, segment int64, ids []uint32) ([]bool, error)
	Load(ctx context.Context, tenant, segment int64) ([]byte, error)
}

// RedisMembers reads segment bitmaps (segment.Membership) with pipelined GETBITs.
type RedisMembers struct{ RDB redis.Cmdable }

func (m RedisMembers) AreMembers(ctx context.Context, tenant, seg int64, ids []uint32) ([]bool, error) {
	pipe := m.RDB.Pipeline()
	cmds := make([]*redis.IntCmd, len(ids))
	key := segment.MemberKey(tenant, seg)
	for i, id := range ids {
		cmds[i] = pipe.GetBit(ctx, key, int64(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]bool, len(ids))
	for i, c := range cmds {
		out[i] = c.Val() == 1
	}
	return out, nil
}

func (m RedisMembers) Load(ctx context.Context, tenant, seg int64) ([]byte, error) {
	return segment.Membership{RDB: m.RDB}.Load(ctx, tenant, seg)
}

// Syncer runs audience sync.
type Syncer struct {
	Store   AudienceStore
	Members MemberCheck
	Service *Service // opens connector secrets, HTTP client
	Log     *slog.Logger
	// Platform builds a connector's client (tests swap it); nil uses NewPlatform.
	Platform func(c Connector) (Platform, error)

	mu      sync.Mutex
	syncs   map[int64][]AudienceSync // by tenant
	exp     time.Time
	backoff map[int64]time.Time // sync → when to try again after an error
	fails   map[int64]int
}

const (
	audienceBatch = 5000
	syncCacheTTL  = 10 * time.Second
)

// Topics are what the audience worker consumes.
func AudienceTopics() []string { return []string{event.TopicMembership, event.TopicEnriched} }

func (s *Syncer) forTenant(ctx context.Context, tenant int64) ([]AudienceSync, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncs == nil || time.Now().After(s.exp) {
		all, err := s.Store.ActiveAudienceSyncs(ctx)
		if err != nil {
			return nil, err
		}
		s.syncs = map[int64][]AudienceSync{}
		for _, a := range all {
			s.syncs[a.TenantID] = append(s.syncs[a.TenantID], a)
		}
		s.exp = time.Now().Add(syncCacheTTL)
	}
	return s.syncs[tenant], nil
}

// audienceAttrs are the profile attributes whose change can move a contact in or out of an audience.
var audienceAttrs = map[string]bool{"email": true, "phone": true, consent.Key(consent.All): true, event.AttrMergedInto: true}

// HandleBatch queues the contacts whose audiences may have changed.
func (s *Syncer) HandleBatch(ctx context.Context, recs []*kgo.Record) error {
	queue := map[int64]map[uint32]bool{}
	add := func(sync int64, c uint32) {
		if queue[sync] == nil {
			queue[sync] = map[uint32]bool{}
		}
		queue[sync][c] = true
	}
	for _, r := range recs {
		switch r.Topic {
		case event.TopicMembership:
			var m model.MembershipChange
			if json.Unmarshal(r.Value, &m) != nil {
				continue
			}
			syncs, err := s.forTenant(ctx, m.TenantID)
			if err != nil {
				return err
			}
			for _, a := range syncs {
				if a.SegmentID == m.SegmentID {
					add(a.ID, m.ContactID)
				}
			}
		case event.TopicEnriched:
			var e event.Event
			if json.Unmarshal(r.Value, &e) != nil || e.ContactID == 0 {
				continue
			}
			relevant := false
			for _, k := range e.ChangedAttrs {
				relevant = relevant || audienceAttrs[k]
			}
			if !relevant {
				continue
			}
			syncs, err := s.forTenant(ctx, e.TenantID)
			if err != nil {
				return err
			}
			for _, a := range syncs {
				add(a.ID, e.ContactID)
			}
		}
	}
	for sync, cs := range queue {
		ids := make([]uint32, 0, len(cs))
		for c := range cs {
			ids = append(ids, c)
		}
		if err := s.Store.QueueAudience(ctx, sync, ids); err != nil {
			return err
		}
	}
	return nil
}

// Wanted decides whether a contact belongs in an audience and with which hashes: a segment member
// with an email or phone who isn't unsubscribed from everything and wasn't merged away.
func Wanted(kind string, inSegment bool, c *model.Contact, defaultCountry string) (Identity, bool) {
	if !inSegment || c == nil {
		return Identity{}, false
	}
	if consent.OptedOut(c.Attrs, consent.All) || attr.String(c.Attrs[event.AttrMergedInto]) != "" {
		return Identity{}, false
	}
	email := c.Email
	if email == "" {
		email = attr.String(c.Attrs["email"])
	}
	id := HashFor(kind, email, attr.String(c.Attrs["phone"]), defaultCountry)
	id.ContactID = c.ID
	return id, !id.Empty()
}

// platform builds the sync's connector client.
func (s *Syncer) platform(ctx context.Context, a AudienceSync) (Connector, Platform, error) {
	c, err := s.Store.GetIntegration(ctx, a.TenantID, a.IntegrationID)
	if err != nil {
		return c, nil, err
	}
	if err := s.Service.OpenSecrets(&c); err != nil {
		return c, nil, err
	}
	if !c.Active {
		return c, nil, errors.New("the integration is turned off")
	}
	mk := s.Platform
	if mk == nil {
		mk = func(c Connector) (Platform, error) { return NewPlatform(c, s.Service.HTTP) }
	}
	p, err := mk(c)
	return c, p, err
}

// Process brings a batch of contacts in line with the segment and uploads the difference.
func (s *Syncer) Process(ctx context.Context, a AudienceSync, ids []uint32) (Outcome, error) {
	var o Outcome
	if len(ids) == 0 {
		return o, nil
	}
	conn, p, err := s.platform(ctx, a)
	if err != nil {
		return o, err
	}
	in, err := s.Members.AreMembers(ctx, a.TenantID, a.SegmentID, ids)
	if err != nil {
		return o, err
	}
	cs, err := s.Store.ContactsByIDs(ctx, a.TenantID, ids)
	if err != nil {
		return o, err
	}
	byID := make(map[uint32]*model.Contact, len(cs))
	for i := range cs {
		byID[cs[i].ID] = &cs[i]
	}
	have, err := s.Store.AudienceMembers(ctx, a.ID, ids)
	if err != nil {
		return o, err
	}
	country := conn.Setting("default_country")
	var adds, removes []Identity
	for i, id := range ids {
		want, ok := Wanted(conn.Kind, in[i], byID[id], country)
		old, uploaded := have[id]
		switch {
		case ok && uploaded && old.EmailHash == want.EmailHash && old.PhoneHash == want.PhoneHash:
			// already there
		case ok:
			if uploaded {
				removes = append(removes, old) // the email or phone changed: take the old one out
			}
			adds = append(adds, want)
			o.Keep = append(o.Keep, want)
		default:
			if in[i] {
				o.Skipped++
			}
			if uploaded {
				removes = append(removes, old)
				o.Forget = append(o.Forget, id)
			}
		}
	}
	for start := 0; start < len(removes); start += PlatformBatch {
		if err := p.Remove(ctx, a.AudienceID, removes[start:min(start+PlatformBatch, len(removes))]); err != nil {
			return Outcome{}, fmt.Errorf("remove from the audience: %w", err)
		}
	}
	for start := 0; start < len(adds); start += PlatformBatch {
		if err := p.Add(ctx, a.AudienceID, adds[start:min(start+PlatformBatch, len(adds))]); err != nil {
			return Outcome{}, fmt.Errorf("add to the audience: %w", err)
		}
	}
	o.Added, o.Removed = len(adds), len(o.Forget)
	return o, s.Store.ApplyAudience(ctx, a.ID, o)
}

func (s *Syncer) failed(ctx context.Context, a AudienceSync, err error) {
	s.mu.Lock()
	if s.backoff == nil {
		s.backoff, s.fails = map[int64]time.Time{}, map[int64]int{}
	}
	s.fails[a.ID]++
	wait := min(time.Duration(1<<min(s.fails[a.ID], 9))*time.Second, 10*time.Minute)
	s.backoff[a.ID] = time.Now().Add(wait)
	s.mu.Unlock()
	_ = s.Store.AudienceError(context.WithoutCancel(ctx), a.ID, err.Error())
	_ = s.Store.LogIntegration(context.WithoutCancel(ctx), a.IntegrationID, LogEntry{Action: "audience", OK: false,
		Message: fmt.Sprintf("audience %s: %v", a.AudienceID, err)})
	if s.Log != nil {
		s.Log.Warn("audience sync failed", "sync", a.ID, "err", err)
	}
}

func (s *Syncer) ready(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().After(s.backoff[id])
}

func (s *Syncer) succeeded(id int64) {
	s.mu.Lock()
	delete(s.backoff, id)
	delete(s.fails, id)
	s.mu.Unlock()
}

func (s *Syncer) byID(ctx context.Context, id int64) (AudienceSync, bool) {
	all, err := s.Store.ActiveAudienceSyncs(ctx)
	if err != nil {
		return AudienceSync{}, false
	}
	for _, a := range all {
		if a.ID == id {
			return a, true
		}
	}
	return AudienceSync{}, false
}

// Flush processes queued contacts once: every sync with a queue, one batch each.
func (s *Syncer) Flush(ctx context.Context) error {
	ids, err := s.Store.QueuedAudienceSyncs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !s.ready(id) {
			continue
		}
		a, ok := s.byID(ctx, id)
		if !ok {
			continue // paused or deleted: its queue waits (or went with it)
		}
		contacts, err := s.Store.ClaimAudienceQueue(ctx, id, audienceBatch)
		if err != nil {
			return err
		}
		o, err := s.Process(ctx, a, contacts)
		if err != nil {
			_ = s.Store.QueueAudience(context.WithoutCancel(ctx), id, contacts) // try them again later
			s.failed(ctx, a, err)
			continue
		}
		s.succeeded(id)
		if o.Added+o.Removed > 0 {
			_ = s.Store.LogIntegration(ctx, a.IntegrationID, LogEntry{Action: "audience", OK: true, Count: o.Added + o.Removed,
				Message: fmt.Sprintf("audience %s: %d added, %d removed", a.AudienceID, o.Added, o.Removed)})
		}
	}
	return nil
}

// Reconcile walks the whole segment and everyone uploaded, in batches.
func (s *Syncer) Reconcile(ctx context.Context, a AudienceSync) error {
	bitmap, err := s.Members.Load(ctx, a.TenantID, a.SegmentID)
	if err != nil {
		return err
	}
	var total Outcome
	run := func(ids []uint32) error {
		o, err := s.Process(ctx, a, ids)
		total.Added, total.Removed, total.Skipped = total.Added+o.Added, total.Removed+o.Removed, total.Skipped+o.Skipped
		return err
	}
	members := segment.Members(bitmap)
	for start := 0; start < len(members); start += audienceBatch {
		if err := run(members[start:min(start+audienceBatch, len(members))]); err != nil {
			return err
		}
	}
	// Uploaded contacts no longer in the segment (or deleted) come out.
	var after uint32
	for {
		up, err := s.Store.UploadedAfter(ctx, a.ID, after, audienceBatch)
		if err != nil {
			return err
		}
		var gone []uint32
		for _, id := range up {
			if !segment.Has(bitmap, id) {
				gone = append(gone, id)
			}
		}
		if err := run(gone); err != nil {
			return err
		}
		if len(up) < audienceBatch {
			break
		}
		after = up[len(up)-1]
	}
	if err := s.Store.FinishReconcile(ctx, a.ID, int64(total.Skipped)); err != nil {
		return err
	}
	return s.Store.LogIntegration(ctx, a.IntegrationID, LogEntry{Action: "reconcile", OK: true, Count: len(members),
		Message: fmt.Sprintf("audience %s: %s members checked, %d added, %d removed, %d left out (no consent or no email/phone)",
			a.AudienceID, strconv.Itoa(len(members)), total.Added, total.Removed, total.Skipped)})
}

// Run flushes the queue every few seconds and starts due reconciles, until ctx is done.
func (s *Syncer) Run(ctx context.Context, reconcileEvery time.Duration) error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	last := time.Time{}
	for {
		if err := s.Flush(ctx); err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Error("audience flush", "err", err)
		}
		if time.Since(last) > 5*time.Second {
			last = time.Now()
			due, err := s.Store.ClaimDueReconciles(ctx, reconcileEvery, 2)
			if err != nil && ctx.Err() == nil && s.Log != nil {
				s.Log.Error("audience reconcile", "err", err)
			}
			if len(due) > 0 {
				// A new or resumed link may not be in the cache yet: reload it now, so entries and
				// exits from here on are queued (the reconcile reads the state up to now).
				s.mu.Lock()
				s.exp = time.Time{}
				s.mu.Unlock()
			}
			for _, a := range due {
				if !s.ready(a.ID) {
					continue
				}
				if err := s.Reconcile(ctx, a); err != nil {
					s.failed(ctx, a, err)
				} else {
					s.succeeded(a.ID)
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
