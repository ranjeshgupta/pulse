package api

// M7: the contacts behind every number. The Contacts list's search box takes filters that name
// the sets the app counts elsewhere, so a click on a count opens exactly those contacts:
//
//   - set maths in Redis, on the bitmaps the counts come from: in:<segments> (in any of them),
//     not_in:<segments> (in none), subscribed:<channel> (not unsubscribed), reachable:<channel>
//     (not unsubscribed and with a working address), under_cap:<channel> (not held back by the
//     channel's frequency cap now), journey:<id> with at:<step> and journey_status:<status>
//     (enrollments, from Postgres, turned into a bitmap);
//   - outcomes and events in ClickHouse: campaign:broadcast/<id> | campaign:journey/<id> with
//     run:, step:, outcome:<kind> and reason:<code>; event:<name> and no_event:<name> with
//     product: and category:; active:any|web|android|ios|other|new|returning with utm:;
//     amp_poll:<template>/<block> with answer:; form:<id>; period:<from>..<to> (UTC days, both
//     included) for any of them.
//
// A view with a bitmap filter pages through the bitmap (newest contact first) and lets ClickHouse
// check the candidates against the other filters; its count is BITCOUNT when nothing else
// filters, else exact up to viewCountCap candidates and from an even sample above. Bitmaps are
// cached for a minute per view, and building views is limited per workspace.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"pulse/internal/analytics"
	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/dispatch"
	"pulse/internal/event"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/reach"
	"pulse/internal/reasons"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
)

const (
	viewSetTTL         = time.Minute
	viewsPerMin        = 120 // per workspace: first pages of views with these filters, and bitmap builds
	maxSetSegments     = 10
	maxPeriodDays      = 366
	viewSampleSize     = 5000
	viewCapExact       = 50000 // like the estimate's frequency-cap check
	maxViewSegment     = 500000
	viewSegmentsPerMin = 10   // per workspace: "Create segment from this list" requests
	viewSegmentRuns    = 2    // per workspace: segments filling in the background at once
	viewCandidateMax   = 5000 // ids per ClickHouse check
)

// specialKeys are the M7 filter names (see the package comment above).
var specialKeys = map[string]bool{"in": true, "not_in": true, "subscribed": true, "reachable": true, "under_cap": true,
	"journey": true, "at": true, "journey_status": true, "campaign": true, "run": true, "step": true, "outcome": true,
	"reason": true, "event": true, "no_event": true, "product": true, "category": true, "active": true, "utm": true,
	"amp_poll": true, "answer": true, "form": true, "period": true, "via": true, "template": true}

// Outcomes a campaign filter takes. With reason:, outcome: is the raw outcome kind of the reasons
// table (failed, bounced, complained, suppressed, capped).
var outcomeWords = map[string]string{
	"targeted": "Targeted", "sent": "Sent", "delivered": "Delivered", "opened": "Opened", "read": "Read",
	"clicked": "Clicked", "downloaded": "Downloaded a file", "bounced": "Bounced", "hard_bounced": "Bounced (hard)",
	"soft_bounced": "Bounced (soft)", "complained": "Complained (spam or blocked)", "failed": "Failed", "failed_all": "Failed or skipped when sending",
	"unsubscribed": "Unsubscribed",
	"suppressed":   "Skipped (consent)", "capped": "Held back by the frequency cap", "fallback": "Sent as SMS instead",
	"skipped": "Skipped", "skipped_consent": "Skipped: unsubscribed", "skipped_reachability": "Skipped: no address on the channel",
	"skipped_cap": "Skipped: frequency cap", "skipped_missing_value": "Skipped: missing personalisation value",
	"skipped_deadline": "Skipped: past the send deadline",
}

var journeyStatusWords = map[string][]string{
	"in_progress": {journey.StatusActive, journey.StatusWaiting}, "completed": {journey.StatusCompleted},
	"converted": {journey.StatusConverted}, "active": {journey.StatusActive}, "waiting": {journey.StatusWaiting},
	"exited": {"exited"},
}

var activeWords = map[string]string{"any": "Active", "web": "Active on the web", "android": "Active on Android",
	"ios": "Active on iOS", "other": "Active (other platforms)", "new": "New (first active)", "returning": "Returning"}

// viewSpecial holds a view's M7 filters.
type viewSpecial struct {
	In                              [][]int64 // each in: chip: in any of its segments; chips together: in all
	NotIn                           []int64
	Subscribed, Reachable, UnderCap string

	Journey       int64
	At            string
	JourneyStatus string

	Campaign       bool   // a campaign: filter was given
	Source         string // broadcast | journey ("" with Outcome/Reason: every campaign)
	SourceID       int64
	Run            int64
	Step           string
	Outcome        string
	Reason         string
	Channel        string // via: the messages' channel (with campaign: / outcome:)
	Template       int64  // template: messages of one template, in any campaign (O4 template reports)
	Events         []string
	NoEvents       []string
	Product        string
	Category       string
	Active         string
	UTM            *string
	PollTemplate   int64
	PollBlock      string
	Answer         *string
	Form           int64
	From, To       time.Time // To excludes
	periodSet      bool
	campaignName   string
	journeyName    string
	pollName       string
	formName       string
	stepName       string
	segmentNames   map[int64]string
	needsByFilters []string // viewFilterNeeds keys used
}

func (sp *viewSpecial) any() bool {
	return sp.bitmap() || sp.ch()
}

// bitmap reports whether the view starts from a Redis bitmap.
func (sp *viewSpecial) bitmap() bool {
	return len(sp.In) > 0 || len(sp.NotIn) > 0 || sp.Subscribed != "" || sp.Reachable != "" || sp.UnderCap != "" || sp.Journey != 0
}

// ch reports whether the view has ClickHouse conditions of its own.
func (sp *viewSpecial) ch() bool {
	return sp.Campaign || sp.Outcome != "" || sp.Reason != "" || len(sp.Events) > 0 || len(sp.NoEvents) > 0 || sp.Active != "" ||
		sp.PollTemplate != 0 || sp.Form != 0 || sp.Template != 0
}

// viewLookups for the M7 filters (each nil when the caller can't use it).
type specialLookup struct {
	Broadcast func(id int64) (string, error)
	Journey   func(id int64) (model.Journey, error)
	Journeys  func() ([]model.Journey, error)
	Template  func(id int64) (string, error)
	Form      func(id int64) (string, error)
	Allowed   func(filter string) bool // viewFilterAllowed for the caller
	Today     time.Time
}

