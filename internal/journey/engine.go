package journey

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/automation"
	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/eventschema"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/store/pg"
)

// ChangesChannel is the Redis pub/sub channel the API announces journey changes on (activate,
// pause, edit, archive, delete), so engines reload at once instead of at the next refresh
// (see changes.go).
const ChangesChannel = "journeys:changed"

const (
	refreshEvery = 2 * time.Second
	pollEvery    = 500 * time.Millisecond
	timerBatch   = 500
)

type EngineStore interface {
	Store
	ActiveJourneys(ctx context.Context) ([]model.Journey, error)
	Enroll(ctx context.Context, tenant, journey int64, contact uint32, node string, re pg.Reentry) (model.Enrollment, bool, error)
	ClaimDueTimers(ctx context.Context, limit int, fn func(model.Timer) error) (int, error)
	Resume(ctx context.Context, enrollment int64, waitNode, resumeNode string) (bool, error)
	EnrollmentStatus(ctx context.Context, enrollment int64) (string, error)
	EventWaits(ctx context.Context, tenant int64, contact uint32, eventName string) ([]pg.EventWait, error)
	Convert(ctx context.Context, tenant int64, contact uint32, journeys []int64) (int, error)
	// SetEnrollmentForm records the form submission an enrollment started or resumed with.
	SetEnrollmentForm(ctx context.Context, enrollment int64, props map[string]any) error
	// SetEnrollmentTrigger records the properties of the event that started an enrollment, for
	// {{event.x}} in its messages (see eventschema.MessageProps).
	SetEnrollmentTrigger(ctx context.Context, enrollment int64, props json.RawMessage) error
	// RemoveFromJourneys ends a contact's live enrollments in these journeys (change_journey steps).
	RemoveFromJourneys(ctx context.Context, tenant int64, contact uint32, journeys []int64) (int, error)
}

type active struct {
	model.Journey
	graph Graph
}

type index struct {
	byID      map[int64]*active
	bySegment map[int64][]*active            // segment id -> journeys (segment ids are global)
	byEvent   map[int64]map[string][]*active // tenant -> event name -> journeys
	// Events that matter beyond triggers, so most events skip the database entirely.
	awaited map[int64]map[string]bool    // tenant -> events some wait_for_event step waits for
	goals   map[int64]map[string][]int64 // tenant -> goal event -> journeys
}

func newIndex() *index {
	return &index{byID: map[int64]*active{}, bySegment: map[int64][]*active{}, byEvent: map[int64]map[string][]*active{},
		awaited: map[int64]map[string]bool{}, goals: map[int64]map[string][]int64{}}
}

// SchemaLookup finds an event's schema (eventschema.Cache), so properties marked personal are
// never copied into messages.
type SchemaLookup interface {
	Lookup(ctx context.Context, tenant int64, name string) *eventschema.Schema
}

type Engine struct {
	store EngineStore
	exec  *Executor
	log   *slog.Logger
	// Schemas (optional) leaves personal properties out of the trigger event kept for messages.
	Schemas SchemaLookup
	idx     atomic.Pointer[index]
	// changed asks RunTimers for a refresh now (Notify).
	changed chan struct{}
	// invalid remembers the reason each skipped journey was last logged with (journey id → string).
	invalid sync.Map
	// changes records loaded versions for Announce (WatchChanges); nil without Redis.
	changes atomic.Pointer[changeLog]
}

// NewEngine wires the executor; x.Store is set to store.
func NewEngine(store EngineStore, x *Executor, log *slog.Logger) *Engine {
	x.Store = store
	if x.Now == nil {
		x.Now = time.Now
	}
	e := &Engine{store: store, exec: x, log: log, changed: make(chan struct{}, 1)}
	e.idx.Store(newIndex())
	return e
}

