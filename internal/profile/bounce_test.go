package profile

import (
	"testing"
	"time"

	"pulse/internal/automation"
	"pulse/internal/consent"
	"pulse/internal/event"
)

func rulesOf(s automation.Settings) BounceRules {
	return func(_ int64, ch string) automation.ChannelBounces { return s.BouncesFor(ch) }
}

// apply runs one event through the merge and the bounce counting, then its follow-ups.
func apply(t *testing.T, cur map[string]any, e event.Event, rules BounceRules) (map[string]any, []event.Event) {
	t.Helper()
	merged, changed := MergeScored(cur, &e, 0)
	merged, changed = resetBounces(cur, merged, changed)
	merged, _, follows := countBounce(merged, changed, &e, rules)
	for _, f := range follows {
		NormalizeConsent(&f)
		merged, _ = MergeScored(merged, &f, 0)
	}
	return merged, follows
}

func TestDefaultBounceRulesKeepOldBehaviour(t *testing.T) {
	rules := rulesOf(automation.Defaults())
	p := map[string]any{"email": "a@x.com", "phone": "+919800000001"}
	hard := event.Event{ID: "1", Name: "email_bounced", Ts: time.Now(), Props: map[string]any{"kind": "hard", "same_address": true}}
	p, f := apply(t, p, hard, rules)
	if p[consent.AttrEmailBounced] != true || p["email_hard_bounces"] != float64(1) || len(f) != 1 || f[0].Name != "email_bounce_action" {
		t.Errorf("email hard bounce: %v %v", p, f)
	}
	p, f = apply(t, p, event.Event{ID: "2", Name: "email_complained", Ts: time.Now()}, rules)
	if p[consent.Key("email")] != true || f[0].Name != consent.Unsubscribed || f[0].Props["source"] != "complaint" {
		t.Errorf("email complaint: %v %v", p, f)
	}
	p, f = apply(t, p, event.Event{ID: "3", Name: "sms_failed", Ts: time.Now(), Props: map[string]any{"reason_code": "invalid_number", "bounce": true}}, rules)
	if p[consent.AttrSMSBounced] != true || p["sms_hard_bounces"] != float64(1) {
		t.Errorf("sms permanent failure: %v %v", p, f)
	}
	p, f = apply(t, p, event.Event{ID: "4", Name: "whatsapp_failed", Ts: time.Now(), Props: map[string]any{"reason_code": "blocked"}}, rules)
	if p["whatsapp_complaints"] != float64(1) || len(f) != 0 || p[consent.AttrWhatsAppBounced] != nil {
		t.Errorf("whatsapp blocked is only counted by default: %v %v", p, f)
	}
	// Provider-side failures aren't the contact's: not counted.
	p, _ = apply(t, p, event.Event{ID: "5", Name: "sms_failed", Ts: time.Now(), Props: map[string]any{"reason_code": "timeout"}}, rules)
	if _, ok := p["sms_soft_bounces"]; ok {
		t.Errorf("a timeout counted: %v", p)
	}
}

