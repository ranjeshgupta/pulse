package journey

// The steps that map Mautic's campaign decisions, actions and conditions onto Pulse. Each is
// validated here; graph.go keeps the original steps.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"

	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/segment"
)

const (
	// Decisions: wait up to Seconds for the contact to do something (yes) or not (no).
	NodeOnForm   = "on_form"   // submits a form (any, or FormID)
	NodeOnAsset  = "on_asset"  // downloads an asset (any, or AssetID)
	NodeOnPage   = "on_page"   // visits a page (URL contains, or a landing page)
	NodeOnDevice = "on_device" // visits from a device (DeviceType and/or OS)
	NodeOnDWC    = "on_dwc"    // the contact's page requests a Dynamic Web Content slot

	// Conditions: checked right now, yes or no.
	NodeIfJourney      = "if_journey"      // in / completed / entered other journeys
	NodeIfFormField    = "if_form_field"   // a field of the contact's form submission
	NodeIfNotification = "if_notification" // has a push device or an unread app inbox message
	NodeIfValidEmail   = "if_valid_email"  // email syntax and its domain accepts mail (MX)
	NodeIfDNC          = "if_dnc"          // marked Do Not Contact on a channel
	NodeIfBefore       = "if_before"       // yes until a date: "prevent action after date"
	NodeIfVisited      = "if_visited"      // has visited a page (ever, or in N days)

	// Actions.
	NodeNotifyUser      = "notify_user"      // email to workspace users, the owner or fixed addresses
	NodeSendMessage     = "send_message"     // the first channel of a list the contact can receive
	NodeShowFocus       = "show_focus"       // show a focus item to this contact on their next page view
	NodePushDWC         = "push_dwc"         // this contact's content for a DWC slot
	NodeUpdateOwner     = "update_owner"     // assign (or unassign) the contact's owner
	NodeUpdateCompany   = "update_company"   // set attributes of the contact's company
	NodeAddCompany      = "add_company"      // link the contact to a company
	NodeCompanyPoints   = "company_points"   // add to the company's score
	NodeChangeJourney   = "change_journey"   // enroll in / remove from other journeys
	NodeJump            = "jump"             // continue at another step (terminal; capped)
	NodeDNCAdd          = "dnc_add"          // add Do Not Contact
	NodeDNCRemove       = "dnc_remove"       // remove Do Not Contact
	NodeDeleteContact   = "delete_contact"   // permanently delete the contact (terminal)
	NodePushIntegration = "push_integration" // send the contact to a CRM integration (mapped fields)

	// MaxJumps is how many jump steps one enrollment may take; the next one ends the journey for it.
	MaxJumps = 25
	// DeleteConfirm must be typed into a delete_contact step.
	DeleteConfirm = "DELETE"

	maxURLFilter  = 500
	maxDWCHTML    = 64 << 10
	maxRecipients = 20
	maxRefs       = 20
	minDecision   = 60
)

// Events the new decisions wait for.
const (
	EventFormSubmitted = "form_submitted"
	EventDWCRequested  = event.DWCRequested
	EventJourneyChange = event.JourneyChange // the engine enrolls / removes (props add, remove)
	EventJumpLimit     = event.JourneyJumpLimit
	EventPushFailed    = "integration_push_failed" // a push_integration step couldn't reach the CRM: props integration_id, error
)

// ChannelMessage is one channel of a send_message step.
type ChannelMessage struct {
	Channel    string `json:"channel"`
	TemplateID int64  `json:"template_id"`
}

type kind int

const (
	kindUnknown kind = iota
	kindAction
	kindDecision
	kindCondition
	kindTerminal
)

var kinds = map[string]kind{
	NodeOnForm: kindDecision, NodeOnAsset: kindDecision, NodeOnPage: kindDecision, NodeOnDevice: kindDecision, NodeOnDWC: kindDecision,
	NodeIfJourney: kindCondition, NodeIfFormField: kindCondition, NodeIfNotification: kindCondition, NodeIfValidEmail: kindCondition,
	NodeIfDNC: kindCondition, NodeIfBefore: kindCondition, NodeIfVisited: kindCondition,
	NodeNotifyUser: kindAction, NodeSendMessage: kindAction, NodeShowFocus: kindAction, NodePushDWC: kindAction,
	NodeUpdateOwner: kindAction, NodeUpdateCompany: kindAction, NodeAddCompany: kindAction, NodeCompanyPoints: kindAction,
	NodeChangeJourney: kindAction, NodeDNCAdd: kindAction, NodeDNCRemove: kindAction, NodePushIntegration: kindAction,
	NodeJump: kindTerminal, NodeDeleteContact: kindTerminal,
}

