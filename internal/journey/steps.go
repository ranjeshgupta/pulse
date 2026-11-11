package journey

// Execution of the builder's newer steps (nodes.go): decisions are parked by Run; conditions and
// actions run here. Every side effect carries an id derived from StepKey, so a retried run applies
// it once.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/onsite"
	"pulse/internal/personalize"
	"pulse/internal/profile"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
)

// Data is what the newer steps read and change in the workspace (Postgres in production).
type Data interface {
	ListUsers(ctx context.Context, tenant int64) ([]model.User, error)
	GetCompany(ctx context.Context, tenant, id int64) (pg.Company, error)
	// MergeCompanyAttrs sets attributes on a company and returns it.
	MergeCompanyAttrs(ctx context.Context, tenant, id int64, attrs map[string]any) (pg.Company, error)
	// AddCompanyScore adds delta to the company's score attribute once per key; applied is false
	// when the key was used before (a retried step).
	AddCompanyScore(ctx context.Context, tenant, id int64, key string, delta int) (c pg.Company, applied bool, err error)
	MemberIDs(ctx context.Context, tenant int64, key, value string, after uint32, limit int) ([]string, uint32, error)
	JourneyStates(ctx context.Context, tenant int64, contact uint32, journeys []int64) (map[int64]pg.JourneyState, error)
	ContactDevices(ctx context.Context, tenant int64, contact uint32) ([]model.Device, error)
	UnreadInbox(ctx context.Context, tenant int64, contact uint32) (int, error)
	// EnrollmentForm is the form submission recorded on the enrollment (props of form_submitted).
	EnrollmentForm(ctx context.Context, enrollment int64) (map[string]any, error)
}

// Onsite keeps what a contact sees on their next page view.
type Onsite interface {
	PushFocus(ctx context.Context, tenant int64, contact uint32, focusID int64) error
	PushDWC(ctx context.Context, tenant int64, contact uint32, slot, html string, ttl time.Duration) error
}

// History answers questions about a contact's past events.
type History interface {
	// CountVisits counts page_view events whose url contains urlContains, in the last days (0 = ever).
	CountVisits(ctx context.Context, tenant int64, contact uint32, urlContains string, days int) (uint64, error)
	// LatestForm returns the props of the contact's latest form_submitted (of form, 0 = any), or nil.
	LatestForm(ctx context.Context, tenant int64, contact uint32, form int64) (map[string]any, error)
}

// Deleter permanently deletes contacts with everything kept for them.
type Deleter interface {
	DeleteContacts(ctx context.Context, tenant int64, ids []uint32) error
}

// MXChecker reports whether an email domain accepts mail.
type MXChecker interface {
	AcceptsMail(ctx context.Context, domain string) bool
}

// Pusher sends a contact to an integration (a CRM) through its field mapping and returns the
// remote record's id. eventID makes the recorded crm_pushed event idempotent.
type Pusher interface {
	PushContact(ctx context.Context, tenant, integration int64, c model.Contact, eventID string) (string, error)
}

// StepCounter counts contacts per step and output: "entered", "yes", "no", "v0".. (best effort).
type StepCounter interface {
	Count(ctx context.Context, tenant, journey int64, step, output string)
}

var errNotConfigured = errors.New("this step isn't available on this server")

const fanOutPage = 1000

func (x *Executor) count(ctx context.Context, en model.Enrollment, step, output string) {
	if x.Stats != nil {
		x.Stats.Count(ctx, en.TenantID, en.JourneyID, step, output)
	}
}

// branch records a yes/no step's outcome and returns the next step.
func (x *Executor) branch(ctx context.Context, en model.Enrollment, id string, n Node, yes bool) string {
	if yes {
		x.count(ctx, en, id, "yes")
		return n.Yes
	}
	x.count(ctx, en, id, "no")
	return n.No
}

