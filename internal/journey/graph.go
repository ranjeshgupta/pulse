// Package journey runs multi-step journeys: a trigger enrolls a contact, then the executor walks
// the node graph until it reaches a wait (parked on a durable timer) or an exit.
package journey

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/segment"
)

const (
	TriggerSegmentEntered = "segment_entered"
	TriggerEvent          = "event"

	NodeWait         = "wait"
	NodeCondition    = "condition"
	NodeSendEmail    = "send_email"
	NodeSendSMS      = "send_sms"
	NodeSendWhatsApp = "send_whatsapp"
	NodeSendRCS      = "send_rcs"
	NodeSendPush     = "send_push"
	NodeSendInbox    = "send_inbox" // a message in the contact's app inbox
	NodeExit         = "exit"
	NodeWaitUntil    = "wait_until"     // a date/time, or the next time of day in the contact's zone
	NodeWaitForEvent = "wait_for_event" // until the contact does an event (yes) or the timeout passes (no)
	NodeCheck        = "check"          // any segment rule, evaluated for this contact right now
	NodeSplit        = "split"          // A/B/n split by weight, stable per enrollment
	NodeUpdate       = "update_attr"    // set profile attributes
	NodeWebhook      = "webhook"        // call an external HTTP endpoint
	NodeTags         = "update_tags"    // add and remove tags
	NodePoints       = "adjust_points"  // add (or take away) points
	NodeList         = "update_list"    // add to or remove from a static list

	ReentryOnce      = "once"
	ReentryAlways    = "always"     // again after each finish
	ReentryAfterDays = "after_days" // again once `days` have passed since the last entry

	maxNodes    = 500
	maxWaitSecs = 365 * 24 * 3600
)

// sendNodes maps each send node type to its channel.
var sendNodes = map[string]string{
	NodeSendEmail: channel.Email, NodeSendSMS: channel.SMS, NodeSendWhatsApp: channel.WhatsApp,
	NodeSendRCS: channel.RCS, NodeSendPush: channel.Push, NodeSendInbox: channel.Inbox,
}

// SendChannel returns the channel of a send node ("" for other node types).
func SendChannel(nodeType string) string { return sendNodes[nodeType] }

type Trigger struct {
	Type      string `json:"type"`
	SegmentID int64  `json:"segment_id,omitempty"`
	Event     string `json:"event,omitempty"`
	// IncludeBackfill also enrolls contacts that entered the segment through a backfill or sweep,
	// not just live changes. Off by default so creating a segment never mass-enrolls by surprise.
	IncludeBackfill bool `json:"include_backfill,omitempty"`
	// Reentry: once (default), always, or after_days with Days.
	Reentry string `json:"reentry,omitempty"`
	Days    int    `json:"days,omitempty"`
}

// Goal ends the journey early for contacts who do the goal event: they count as converted.
type Goal struct {
	Event string `json:"event"`
}

type Variant struct {
	Weight int    `json:"weight"`
	Next   string `json:"next"`
	Label  string `json:"label,omitempty"`
}

type Node struct {
	Type       string `json:"type"`
	Next       string `json:"next,omitempty"`        // wait, send_email
	Seconds    int    `json:"seconds,omitempty"`     // wait
	SegmentID  int64  `json:"segment_id,omitempty"`  // condition
	Yes        string `json:"yes,omitempty"`         // condition
	No         string `json:"no,omitempty"`          // condition
	TemplateID int64  `json:"template_id,omitempty"` // send_*
	// FallbackTemplateID (send_whatsapp, send_rcs): SMS template sent if the contact can't be reached.
	FallbackTemplateID int64 `json:"fallback_template_id,omitempty"`

	Until      string          `json:"until,omitempty"`       // wait_until: RFC 3339 date-time
	TimeOfDay  string          `json:"time_of_day,omitempty"` // wait_until: "09:30", next occurrence
	Timezone   string          `json:"timezone,omitempty"`    // wait_until: zone when the contact has none
	Event      string          `json:"event,omitempty"`       // wait_for_event
	Rule       json.RawMessage `json:"rule,omitempty"`        // check: a segment rule
	Variants   []Variant       `json:"variants,omitempty"`    // split
	Attrs      map[string]any  `json:"attrs,omitempty"`       // update_attr
	AddTags    []string        `json:"add_tags,omitempty"`    // update_tags
	RemoveTags []string        `json:"remove_tags,omitempty"` // update_tags
	Points     int             `json:"points,omitempty"`      // adjust_points
	ListID     int64           `json:"list_id,omitempty"`     // update_list
	ListAction string          `json:"list_action,omitempty"` // update_list: add | remove
	URL        string          `json:"url,omitempty"`         // webhook
	Body       string          `json:"body,omitempty"`        // webhook: JSON template; empty sends the contact
	// UI layout, ignored by the engine.
	X float64 `json:"x,omitempty"`
	Y float64 `json:"y,omitempty"`
}

