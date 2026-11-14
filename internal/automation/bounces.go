package automation

// Bounce rules: what happens to a contact whose address on a channel bounces or who complains,
// after how many times (Settings → Bounces & unsubscribes, the "bounces" section). The profile step
// counts each contact's hard bounces, soft bounces and complaints per channel (the bounce types of
// package reasons) and applies the rule when a count reaches it.

import (
	"fmt"
	"slices"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/reasons"
)

// Bounce actions.
const (
	BounceRecord        = "record"         // count it, nothing more
	BounceStop          = "stop"           // stop sending on this channel to that address (until it changes or is cleared)
	BounceUnsubscribe   = "unsubscribe"    // unsubscribe the contact from this channel
	BounceLeaveSegments = "leave_segments" // stop, and leave every segment (dynamic ones too) until cleared
	BounceDoNotContact  = "do_not_contact" // unsubscribe from every channel
	BounceDelete        = "delete"         // delete the contact and their history
)

// BounceActions lists the actions in the order the app offers them.
var BounceActions = []string{BounceRecord, BounceStop, BounceUnsubscribe, BounceLeaveSegments, BounceDoNotContact, BounceDelete}

// BounceChannels are the channels whose bounces are counted (push device tokens that are gone are
// removed by the dispatcher instead).
var BounceChannels = []string{channel.Email, channel.SMS, channel.WhatsApp, channel.RCS}

// BounceTypes are the kinds counted for each channel.
var BounceTypes = []string{reasons.BounceHard, reasons.BounceSoft, reasons.BounceComplaint}

// BounceRule fires Action when a contact's count of one bounce type on one channel reaches Count.
type BounceRule struct {
	Count  int    `json:"count"`
	Action string `json:"action"`
}

// ChannelBounces are one channel's rules.
type ChannelBounces struct {
	Hard      BounceRule `json:"hard"`
	Soft      BounceRule `json:"soft"`
	Complaint BounceRule `json:"complaint"`
}

// Rule is the rule of one bounce type.
func (c ChannelBounces) Rule(bounceType string) BounceRule {
	switch bounceType {
	case reasons.BounceHard:
		return c.Hard
	case reasons.BounceSoft:
		return c.Soft
	case reasons.BounceComplaint:
		return c.Complaint
	}
	return BounceRule{Count: 1, Action: BounceRecord}
}

// DefaultBounces are what Pulse did before the rules existed: an email hard bounce stops email to
// the address, an email complaint unsubscribes from email, an SMS that fails permanently stops SMS
// to the number; everything else is only recorded.
func DefaultBounces() map[string]ChannelBounces {
	record := BounceRule{Count: 1, Action: BounceRecord}
	return map[string]ChannelBounces{
		channel.Email:    {Hard: BounceRule{1, BounceStop}, Soft: record, Complaint: BounceRule{1, BounceUnsubscribe}},
		channel.SMS:      {Hard: BounceRule{1, BounceStop}, Soft: record, Complaint: record},
		channel.WhatsApp: {Hard: record, Soft: record, Complaint: record},
		channel.RCS:      {Hard: record, Soft: record, Complaint: record},
	}
}

// BouncesFor is a channel's rules (the default for a channel the workspace didn't set).
func (s Settings) BouncesFor(ch string) ChannelBounces {
	if b, ok := s.Bounces[ch]; ok {
		return b
	}
	return DefaultBounces()[ch]
}

// MaxBounceCount caps a rule's count.
const MaxBounceCount = 100

func validateBounces(b map[string]ChannelBounces) error {
	for ch, rules := range b {
		if !slices.Contains(BounceChannels, ch) {
			return fmt.Errorf("bounces: %q isn't a channel with bounce rules (%s)", ch, strings.Join(BounceChannels, ", "))
		}
		for _, t := range BounceTypes {
			r := rules.Rule(t)
			if r.Count < 1 || r.Count > MaxBounceCount {
				return fmt.Errorf("bounces[%s].%s: the count must be 1 to %d", ch, t, MaxBounceCount)
			}
			if !slices.Contains(BounceActions, r.Action) {
				return fmt.Errorf("bounces[%s].%s: the action must be one of %s", ch, t, strings.Join(BounceActions, ", "))
			}
		}
	}
	return nil
}
