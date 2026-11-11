package journey

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strconv"
	"time"

	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/personalize"
	"pulse/internal/profile"
	"pulse/internal/segment"
)

const (
	StatusActive    = "active"
	StatusWaiting   = "waiting"
	StatusCompleted = "completed"
	StatusConverted = "converted" // did the journey's goal event and left early
)

type Store interface {
	SetNode(ctx context.Context, enrollment int64, node, status string) error
	Wait(ctx context.Context, enrollment int64, waitNode, resumeNode string, due time.Time) error
	WaitForEvent(ctx context.Context, en model.Enrollment, waitNode, eventName, messageID, yesNode, timeoutNode string, due time.Time) error
	// Jump moves the enrollment to target and counts the jump; it returns the new jump count.
	Jump(ctx context.Context, enrollment int64, target string) (int, error)
}

type Membership interface {
	IsMember(ctx context.Context, tenant, segment int64, contact uint32) (bool, error)
}

type Sender interface {
	Send(ctx context.Context, m model.Message) error
}

// Contacts loads a contact's profile (check, update and webhook steps, and time zones).
type Contacts interface {
	ContactByID(ctx context.Context, tenant int64, id uint32) (model.Contact, error)
}

// Events publishes events into the pipeline (attribute updates, webhook outcomes).
type Events interface {
	PublishEvent(ctx context.Context, e event.Event) error
}

type Executor struct {
	Store      Store
	Membership Membership
	Sender     Sender
	Contacts   Contacts
	Events     Events
	// Counters returns event counts for check steps (Redis day buckets in production).
	Counters func(tenant int64, contact uint32, asOf time.Time) segment.Counter
	HTTP     *http.Client // webhook steps; must refuse private addresses (safehttp)
	Now      func() time.Time

	// Collaborators of the builder's newer steps (nil: those steps fail with "not configured").
	Data    Data        // users, companies, journey states, devices, inbox, form context
	Onsite  Onsite      // per-contact focus items and DWC content
	History History     // past page views and form submissions (ClickHouse)
	Deleter Deleter     // permanent contact deletion (the cleanup path)
	MX      MXChecker   // email domain checks
	Stats   StepCounter // per-step entered / yes / no counts (best effort)
	// Integrations pushes contacts to CRM connectors (push_integration).
	Integrations Pusher
}