type Graph struct {
	Trigger Trigger         `json:"trigger"`
	Goal    *Goal           `json:"goal,omitempty"`
	Entry   string          `json:"entry"`
	Nodes   map[string]Node `json:"nodes"`
}

func ParseGraph(b []byte) (Graph, error) {
	var g Graph
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return Graph{}, fmt.Errorf("decode journey: %w", err)
	}
	return g, g.Validate()
}

// Validate checks the trigger, every node, every edge, and that the graph has no cycles,
// so the executor always terminates.
func (g Graph) Validate() error {
	switch g.Trigger.Type {
	case TriggerSegmentEntered:
		if g.Trigger.SegmentID <= 0 {
			return errors.New("segment_entered trigger needs segment_id")
		}
	case TriggerEvent:
		if !event.ValidName(g.Trigger.Event) {
			return fmt.Errorf("event trigger: invalid event name %q", g.Trigger.Event)
		}
	default:
		return fmt.Errorf("unknown trigger type %q", g.Trigger.Type)
	}
	switch g.Trigger.Reentry {
	case "", ReentryOnce, ReentryAlways:
	case ReentryAfterDays:
		if g.Trigger.Days < 1 || g.Trigger.Days > 3650 {
			return errors.New("re-entry after days needs 1-3650 days")
		}
	default:
		return fmt.Errorf("unknown re-entry rule %q", g.Trigger.Reentry)
	}
	if g.Goal != nil && !event.ValidName(g.Goal.Event) {
		return fmt.Errorf("goal: invalid event name %q", g.Goal.Event)
	}
	if len(g.Nodes) == 0 || len(g.Nodes) > maxNodes {
		return fmt.Errorf("journey needs 1-%d nodes", maxNodes)
	}
	if _, ok := g.Nodes[g.Entry]; !ok {
		return fmt.Errorf("entry node %q does not exist", g.Entry)
	}
	for id, n := range g.Nodes {
		for _, next := range n.edges() {
			if next == "" {
				return fmt.Errorf("node %q (%s) is missing an edge", id, n.Type)
			}
			if _, ok := g.Nodes[next]; !ok {
				return fmt.Errorf("node %q points to unknown node %q", id, next)
			}
		}
		switch n.Type {
		case NodeWait:
			if n.Seconds < 1 || n.Seconds > maxWaitSecs {
				return fmt.Errorf("wait %q: seconds must be 1-%d", id, maxWaitSecs)
			}
		case NodeCondition:
			if n.SegmentID <= 0 {
				return fmt.Errorf("condition %q needs segment_id", id)
			}
		case NodeSendEmail, NodeSendSMS, NodeSendWhatsApp, NodeSendRCS, NodeSendPush, NodeSendInbox:
			if n.TemplateID <= 0 {
				return fmt.Errorf("%s %q needs template_id", n.Type, id)
			}
			if n.FallbackTemplateID != 0 && !channel.FallsBackToSMS(sendNodes[n.Type]) {
				return fmt.Errorf("%s %q: only send_whatsapp and send_rcs take an SMS fallback", n.Type, id)
			}
		case NodeWaitUntil:
			if err := validWaitUntil(n); err != nil {
				return fmt.Errorf("wait until %q: %w", id, err)
			}
		case NodeWaitForEvent:
			if !event.ValidName(n.Event) {
				return fmt.Errorf("wait for event %q: enter the event to wait for", id)
			}
			if n.Seconds < 1 || n.Seconds > maxWaitSecs {
				return fmt.Errorf("wait for event %q: the timeout must be 1 second to 365 days", id)
			}
		case NodeCheck:
			if _, err := segment.Parse(n.Rule); err != nil {
				return fmt.Errorf("check %q: %w", id, err)
			}
		case NodeSplit:
			if len(n.Variants) < 2 || len(n.Variants) > 5 {
				return fmt.Errorf("split %q needs 2 to 5 paths", id)
			}
			total := 0
			for _, v := range n.Variants {
				if v.Weight < 1 {
					return fmt.Errorf("split %q: every path needs a weight of at least 1", id)
				}
				total += v.Weight
			}
			if total > 10000 {
				return fmt.Errorf("split %q: weights are too large", id)
			}
		case NodeUpdate:
			if len(n.Attrs) == 0 {
				return fmt.Errorf("update %q: set at least one attribute", id)
			}
			for k := range n.Attrs {
				if !attr.ValidKey(k) || consent.IsReserved(k) {
					return fmt.Errorf("update %q: %q can't be set here (consent changes only through unsubscribe events)", id, k)
				}
			}
		case NodeTags:
			if len(n.AddTags)+len(n.RemoveTags) == 0 {
				return fmt.Errorf("tags %q: add or remove at least one tag", id)
			}
			for _, t := range append(append([]string{}, n.AddTags...), n.RemoveTags...) {
				if !segment.ValidTag(t) {
					return fmt.Errorf("tags %q: %q isn't a valid tag (letters, digits, - and _)", id, t)
				}
			}
		case NodePoints:
			if n.Points == 0 || n.Points > 100000 || n.Points < -100000 {
				return fmt.Errorf("points %q: enter a non-zero number of points", id)
			}
		case NodeList:
			if n.ListID <= 0 || (n.ListAction != "add" && n.ListAction != "remove") {
				return fmt.Errorf("list %q: choose a list and whether to add or remove", id)
			}
		case NodeWebhook:
			u, err := url.Parse(n.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fmt.Errorf("webhook %q needs an http(s) URL", id)
			}
		case NodeExit:
		default:
			return fmt.Errorf("node %q: unknown type %q", id, n.Type)
		}
	}
	return g.checkAcyclic()
}

