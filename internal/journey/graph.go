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
	"slices"
	"strconv"
	"strings"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/profile"
	"pulse/internal/segment"
)

const (
	TriggerSegmentEntered = "segment_entered"
	TriggerEvent          = "event"
	TriggerFormSubmitted  = "form_submitted" // a form submission: any form, or form_id

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
	// NodeMessageDecision waits for an earlier send step's message to be opened, clicked, delivered
	// or read (yes) or the timeout to pass (no): Mautic's "opens email" / WebEngage's "message read".
	NodeMessageDecision = "message_decision"
	NodeCheck           = "check"         // any segment rule, evaluated for this contact right now
	NodeSplit           = "split"         // A/B/n split by weight, stable per enrollment
	NodeUpdate          = "update_attr"   // set profile attributes
	NodeWebhook         = "webhook"       // call an external HTTP endpoint
	NodeTags            = "update_tags"   // add and remove tags
	NodePoints          = "adjust_points" // add (or take away) points
	NodeList            = "update_list"   // add to or remove from a static list
	NodeStage           = "change_stage"  // move the contact to a lifecycle stage

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
	FormID    int64  `json:"form_id,omitempty"` // form_submitted: this form only (0 = any form)
	// Where (event triggers): only events whose properties pass every filter enroll, e.g.
	// payment_method = upi. Same operators and paths as segment property filters.
	Where []segment.PropFilter `json:"where,omitempty"`
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

// SplitWinner turns a split into an A/B test with an automatic winner: once the test has run for
// AfterSeconds (from the first contact through the split) and every path has MinPerVariant contacts,
// the path with the best rate of Metric wins and every later contact takes it.
type SplitWinner struct {
	Metric        string `json:"metric"`          // opened | clicked (a send step after the split) | converted (the goal)
	AfterSeconds  int    `json:"after_seconds"`   // test window
	MinPerVariant int    `json:"min_per_variant"` // contacts each path needs before a decision
}

// SplitDecision is a split's automatic winner, recorded on the journey (journeys.split_winners).
type SplitDecision struct {
	Path      int                  `json:"path"`
	Next      string               `json:"next"`
	Label     string               `json:"label,omitempty"`
	Metric    string               `json:"metric"`
	DecidedAt time.Time            `json:"decided_at"`
	Stats     []analytics.PathStat `json:"stats"`
}

const maxSplitTest = 90 * 24 * 3600

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
	Step       string          `json:"step,omitempty"`        // message_decision: the send step whose message it watches
	Outcome    string          `json:"outcome,omitempty"`     // message_decision: opened | clicked | delivered | read | replied
	Rule       json.RawMessage `json:"rule,omitempty"`        // check: a segment rule
	Variants   []Variant       `json:"variants,omitempty"`    // split
	Winner     *SplitWinner    `json:"winner,omitempty"`      // split: pick a winning path automatically
	Attrs      map[string]any  `json:"attrs,omitempty"`       // update_attr
	AddTags    []string        `json:"add_tags,omitempty"`    // update_tags
	RemoveTags []string        `json:"remove_tags,omitempty"` // update_tags
	Points     int             `json:"points,omitempty"`      // adjust_points
	ListID     int64           `json:"list_id,omitempty"`     // update_list
	ListAction string          `json:"list_action,omitempty"` // update_list: add | remove
	Stage      string          `json:"stage,omitempty"`       // change_stage: the stage's name
	URL        string          `json:"url,omitempty"`         // webhook; message_decision (clicked: link contains); on_page, if_visited (contains)
	Body       string          `json:"body,omitempty"`        // webhook: JSON template; empty sends the contact

	// Steps added with the Mautic-style builder (see nodes.go for what each one takes).
	FormID           int64            `json:"form_id,omitempty"`           // on_form, if_form_field (0 = any form)
	AssetID          int64            `json:"asset_id,omitempty"`          // on_asset (0 = any asset)
	PageID           int64            `json:"page_id,omitempty"`           // on_page: a landing page
	DeviceType       string           `json:"device_type,omitempty"`       // on_device
	OS               string           `json:"os,omitempty"`                // on_device
	Slot             string           `json:"slot,omitempty"`              // on_dwc, push_dwc
	HTML             string           `json:"html,omitempty"`              // push_dwc
	Days             int              `json:"days,omitempty"`              // if_visited (0 = ever), push_dwc (how long)
	Preset           string           `json:"preset,omitempty"`            // check: the builder preset (UI hint)
	JourneyIDs       []int64          `json:"journey_ids,omitempty"`       // if_journey
	JourneyState     string           `json:"journey_state,omitempty"`     // if_journey: active | completed | entered
	Field            string           `json:"field,omitempty"`             // if_form_field
	Op               string           `json:"op,omitempty"`                // if_form_field
	Value            string           `json:"value,omitempty"`             // if_form_field
	Notify           string           `json:"notify,omitempty"`            // if_notification: push | inbox | any
	RejectDisposable bool             `json:"reject_disposable,omitempty"` // if_valid_email
	Channel          string           `json:"channel,omitempty"`           // if_dnc, dnc_add, dnc_remove
	Reason           string           `json:"reason,omitempty"`            // dnc_add
	UserIDs          []int64          `json:"user_ids,omitempty"`          // notify_user
	ToOwner          bool             `json:"to_owner,omitempty"`          // notify_user
	Emails           []string         `json:"emails,omitempty"`            // notify_user
	Channels         []ChannelMessage `json:"channels,omitempty"`          // send_message, in priority order
	FocusID          int64            `json:"focus_id,omitempty"`          // show_focus
	OwnerID          *int64           `json:"owner_id,omitempty"`          // update_owner (0 = unassign)
	CompanyAttrs     map[string]any   `json:"company_attrs,omitempty"`     // update_company
	CompanyID        int64            `json:"company_id,omitempty"`        // add_company
	AddLists         []int64          `json:"add_lists,omitempty"`         // update_list
	RemoveLists      []int64          `json:"remove_lists,omitempty"`      // update_list
	AddJourneys      []int64          `json:"add_journeys,omitempty"`      // change_journey
	RemoveJourneys   []int64          `json:"remove_journeys,omitempty"`   // change_journey
	Target           string           `json:"target,omitempty"`            // jump
	Confirm          string           `json:"confirm,omitempty"`           // delete_contact: "DELETE"
	IntegrationID    int64            `json:"integration_id,omitempty"`    // push_integration: a CRM connector
	// UI layout, ignored by the engine.
	X float64 `json:"x,omitempty"`
	Y float64 `json:"y,omitempty"`

	decided *SplitDecision // split: the recorded winner (ApplyDecisions), never from JSON
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

// TriggerEventName is the event that enrolls contacts in an event or form trigger ("" for segments).
func (t Trigger) TriggerEventName() string {
	switch t.Type {
	case TriggerEvent:
		return t.Event
	case TriggerFormSubmitted:
		return EventFormSubmitted
	}
	return ""
}

// MatchesEvent reports whether an event of the trigger's name enrolls: a form trigger with a
// form_id takes that form's submissions only, and an event trigger's property filters must pass.
func (t Trigger) MatchesEvent(props map[string]any) bool {
	if len(t.Where) > 0 && !segment.MatchProps(t.Where, props) {
		return false
	}
	if t.Type != TriggerFormSubmitted || t.FormID == 0 {
		return true
	}
	v, _ := attr.Number(attr.String(props["form_id"]))
	return int64(v) == t.FormID
}

// Validate checks the trigger, every node, every edge, and that the graph has no cycles,
// so the executor always terminates.
func (g Graph) Validate() error {
	switch g.Trigger.Type {
	case TriggerSegmentEntered:
		if g.Trigger.SegmentID <= 0 {
			return errors.New("segment_entered trigger needs segment_id")
		}
		if len(g.Trigger.Where) > 0 {
			return errors.New("segment trigger: property filters apply to event triggers only")
		}
	case TriggerEvent:
		if !event.ValidName(g.Trigger.Event) {
			return fmt.Errorf("event trigger: invalid event name %q", g.Trigger.Event)
		}
		if err := segment.ValidatePropFilters("event trigger", g.Trigger.Where); err != nil {
			return err
		}
	case TriggerFormSubmitted:
		if g.Trigger.FormID < 0 {
			return errors.New("form trigger: invalid form_id")
		}
		if len(g.Trigger.Where) > 0 {
			return errors.New("form trigger: property filters apply to event triggers only (use a form field condition)")
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
		case NodeMessageDecision:
			if err := g.validMessageDecision(id, n); err != nil {
				return err
			}
		case NodeWaitForEvent:
			if !event.ValidName(n.Event) {
				return fmt.Errorf("wait for event %q: enter the event to wait for", id)
			}
			if n.Seconds < 1 || n.Seconds > maxWaitSecs {
				return fmt.Errorf("wait for event %q: the timeout must be 1 second to 365 days", id)
			}
		case NodeCheck:
			if len(bytes.TrimSpace(n.Rule)) == 0 || string(bytes.TrimSpace(n.Rule)) == "null" {
				return fmt.Errorf("check %q: describe who takes the Yes path", id)
			}
			rule, err := segment.Parse(n.Rule)
			if err != nil {
				return fmt.Errorf("check %q: %w", id, err)
			}
			if rule.SweepOnly(time.Now()) {
				return fmt.Errorf("check %q: a condition can count events of the last 365 days at most; for older ranges, build a segment and use an in-segment condition", id)
			}
			if !slices.Contains(checkPresets, n.Preset) {
				return fmt.Errorf("check %q: unknown preset %q", id, n.Preset)
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
			if err := g.validWinner(id, n); err != nil {
				return err
			}
		case NodeUpdate:
			if len(n.Attrs) == 0 {
				return fmt.Errorf("update %q: set at least one attribute", id)
			}
			for k := range n.Attrs {
				if !attr.ValidKey(k) || consent.IsReserved(k) {
					return fmt.Errorf("update %q: %q can't be set here (Pulse manages it: consent, tags, points, stage and company change through their own events and steps)", id, k)
				}
			}
			if err := validUpdateValues(n.Attrs); err != nil {
				return fmt.Errorf("update %q: %w", id, err)
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
			if err := validList(n); err != nil {
				return fmt.Errorf("list %q: %w", id, err)
			}
		case NodeStage:
			if !profile.ValidStageName(n.Stage) {
				return fmt.Errorf("stage %q: choose the stage to move the contact to", id)
			}
		case NodeWebhook:
			u, err := url.Parse(n.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fmt.Errorf("webhook %q needs an http(s) URL", id)
			}
		case NodeExit:
		default:
			ok, err := g.validNew(id, n)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("node %q: unknown type %q", id, n.Type)
			}
		}
	}
	return g.checkAcyclic()
}

func (n Node) edges() []string {
	switch n.Type {
	case NodeWait, NodeSendEmail, NodeSendSMS, NodeSendWhatsApp, NodeSendRCS, NodeSendPush, NodeSendInbox, NodeWaitUntil, NodeUpdate, NodeWebhook,
		NodeTags, NodePoints, NodeList, NodeStage:
		return []string{n.Next}
	case NodeCondition, NodeCheck, NodeWaitForEvent, NodeMessageDecision:
		return []string{n.Yes, n.No}
	case NodeSplit:
		out := make([]string, len(n.Variants))
		for i, v := range n.Variants {
			out[i] = v.Next
		}
		return out
	}
	switch kindOf(n.Type) {
	case kindAction:
		return []string{n.Next}
	case kindDecision, kindCondition:
		return []string{n.Yes, n.No}
	}
	return nil
}

func (g Graph) validWinner(id string, n Node) error {
	w := n.Winner
	if w == nil {
		return nil
	}
	switch w.Metric {
	case analytics.SplitOpened, analytics.SplitClicked:
		if len(g.TrackedSendsAfter(id)) == 0 {
			return fmt.Errorf("split %q: a winner by %s needs an email or app inbox step after the split", id, w.Metric)
		}
	case analytics.SplitConverted:
		if g.Goal == nil {
			return fmt.Errorf("split %q: a winner by conversions needs the journey's goal event", id)
		}
	default:
		return fmt.Errorf("split %q: pick the winner by opened, clicked or converted", id)
	}
	if w.AfterSeconds < 1 || w.AfterSeconds > maxSplitTest {
		return fmt.Errorf("split %q: test for 1 second to 90 days", id)
	}
	if w.MinPerVariant < 1 || w.MinPerVariant > 1_000_000 {
		return fmt.Errorf("split %q: each path needs 1 to 1,000,000 contacts before a winner is picked", id)
	}
	return nil
}

// TrackedSendsAfter lists the send steps reachable from a split whose messages can be opened and
// clicked (email, app inbox), sorted: the steps an A/B winner by opens or clicks is measured on.
func (g Graph) TrackedSendsAfter(split string) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		n, ok := g.Nodes[id]
		if !ok {
			return
		}
		if ch := sendNodes[n.Type]; ch == channel.Email || ch == channel.Inbox {
			out = append(out, id)
		}
		for _, next := range n.edges() {
			walk(next)
		}
	}
	for _, next := range g.Nodes[split].edges() {
		walk(next)
	}
	slices.Sort(out)
	return out
}