// parseSpecial reads one M7 filter into sp and returns its chip label.
func parseSpecial(sp *viewSpecial, key, val string, lk viewLookup) (string, error) {
	sl := lk.Special
	need := func(filter string) error {
		if sl.Allowed != nil && !sl.Allowed(filter) {
			return fmt.Errorf("%s: your role can't list these contacts (it needs view access to %s)", key, needWords(filter))
		}
		sp.needsByFilters = append(sp.needsByFilters, filter)
		return nil
	}
	once := func(set bool) error {
		if set {
			return fmt.Errorf("use %s: once", key)
		}
		return nil
	}
	switch key {
	case "in", "not_in":
		ids, names, err := resolveSegments(val, lk)
		if err != nil {
			return "", err
		}
		if sp.segmentNames == nil {
			sp.segmentNames = map[int64]string{}
		}
		for i, id := range ids {
			sp.segmentNames[id] = names[i]
		}
		quoted := quoteList(names)
		if key == "in" {
			sp.In = append(sp.In, sortedIDs(ids))
			total := 0
			for _, g := range sp.In {
				total += len(g)
			}
			if total > maxSetSegments {
				return "", fmt.Errorf("in: at most %d segments", maxSetSegments)
			}
			if len(ids) > 1 {
				return "In any of " + quoted, nil
			}
			return "In segment " + quoted, nil
		}
		sp.NotIn = sortedIDs(append(sp.NotIn, ids...))
		if len(sp.NotIn) > maxSetSegments {
			return "", fmt.Errorf("not_in: at most %d segments", maxSetSegments)
		}
		if len(ids) > 1 {
			return "Not in any of " + quoted, nil
		}
		return "Not in segment " + quoted, nil
	case "subscribed", "reachable", "under_cap":
		c := strings.ToLower(val)
		if !channel.Valid(c) {
			return "", fmt.Errorf("%s:%s: use a channel: email, sms, whatsapp, rcs, push or inbox", key, val)
		}
		name := channelWords(c)
		switch key {
		case "subscribed":
			if err := once(sp.Subscribed != ""); err != nil {
				return "", err
			}
			sp.Subscribed = c
			return "Not unsubscribed from " + name, nil
		case "reachable":
			if err := once(sp.Reachable != ""); err != nil {
				return "", err
			}
			sp.Reachable = c
			return "Reachable by " + name, nil
		}
		if err := once(sp.UnderCap != ""); err != nil {
			return "", err
		}
		sp.UnderCap = c
		return "Not held back by the " + name + " frequency cap", nil
	case "journey":
		if err := once(sp.Journey != 0); err != nil {
			return "", err
		}
		if err := need("journey"); err != nil {
			return "", err
		}
		j, err := findJourney(val, sl)
		if err != nil {
			return "", err
		}
		sp.Journey, sp.journeyName = j.ID, j.Name
		return "Entered “" + j.Name + "”", nil
	case "at":
		if err := once(sp.At != ""); err != nil {
			return "", err
		}
		if !validNodeID(val) {
			return "", fmt.Errorf("at:%s isn't a step id", val)
		}
		sp.At = val
		return "At step " + val, nil // relabelled once the journey is known (finishSpecial)
	case "journey_status":
		if err := once(sp.JourneyStatus != ""); err != nil {
			return "", err
		}
		v := strings.ToLower(val)
		if _, ok := journeyStatusWords[v]; !ok {
			return "", errors.New("journey_status: use in_progress, completed, converted, exited, active or waiting")
		}
		sp.JourneyStatus = v
		return map[string]string{"in_progress": "Still in the journey", "completed": "Finished the journey", "converted": "Reached the goal",
			"active": "Moving through the journey", "waiting": "Waiting in the journey", "exited": "Left the journey early"}[v], nil
	case "campaign":
		if err := once(sp.Campaign); err != nil {
			return "", err
		}
		kind, idText, ok := strings.Cut(val, "/")
		id, err := strconv.ParseInt(idText, 10, 64)
		if !ok || err != nil || id <= 0 || (kind != "broadcast" && kind != "journey") {
			return "", fmt.Errorf("campaign:%s: use campaign:broadcast/<id> or campaign:journey/<id>", val)
		}
		if err := need("campaign:" + kind); err != nil {
			return "", err
		}
		var name string
		if kind == "broadcast" {
			if sl.Broadcast == nil {
				return "", errors.New("campaign: isn't available here")
			}
			name, err = sl.Broadcast(id)
		} else {
			if sl.Journey == nil {
				return "", errors.New("campaign: isn't available here")
			}
			var j model.Journey
			j, err = sl.Journey(id)
			name = j.Name
		}
		if err != nil {
			return "", fmt.Errorf("there is no campaign %s", val)
		}
		sp.Campaign, sp.Source, sp.SourceID, sp.campaignName = true, kind, id, name
		return "Campaign “" + name + "”", nil
	case "run":
		if err := once(sp.Run != 0); err != nil {
			return "", err
		}
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil || n <= 0 {
			return "", errors.New("run: is a send's time in Unix seconds")
		}
		sp.Run = n
		return "Send of " + time.Unix(n, 0).UTC().Format("2 Jan 2006 15:04 UTC"), nil
	case "step":
		if err := once(sp.Step != ""); err != nil {
			return "", err
		}
		if !validNodeID(val) {
			return "", fmt.Errorf("step:%s isn't a step id", val)
		}
		sp.Step = val
		return "Step " + val, nil
	case "outcome":
		if err := once(sp.Outcome != ""); err != nil {
			return "", err
		}
		v := strings.ToLower(val)
		w, ok := outcomeWords[v]
		if !ok {
			return "", fmt.Errorf("outcome:%s isn't an outcome; use sent, delivered, opened, read, clicked, bounced, complained, failed, unsubscribed, targeted or skipped", val)
		}
		sp.Outcome = v
		return w, nil
	case "via":
		if err := once(sp.Channel != ""); err != nil {
			return "", err
		}
		c := strings.ToLower(val)
		if !channel.Valid(c) {
			return "", fmt.Errorf("via:%s: use a channel: email, sms, whatsapp, rcs, push or inbox", val)
		}
		sp.Channel = c
		return "By " + channelWords(c), nil
	case "reason":
		if err := once(sp.Reason != ""); err != nil {
			return "", err
		}
		if !reasons.Valid(val) {
			return "", fmt.Errorf("reason:%s isn't a reason code", val)
		}
		sp.Reason = val
		return "Reason: " + reasons.Label(val), nil
	case "event", "no_event":
		if !event.ValidName(val) {
			return "", fmt.Errorf("%s:%s isn't an event name", key, val)
		}
		if len(sp.Events)+len(sp.NoEvents) >= 5 {
			return "", errors.New("use at most 5 event filters")
		}
		if key == "event" {
			sp.Events = append(sp.Events, val)
			return "Did " + val, nil
		}
		sp.NoEvents = append(sp.NoEvents, val)
		return "Didn't do " + val, nil
	case "product", "category":
		if utf8.RuneCountInString(val) > 200 {
			return "", fmt.Errorf("%s: is too long", key)
		}
		if key == "product" {
			if err := once(sp.Product != ""); err != nil {
				return "", err
			}
			sp.Product = val
			return "Product " + val, nil
		}
		if err := once(sp.Category != ""); err != nil {
			return "", err
		}
		sp.Category = val
		return "Category " + val, nil
	case "active":
		if err := once(sp.Active != ""); err != nil {
			return "", err
		}
		v := strings.ToLower(val)
		w, ok := activeWords[v]
		if !ok {
			return "", errors.New("active: use any, web, android, ios, other, new or returning")
		}
		sp.Active = v
		return w, nil
	case "utm":
		if err := once(sp.UTM != nil); err != nil {
			return "", err
		}
		v := strings.ToLower(val)
		if utf8.RuneCountInString(v) > 64 {
			return "", errors.New("utm: is too long")
		}
		if v == "none" {
			v = ""
			sp.UTM = &v
			return "No campaign source", nil
		}
		sp.UTM = &v
		return "Came from " + v, nil
	case "amp_poll":
		if err := once(sp.PollTemplate != 0); err != nil {
			return "", err
		}
		if err := need("amp_poll"); err != nil {
			return "", err
		}
		tpl, block, ok := strings.Cut(val, "/")
		id, err := strconv.ParseInt(tpl, 10, 64)
		if !ok || err != nil || id <= 0 || !validNodeID(block) {
			return "", errors.New("amp_poll: use amp_poll:<template id>/<poll block id>")
		}
		if sl.Template == nil {
			return "", errors.New("amp_poll: isn't available here")
		}
		name, err := sl.Template(id)
		if err != nil {
			return "", fmt.Errorf("there is no template %d", id)
		}
		sp.PollTemplate, sp.PollBlock, sp.pollName = id, block, name
		return "Answered the poll in “" + name + "”", nil
	case "answer":
		if err := once(sp.Answer != nil); err != nil {
			return "", err
		}
		if utf8.RuneCountInString(val) > 200 {
			return "", errors.New("answer: is too long")
		}
		v := val
		sp.Answer = &v
		return "Answer “" + val + "”", nil
	case "template":
		if err := once(sp.Template != 0); err != nil {
			return "", err
		}
		if err := need("campaign:any"); err != nil {
			return "", err
		}
		id, err := strconv.ParseInt(val, 10, 64)
		if err != nil || id <= 0 || sl.Template == nil {
			return "", errors.New("template: use template:<id>")
		}
		name, err := sl.Template(id)
		if err != nil {
			return "", fmt.Errorf("there is no template %d", id)
		}
		sp.Template = id
		return "Template “" + name + "”", nil
	case "form":
		if err := once(sp.Form != 0); err != nil {
			return "", err
		}
		if err := need("form"); err != nil {
			return "", err
		}
		id, err := strconv.ParseInt(val, 10, 64)
		if err != nil || id <= 0 || sl.Form == nil {
			return "", errors.New("form: use form:<id>")
		}
		name, err := sl.Form(id)
		if err != nil {
			return "", fmt.Errorf("there is no form %d", id)
		}
		sp.Form, sp.formName = id, name
		return "Submitted “" + name + "”", nil
	case "period":
		if err := once(sp.periodSet); err != nil {
			return "", err
		}
		from, to, err := parsePeriod(val)
		if err != nil {
			return "", err
		}
		sp.From, sp.To, sp.periodSet = from, to, true
		return periodWords(from, to), nil
	}
	return "", fmt.Errorf("unknown filter %s:", key)
}

