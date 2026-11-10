package journey

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/store/pg"
)

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

type Engine struct {
	store EngineStore
	exec  *Executor
	log   *slog.Logger
	idx   atomic.Pointer[index]
}

// NewEngine wires the executor; x.Store is set to store.
func NewEngine(store EngineStore, x *Executor, log *slog.Logger) *Engine {
	x.Store = store
	if x.Now == nil {
		x.Now = time.Now
	}
	e := &Engine{store: store, exec: x, log: log}
	e.idx.Store(newIndex())
	return e
}

func (e *Engine) Refresh(ctx context.Context) error {
	js, err := e.store.ActiveJourneys(ctx)
	if err != nil {
		return err
	}
	idx := newIndex()
	for _, j := range js {
		g, err := ParseGraph(j.Graph)
		if err != nil {
			e.log.Error("skipping invalid journey", "journey", j.ID, "err", err)
			continue
		}
		a := &active{Journey: j, graph: g}
		idx.byID[j.ID] = a
		switch g.Trigger.Type {
		case TriggerSegmentEntered:
			idx.bySegment[g.Trigger.SegmentID] = append(idx.bySegment[g.Trigger.SegmentID], a)
		case TriggerEvent:
			if idx.byEvent[j.TenantID] == nil {
				idx.byEvent[j.TenantID] = map[string][]*active{}
			}
			idx.byEvent[j.TenantID][g.Trigger.Event] = append(idx.byEvent[j.TenantID][g.Trigger.Event], a)
		}
		for _, n := range g.Nodes {
			name := n.Event
			if n.Type == NodeMessageDecision {
				name, _ = g.MessageEvent(0, n)
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
		// Goals first: a contact who converts leaves before anything else reacts to this event.
		if js := idx.goals[ev.TenantID][ev.Name]; len(js) > 0 {
			if _, err := e.store.Convert(ctx, ev.TenantID, ev.ContactID, js); err != nil {
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
				won, err := e.store.Resume(ctx, w.Enrollment.ID, w.WaitNode, w.YesNode)
				if err != nil {
					return err
				}
				if won {
					if err := e.exec.Run(ctx, j.graph, w.Enrollment, w.YesNode); err != nil {
						return err // the safety timer retries it
					}
				}
			}
		}
		for _, j := range idx.byEvent[ev.TenantID][ev.Name] {
			if err := e.enroll(ctx, j, ev.ContactID); err != nil {
				return err
			}
		}
	}
	return nil
}

// enroll starts a contact on a journey, if the journey's schedule is live. Contacts already inside
// keep going after the end: the schedule controls who enters, not who finishes.
func (e *Engine) enroll(ctx context.Context, j *active, contact uint32) error {
	if !(schedule.Window{Start: j.StartAt, End: j.EndAt}).Active(time.Now()) {
		return nil
	}
	en, run, err := e.store.Enroll(ctx, j.TenantID, j.ID, contact, j.graph.Entry, pg.Reentry{Mode: j.graph.Trigger.Reentry, Days: j.graph.Trigger.Days})
	if err != nil || !run {
		return err
	}
	return e.exec.Run(ctx, j.graph, en, j.graph.Entry)
}

// RunTimers refreshes the journey index and fires due timers until ctx is done.
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
// user just did (an event, a list edit, a segment it depends on), rather than a backfill or sweep.
func liveSource(src string) bool {
	return src == "stream" || src == "dependent" || src == "manual"
}