func (x *Executor) publish(ctx context.Context, en model.Enrollment, id string, c *model.Contact, i int, name string, props map[string]any) error {
	if props == nil {
		props = map[string]any{}
	}
	props["journey_id"], props["step"] = en.JourneyID, id
	e := event.Event{ID: fmt.Sprintf("jstep:%s:%d", StepKey(en, id), i), TenantID: en.TenantID, ExternalID: c.ExternalID,
		Name: name, Ts: x.Now().UTC(), Props: props}
	if err := x.Events.PublishEvent(ctx, e); err != nil {
		return err
	}
	c.Attrs, _ = profile.Merge(c.Attrs, &e) // later checks in this run see the change
	return nil
}

// runStep runs one of the newer steps. stop means the enrollment is done (or parked).
func (x *Executor) runStep(ctx context.Context, g Graph, en *model.Enrollment, id string, n Node, c *model.Contact) (string, bool, error) {
	need := func(ok bool) error {
		if !ok {
			return fmt.Errorf("%s step %q: %w", n.Type, id, errNotConfigured)
		}
		return nil
	}
	switch n.Type {
	// ---- conditions ----
	case NodeIfBefore:
		until, _ := time.Parse(time.RFC3339, n.Until)
		return x.branch(ctx, *en, id, n, x.Now().Before(until)), false, nil

	case NodeIfDNC:
		return x.branch(ctx, *en, id, n, MarkedDNC(c.Attrs, n.Channel)), false, nil

	case NodeIfValidEmail:
		if err := need(x.MX != nil); err != nil {
			return "", false, err
		}
		return x.branch(ctx, *en, id, n, ValidEmail(ctx, x.MX, c.Email, n.RejectDisposable)), false, nil

	case NodeIfJourney:
		if err := need(x.Data != nil); err != nil {
			return "", false, err
		}
		states, err := x.Data.JourneyStates(ctx, en.TenantID, en.ContactID, n.JourneyIDs)
		if err != nil {
			return "", false, err
		}
		ok := false
		for _, j := range n.JourneyIDs {
			st, in := states[j]
			switch n.JourneyState {
			case "active":
				ok = ok || st.Active
			case "completed":
				ok = ok || st.Completed
			default:
				ok = ok || in
			}
		}
		return x.branch(ctx, *en, id, n, ok), false, nil

	case NodeIfNotification:
		if err := need(x.Data != nil); err != nil {
			return "", false, err
		}
		ok := false
		if n.Notify != "inbox" && !consent.OptedOut(c.Attrs, channel.Push) {
			devs, err := x.Data.ContactDevices(ctx, en.TenantID, en.ContactID)
			if err != nil {
				return "", false, err
			}
			ok = len(devs) > 0
		}
		if !ok && n.Notify != "push" {
			unread, err := x.Data.UnreadInbox(ctx, en.TenantID, en.ContactID)
			if err != nil {
				return "", false, err
			}
			ok = unread > 0
		}
		return x.branch(ctx, *en, id, n, ok), false, nil

	case NodeIfVisited:
		if err := need(x.History != nil); err != nil {
			return "", false, err
		}
		v, err := x.History.CountVisits(ctx, en.TenantID, en.ContactID, n.URL, n.Days)
		if err != nil {
			return "", false, err
		}
		return x.branch(ctx, *en, id, n, v > 0), false, nil

	case NodeIfFormField:
		if err := need(x.Data != nil); err != nil {
			return "", false, err
		}
		sub, err := x.Data.EnrollmentForm(ctx, en.ID)
		if err != nil {
			return "", false, err
		}
		if sub == nil || n.FormID != 0 && formID(sub) != n.FormID {
			sub = nil
			if x.History != nil {
				if sub, err = x.History.LatestForm(ctx, en.TenantID, en.ContactID, n.FormID); err != nil {
					return "", false, err
				}
			}
		}
		ok := false
		if fields, _ := sub["fields"].(map[string]any); fields != nil {
			rule, err := segment.Parse(FormFieldRule(n))
			if err != nil {
				return "", false, err
			}
			if ok, err = segment.Eval(ctx, rule, fields, segment.BucketCounter{}); err != nil {
				return "", false, err
			}
		}
		return x.branch(ctx, *en, id, n, ok), false, nil

	// ---- actions ----
	case NodeNotifyUser:
		to, err := x.notifyRecipients(ctx, en.TenantID, n, c)
		if err != nil {
			return "", false, err
		}
		if len(to) > 0 {
			m, err := x.message(ctx, g, *en, id, channel.Email, n.TemplateID)
			if err != nil {
				return "", false, err
			}
			m.To = to
			if err := x.Sender.Send(ctx, m); err != nil {
				return "", false, err
			}
		}
		return n.Next, false, nil

	case NodeSendMessage:
		var ev map[string]any
		if HasConditions(n.Channels) {
			var err error
			if ev, err = x.triggerProps(ctx, g, *en); err != nil {
				return "", false, err
			}
		}
		matched := 0
		for _, cm := range n.Channels {
			if !cm.Matches(*c, ev) {
				continue
			}
			matched++
			ok, err := x.reachable(ctx, *c, cm.Channel)
			if err != nil {
				return "", false, err
			}
			if !ok {
				continue
			}
			m, err := x.message(ctx, g, *en, id, cm.Channel, cm.TemplateID)
			if err != nil {
				return "", false, err
			}
			m.FallbackTemplateID = cm.FallbackTemplateID
			m.Deadline = n.Deadline.After(x.Now())
			if err := x.Sender.Send(ctx, m); err != nil {
				return "", false, err
			}
			x.count(ctx, *en, id, ChannelOutput(cm.Channel))
			return n.Next, false, nil
		}
		x.count(ctx, *en, id, ChannelOutput(""))
		if matched == 0 {
			// No rule applied to this contact (and no "otherwise"): record why for the report.
			return n.Next, false, x.publish(ctx, *en, id, c, 0, EventMessageSkipped,
				map[string]any{"channels": len(n.Channels), "reason": "no channel rule matched the contact"})
		}
		// Nobody to reach on any channel: record it so the report can show why.
		return n.Next, false, x.publish(ctx, *en, id, c, 0, "message_unreachable", map[string]any{"channels": len(n.Channels)})

	case NodeShowFocus:
		if err := need(x.Onsite != nil); err != nil {
			return "", false, err
		}
		return n.Next, false, x.Onsite.PushFocus(ctx, en.TenantID, en.ContactID, n.FocusID)

	case NodePushDWC:
		if err := need(x.Onsite != nil); err != nil {
			return "", false, err
		}
		days := n.Days
		if days == 0 {
			days = 30
		}
		html := n.HTML
		if strings.Contains(html, "{{") {
			ev, err := x.triggerProps(ctx, g, *en)
			if err != nil {
				return "", false, err
			}
			r := personalize.New(personalize.Vars{Contact: *c, Event: ev,
				Campaign: personalize.Campaign{ID: en.JourneyID, Name: g.Name, Type: "journey", Step: id, Channel: "dwc"}})
			out, err := r.HTML("html", html, webhookData{Contact: *c, Attrs: c.Attrs})
			// The page asks for it by the visitor's ID: the contact's data is shown only to a
			// verified identity, so an anonymous version (defaults) is kept beside it.
			anon := model.Contact{Attrs: map[string]any{}}
			ar := personalize.New(personalize.Vars{Campaign: personalize.Campaign{ID: en.JourneyID, Name: g.Name, Type: "journey", Step: id, Channel: "dwc"}})
			aout, aerr := ar.HTML("html", html, webhookData{Contact: anon, Attrs: anon.Attrs})
			if err == nil && aerr == nil {
				html = onsite.PersonalDWC(out, aout)
			}
		}
		return n.Next, false, x.Onsite.PushDWC(ctx, en.TenantID, en.ContactID, n.Slot, html, time.Duration(days)*24*time.Hour)

	case NodeUpdateOwner:
		props := map[string]any{"owner_id": 0, "source": "journey"}
		if *n.OwnerID > 0 {
			if err := need(x.Data != nil); err != nil {
				return "", false, err
			}
			u, ok, err := x.user(ctx, en.TenantID, *n.OwnerID)
			if err != nil {
				return "", false, err
			}
			if !ok {
				return n.Next, false, nil // the user was deleted: leave the owner alone
			}
			props["owner_id"], props["owner_name"] = u.ID, userName(u)
		}
		return n.Next, false, x.publish(ctx, *en, id, c, 0, event.OwnerChanged, props)

	case NodeAddCompany:
		if err := need(x.Data != nil); err != nil {
			return "", false, err
		}
		co, err := x.Data.GetCompany(ctx, en.TenantID, n.CompanyID)
		if errors.Is(err, pg.ErrNotFound) {
			return n.Next, false, nil // deleted since the journey was saved
		} else if err != nil {
			return "", false, err
		}
		return n.Next, false, x.publish(ctx, *en, id, c, 0, event.CompanyLinked, CompanyProps(co, "journey"))

	case NodeUpdateCompany, NodeCompanyPoints:
		if err := need(x.Data != nil); err != nil {
			return "", false, err
		}
		cid, _ := attr.Number(attr.String(c.Attrs[event.AttrCompanyID]))
		if cid <= 0 {
			return n.Next, false, nil // no company: nothing to update
		}
		var co pg.Company
		var err error
		applied := true
		if n.Type == NodeUpdateCompany {
			co, err = x.Data.MergeCompanyAttrs(ctx, en.TenantID, int64(cid), n.CompanyAttrs)
		} else {
			co, applied, err = x.Data.AddCompanyScore(ctx, en.TenantID, int64(cid), "jcs:"+StepKey(*en, id), n.Points)
		}
		if errors.Is(err, pg.ErrNotFound) {
			return n.Next, false, nil
		} else if err != nil {
			return "", false, err
		}
		if !applied {
			return n.Next, false, nil
		}
		return n.Next, false, x.fanOutCompany(ctx, *en, id, co, c)

	case NodeChangeJourney:
		return n.Next, false, x.publish(ctx, *en, id, c, 0, EventJourneyChange,
			map[string]any{"add": n.AddJourneys, "remove": n.RemoveJourneys})

	case NodeDNCAdd, NodeDNCRemove:
		name, props := consent.Resubscribed, map[string]any{"channel": n.Channel, "source": "journey", "method": consent.MethodManual}
		if n.Type == NodeDNCAdd {
			name = consent.Unsubscribed
			if n.Reason != "" {
				props["reason"] = n.Reason
			}
		}
		return n.Next, false, x.publish(ctx, *en, id, c, 0, name, props)

	case NodeJump:
		if en.Jumps >= MaxJumps {
			// The loop guard: leave the journey instead of jumping again.
			if c, err := x.Contacts.ContactByID(ctx, en.TenantID, en.ContactID); err == nil && x.Events != nil {
				if err := x.publish(ctx, *en, id, &c, 0, EventJumpLimit, map[string]any{"target": n.Target, "jumps": en.Jumps}); err != nil {
					return "", false, err
				}
			}
			if err := x.Store.SetNode(ctx, en.ID, id, StatusCompleted); err != nil {
				return "", false, err
			}
			return "", true, x.Milestone(ctx, *en, "journey_completed")
		}
		jumps, err := x.Store.Jump(ctx, en.ID, n.Target)
		if err != nil {
			return "", false, err
		}
		en.Jumps = jumps
		return n.Target, false, nil

	case NodePushIntegration:
		if err := need(x.Integrations != nil); err != nil {
			return "", false, err
		}
		// Like the webhook step, a failed push is recorded and the journey carries on: one CRM
		// outage must never stall everyone behind it.
		_, err := x.Integrations.PushContact(ctx, en.TenantID, n.IntegrationID, *c, "jpush:"+StepKey(*en, id))
		if err != nil {
			if ctx.Err() != nil {
				return "", false, ctx.Err()
			}
			return n.Next, false, x.publish(ctx, *en, id, c, 0, EventPushFailed,
				map[string]any{"integration_id": n.IntegrationID, "error": err.Error()})
		}
		return n.Next, false, nil

	case NodeDeleteContact:
		if err := need(x.Deleter != nil); err != nil {
			return "", false, err
		}
		// Deleting the contact also deletes its enrollments: nothing is left to record.
		return "", true, x.Deleter.DeleteContacts(ctx, en.TenantID, []uint32{en.ContactID})
	}
	return "", false, fmt.Errorf("journey %d: step %q has unknown type %q", en.JourneyID, id, n.Type)
}