func (e *Engine) Refresh(ctx context.Context) error {
	cl := e.changes.Load()
	v := cl.version(ctx) // read first: the journeys loaded below include at least this change
	js, err := e.store.ActiveJourneys(ctx)
	if err != nil {
		return err
	}
	idx := newIndex()
	for _, j := range js {
		g, err := ParseGraph(j.Graph)
		if err != nil {
			// Logged once per journey and reason, not on every refresh; the app shows it too.
			if prev, _ := e.invalid.Swap(j.ID, err.Error()); prev != err.Error() {
				e.log.Error("skipping invalid journey", "journey", j.ID, "err", err)
			}
			continue
		}
		e.invalid.Delete(j.ID)
		if ds, err := Decisions(j.SplitWinners); err != nil {
			e.log.Error("ignoring invalid A/B winners", "journey", j.ID, "err", err)
		} else {
			g.ApplyDecisions(ds)
		}
		g.Name = j.Name
		a := &active{Journey: j, graph: g}
		idx.byID[j.ID] = a
		switch g.Trigger.Type {
		case TriggerSegmentEntered:
			idx.bySegment[g.Trigger.SegmentID] = append(idx.bySegment[g.Trigger.SegmentID], a)
		case TriggerEvent, TriggerFormSubmitted:
			name := g.Trigger.TriggerEventName()
			if idx.byEvent[j.TenantID] == nil {
				idx.byEvent[j.TenantID] = map[string][]*active{}
			}
			idx.byEvent[j.TenantID][name] = append(idx.byEvent[j.TenantID][name], a)
		}
		for _, n := range g.Nodes {
			name := n.Event
			if n.Type == NodeMessageDecision {
				name, _ = g.MessageEvent(0, n)
			} else if d := DecisionEvent(n); d != "" {
				name = d
			} else if n.Type != NodeWaitForEvent {
				continue
			}
			if idx.awaited[j.TenantID] == nil {
				idx.awaited[j.TenantID] = map[string]bool{}
			}
			idx.awaited[j.TenantID][name] = true
		}
		if g.Goal != nil {
			if idx.goals[j.TenantID] == nil {
				idx.goals[j.TenantID] = map[string][]int64{}
			}
			idx.goals[j.TenantID][g.Goal.Event] = append(idx.goals[j.TenantID][g.Goal.Event], j.ID)
		}
	}
	e.idx.Store(idx)
	cl.loaded(ctx, v, e.log)
	return nil
}

// Handle consumes segment.membership and events.enriched records.
func (e *Engine) Handle(ctx context.Context, r *kgo.Record) error {
	idx := e.idx.Load()
	switch r.Topic {
	case event.TopicMembership:
		var c model.MembershipChange
		if err := json.Unmarshal(r.Value, &c); err != nil {
			return fmt.Errorf("decode membership change: %w", err)
		}
		if !c.Entered {
			return nil
		}
		for _, j := range idx.bySegment[c.SegmentID] {
			if j.TenantID != c.TenantID || (!liveSource(c.Source) && !j.graph.Trigger.IncludeBackfill) {
				continue
			}
			if err := e.enroll(ctx, j, c.ContactID); err != nil {
				return err
			}
		}
	case event.TopicEnriched:
		var ev event.Event
		if err := json.Unmarshal(r.Value, &ev); err != nil {
			return fmt.Errorf("decode event: %w", err)
		}
		// A bounce rule that deletes contacts (package profile adds bounce_action): this role holds
		// the deleter that the "delete contact" step uses.
		if event.IsBounceAction(ev.Name) && attr.String(ev.Props["action"]) == automation.BounceDelete && ev.ContactID != 0 {
			if e.exec.Deleter == nil {
				e.log.Error("bounce rule says delete, but no deleter is set", "tenant", ev.TenantID, "contact", ev.ContactID)
				return nil
			}
			return e.exec.Deleter.DeleteContacts(ctx, ev.TenantID, []uint32{ev.ContactID})
		}
		// Double opt-in: the form's confirmation email, with the event (its confirm_url) for {{event.x}}.
		if ev.Name == event.OptinRequested && ev.ContactID != 0 {
			return e.sendOptin(ctx, ev)
		}
		// Goals first: a contact who converts leaves before anything else reacts to this event.
		if js := idx.goals[ev.TenantID][ev.Name]; len(js) > 0 {
			if _, err := e.store.Convert(ctx, ev.TenantID, ev.ContactID, js); err != nil {
				return err
			}
		}
		if ev.Name == EventJourneyChange {
			if err := e.changeJourneys(ctx, idx, ev); err != nil {
				return err
			}
		}
		if idx.awaited[ev.TenantID][ev.Name] {
			waits, err := e.store.EventWaits(ctx, ev.TenantID, ev.ContactID, ev.Name)
			if err != nil {
				return err
			}
			for _, w := range waits {
				if w.MessageID != "" && attr.String(ev.Props["message_id"]) != w.MessageID {
					continue // a message decision waits for one message's event, not any
				}
				j := idx.byID[w.Enrollment.JourneyID]
				if j == nil {
					continue // paused: the contact keeps waiting, and the timeout still applies
				}
				if !j.graph.Nodes[w.WaitNode].Matches(ev.Props) {
					continue // another form, page, asset, device, slot or link than the step waits for
				}
				won, err := e.store.Resume(ctx, w.Enrollment.ID, w.WaitNode, w.YesNode)
				if err != nil {
					return err
				}
				if won {
					e.exec.count(ctx, w.Enrollment, w.WaitNode, "yes")
					if ev.Name == EventFormSubmitted {
						if err := e.store.SetEnrollmentForm(ctx, w.Enrollment.ID, ev.Props); err != nil {
							return err
						}
					}
					if err := e.exec.Run(ctx, j.graph, w.Enrollment, w.YesNode); err != nil {
						return err // the safety timer retries it
					}
				}
			}
		}
		for _, j := range idx.byEvent[ev.TenantID][ev.Name] {
			if !j.graph.Trigger.MatchesEvent(ev.Props) {
				continue // a form trigger for another form
			}
			if err := e.enrollWith(ctx, j, ev.ContactID, &ev); err != nil {
				return err
			}
		}
	}
	return nil
}