func kindOf(t string) kind { return kinds[t] }

// DecisionEvent is the event a decision step (on_*) waits for.
func DecisionEvent(n Node) string {
	switch n.Type {
	case NodeOnForm:
		return EventFormSubmitted
	case NodeOnAsset:
		return event.AssetDownloaded
	case NodeOnPage:
		if n.PageID > 0 {
			return event.LandingPageViewed
		}
		return event.PageView
	case NodeOnDevice:
		return event.PageView
	case NodeOnDWC:
		return EventDWCRequested
	}
	return ""
}

// Matches reports whether an event the step waits for counts: the right form, asset, page,
// device, slot or (message decisions) link. Other steps match any event of their name.
func (n Node) Matches(props map[string]any) bool {
	num := func(k string) int64 {
		v, _ := attr.Number(attr.String(props[k]))
		return int64(v)
	}
	switch n.Type {
	case NodeOnForm:
		return n.FormID == 0 || num("form_id") == n.FormID
	case NodeOnAsset:
		return n.AssetID == 0 || num("asset_id") == n.AssetID
	case NodeOnPage:
		if n.PageID > 0 {
			return num("page_id") == n.PageID
		}
		return n.URL == "" || strings.Contains(attr.String(props["url"]), n.URL)
	case NodeOnDevice:
		return (n.DeviceType == "" || strings.EqualFold(attr.String(props["device_type"]), n.DeviceType)) &&
			(n.OS == "" || strings.EqualFold(attr.String(props["os"]), n.OS))
	case NodeOnDWC:
		return attr.String(props["slot"]) == n.Slot
	case NodeMessageDecision:
		return n.URL == "" || strings.Contains(attr.String(props["url"]), n.URL)
	}
	return true
}

var (
	slotRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	fieldVarRe = regexp.MustCompile(`\{contactfield=([^}]*)\}`)
	dateMathRe = regexp.MustCompile(`^now(?:([+-])([0-9]{1,4})\s*(minute|hour|day|week|month|year)s?)?$`)
)

var deviceTypes = []string{"desktop", "mobile", "tablet"}
var checkPresets = []string{"", "field", "tags", "stage", "points", "owner", "device"}
var formOps = []string{"eq", "ne", "contains", "not_contains", "starts_with", "ends_with", "gt", "gte", "lt", "lte", "exists", "not_exists"}

// dncChannel reports whether ch names a channel (or "all") for Do Not Contact steps.
func dncChannel(ch string) bool { return ch == "all" || channel.Valid(ch) }

func validDecisionWait(id string, n Node) error {
	if n.Seconds < minDecision || n.Seconds > maxWaitSecs {
		return fmt.Errorf("decision %q: wait 1 minute to 365 days for it", id)
	}
	return nil
}

func positive(ids []int64) bool {
	for _, id := range ids {
		if id <= 0 {
			return false
		}
	}
	return true
}

func overlap(a, b []int64) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func validList(n Node) error {
	if n.ListID != 0 || n.ListAction != "" {
		if n.ListID <= 0 || (n.ListAction != "add" && n.ListAction != "remove") || len(n.AddLists)+len(n.RemoveLists) > 0 {
			return errors.New("choose a list and whether to add or remove")
		}
		return nil
	}
	switch {
	case len(n.AddLists)+len(n.RemoveLists) == 0:
		return errors.New("choose a list to add to or remove from")
	case len(n.AddLists) > maxRefs || len(n.RemoveLists) > maxRefs:
		return fmt.Errorf("up to %d lists each", maxRefs)
	case !positive(n.AddLists) || !positive(n.RemoveLists):
		return errors.New("choose a list to add to or remove from")
	case overlap(n.AddLists, n.RemoveLists):
		return errors.New("a list can't be both added to and removed from")
	}
	return nil
}