func formID(props map[string]any) int64 {
	v, _ := attr.Number(attr.String(props["form_id"]))
	return int64(v)
}

// MarkedDNC reports whether the contact is marked Do Not Contact on ch ("any": on any channel):
// unsubscribed from it or from everything, or (email, SMS) bounced.
func MarkedDNC(attrs map[string]any, ch string) bool {
	if ch != "any" {
		return consent.OptedOut(attrs, ch) ||
			ch == channel.Email && truthy(attrs[consent.AttrEmailBounced]) ||
			ch == channel.SMS && truthy(attrs[consent.AttrSMSBounced])
	}
	for _, c := range channel.All {
		if MarkedDNC(attrs, c) {
			return true
		}
	}
	return false
}

func truthy(v any) bool {
	s := attr.String(v)
	return s == "true" || s == "1" || s == "yes"
}

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// reachable reports whether a marketing message on ch can reach the contact: an address, consent,
// and no bounce (the same checks the dispatcher makes, done first so the next channel can be tried).
func (x *Executor) reachable(ctx context.Context, c model.Contact, ch string) (bool, error) {
	if consent.OptedOut(c.Attrs, ch) {
		return false, nil
	}
	phone := e164.MatchString(attr.String(c.Attrs["phone"]))
	switch ch {
	case channel.Email:
		return c.Email != "" && !truthy(c.Attrs[consent.AttrEmailBounced]), nil
	case channel.SMS:
		return phone && !truthy(c.Attrs[consent.AttrSMSBounced]), nil
	case channel.WhatsApp, channel.RCS:
		return phone, nil
	case channel.Push:
		if x.Data == nil {
			return false, nil
		}
		devs, err := x.Data.ContactDevices(ctx, c.TenantID, c.ID)
		return len(devs) > 0, err
	case channel.Inbox:
		return true, nil
	}
	return false, nil
}