// Run walks the graph from node `from` until the enrollment waits or exits.
// Every step is safe to repeat: sends carry the idempotency key "{enrollment}:{node}", attribute
// updates and webhooks carry ids derived from it.
func (x *Executor) Run(ctx context.Context, g Graph, en model.Enrollment, from string) error {
	id := from
	var contact *model.Contact
	loadContact := func() (model.Contact, error) {
		if contact == nil {
			c, err := x.Contacts.ContactByID(ctx, en.TenantID, en.ContactID)
			if err != nil {
				return c, fmt.Errorf("load contact: %w", err)
			}
			contact = &c
		}
		return *contact, nil
	}
	// Validated graphs are acyclic except through jump steps, which are capped per enrollment.
	for steps := 0; steps <= (len(g.Nodes)+1)*(MaxJumps+2); steps++ {
		n, ok := g.Nodes[id]
		if !ok {
			return fmt.Errorf("journey %d: unknown node %q", en.JourneyID, id)
		}
		x.count(ctx, en, id, "entered")
		switch n.Type {
		case NodeWait:
			due := x.Now().Add(time.Duration(n.Seconds) * time.Second)
			return x.Store.Wait(ctx, en.ID, id, n.Next, due)

		case NodeWaitUntil:
			c, err := loadContact()
			if err != nil {
				return err
			}
			due := WaitUntil(n, c.Attrs, x.Now())
			if !due.After(x.Now()) { // the moment has passed: carry on now
				id = n.Next
				continue
			}
			return x.Store.Wait(ctx, en.ID, id, n.Next, due)

		case NodeWaitForEvent:
			due := x.Now().Add(time.Duration(n.Seconds) * time.Second)
			return x.Store.WaitForEvent(ctx, en, id, n.Event, "", n.Yes, n.No, due)

		case NodeOnForm, NodeOnAsset, NodeOnPage, NodeOnDevice, NodeOnDWC:
			due := x.Now().Add(time.Duration(n.Seconds) * time.Second)
			return x.Store.WaitForEvent(ctx, en, id, DecisionEvent(n), "", n.Yes, n.No, due)

		case NodeMessageDecision:
			due := x.Now().Add(time.Duration(n.Seconds) * time.Second)
			name, msgID := g.MessageEventFor(en, n)
			return x.Store.WaitForEvent(ctx, en, id, name, msgID, n.Yes, n.No, due)

		case NodeCondition:
			in, err := x.Membership.IsMember(ctx, en.TenantID, n.SegmentID, en.ContactID)
			if err != nil {
				return err
			}
			id = x.branch(ctx, en, id, n, in)

		case NodeCheck:
			rule, err := segment.Parse(n.Rule)
			if err != nil {
				return err
			}
			c, err := loadContact()
			if err != nil {
				return err
			}
			ok, err := segment.Eval(ctx, rule, c.Attrs, x.Counters(en.TenantID, en.ContactID, x.Now()))
			if errors.Is(err, segment.ErrNeedsSweep) {
				// A range that has aged past the counters since the journey was saved (saving
				// refuses such ranges): the condition can't hold, rather than retrying forever.
				ok, err = false, nil
			}
			if err != nil {
				return err
			}
			id = x.branch(ctx, en, id, n, ok)

		case NodeSplit:
			path, decided := pickPath(n.Variants, en.ID, id), false
			if d := n.decided; d != nil { // an A/B winner: everyone takes it from now on
				path, decided = d.Path, true
			}
			if x.Events != nil && x.Contacts != nil {
				c, err := loadContact()
				if err != nil {
					return err
				}
				v := n.Variants[path]
				if err := x.Events.PublishEvent(ctx, event.Event{
					ID: "jsplit:" + StepKey(en, id), TenantID: en.TenantID, ExternalID: c.ExternalID,
					Name: event.JourneySplit, Ts: x.Now().UTC(),
					Props: map[string]any{"journey_id": en.JourneyID, "step": id, "path": path, "label": v.Label, "decided": decided},
				}); err != nil {
					return err
				}
			}
			x.count(ctx, en, id, "v"+strconv.Itoa(path))
			id = n.Variants[path].Next

		case NodeUpdate:
			c, err := loadContact()
			if err != nil {
				return err
			}
			ev, err := x.triggerProps(ctx, g, en)
			if err != nil {
				return err
			}
			attrs := ResolveAttrsWith(n.Attrs, c, ev, x.Now())
			if err := x.Events.PublishEvent(ctx, event.Event{
				ID: "jattr:" + StepKey(en, id), TenantID: en.TenantID, ExternalID: c.ExternalID,
				Name: event.Identify, Ts: x.Now().UTC(), Attrs: attrs,
				Props: map[string]any{"journey_id": en.JourneyID, "step": id},
			}); err != nil {
				return err
			}
			for k, v := range attrs { // later checks in this run see the new values
				contact.Attrs[k] = v
			}
			id = n.Next

		case NodeTags, NodePoints, NodeList, NodeStage:
			c, err := loadContact()
			if err != nil {
				return err
			}
			for i, e := range audienceEvents(n) {
				e.ID = fmt.Sprintf("jaud:%s:%d", StepKey(en, id), i)
				e.TenantID, e.ExternalID, e.Ts = en.TenantID, c.ExternalID, x.Now().UTC()
				e.Props["journey_id"], e.Props["step"] = en.JourneyID, id
				if err := x.Events.PublishEvent(ctx, e); err != nil {
					return err
				}
				// Later checks in this run see the change (the profile role applies the same merge).
				contact.Attrs, _ = profile.Merge(contact.Attrs, &e)
			}
			id = n.Next

		case NodeWebhook:
			c, err := loadContact()
			if err != nil {
				return err
			}
			if err := x.webhook(ctx, g, n, en, id, c); err != nil {
				return err
			}
			id = n.Next

		case NodeSendEmail, NodeSendSMS, NodeSendWhatsApp, NodeSendRCS, NodeSendPush, NodeSendInbox:
			m, err := x.message(ctx, g, en, id, SendChannel(n.Type), n.TemplateID)
			if err != nil {
				return err
			}
			m.FallbackTemplateID = n.FallbackTemplateID
			m.Deadline = n.Deadline.After(x.Now())
			if err := x.Sender.Send(ctx, m); err != nil {
				return err
			}
			id = n.Next

		case NodeExit:
			if err := x.Store.SetNode(ctx, en.ID, id, StatusCompleted); err != nil {
				return err
			}
			return x.Milestone(ctx, en, "journey_completed")

		default:
			if n.Type != NodeIfBefore && n.Type != NodeJump {
				if _, err := loadContact(); err != nil {
					return err
				}
			}
			next, stop, err := x.runStep(ctx, g, &en, id, n, contact)
			if err != nil || stop {
				return err
			}
			id = next
		}
	}
	return fmt.Errorf("journey %d: step limit exceeded", en.JourneyID)
}