// ApplyDecisions attaches recorded A/B winners to their split steps, so the executor routes every
// later contact down the winning path. A decision whose step is no longer a split with a winner, or
// whose path now leads elsewhere (the journey was edited), is ignored.
func (g Graph) ApplyDecisions(ds map[string]SplitDecision) {
	for step, d := range ds {
		n, ok := g.Nodes[step]
		if !ok || n.Type != NodeSplit || n.Winner == nil || d.Path < 0 || d.Path >= len(n.Variants) || n.Variants[d.Path].Next != d.Next {
			continue
		}
		d := d
		n.decided = &d
		g.Nodes[step] = n
	}
}

// Decision is a split step's applied winner, if any.
func (n Node) Decision() *SplitDecision { return n.decided }

// Outcomes lists what a message decision can wait for on each channel.
var Outcomes = map[string][]string{
	channel.Email:    {"opened", "clicked", "delivered", "replied"},
	channel.SMS:      {"delivered"},
	channel.WhatsApp: {"delivered", "read"},
	channel.RCS:      {"delivered", "read", "replied"},
	channel.Push:     {"delivered", "clicked"},
	channel.Inbox:    {"opened", "clicked"},
}

func (g Graph) validMessageDecision(id string, n Node) error {
	step, ok := g.Nodes[n.Step]
	ch := sendNodes[step.Type]
	if !ok || ch == "" {
		return fmt.Errorf("message decision %q: choose a send step it follows", id)
	}
	if !slices.Contains(Outcomes[ch], n.Outcome) {
		return fmt.Errorf("message decision %q: a %s message can be %s", id, ch, strings.Join(Outcomes[ch], ", "))
	}
	if n.Seconds < 60 || n.Seconds > maxWaitSecs {
		return fmt.Errorf("message decision %q: wait 1 minute to 365 days for it", id)
	}
	if n.URL != "" && ch == channel.Push {
		return fmt.Errorf("message decision %q: a push click has no link to filter on; leave the link empty", id)
	}
	if n.URL != "" && (n.Outcome != "clicked" || len(n.URL) > maxURLFilter) {
		return fmt.Errorf("message decision %q: a link filter (up to %d characters) only applies to clicks", id, maxURLFilter)
	}
	if !g.reaches(n.Step, id) {
		return fmt.Errorf("message decision %q: put the send step it watches (%q) above it", id, n.Step)
	}
	return nil
}

// reaches reports whether a contact at step from can arrive at step to (following edges, not jumps).
func (g Graph) reaches(from, to string) bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, next := range g.Nodes[id].edges() {
			if next == to {
				return true
			}
			stack = append(stack, next)
		}
	}
	return false
}

// MessageEvent is the event and message id a message decision waits for.
func (g Graph) MessageEvent(enrollment int64, n Node) (string, string) {
	return g.MessageEventFor(model.Enrollment{ID: enrollment}, n)
}

// MessageEventFor is MessageEvent for an enrollment that may have jumped (its message ids carry the jump count).
func (g Graph) MessageEventFor(en model.Enrollment, n Node) (string, string) {
	return sendNodes[g.Nodes[n.Step].Type] + "_" + n.Outcome, StepKey(en, n.Step)
}

// StepKey is the idempotency key of a step for an enrollment: "{enrollment}:{step}", plus ":j{n}"
// once the enrollment has jumped back n times, so a step repeated after a jump really runs again.
func StepKey(en model.Enrollment, step string) string {
	k := strconv.FormatInt(en.ID, 10) + ":" + step
	if en.Jumps > 0 {
		k += ":j" + strconv.Itoa(en.Jumps)
	}
	return k
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