func userName(u model.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

func (x *Executor) user(ctx context.Context, tenant, id int64) (model.User, bool, error) {
	us, err := x.Data.ListUsers(ctx, tenant)
	if err != nil {
		return model.User{}, false, err
	}
	for _, u := range us {
		if u.ID == id {
			return u, true, nil
		}
	}
	return model.User{}, false, nil
}

// notifyRecipients resolves a notify_user step's addresses: fixed ones, the chosen users and the
// contact's owner, without duplicates.
func (x *Executor) notifyRecipients(ctx context.Context, tenant int64, n Node, c *model.Contact) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(e string) {
		if k := strings.ToLower(e); e != "" && !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	for _, e := range n.Emails {
		add(e)
	}
	owner, _ := attr.Number(attr.String(c.Attrs[event.AttrOwnerID]))
	if len(n.UserIDs) > 0 || n.ToOwner && owner > 0 {
		if x.Data == nil {
			return nil, errNotConfigured
		}
		us, err := x.Data.ListUsers(ctx, tenant)
		if err != nil {
			return nil, err
		}
		for _, u := range us {
			for _, id := range n.UserIDs {
				if u.ID == id {
					add(u.Email)
				}
			}
			if n.ToOwner && u.ID == int64(owner) {
				add(u.Email)
			}
		}
	}
	return out, nil
}

// CompanyProps are the props of company_linked / company_updated: what the contact copies.
func CompanyProps(co pg.Company, source string) map[string]any {
	return map[string]any{"company_id": co.ID, "name": co.Name, "attrs": co.Attrs, "source": source}
}

// fanOutCompany refreshes the company's copies on every linked contact (company_updated).
func (x *Executor) fanOutCompany(ctx context.Context, en model.Enrollment, id string, co pg.Company, c *model.Contact) error {
	var after uint32
	key := StepKey(en, id)
	for {
		exts, last, err := x.Data.MemberIDs(ctx, en.TenantID, event.AttrCompanyID, strconv.FormatInt(co.ID, 10), after, fanOutPage)
		if err != nil {
			return err
		}
		for _, ext := range exts {
			e := event.Event{ID: "jco:" + key + ":" + ext, TenantID: en.TenantID, ExternalID: ext, Name: event.CompanyUpdated,
				Ts: x.Now().UTC(), Props: CompanyProps(co, "journey")}
			if err := x.Events.PublishEvent(ctx, e); err != nil {
				return err
			}
			if ext == c.ExternalID {
				c.Attrs, _ = profile.Merge(c.Attrs, &e)
			}
		}
		if len(exts) < fanOutPage {
			return nil
		}
		after = last
	}
}

// ResolveAttrs fills an update step's values for this contact: {contactfield=key}, the standard
// variables ({{contact.city}}) and Go template fields ({{.Attrs.key}}) take the contact's values, and
// "now", "now+3hours", "now-2days" (minutes, hours, days, weeks, months, years) become that time
// (RFC 3339, UTC).
func ResolveAttrs(attrs map[string]any, c model.Contact, now time.Time) map[string]any {
	return ResolveAttrsWith(attrs, c, nil, now)
}

// ResolveAttrsWith is ResolveAttrs with the journey's trigger event for {{event.x}}.
func ResolveAttrsWith(attrs map[string]any, c model.Contact, ev map[string]any, now time.Time) map[string]any {
	out := make(map[string]any, len(attrs))
	field := func(k string) string {
		switch k {
		case "email":
			if c.Email != "" {
				return c.Email
			}
		case "external_id":
			return c.ExternalID
		}
		return attr.String(c.Attrs[k])
	}
	for k, v := range attrs {
		s, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		if t, ok := dateMath(strings.TrimSpace(s), now); ok {
			out[k] = t.UTC().Format(time.RFC3339)
			continue
		}
		s = fieldVarRe.ReplaceAllStringFunc(s, func(m string) string { return field(fieldVarRe.FindStringSubmatch(m)[1]) })
		if strings.Contains(s, "{{") {
			r := personalize.New(personalize.Vars{Contact: c, Event: ev})
			if res, err := r.Text(k, s, webhookData{Contact: c, Attrs: c.Attrs}); err == nil {
				s = res
			}
		}
		out[k] = s
	}
	return out
}

func dateMath(s string, now time.Time) (time.Time, bool) {
	m := dateMathRe.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return time.Time{}, false
	}
	if m[1] == "" {
		return now, true
	}
	n, _ := strconv.Atoi(m[2])
	if m[1] == "-" {
		n = -n
	}
	switch m[3] {
	case "minute":
		return now.Add(time.Duration(n) * time.Minute), true
	case "hour":
		return now.Add(time.Duration(n) * time.Hour), true
	case "day":
		return now.AddDate(0, 0, n), true
	case "week":
		return now.AddDate(0, 0, 7*n), true
	case "month":
		return now.AddDate(0, n, 0), true
	}
	return now.AddDate(n, 0, 0), true
}