// pickVariant chooses a split path by weight; see pickPath.
func pickVariant(vs []Variant, enrollment int64, step string) string {
	return vs[pickPath(vs, enrollment, step)].Next
}

// pickPath chooses a split path's index by weight. The choice is a hash of the enrollment and step,
// so a retried run always takes the same path.
func pickPath(vs []Variant, enrollment int64, step string) int {
	total := 0
	for _, v := range vs {
		total += v.Weight
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.FormatInt(enrollment, 10) + ":" + step))
	r := int(h.Sum32() % uint32(total))
	for i, v := range vs {
		if r < v.Weight {
			return i
		}
		r -= v.Weight
	}
	return len(vs) - 1
}

// WaitUntil resolves a wait_until step to a moment: the fixed date-time, or the next occurrence of
// the time of day in the contact's time zone (attribute "timezone"), else the step's, else UTC.
func WaitUntil(n Node, attrs map[string]any, now time.Time) time.Time {
	if n.Until != "" {
		t, _ := time.Parse(time.RFC3339, n.Until)
		return t
	}
	loc := time.UTC
	for _, name := range []string{fmt.Sprint(attrs["timezone"]), n.Timezone} {
		if l, err := time.LoadLocation(name); err == nil && name != "" && name != "<nil>" {
			loc = l
			break
		}
	}
	hh, _ := strconv.Atoi(n.TimeOfDay[:2])
	mm, _ := strconv.Atoi(n.TimeOfDay[3:])
	local := now.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, loc)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

type webhookData struct {
	Contact      model.Contact
	Attrs        map[string]any
	JourneyID    int64
	EnrollmentID int64
	Step         string
}

// jsonFunc is {{json .Attrs}} in webhook bodies.
var jsonFunc = map[string]any{"json": func(v any) personalize.JSONRaw {
	b, _ := json.Marshal(v)
	return personalize.JSONRaw(b)
}}

// checkBody checks a webhook body's variables and pipes. (Go template mistakes surface when the step
// runs, as before, so a journey saved earlier never stops loading.)
func checkBody(body string) error {
	_, _, err := personalize.Translate(body, false)
	return err
}

// TriggerReader reads the event an enrollment started with (pg.Store).
type TriggerReader interface {
	EnrollmentTrigger(ctx context.Context, enrollment int64) (json.RawMessage, error)
}

// trigger returns the properties of the event that started the enrollment (for {{event.x}}), or
// nil: journeys started by a segment have none, nor do contacts added by another journey.
func (x *Executor) trigger(ctx context.Context, g Graph, en model.Enrollment) (json.RawMessage, error) {
	if g.Trigger.Type != TriggerEvent && g.Trigger.Type != TriggerFormSubmitted {
		return nil, nil
	}
	tr, ok := x.Store.(TriggerReader)
	if !ok {
		return nil, nil
	}
	return tr.EnrollmentTrigger(ctx, en.ID)
}

// triggerProps is trigger, decoded.
func (x *Executor) triggerProps(ctx context.Context, g Graph, en model.Enrollment) (map[string]any, error) {
	raw, err := x.trigger(ctx, g, en)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m, nil
}