// finishSpecial checks the filters that only make sense together, and the permissions of
// filters that depend on others (outcomes without a campaign).
func finishSpecial(sp *viewSpecial, lk viewLookup, filters []viewFilter) ([]viewFilter, error) {
	switch {
	case (sp.At != "" || sp.JourneyStatus != "") && sp.Journey == 0:
		return nil, errors.New("at: and journey_status: need journey:<id>")
	case sp.Step != "" && sp.Source != "journey":
		return nil, errors.New("step: needs campaign:journey/<id>")
	case sp.Channel != "" && !sp.Campaign && sp.Outcome == "" && sp.Reason == "" && sp.Template == 0:
		return nil, errors.New("via: needs campaign:, template: or outcome:")
	case sp.Run != 0 && sp.Source != "broadcast":
		return nil, errors.New("run: needs campaign:broadcast/<id>")
	case sp.Answer != nil && sp.PollTemplate == 0:
		return nil, errors.New("answer: needs amp_poll:<template>/<block>")
	case (sp.Product != "" || sp.Category != "") && len(sp.Events)+len(sp.NoEvents) == 0:
		return nil, errors.New("product: and category: need event: or no_event:")
	case sp.UTM != nil && sp.Active == "":
		return nil, errors.New("utm: needs active:")
	case sp.Active != "" && !sp.periodSet:
		return nil, errors.New("active: needs period:<from>..<to>")
	case sp.periodSet && !sp.ch():
		return nil, errors.New("period: applies to campaign:, template:, outcome:, event:, active:, amp_poll: and form:")
	case (sp.Campaign || sp.Template != 0) && sp.Outcome == "" && sp.Reason == "":
		sp.Outcome = "targeted"
	}
	if !sp.Campaign && sp.Template == 0 && (sp.Outcome != "" || sp.Reason != "") {
		if lk.Special.Allowed != nil && !lk.Special.Allowed("campaign:any") {
			return nil, errors.New("outcome: without campaign: lists every campaign's recipients, which needs view access to reports")
		}
		sp.needsByFilters = append(sp.needsByFilters, "campaign:any")
	}
	if sp.Journey != 0 && sp.At != "" && lk.Special.Journey != nil {
		if j, err := lk.Special.Journey(sp.Journey); err == nil {
			var g journey.Graph
			_ = json.Unmarshal(j.Graph, &g)
			if _, ok := g.Nodes[sp.At]; !ok {
				return nil, fmt.Errorf("“%s” has no step %s", j.Name, sp.At)
			}
			sp.stepName = stepWords(g.Nodes[sp.At])
			for i := range filters {
				if strings.HasPrefix(filters[i].Token, "at:") {
					filters[i].Label = "Now at “" + sp.stepName + "”"
				}
			}
		}
	}
	if sp.Source == "journey" && sp.Step != "" && lk.Special.Journey != nil {
		if j, err := lk.Special.Journey(sp.SourceID); err == nil {
			var g journey.Graph
			_ = json.Unmarshal(j.Graph, &g)
			if n, ok := g.Nodes[sp.Step]; ok {
				for i := range filters {
					if strings.HasPrefix(filters[i].Token, "step:") {
						filters[i].Label = "At “" + stepWords(n) + "”"
					}
				}
			}
		}
	}
	return filters, nil
}