// enroll starts a contact on a journey, if the journey's schedule is live. Contacts already inside
// keep going after the end: the schedule controls who enters, not who finishes.
func (e *Engine) enroll(ctx context.Context, j *active, contact uint32) error {
	return e.enrollWith(ctx, j, contact, nil)
}

// enrollWith is enroll; a form_submitted trigger event is recorded on the enrollment for form
// field conditions.
func (e *Engine) enrollWith(ctx context.Context, j *active, contact uint32, ev *event.Event) error {
	if !(schedule.Window{Start: j.StartAt, End: j.EndAt}).Active(time.Now()) {
		return nil
	}
	en, run, err := e.store.Enroll(ctx, j.TenantID, j.ID, contact, j.graph.Entry, pg.Reentry{Mode: j.graph.Trigger.Reentry, Days: j.graph.Trigger.Days})
	if err != nil || !run {
		return err
	}
	if ev != nil && ev.Name == EventFormSubmitted {
		if err := e.store.SetEnrollmentForm(ctx, en.ID, ev.Props); err != nil {
			return err
		}
	}
	if ev != nil {
		var s *eventschema.Schema
		if e.Schemas != nil {
			s = e.Schemas.Lookup(ctx, ev.TenantID, ev.Name)
		}
		if props := messageTrigger(ev, s); props != nil {
			if err := e.store.SetEnrollmentTrigger(ctx, en.ID, props); err != nil {
				return err
			}
		}
	}
	if err := e.exec.Milestone(ctx, en, "journey_entered"); err != nil {
		return err
	}
	return e.exec.Run(ctx, j.graph, en, j.graph.Entry)
}