func (n Node) edges() []string {
	switch n.Type {
	case NodeWait, NodeSendEmail, NodeSendSMS, NodeSendWhatsApp, NodeSendRCS, NodeSendPush, NodeSendInbox, NodeWaitUntil, NodeUpdate, NodeWebhook,
		NodeTags, NodePoints, NodeList:
		return []string{n.Next}
	case NodeCondition, NodeCheck, NodeWaitForEvent:
		return []string{n.Yes, n.No}
	case NodeSplit:
		out := make([]string, len(n.Variants))
		for i, v := range n.Variants {
			out[i] = v.Next
		}
		return out
	}
	return nil
}

var timeOfDay = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func validWaitUntil(n Node) error {
	switch {
	case n.Until != "" && n.TimeOfDay != "":
		return errors.New("choose a date-time or a time of day, not both")
	case n.Until != "":
		if _, err := time.Parse(time.RFC3339, n.Until); err != nil {
			return errors.New("the date-time isn't valid")
		}
	case n.TimeOfDay != "":
		if !timeOfDay.MatchString(n.TimeOfDay) {
			return errors.New("time of day must look like 09:30")
		}
		if n.Timezone != "" {
			if _, err := time.LoadLocation(n.Timezone); err != nil {
				return fmt.Errorf("unknown time zone %q", n.Timezone)
			}
		}
	default:
		return errors.New("choose a date-time or a time of day")
	}
	return nil
}

func (g Graph) checkAcyclic() error {
	const (
		unseen = iota
		onPath
		done
	)
	state := map[string]int{}
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case onPath:
			return fmt.Errorf("journey has a cycle through node %q", id)
		case done:
			return nil
		}
		state[id] = onPath
		for _, next := range g.Nodes[id].edges() {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[id] = done
		return nil
	}
	for id := range g.Nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