func needWords(filter string) string {
	switch filter {
	case "campaign:broadcast":
		return "one-time campaigns or reports"
	case "campaign:journey", "journey":
		return "triggered & automation campaigns or reports"
	case "campaign:any":
		return "reports"
	}
	return "content or reports"
}

func channelWords(c string) string {
	if n, ok := map[string]string{"email": "email", "sms": "SMS", "whatsapp": "WhatsApp", "rcs": "RCS", "push": "push", "inbox": "app inbox"}[c]; ok {
		return n
	}
	return c
}

func quoteList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "“" + n + "”"
	}
	return strings.Join(q, ", ")
}

// resolveSegments reads in:/not_in: values: a segment's id or name, or several separated by commas.
func resolveSegments(val string, lk viewLookup) ([]int64, []string, error) {
	segs, err := lk.Segments()
	if err != nil {
		return nil, nil, err
	}
	if s, err := findSegment(segs, val); err == nil {
		return []int64{s.ID}, []string{s.Name}, nil
	}
	var ids []int64
	var names []string
	for _, part := range strings.Split(val, ",") {
		s, err := findSegment(segs, strings.TrimSpace(part))
		if err != nil {
			return nil, nil, err
		}
		ids, names = append(ids, s.ID), append(names, s.Name)
	}
	return ids, names, nil
}

func findJourney(val string, sl specialLookup) (model.Journey, error) {
	if id, err := strconv.ParseInt(val, 10, 64); err == nil && sl.Journey != nil {
		if j, err := sl.Journey(id); err == nil {
			return j, nil
		}
		return model.Journey{}, fmt.Errorf("there is no journey %s", val)
	}
	if sl.Journeys == nil {
		return model.Journey{}, errors.New("journey: isn't available here")
	}
	js, err := sl.Journeys()
	if err != nil {
		return model.Journey{}, err
	}
	for _, j := range js {
		if strings.EqualFold(j.Name, val) {
			return j, nil
		}
	}
	return model.Journey{}, fmt.Errorf("there is no journey %q", val)
}

func validNodeID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// stepWords names a journey step for people: its type in words.
func stepWords(n journey.Node) string {
	words := map[string]string{"send_email": "Send email", "send_sms": "Send SMS", "send_whatsapp": "Send WhatsApp",
		"send_rcs": "Send RCS", "send_push": "Send push", "send_inbox": "Send to app inbox", "send_message": "Send marketing message",
		"wait": "Wait", "wait_until": "Wait until", "wait_for_event": "Wait for an event", "condition": "Condition", "check": "Check",
		"split": "Split", "exit": "Exit", "message_decision": "Message decision"}
	if w, ok := words[n.Type]; ok {
		return w
	}
	t := strings.ReplaceAll(n.Type, "_", " ")
	if t == "" {
		return "step"
	}
	return strings.ToUpper(t[:1]) + t[1:]
}

// parsePeriod reads YYYY-MM-DD..YYYY-MM-DD (UTC days, both included) or one day. To is returned
// as the start of the day after the last one.
func parsePeriod(v string) (time.Time, time.Time, error) {
	a, b, two := strings.Cut(v, "..")
	if !two {
		b = a
	}
	from, err1 := time.Parse(time.DateOnly, a)
	to, err2 := time.Parse(time.DateOnly, b)
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, errors.New("period: use period:2026-10-01..2026-10-31 (UTC days, both included)")
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, errors.New("period: the first day must be on or before the last")
	}
	if to.Sub(from) > (maxPeriodDays-1)*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("period: at most %d days", maxPeriodDays)
	}
	return from, to.AddDate(0, 0, 1), nil
}

func periodWords(from, to time.Time) string {
	last := to.AddDate(0, 0, -1)
	if last.Equal(from) {
		return "On " + from.Format("2 Jan 2006")
	}
	return "Between " + from.Format("2 Jan") + " and " + last.Format("2 Jan 2006")
}

// ---- ClickHouse conditions ----