// changeJourneys applies a change_journey step: removes the contact from journeys, then enrolls
// them in active ones (their schedule and re-entry rules apply). Inactive journeys are skipped.
func (e *Engine) changeJourneys(ctx context.Context, idx *index, ev event.Event) error {
	ids := func(k string) []int64 {
		var out []int64
		list, _ := ev.Props[k].([]any)
		for _, v := range list {
			if f, ok := attr.Number(attr.String(v)); ok && f > 0 {
				out = append(out, int64(f))
			}
		}
		return out
	}
	if rm := ids("remove"); len(rm) > 0 {
		if _, err := e.store.RemoveFromJourneys(ctx, ev.TenantID, ev.ContactID, rm); err != nil {
			return err
		}
	}
	for _, id := range ids("add") {
		if j := idx.byID[id]; j != nil && j.TenantID == ev.TenantID {
			if err := e.enroll(ctx, j, ev.ContactID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Notify asks for the journey index to be reloaded now (a journey was activated or changed).
func (e *Engine) Notify() {
	select {
	case e.changed <- struct{}{}:
	default: // a refresh is already pending
	}
}

// RunTimers refreshes the journey index (every refreshEvery, and at once after Notify) and fires
// due timers until ctx is done.
func (e *Engine) RunTimers(ctx context.Context) error {
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	lastRefresh := time.Time{}
	for {
		if time.Since(lastRefresh) >= refreshEvery {
			if err := e.Refresh(ctx); err != nil && ctx.Err() == nil {
				e.log.Error("refresh journeys", "err", err)
			}
			lastRefresh = time.Now()
		}
		for ctx.Err() == nil {
			n, err := e.store.ClaimDueTimers(ctx, timerBatch, e.fire)
			if err != nil && ctx.Err() == nil {
				e.log.Error("claim timers", "err", err)
			}
			if n < timerBatch {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		case <-e.changed:
			lastRefresh = time.Time{} // refresh now
		}
	}
}

func (e *Engine) fire(t model.Timer) error {
	j := e.idx.Load().byID[t.Enrollment.JourneyID]
	if j == nil {
		return fmt.Errorf("journey %d is not active", t.Enrollment.JourneyID) // retried after the lease
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if t.WaitNode != "" {
		// A wait's timer: resume only if the enrollment is still parked there (its awaited event,
		// or a goal, may have moved it on already).
		won, err := e.store.Resume(ctx, t.Enrollment.ID, t.WaitNode, t.Node)
		if err != nil || !won {
			return err
		}
		if n := j.graph.Nodes[t.WaitNode]; n.No != "" && n.No == t.Node {
			e.exec.count(ctx, t.Enrollment, t.WaitNode, "no") // a decision timed out
		}
	} else {
		// A safety timer (or one from before waits were recorded): retry unless it already finished.
		st, err := e.store.EnrollmentStatus(ctx, t.Enrollment.ID)
		if err != nil || st == StatusCompleted || st == StatusConverted {
			return err
		}
	}
	if err := e.exec.Run(ctx, j.graph, t.Enrollment, t.Node); err != nil {
		e.log.Error("resume enrollment", "enrollment", t.Enrollment.ID, "err", err)
		return err
	}
	return nil
}

// KafkaSender publishes send requests to the channel topics.
type KafkaSender struct{ Pub kafka.Publisher }

// PublishEvent puts an event into the pipeline (attribute updates, webhook outcomes).
func (s KafkaSender) PublishEvent(ctx context.Context, e event.Event) error {
	rec, err := kafka.JSON(event.TopicRaw, e.RawKey(), e)
	if err != nil {
		return err
	}
	return s.Pub.Publish(ctx, rec)
}

func (s KafkaSender) Send(ctx context.Context, m model.Message) error {
	rec, err := kafka.JSON(channel.Topic(m.Channel), event.ContactKey(m.TenantID, m.ContactID), m)
	if err != nil {
		return err
	}
	return s.Pub.Publish(ctx, rec)
}

// liveSource reports whether a membership change happened because of something the contact or a
// user just did (an event, a list edit, a segment it depends on), or a daily segment's rebuild
// (its only way to change), rather than a backfill or sweep.
func liveSource(src string) bool {
	return src == "stream" || src == "dependent" || src == "manual" || src == "daily"
}

// messageTrigger is what an enrollment keeps of its trigger event for {{event.x}}: the properties
// without personal ones, capped; nothing for an unverified browser or app event, which anyone
// could have sent for this ID (its text and links must not reach the contact's messages).
func messageTrigger(ev *event.Event, s *eventschema.Schema) json.RawMessage {
	if ev == nil || ev.Unverified {
		return nil
	}
	return eventschema.MessageProps(s, ev.Props)
}

// sendOptin sends a double opt-in confirmation email (one per optin_requested event: the message
// id is the event's, so a redelivered event is deduplicated by the dispatcher).
func (e *Engine) sendOptin(ctx context.Context, ev event.Event) error {
	tid, ok := attr.Number(attr.String(ev.Props["template_id"]))
	if !ok || tid <= 0 || e.exec.Sender == nil {
		return nil
	}
	props, _ := json.Marshal(ev.Props)
	return e.exec.Sender.Send(ctx, model.Message{ID: "optin:" + ev.ID, TenantID: ev.TenantID, ContactID: ev.ContactID,
		Channel: channel.Email, TemplateID: int64(tid), Campaign: "Double opt-in", Event: props})
}
