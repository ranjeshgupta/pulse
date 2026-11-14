package ch

import (
	"testing"

	"pulse/internal/model"
	"pulse/internal/reasons"
)

func TestFunnelCountsBouncesOnEveryChannel(t *testing.T) {
	rep := Report{Totals: model.MessageCounts{Sent: 100, Failed: 12, Bounced: 3, Complained: 1}, Reasons: []ReasonCount{
		{Kind: "bounced", Code: reasons.HardBounce, Contacts: 2},                         // email hard
		{Kind: "bounced", Code: reasons.SoftBounce, Contacts: 1},                         // email soft
		{Kind: "failed", Code: reasons.InvalidNumber, Contacts: 5},                       // SMS / WhatsApp: hard
		{Kind: "failed", Code: reasons.Blocked, Contacts: 2},                             // WhatsApp block: a complaint
		{Kind: "failed", Code: reasons.NotOnChannel, Group: "reachability", Contacts: 4}, // shown as skipped, not again
		{Kind: "failed", Code: reasons.Timeout, Contacts: 1},                             // the provider's fault: no bounce
		{Kind: "complained", Code: reasons.Complaint, Contacts: 1},
	}}
	f := BuildFunnel(rep)
	if f.HardBounced != 7 || f.SoftBounced != 1 || f.Bounced != 8 || f.Complained != 3 {
		t.Errorf("hard %d soft %d bounced %d complained %d, want 7 1 8 3", f.HardBounced, f.SoftBounced, f.Bounced, f.Complained)
	}
}