// skipCodes are the reason codes of a funnel skip group ("" = every skip group).
func skipCodes(group string) []any {
	var out []any
	for _, c := range reasons.Codes {
		if g := reasons.SkipGroup(c); g != "" && (group == "" || g == group) {
			out = append(out, c)
		}
	}
	return out
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

// reasonExpr is a message event's reason code, with the defaults reports give rows without one.
const reasonExpr = "if(reason_code != '', reason_code, multiIf(kind = 'bounced', 'soft_bounce', kind = 'complained', 'complaint', kind = 'capped', 'capped', kind = 'suppressed', 'consent', 'unknown'))"

// outcomeCond is a condition over message_events for an outcome, as the reports count it.
func outcomeCond(outcome string, withReason bool) (string, []any) {
	if withReason {
		if outcome == "" {
			return "kind IN " + reasonKindsSQL, nil
		}
		return "kind = ?", []any{outcome}
	}
	group := func(g string) (string, []any) {
		codes := skipCodes(g)
		c := "(kind IN " + reasonKindsSQL + " AND reason_code IN (" + marks(len(codes)) + "))"
		switch g {
		case "consent":
			c = "(" + c + " OR kind = 'suppressed')"
		case "cap":
			c = "(" + c + " OR kind = 'capped')"
		case "":
			c = "(" + c + " OR kind IN ('suppressed', 'capped'))"
		}
		return c, codes
	}
	switch outcome {
	case "targeted":
		return "kind IN ('sent', 'failed', 'suppressed', 'capped', 'fallback', 'bounced')", nil
	case "downloaded":
		return "kind = 'clicked' AND match(url, ?)", []any{ch.DownloadURL}
	// Bounces and complaints on every channel, by the reason codes' bounce type (the same the
	// bounce rules count): an SMS to an invalid number is a hard bounce, a WhatsApp block a complaint.
	case "hard_bounced", "soft_bounced", "bounced":
		var codes []any
		for _, t := range map[string][]string{"hard_bounced": {reasons.BounceHard}, "soft_bounced": {reasons.BounceSoft},
			"bounced": {reasons.BounceHard, reasons.BounceSoft}}[outcome] {
			for _, c := range reasons.BounceCodes(t) {
				if reasons.SkipGroup(c) == "" { // the funnel shows those as skipped, not bounced
					codes = append(codes, c)
				}
			}
		}
		return "kind IN ('bounced', 'failed') AND reason_code IN (" + marks(len(codes)) + ")", codes
	case "complained":
		var codes []any
		for _, c := range reasons.BounceCodes(reasons.BounceComplaint) {
			codes = append(codes, c)
		}
		return "(kind = 'complained' OR kind = 'failed' AND reason_code IN (" + marks(len(codes)) + "))", codes
	case "failed_all": // every failed outcome, skips included (the reports' Failed column)
		return "kind = 'failed'", nil
	case "failed": // tried and failed: skips are their own step
		codes := skipCodes("")
		return "kind = 'failed' AND reason_code NOT IN (" + marks(len(codes)) + ")", codes
	case "skipped":
		return group("")
	}
	if g, ok := strings.CutPrefix(outcome, "skipped_"); ok {
		return group(g)
	}
	return "kind = ?", []any{outcome}
}

const reasonKindsSQL = "('failed', 'bounced', 'complained', 'suppressed', 'capped')"

// chConds compiles the view's ClickHouse filters into conditions over the contacts table. Every
// subquery is limited to the tenant.
func (sp *viewSpecial) chConds(tenant int64) (string, []any) {
	var parts []string
	var args []any
	t := uint64(tenant)
	period := func(col string) {
		if sp.periodSet {
			parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], ")") + " AND " + col + " >= ? AND " + col + " < ?)"
			args = append(args, sp.From, sp.To)
		}
	}
	if sp.Campaign || sp.Outcome != "" || sp.Reason != "" || sp.Template != 0 {
		q := "id IN (SELECT contact_id FROM message_events WHERE tenant_id = ?"
		args = append(args, t)
		switch {
		case sp.Source != "":
			q += " AND source = ? AND source_id = ?"
			args = append(args, sp.Source, uint64(sp.SourceID))
		case sp.Template == 0:
			q += " AND source IN ('broadcast', 'journey')"
		}
		if sp.Template != 0 {
			// A template's report counts every send of it, tests and API sends included.
			q += " AND template_id = ?"
			args = append(args, uint64(sp.Template))
		}
		if sp.Run != 0 {
			q += " AND run = ?"
			args = append(args, uint64(sp.Run))
		}
		if sp.Step != "" {
			q += " AND node = ?"
			args = append(args, sp.Step)
		}
		c, cargs := outcomeCond(sp.Outcome, sp.Reason != "")
		q += " AND " + c
		args = append(args, cargs...)
		if sp.Channel != "" {
			q += " AND channel = ?"
			args = append(args, sp.Channel)
		}
		if sp.Reason != "" {
			q += " AND " + reasonExpr + " = ?"
			args = append(args, sp.Reason)
		}
		parts = append(parts, q+")")
		period("ts")
	}
	eventSub := func(name string) string {
		q := "(SELECT contact_id FROM events WHERE tenant_id = ? AND name = ?"
		args = append(args, t, name)
		if sp.Product != "" {
			q += " AND product_code = ?"
			args = append(args, sp.Product)
		}
		if sp.Category != "" {
			q += " AND product_category = ?"
			args = append(args, sp.Category)
		}
		if sp.periodSet {
			q += " AND ts >= ? AND ts < ?"
			args = append(args, sp.From, sp.To)
		}
		return q + ")"
	}
	for _, e := range sp.Events {
		parts = append(parts, "id IN "+eventSub(e))
	}
	for _, e := range sp.NoEvents {
		parts = append(parts, "id NOT IN "+eventSub(e))
	}
	if sp.Active != "" {
		switch sp.Active {
		case "new", "returning":
			first := "id IN (SELECT contact_id FROM first_active WHERE tenant_id = ? GROUP BY contact_id HAVING min(first) >= toDate(?) AND min(first) < toDate(?))"
			if sp.Active == "new" {
				parts = append(parts, first)
				args = append(args, t, sp.From, sp.To)
				break
			}
			parts = append(parts, "id IN (SELECT contact_id FROM events WHERE tenant_id = ? AND ts >= ? AND ts < ? AND ("+analytics.ActiveCondition+")) AND NOT "+first)
			args = append(args, t, sp.From, sp.To, t, sp.From, sp.To)
		default:
			q := "id IN (SELECT contact_id FROM events WHERE tenant_id = ? AND ts >= ? AND ts < ? AND (" + analytics.ActiveCondition + ")"
			args = append(args, t, sp.From, sp.To)
			if sp.Active != "any" {
				q += " AND multiIf(JSONExtractString(props, 'platform') IN ('web', 'android', 'ios'), JSONExtractString(props, 'platform'), 'other') = ?"
				args = append(args, sp.Active)
			}
			if sp.UTM != nil {
				q += " AND substring(lower(JSONExtractString(props, 'utm_source')), 1, 64) = ?"
				args = append(args, *sp.UTM)
			}
			parts = append(parts, q+")")
		}
	}
	if sp.PollTemplate != 0 {
		q := "id IN (SELECT contact_id FROM events WHERE tenant_id = ? AND name = ? AND JSONExtractUInt(props, 'template_id') = ? AND JSONExtractString(props, 'block') = ?"
		args = append(args, t, event.AMPPollAnswered, uint64(sp.PollTemplate), sp.PollBlock)
		if sp.Answer != nil {
			q += " AND JSONExtractString(props, 'answer') = ?"
			args = append(args, *sp.Answer)
		}
		parts = append(parts, q+")")
		period("ts")
	}
	if sp.Form != 0 {
		parts = append(parts, "id IN (SELECT contact_id FROM events WHERE tenant_id = ? AND name = 'form_submitted' AND JSONExtractUInt(props, 'form_id') = ?)")
		args = append(args, t, uint64(sp.Form))
		period("ts")
	}
	return strings.Join(parts, " AND "), args
}