func TestBounceRulesCountAndAct(t *testing.T) {
	s := automation.Defaults()
	s.Bounces["email"] = automation.ChannelBounces{
		Hard:      automation.BounceRule{Count: 2, Action: automation.BounceLeaveSegments},
		Soft:      automation.BounceRule{Count: 3, Action: automation.BounceStop},
		Complaint: automation.BounceRule{Count: 1, Action: automation.BounceDoNotContact},
	}
	rules := rulesOf(s)
	p := map[string]any{"email": "a@x.com"}
	soft := func(id string) event.Event {
		return event.Event{ID: id, Name: "email_bounced", Ts: time.Now(), Props: map[string]any{"kind": "soft", "same_address": true}}
	}
	for i, want := range []any{nil, nil, true} {
		var f []event.Event
		p, f = apply(t, p, soft(string(rune('a'+i))), rules)
		if p[consent.AttrEmailBounced] != want {
			t.Errorf("soft bounce %d: email_bounced = %v (follow-ups %v)", i+1, p[consent.AttrEmailBounced], f)
		}
	}
	// A fourth soft bounce doesn't act again.
	if _, f := apply(t, p, soft("d"), rules); len(f) != 0 {
		t.Errorf("acted twice: %v", f)
	}
	hard := event.Event{ID: "h", Name: "email_bounced", Ts: time.Now(), Props: map[string]any{"kind": "hard", "same_address": true}}
	p, _ = apply(t, p, hard, rules)
	if p[consent.AttrSegmentsExcluded] != nil {
		t.Error("left segments after one hard bounce; the rule says two")
	}
	hard.ID = "h2"
	p, _ = apply(t, p, hard, rules)
	if p[consent.AttrSegmentsExcluded] != true {
		t.Errorf("two hard bounces should leave every segment: %v", p)
	}
	// A bounce of an address the contact has since replaced acts on nothing.
	q := map[string]any{"email": "new@x.com"}
	old := event.Event{ID: "o", Name: "email_bounced", Ts: time.Now(), Props: map[string]any{"kind": "hard", "same_address": false}}
	q, _ = apply(t, q, old, rules)
	q, f := apply(t, q, event.Event{ID: "o2", Name: "email_bounced", Ts: time.Now(), Props: map[string]any{"kind": "hard", "same_address": false}}, rules)
	if q[consent.AttrSegmentsExcluded] != nil || len(f) != 0 {
		t.Errorf("acted on an old address: %v %v", q, f)
	}
	// Do Not Contact unsubscribes from everything.
	p, f = apply(t, p, event.Event{ID: "c", Name: "email_complained", Ts: time.Now()}, rules)
	if p[consent.Key(consent.All)] != true || f[0].Props["channel"] != consent.All {
		t.Errorf("complaint → do not contact: %v %v", p, f)
	}
	// A new address starts over: counts, the stop and the segment exclusion go.
	p, _ = apply(t, p, event.Event{ID: "n", Name: "identify", Ts: time.Now(), Email: "fresh@x.com"}, rules)
	if p["email_soft_bounces"] != nil || p[consent.AttrEmailBounced] != nil || p[consent.AttrSegmentsExcluded] != nil {
		t.Errorf("new address kept the bounce state: %v", p)
	}
}

func TestBounceStopSurvivesAddressSwitching(t *testing.T) {
	s := automation.Defaults()
	s.Bounces["email"] = automation.ChannelBounces{
		Hard: automation.BounceRule{Count: 1, Action: automation.BounceLeaveSegments},
		Soft: automation.BounceRule{Count: 1, Action: automation.BounceRecord}, Complaint: automation.BounceRule{Count: 1, Action: automation.BounceRecord},
	}
	rules := rulesOf(s)
	p := map[string]any{"email": "a@x.com", "phone": "+919800000001"}
	p, _ = apply(t, p, event.Event{ID: "1", Name: "email_bounced", Ts: time.Now(), Props: map[string]any{"kind": "hard", "same_address": true}}, rules)
	if p[consent.AttrEmailBounced] != true || p[consent.AttrSegmentsExcluded] != true || p["email_bounced_address"] != "a@x.com" {
		t.Fatalf("after the bounce: %v", p)
	}
	// A new phone number doesn't end an exclusion that email caused.
	p, _ = apply(t, p, event.Event{ID: "2", Name: "identify", Ts: time.Now(), Attrs: map[string]any{"phone": "+919800000002"}}, rules)
	if p[consent.AttrSegmentsExcluded] != true {
		t.Errorf("a new phone ended the email exclusion: %v", p)
	}
	// A new email ends it; changing back to the bounced address stops email again.
	p, _ = apply(t, p, event.Event{ID: "3", Name: "identify", Ts: time.Now(), Email: "b@x.com"}, rules)
	if p[consent.AttrEmailBounced] != nil || p[consent.AttrSegmentsExcluded] != nil {
		t.Errorf("new address kept the stop: %v", p)
	}
	p, _ = apply(t, p, event.Event{ID: "4", Name: "identify", Ts: time.Now(), Email: "a@x.com"}, rules)
	if p[consent.AttrEmailBounced] != true {
		t.Errorf("back to the bounced address lifted the stop: %v", p)
	}
}