// FormFieldRule is the segment rule an if_form_field step evaluates over the submission's fields.
func FormFieldRule(n Node) json.RawMessage {
	r := map[string]any{"attr": n.Field, "op": n.Op}
	if n.Op != "exists" && n.Op != "not_exists" {
		var v any = n.Value
		if f, ok := attr.Number(n.Value); ok && (n.Op == "gt" || n.Op == "gte" || n.Op == "lt" || n.Op == "lte") {
			v = f
		}
		r["value"] = v
	}
	b, _ := json.Marshal(r)
	return b
}

// validUpdateValues checks update_attr variables: {contactfield=key} names a valid key and Go
// templates parse.
func validUpdateValues(attrs map[string]any) error {
	for k, v := range attrs {
		s, ok := v.(string)
		if !ok {
			continue
		}
		for _, m := range fieldVarRe.FindAllStringSubmatch(s, -1) {
			if !attr.ValidKey(m[1]) {
				return fmt.Errorf("%s: {contactfield=%s} doesn't name a field", k, m[1])
			}
		}
		if strings.Contains(s, "{{") {
			if _, err := template.New(k).Parse(s); err != nil {
				return fmt.Errorf("%s: %v", k, err)
			}
		}
	}
	return nil
}

// validNew validates the builder's steps; false means the type is unknown.
func (g Graph) validNew(id string, n Node) (bool, error) {
	fail := func(format string, a ...any) (bool, error) {
		return true, fmt.Errorf("%s %q: %s", n.Type, id, fmt.Sprintf(format, a...))
	}
	switch n.Type {
	case NodeOnForm, NodeOnAsset, NodeOnDWC:
		if n.FormID < 0 || n.AssetID < 0 {
			return fail("choose a form or asset")
		}
		if n.Type == NodeOnDWC && !slotRe.MatchString(n.Slot) {
			return fail("enter the slot name (lower case letters, digits, - and _)")
		}
		return true, validDecisionWait(id, n)
	case NodeOnPage:
		if n.URL != "" && n.PageID != 0 || n.PageID < 0 || len(n.URL) > maxURLFilter {
			return fail("choose a landing page or enter what the URL contains, not both")
		}
		return true, validDecisionWait(id, n)
	case NodeOnDevice:
		if n.DeviceType == "" && strings.TrimSpace(n.OS) == "" {
			return fail("choose a device type or an operating system")
		}
		if n.DeviceType != "" && !slices.Contains(deviceTypes, n.DeviceType) || len(n.OS) > 50 {
			return fail("device type is desktop, mobile or tablet")
		}
		return true, validDecisionWait(id, n)

	case NodeIfJourney:
		if len(n.JourneyIDs) == 0 || len(n.JourneyIDs) > maxRefs || !positive(n.JourneyIDs) {
			return fail("choose 1 to %d journeys", maxRefs)
		}
		if n.JourneyState != "active" && n.JourneyState != "completed" && n.JourneyState != "entered" {
			return fail("choose whether the contact is in, has completed or has entered them")
		}
	case NodeIfFormField:
		if n.FormID < 0 || !attr.ValidKey(n.Field) || !slices.Contains(formOps, n.Op) {
			return fail("choose a field and how to compare it")
		}
		if _, err := segment.Parse(FormFieldRule(n)); err != nil {
			return fail("%v", err)
		}
	case NodeIfNotification:
		if n.Notify != "push" && n.Notify != "inbox" && n.Notify != "any" {
			return fail("choose push, app inbox or either")
		}
	case NodeIfValidEmail:
	case NodeIfDNC:
		if n.Channel != "any" && !channel.Valid(n.Channel) {
			return fail("choose a channel, or any")
		}
	case NodeIfBefore:
		if _, err := time.Parse(time.RFC3339, n.Until); err != nil {
			return fail("choose the date after which contacts take No")
		}
	case NodeIfVisited:
		if n.Days < 0 || n.Days > 365 || len(n.URL) > maxURLFilter {
			return fail("look back 1 to 365 days, or 0 for ever")
		}

	case NodeNotifyUser:
		if n.TemplateID <= 0 {
			return fail("choose an email template")
		}
		if !n.ToOwner && len(n.UserIDs)+len(n.Emails) == 0 {
			return fail("choose who gets it: users, the contact's owner or email addresses")
		}
		if len(n.UserIDs) > maxRecipients || len(n.Emails) > maxRecipients || !positive(n.UserIDs) {
			return fail("up to %d users and %d addresses", maxRecipients, maxRecipients)
		}
		for _, e := range n.Emails {
			if a, err := mail.ParseAddress(e); err != nil || a.Address != e {
				return fail("%q isn't an email address", e)
			}
		}
	case NodeSendMessage:
		if len(n.Channels) == 0 || len(n.Channels) > len(channel.All) {
			return fail("add 1 to %d channels", len(channel.All))
		}
		seen := map[string]bool{}
		for _, c := range n.Channels {
			if !channel.Valid(c.Channel) || seen[c.Channel] {
				return fail("each channel once")
			}
			seen[c.Channel] = true
			if c.TemplateID <= 0 {
				return fail("choose a %s template", c.Channel)
			}
		}
	case NodeShowFocus:
		if n.FocusID <= 0 {
			return fail("choose a focus item")
		}
	case NodePushDWC:
		if !slotRe.MatchString(n.Slot) {
			return fail("enter the slot name (lower case letters, digits, - and _)")
		}
		if strings.TrimSpace(n.HTML) == "" || len(n.HTML) > maxDWCHTML {
			return fail("enter the content (up to 64 KB)")
		}
		if n.Days < 0 || n.Days > 365 {
			return fail("keep it 1 to 365 days")
		}
	case NodeUpdateOwner:
		if n.OwnerID == nil || *n.OwnerID < 0 {
			return fail("choose the owner, or none to unassign")
		}
	case NodeUpdateCompany:
		if len(n.CompanyAttrs) == 0 || len(n.CompanyAttrs) > 50 {
			return fail("set 1 to 50 company attributes")
		}
		for k, v := range n.CompanyAttrs {
			if !attr.ValidKey(k) || len(k) > 64 || k == "id" || k == "name" {
				return fail("%q: letters, digits and _, not id or name", k)
			}
			switch x := v.(type) {
			case string:
				if len(x) > 1000 {
					return fail("%q: keep it under 1000 characters", k)
				}
			case float64, bool:
			default:
				return fail("%q: use text, a number or true/false", k)
			}
		}
	case NodeAddCompany:
		if n.CompanyID <= 0 {
			return fail("choose a company")
		}
	case NodeCompanyPoints:
		if n.Points == 0 || n.Points > 100000 || n.Points < -100000 {
			return fail("enter a non-zero number of points")
		}
	case NodeChangeJourney:
		if len(n.AddJourneys)+len(n.RemoveJourneys) == 0 || len(n.AddJourneys) > maxRefs || len(n.RemoveJourneys) > maxRefs ||
			!positive(n.AddJourneys) || !positive(n.RemoveJourneys) {
			return fail("choose journeys to add the contact to or remove them from")
		}
		if overlap(n.AddJourneys, n.RemoveJourneys) {
			return fail("a journey can't be both added to and removed from")
		}
	case NodeJump:
		t, ok := g.Nodes[n.Target]
		switch {
		case !ok:
			return fail("choose the step to jump to")
		case n.Target == id:
			return fail("a step can't jump to itself")
		case t.Type == NodeJump:
			return fail("jump to a real step, not another jump")
		}
	case NodeDNCAdd, NodeDNCRemove:
		if !dncChannel(n.Channel) {
			return fail("choose a channel, or all")
		}
		if len(n.Reason) > 200 {
			return fail("keep the reason under 200 characters")
		}
	case NodeDeleteContact:
		if n.Confirm != DeleteConfirm {
			return fail("type DELETE to confirm: this permanently deletes the contact")
		}
	case NodePushIntegration:
		if n.IntegrationID <= 0 {
			return fail("choose the integration to push the contact to")
		}
	default:
		return false, nil
	}
	return true, nil
}

// HasType reports whether the journey has a step of type t (permission checks).
func (g Graph) HasType(t string) bool {
	for _, n := range g.Nodes {
		if n.Type == t {
			return true
		}
	}
	return false
}