// ---- Redis bitmaps ----

// viewBitmap returns the Redis key of the view's bitmap (built, or reused for a minute).
func (s *Server) viewBitmap(ctx context.Context, tenant int64, sp *viewSpecial) (string, error) {
	if s.RDB == nil {
		return "", errUser("these filters need Redis")
	}
	spec, _ := json.Marshal(map[string]any{"in": sp.In, "not": sp.NotIn, "sub": sp.Subscribed, "reach": sp.Reachable,
		"j": sp.Journey, "at": sp.At, "js": sp.JourneyStatus})
	sum := sha1.Sum(spec)
	key := "cvset:" + strconv.FormatInt(tenant, 10) + ":" + hex.EncodeToString(sum[:])
	if n, err := s.RDB.Exists(ctx, key).Result(); err == nil && n == 1 {
		return key, nil
	}
	if !s.allowTenant(ctx, "rlview", tenant, viewsPerMin) {
		return "", errTooMany
	}
	var temps []string
	defer func() {
		if len(temps) > 0 {
			s.RDB.Unlink(context.WithoutCancel(ctx), temps...)
		}
	}()
	tmp := func(name string) string {
		k := key + ":" + name
		temps = append(temps, k)
		return k
	}
	segKeys := func(ids []int64) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = segment.MemberKey(tenant, id)
		}
		return out
	}
	p := s.RDB.Pipeline()
	base := reach.Key(tenant, reach.Known)
	for i, g := range sp.In {
		k := tmp("in" + strconv.Itoa(i))
		p.BitOpOr(ctx, k, segKeys(g)...)
		if i == 0 {
			base = k
		} else {
			both := tmp("inall" + strconv.Itoa(i))
			p.BitOpAnd(ctx, both, base, k)
			base = both
		}
	}
	if sp.Journey != 0 {
		jk, err := s.journeyBitmap(ctx, tenant, sp, tmp("j"))
		if err != nil {
			return "", err
		}
		if len(sp.In) > 0 {
			both := tmp("ij")
			p.BitOpAnd(ctx, both, base, jk)
			base = both
		} else {
			base = jk
		}
	}
	// andNot writes a AND NOT b (as a XOR (a AND b), which pads shorter bitmaps correctly).
	n := 0
	andNot := func(a, b string) string {
		n++
		both, dst := tmp("b"+strconv.Itoa(n)), tmp("d"+strconv.Itoa(n))
		p.BitOpAnd(ctx, both, a, b)
		p.BitOpXor(ctx, dst, a, both)
		return dst
	}
	cur := base
	if len(sp.NotIn) > 0 {
		exc := tmp("not")
		p.BitOpOr(ctx, exc, segKeys(sp.NotIn)...)
		cur = andNot(cur, exc)
	}
	for _, c := range []string{sp.Subscribed, sp.Reachable} {
		if c != "" {
			cur = andNot(cur, reach.Key(tenant, reach.OptOut(c)))
		}
	}
	if sp.Reachable != "" {
		r := tmp("r")
		p.BitOpAnd(ctx, r, cur, reach.Key(tenant, reach.AddressOf(sp.Reachable)))
		cur = r
	}
	// The result: a copy under the view's key (BITOP OR of one key copies it, and an empty
	// result still gets a key, so the cache works for empty views too).
	p.BitOpOr(ctx, key, cur)
	p.Expire(ctx, key, viewSetTTL)
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	if n, _ := s.RDB.Exists(ctx, key).Result(); n == 0 {
		s.RDB.Set(ctx, key, []byte{0}, viewSetTTL) // an empty view
	}
	return key, nil
}

var errTooMany = errors.New("too many contact list filters at once; try again in a minute")

// JourneyContactLister lists a journey's enrollments (Postgres).
type JourneyContactLister interface {
	JourneyContacts(ctx context.Context, tenant, journey int64, node string, statuses []string, fn func(uint32)) error
}

// journeyBitmap builds the bitmap of a journey's enrolled contacts (at a step, with a status).
func (s *Server) journeyBitmap(ctx context.Context, tenant int64, sp *viewSpecial, key string) (string, error) {
	jl, ok := s.Store.(JourneyContactLister)
	if !ok {
		return "", errUser("journey filters aren't available")
	}
	var bm []byte
	err := jl.JourneyContacts(ctx, tenant, sp.Journey, sp.At, journeyStatusWords[sp.JourneyStatus], func(id uint32) {
		i := int(id / 8)
		if i >= len(bm) {
			bm = append(bm, make([]byte, i-len(bm)+1+len(bm)/4)...)
		}
		bm[i] |= 0x80 >> (id % 8)
	})
	if err != nil {
		return "", err
	}
	if len(bm) == 0 {
		bm = []byte{0}
	}
	if err := s.RDB.Set(ctx, key, bm, viewSetTTL).Err(); err != nil {
		return "", err
	}
	return key, nil
}

