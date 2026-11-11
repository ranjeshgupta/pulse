package reasons

import "testing"

func TestClassify(t *testing.T) {
	for _, tc := range []struct{ ch, outcome, reason, want string }{
		{"email", "sent", "", ""},
		{"email", "suppressed", "contact unsubscribed from email", Consent},
		{"email", "suppressed", "the email address bounced", BouncedBefore},
		{"sms", "capped", "already sent 3 sms messages in 24 hours", Capped},
		{"email", "failed", "deadline", Deadline},
		{"email", "failed", "skipped: no value for contact.first_name", MissingValue},
		{"email", "failed", "template not found", Template},
		{"sms", "failed", "template 4 is a email template", Template},
		{"email", "failed", "an attachment of the template was deleted", Template},
		{"whatsapp", "failed", "no whatsapp provider configured", NoProvider},
		{"email", "failed", "contact has no email address", NoAddress},
		{"sms", "failed", "permanent delivery failure: invalid or unreachable phone number: twilio 21211: invalid To", InvalidNumber},
		{"whatsapp", "failed", "meta whatsapp 131026: Message undeliverable", Blocked},
		{"whatsapp", "failed", "delivery failed: meta whatsapp 131049: ecosystem", SpamPolicy},
		{"whatsapp", "failed", "permanent delivery failure: meta whatsapp 132001: template does not exist", Template},
		{"whatsapp", "failed", "meta whatsapp 131050: user stopped marketing", Consent},
		{"sms", "failed", "twilio error 30007: filtered", SpamPolicy},
		{"sms", "failed", "twilio error 30003", NotOnChannel},
		{"push", "failed", "recipient unreachable on this channel: fcm: UNREGISTERED", InvalidToken},
		{"rcs", "failed", "recipient unreachable on this channel: google rbm: not found", NotOnChannel},
		{"sms", "failed", "permanent delivery failure: msg91: HTTP 401: bad key", ProviderAuth},
		{"email", "failed", "permanent delivery failure: 550 5.1.1 user unknown", InvalidEmail},
		{"email", "failed", "permanent delivery failure: 554 5.7.1 message rejected", ProviderRejected},
		{"sms", "failed", "gave up after 8 tries: send sms: dial tcp: i/o timeout", Timeout},
		{"sms", "failed", "gave up after 8 tries: send sms: msg91: HTTP 429: slow down", RateLimited},
		{"sms", "failed", "gave up after 8 tries: send sms: msg91: HTTP 503: down", ProviderError},
		{"email", "failed", "", Unknown},
		{"email", "failed", "something odd", ProviderError},
	} {
		if got := Classify(tc.ch, tc.outcome, tc.reason); got != tc.want {
			t.Errorf("Classify(%s, %s, %q) = %q, want %q", tc.ch, tc.outcome, tc.reason, got, tc.want)
		}
	}
}

func TestLabelsAndGroups(t *testing.T) {
	for _, c := range Codes {
		if Label(c) == "" || !Valid(c) {
			t.Errorf("%s has no label", c)
		}
	}
	if Label("nope") != "Not recorded" || Valid("nope") {
		t.Error("unknown codes read Not recorded")
	}
	if Label(Blocked) != "Undeliverable (possibly blocked)" {
		t.Error("blocked label must follow the plan's wording")
	}
	if SkipGroup(Consent) != "consent" || SkipGroup(NoAddress) != "reachability" || SkipGroup(Deadline) != "deadline" ||
		SkipGroup(MissingValue) != "missing_value" || SkipGroup(Capped) != "cap" || SkipGroup(Blocked) != "" {
		t.Error("skip groups")
	}
	if MetaCode(132005) != Template || TwilioCode(99999) != ProviderRejected {
		t.Error("code maps")
	}
}