// message is a send step's message, carrying the journey's personalisation context: the trigger
// event ({{event.x}}), the journey's name and the step.
func (x *Executor) message(ctx context.Context, g Graph, en model.Enrollment, step, ch string, template int64) (model.Message, error) {
	ev, err := x.trigger(ctx, g, en)
	if err != nil {
		return model.Message{}, fmt.Errorf("load trigger event: %w", err)
	}
	return model.Message{ID: StepKey(en, step), TenantID: en.TenantID, ContactID: en.ContactID, Channel: ch,
		TemplateID: template, JourneyID: en.JourneyID, Event: ev, Campaign: g.Name, Step: step}, nil
}

// webhook calls the step's URL. It retries briefly, then records webhook_failed and lets the
// journey continue: one slow endpoint must never stall everyone behind it.
func (x *Executor) webhook(ctx context.Context, g Graph, n Node, en model.Enrollment, step string, c model.Contact) error {
	var body []byte
	if n.Body == "" {
		body, _ = json.Marshal(map[string]any{
			"contact":    map[string]any{"external_id": c.ExternalID, "email": c.Email, "attrs": c.Attrs},
			"journey_id": en.JourneyID, "enrollment_id": en.ID, "step": step,
		})
	} else {
		ev, err := x.triggerProps(ctx, g, en)
		if err != nil {
			return err
		}
		r := personalize.New(personalize.Vars{Contact: c, Event: ev,
			Campaign: personalize.Campaign{ID: en.JourneyID, Name: g.Name, Type: "journey", Step: step, Channel: "webhook"}})
		r.Extra = jsonFunc
		out, err := r.JSON("body", n.Body, webhookData{c, c.Attrs, en.JourneyID, en.ID, step})
		if err != nil {
			return fmt.Errorf("webhook body: %w", err)
		}
		body = []byte(out)
	}
	var last string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Pulse-Webhook/1")
		req.Header.Set("X-Pulse-Idempotency-Key", StepKey(en, step))
		resp, err := x.HTTP.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
			last = "HTTP " + strconv.Itoa(resp.StatusCode)
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				break // the endpoint rejected it: retrying won't help
			}
		} else {
			last = err.Error()
		}
	}
	return x.Events.PublishEvent(ctx, event.Event{
		ID: "jhook:" + StepKey(en, step), TenantID: en.TenantID, ExternalID: c.ExternalID,
		Name: "webhook_failed", Ts: x.Now().UTC(),
		Props: map[string]any{"journey_id": en.JourneyID, "step": step, "url": n.URL, "error": last},
	})
}

// Milestone records journey_entered or journey_completed on the contact (props journey_id), so
// segments can select contacts by journey ("entered Welcome in the last 30 days", "never completed
// Onboarding"). The id makes a retried step record it once.
func (x *Executor) Milestone(ctx context.Context, en model.Enrollment, name string) error {
	if x.Events == nil || x.Contacts == nil {
		return nil
	}
	c, err := x.Contacts.ContactByID(ctx, en.TenantID, en.ContactID)
	if err != nil {
		return err
	}
	return x.Events.PublishEvent(ctx, event.Event{
		ID: fmt.Sprintf("jm:%d:%s", en.ID, name), TenantID: en.TenantID, ExternalID: c.ExternalID,
		Name: name, Ts: x.Now().UTC(), Props: map[string]any{"journey_id": en.JourneyID},
	})
}

// audienceEvents are the events a tags, points or list step records.
func audienceEvents(n Node) []event.Event {
	switch n.Type {
	case NodeTags:
		var out []event.Event
		if len(n.AddTags) > 0 {
			out = append(out, event.Event{Name: event.TagAdded, Props: map[string]any{"tags": n.AddTags}})
		}
		if len(n.RemoveTags) > 0 {
			out = append(out, event.Event{Name: event.TagRemoved, Props: map[string]any{"tags": n.RemoveTags}})
		}
		return out
	case NodePoints:
		return []event.Event{{Name: event.PointsChanged, Props: map[string]any{"delta": n.Points}}}
	case NodeStage:
		return []event.Event{{Name: event.StageChanged, Props: map[string]any{"stage": n.Stage, "source": "journey"}}}
	default:
		name := event.ListJoined
		if n.ListAction == "remove" {
			name = event.ListLeft
		}
		return []event.Event{{Name: name, Props: map[string]any{"list_id": n.ListID}}}
	}
}