// bitmapIDs reads the ids set in key below before (0: all), newest first, a chunk at a time;
// each chunk's ids go through keep (nil keeps all) until limit are found.
func bitmapIDs(ctx context.Context, rdb redis.Cmdable, key string, before uint32, limit int, keep func([]uint32) ([]uint32, error)) ([]uint32, error) {
	size, err := rdb.StrLen(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	end := size - 1
	if before > 0 {
		end = min(end, int64((before-1)/8))
	}
	chunk := int64(8 << 10)
	var out []uint32
	for end >= 0 && len(out) < limit {
		start := max(0, end-chunk+1)
		b, err := rdb.GetRange(ctx, key, start, end).Bytes()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		var cand []uint32
		for i := len(b) - 1; i >= 0; i-- {
			x := b[i]
			for bit := 7; x != 0 && bit >= 0; bit-- {
				if x&(0x80>>uint(bit)) != 0 {
					id := uint32(start+int64(i))*8 + uint32(bit)
					if before == 0 || id < before {
						cand = append(cand, id)
					}
				}
			}
		}
		for len(cand) > 0 && len(out) < limit {
			batch := cand[:min(len(cand), viewCandidateMax)]
			cand = cand[len(batch):]
			if keep != nil {
				if batch, err = keep(batch); err != nil {
					return nil, err
				}
			}
			out = append(out, batch...)
		}
		end = start - 1
		chunk = min(chunk*2, 1<<20)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// capFilter keeps the contacts a channel's frequency cap wouldn't hold back now (nil: no cap).
func (s *Server) capFilter(ctx context.Context, tenant int64, ch string) (func([]uint32) ([]uint32, error), string, error) {
	ds, ok := s.Store.(DeliveryStore)
	if !ok {
		return nil, "", nil
	}
	rules, err := ds.DeliveryRules(ctx, tenant)
	if err != nil {
		return nil, "", err
	}
	cp, ok := rules.Caps[ch]
	if !ok {
		return nil, "no frequency cap is set for " + channelWords(ch), nil
	}
	since := strconv.FormatInt(s.now().Add(-time.Duration(cp.Hours)*time.Hour).UnixMilli(), 10)
	return func(ids []uint32) ([]uint32, error) {
		p := s.RDB.Pipeline()
		cmds := make([]*redis.IntCmd, len(ids))
		for i, id := range ids {
			cmds[i] = p.ZCount(ctx, dispatch.CapKey(tenant, id, ch), "("+since, "+inf")
		}
		if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		out := ids[:0:0]
		for i, c := range cmds {
			if c.Val() < int64(cp.Max) {
				out = append(out, ids[i])
			}
		}
		return out, nil
	}, fmt.Sprintf("%d per %d h", cp.Max, cp.Hours), nil
}

// setView pages and counts a view that starts from a bitmap.
type setView struct {
	key   string
	keep  func([]uint32) ([]uint32, error) // nil: every bit is in the view
	exact int                              // count exactly up to this many bits, else sample
}

func (s *Server) buildSetView(ctx context.Context, tenant int64, view contactView, where string, args []any) (setView, error) {
	key, err := s.viewBitmap(ctx, tenant, view.Special)
	if err != nil {
		return setView{}, err
	}
	sv := setView{key: key, exact: viewCountCap}
	var filters []func([]uint32) ([]uint32, error)
	if where != "" || view.Text != "" {
		cv, ok := s.Analytics.(ContactFilterer)
		if !ok {
			return sv, errUser("analytics unavailable")
		}
		filters = append(filters, func(ids []uint32) ([]uint32, error) {
			return cv.ContactFilter(ctx, tenant, where, args, view.Text, ids)
		})
	}
	if view.Special.UnderCap != "" {
		f, _, err := s.capFilter(ctx, tenant, view.Special.UnderCap)
		if err != nil {
			return sv, err
		}
		if f != nil {
			filters = append(filters, f)
			if len(filters) == 1 {
				sv.exact = viewCapExact
			}
		}
	}
	if len(filters) > 0 {
		sv.keep = func(ids []uint32) ([]uint32, error) {
			var err error
			for _, f := range filters {
				if len(ids) == 0 {
					break
				}
				if ids, err = f(ids); err != nil {
					return nil, err
				}
			}
			return ids, nil
		}
	}
	return sv, nil
}

// count counts a set view: BITCOUNT without filters, else checks every candidate up to sv.exact
// and an even sample above (approximate).
func (s *Server) countSetView(ctx context.Context, sv setView) (uint64, bool, error) {
	n, err := s.RDB.BitCount(ctx, sv.key, nil).Result()
	if err != nil || sv.keep == nil {
		return uint64(n), false, err
	}
	bm, err := s.RDB.Get(ctx, sv.key).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, false, err
	}
	ids := segment.Members(bm)
	ids, factor := reach.SampleIDs(ids, sv.exact, viewSampleSize)
	var kept uint64
	for start := 0; start < len(ids); start += viewCandidateMax {
		k, err := sv.keep(ids[start:min(start+viewCandidateMax, len(ids))])
		if err != nil {
			return 0, false, err
		}
		kept += uint64(len(k))
	}
	if factor > 1 {
		return uint64(float64(kept) * factor), true, nil
	}
	return kept, false, nil
}

// ContactFilterer checks candidate ids against a view in ClickHouse.
type ContactFilterer interface {
	ContactFilter(ctx context.Context, tenant int64, where string, args []any, text string, ids []uint32) ([]uint32, error)
}

// viewWhereAll is the view's ClickHouse condition: its rule and its report filters.
func (s *Server) viewWhereAll(v contactView, tenant int64) (string, []any, error) {
	where, args, err := s.viewWhere(v, tenant)
	if err != nil {
		return "", nil, err
	}
	if v.Special != nil && v.Special.ch() {
		c, cargs := v.Special.chConds(tenant)
		if where != "" {
			where = where + " AND " + c
		} else {
			where = c
		}
		args = append(args, cargs...)
	}
	return where, args, nil
}

// viewNote explains why a list can differ from the number it was opened from.
func viewNote(sp *viewSpecial) string {
	if sp == nil || !sp.any() {
		return ""
	}
	switch {
	case sp.Journey != 0:
		return "Each contact is listed once, even if they entered more than once; the journey's numbers count every entry. Contacts deleted since aren't listed."
	case sp.Active != "":
		return "Counted now from the event log. The Users dashboard keeps counting people who were deleted or merged into another contact since; they aren't listed here, so this can be lower."
	case sp.ch() && !sp.Campaign && (sp.Outcome != "" || sp.Reason != ""):
		return "Every campaign together: a contact who got several campaigns is listed once, while the dashboard's totals add up each campaign's contacts. Contacts merged or deleted since aren't listed."
	case sp.ch():
		return "Counted now from the event log. Contacts merged into another or deleted since aren't listed, so this can be a little lower than the report."
	}
	return "Counted now; segment membership and reachability change as contacts act, so this can differ slightly from a number counted a minute ago."
}

// ---- Create segment from this list ----

type viewSegmentReq struct {
	Q    string `json:"q"`
	Name string `json:"name"`
}

// viewSegment: POST /v1/contacts/view-segment {q, name}. Makes a static segment holding the
// contacts of a Contacts list view now (a snapshot: it doesn't follow the view afterwards). Up
// to 500,000 contacts; members are added in the background through list_joined events, like
// adding them by hand.
func (s *Server) viewSegment(w http.ResponseWriter, r *http.Request, p model.Principal) {
	if !p.Perms.Allows(auth.AreaContacts, auth.View) {
		writeErr(w, http.StatusForbidden, "your role doesn't allow this (view access to contacts is needed)")
		return
	}
	var req viewSegmentReq
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 120 {
		writeErr(w, http.StatusBadRequest, "name the segment (up to 120 characters)")
		return
	}
	q := r.URL.Query()
	q.Set("q", req.Q)
	r.URL.RawQuery = q.Encode()
	view, err := s.parseViewRequest(r, p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if view.Rule == nil && view.Text == "" && (view.Special == nil || !view.Special.any()) {
		writeErr(w, http.StatusBadRequest, "add a filter first: a segment of everyone isn't useful")
		return
	}
	tenant := p.TenantID
	ctx := r.Context()
	if !s.allowTenant(ctx, "rlviewseg", tenant, viewSegmentsPerMin) {
		writeErr(w, http.StatusTooManyRequests, errTooMany.Error())
		return
	}
	release, ok := s.acquireViewSegmentRun(ctx, tenant)
	if !ok {
		writeErr(w, http.StatusTooManyRequests, "other lists are still being saved as segments; try again when they finish")
		return
	}
	pager, count, approx, err := s.viewPager(ctx, tenant, view, true)
	if err != nil {
		release()
		s.viewErr(w, err)
		return
	}
	if count > maxViewSegment {
		release()
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("this list has %s%d contacts; a static segment can take up to %d. Use a dynamic segment (Save as segment) or narrow the filters.",
			map[bool]string{true: "about ", false: ""}[approx], count, maxViewSegment))
		return
	}
	id, err := s.audience().CreateList(ctx, tenant, req.Name)
	if err != nil {
		release()
		s.fail(w, err)
		return
	}
	s.bg(func() {
		defer release()
		bctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.fillViewSegment(bctx, tenant, id, pager); err != nil {
			s.Log.Error("view segment", "tenant", tenant, "segment", id, "err", err)
		}
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "contacts": count, "approximate": approx})
}

// viewPager returns a function that pages through every contact id of a view, with its count.
func (s *Server) viewPager(ctx context.Context, tenant int64, view contactView, counted bool) (func(ctx context.Context, after uint32, limit int) ([]uint32, error), uint64, bool, error) {
	where, args, err := s.viewWhereAll(view, tenant)
	if err != nil {
		return nil, 0, false, errUser(err.Error())
	}
	if view.Special != nil && view.Special.bitmap() {
		sv, err := s.buildSetView(ctx, tenant, view, where, args)
		if err != nil {
			return nil, 0, false, err
		}
		var n uint64
		var approx bool
		if counted {
			if n, approx, err = s.countSetView(ctx, sv); err != nil {
				return nil, 0, false, err
			}
		}
		// The bitmap expires in a minute: keep a copy for the background job.
		snap := sv.key + ":snap"
		if err := s.RDB.Copy(ctx, sv.key, snap, 0, true).Err(); err != nil {
			return nil, 0, false, err
		}
		s.RDB.Expire(ctx, snap, 15*time.Minute)
		sv.key = snap
		return func(c context.Context, after uint32, limit int) ([]uint32, error) {
			return bitmapIDs(c, s.RDB, sv.key, after, limit, sv.keep)
		}, n, approx, nil
	}
	cv, ok := s.Analytics.(ContactViewer)
	if !ok {
		return nil, 0, false, errUser("analytics unavailable")
	}
	var n uint64
	var approx bool
	if counted {
		if n, approx, err = cv.ContactCount(ctx, tenant, where, args, view.Text, maxViewSegment); err != nil {
			return nil, 0, false, err
		}
	}
	return func(c context.Context, after uint32, limit int) ([]uint32, error) {
		return cv.ContactPage(c, tenant, where, args, view.Text, after, limit)
	}, n, approx, nil
}

// acquireViewSegmentRun takes one of the workspace's viewSegmentRuns background slots. The
// counter lives in Redis so every API instance shares it, and expires in case a holder dies.
func (s *Server) acquireViewSegmentRun(ctx context.Context, tenant int64) (func(), bool) {
	if s.RDB == nil {
		return func() {}, true
	}
	key := "vsegrun:" + strconv.FormatInt(tenant, 10)
	n, err := s.RDB.Incr(ctx, key).Result()
	if err != nil {
		return func() {}, true
	}
	s.RDB.Expire(ctx, key, 11*time.Minute) // fills time out after 10 minutes
	release := func() { s.RDB.Decr(context.Background(), key) }
	if n > viewSegmentRuns {
		release()
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(release) }, true
}

// fillViewSegment adds the view's contacts to the segment, stopping at maxViewSegment: the count
// checked before may be sampled, or the list may have grown since.
func (s *Server) fillViewSegment(ctx context.Context, tenant, segment int64, page func(context.Context, uint32, int) ([]uint32, error)) error {
	var after uint32
	added := 0
	for {
		if added >= maxViewSegment {
			return nil
		}
		ids, err := page(ctx, after, 1000)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if rest := maxViewSegment - added; len(ids) > rest {
			ids = ids[:rest]
		}
		cs, err := s.loadInOrder(ctx, tenant, ids)
		if err != nil {
			return err
		}
		evs := make([]event.Event, 0, len(cs))
		for _, c := range cs {
			if c.ExternalID != "" {
				evs = append(evs, event.Event{ExternalID: c.ExternalID, Name: event.ListJoined, Props: map[string]any{"list_id": segment, "source": "contact_list"}})
			}
		}
		if len(evs) > 0 {
			if err := s.publishEvents(ctx, tenant, evs); err != nil {
				return err
			}
		}
		added += len(ids)
		if len(ids) < 1000 {
			return nil
		}
		after = ids[len(ids)-1]
	}
}

func (s *Server) viewErr(w http.ResponseWriter, err error) {
	var ue errUser
	switch {
	case errors.Is(err, errTooMany):
		writeErr(w, http.StatusTooManyRequests, err.Error())
	case errors.As(err, &ue):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		s.fail(w, err)
	}
}
