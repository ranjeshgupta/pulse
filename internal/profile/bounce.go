package profile

// Bounce rules (package automation): every bounce and complaint, on any channel, counts on the
// contact (email_hard_bounces, sms_complaints…); when a count reaches the workspace's rule for it,
// the profile adds the rule's action right after the event, like a points threshold adds a stage
// move: a <channel>_bounce_action event (stop the channel, leave every segment, delete) or an unsubscribed
// event (from the channel, or from everything). A new address on a channel starts its counts over.

import (
	"context"
	"slices"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/automation"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/reasons"
)

// BounceRules gives a workspace's rules for a channel.
type BounceRules func(tenant int64, ch string) automation.ChannelBounces

// bounceOf is the channel and bounce type an event reports ("" when it isn't a bounce or a
// complaint), and whether it is about the contact's current address.
func bounceOf(e *event.Event) (ch, typ string, current bool) {
	current = true
	switch e.Name {
	case "email_bounced":
		typ = attr.String(e.Props["kind"])
		if typ != reasons.BounceHard && typ != reasons.BounceSoft {
			return "", "", false
		}
		if same, ok := e.Props["same_address"].(bool); ok {
			current = same
		}
		return channel.Email, typ, current
	case "email_complained":
		return channel.Email, reasons.BounceComplaint, true
	}
	i := strings.LastIndexByte(e.Name, '_')
	if i < 0 {
		return "", "", false
	}
	ch, outcome := e.Name[:i], e.Name[i+1:]
	if !slices.Contains(automation.BounceChannels, ch) || outcome != "failed" && outcome != "bounced" {
		return "", "", false
	}
	typ = reasons.BounceType(attr.String(e.Props["reason_code"]))
	if b, _ := e.Props["bounce"].(bool); b {
		typ = reasons.BounceHard // the provider said the failure is permanent
	}
	return ch, typ, current
}

// countBounce counts a bounce or complaint on the contact and returns the events its rule adds
// (none until the count reaches the rule). merged must be the profile after e.
func countBounce(merged map[string]any, changed []string, e *event.Event, rules BounceRules) (map[string]any, []string, []event.Event) {
	ch, typ, current := bounceOf(e)
	if typ == "" || rules == nil {
		return merged, changed, nil
	}
	key := consent.BounceCountAttr(ch, typ)
	n, _ := attr.Number(attr.String(merged[key]))
	count := int(n) + 1
	out := make(map[string]any, len(merged)+1)
	for k, v := range merged {
		out[k] = v
	}
	out[key] = float64(count)
	changed = mergeKeys(changed, []string{key})

	rule := rules(e.TenantID, ch).Rule(typ)
	// Fires once, when the count reaches the rule; never for an address the contact has replaced.
	if count != rule.Count || rule.Action == automation.BounceRecord || !current {
		return out, changed, nil
	}
	source := "bounce"
	if typ == reasons.BounceComplaint {
		source = "complaint"
	}
	follow := event.Event{ID: e.ID + ":bounce", TenantID: e.TenantID, ExternalID: e.ExternalID, Ts: e.Ts}
	switch rule.Action {
	case automation.BounceUnsubscribe, automation.BounceDoNotContact:
		unsub := ch
		if rule.Action == automation.BounceDoNotContact {
			unsub = consent.All
		}
		follow.Name = consent.Unsubscribed
		follow.Props = map[string]any{"channel": unsub, "source": source, "bounce_type": typ, "count": count}
	default:
		follow.Name = event.BounceActionName(ch)
		follow.Props = map[string]any{"channel": ch, "type": typ, "count": count, "action": rule.Action}
	}
	return out, changed, []event.Event{follow}
}

// bounceActionUpdates applies a bounce_action event: stop the channel to the address, and for
// leave_segments take the contact out of every segment too. delete is done by the journeys role.
func bounceActionUpdates(e *event.Event, updates map[string]any) {
	ch := attr.String(e.Props["channel"])
	switch attr.String(e.Props["action"]) {
	case automation.BounceLeaveSegments:
		updates[consent.AttrSegmentsExcluded] = true
		fallthrough
	case automation.BounceStop:
		if a := consent.BouncedAttr(ch); a != "" {
			updates[a] = true
		}
	}
}

// resetBounces starts a channel's counts over when its address changes: a new email address
// clears the email counts and email_bounced, a new phone number those of SMS, WhatsApp and RCS
// (sms_bounced is already cleared by MergeScored). Leaving segments ends with a new address too.
func resetBounces(cur, merged map[string]any, changed []string) (map[string]any, []string) {
	var chans []string
	if slices.Contains(changed, "email") && attr.String(cur["email"]) != "" {
		chans = append(chans, channel.Email)
	}
	if slices.Contains(changed, "phone") && attr.String(cur["phone"]) != "" {
		chans = append(chans, channel.SMS, channel.WhatsApp, channel.RCS)
	}
	if len(chans) == 0 {
		return merged, changed
	}
	var drop []string
	for _, ch := range chans {
		for _, t := range automation.BounceTypes {
			drop = append(drop, consent.BounceCountAttr(ch, t))
		}
		if ch != channel.SMS { // MergeScored sets sms_bounced false itself
			drop = append(drop, consent.BouncedAttr(ch))
		}
	}
	drop = append(drop, consent.AttrSegmentsExcluded)
	out := merged
	copied := false
	for _, k := range drop {
		if _, had := merged[k]; !had {
			continue
		}
		if !copied {
			out = make(map[string]any, len(merged))
			for kk, v := range merged {
				out[kk] = v
			}
			copied = true
		}
		delete(out, k)
		changed = mergeKeys(changed, []string{k})
	}
	return out, changed
}

// AutomationStore reads a workspace's automation settings (the bounce rules among them).
type AutomationStore interface {
	AutomationSettings(ctx context.Context, tenant int64) (automation.Settings, error)
}

type cachedBounces struct {
	s   automation.Settings
	exp time.Time
}

const bounceRulesTTL = 30 * time.Second

// bounceRules reads the rules through a 30-second cache per workspace; without an automation
// store (tests), or when the read fails, the defaults (what Pulse always did) apply.
func (s *Service) bounceRules(ctx context.Context) BounceRules {
	as, ok := s.store.(AutomationStore)
	return func(tenant int64, ch string) automation.ChannelBounces {
		if !ok {
			return automation.Defaults().BouncesFor(ch)
		}
		now := s.now()
		if c, hit := s.bounces.Load(tenant); hit && now.Before(c.(cachedBounces).exp) {
			return c.(cachedBounces).s.BouncesFor(ch)
		}
		set, err := as.AutomationSettings(ctx, tenant)
		if err != nil {
			set = automation.Defaults()
		}
		s.bounces.Store(tenant, cachedBounces{s: set, exp: now.Add(bounceRulesTTL)})
		return set.BouncesFor(ch)
	}
}
