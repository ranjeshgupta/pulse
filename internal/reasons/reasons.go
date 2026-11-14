// Package reasons normalises why a message didn't reach a contact. Every <channel>_failed,
// _suppressed, _capped and _bounced event (and email_complained) carries props.reason_code, one of
// the codes below, next to props.reason (the dispatcher's or the provider's own words), so reports
// can group failures by cause while the raw text stays available on the contact's history.
//
// Codes are derived from the reason text the dispatcher and the provider adapters write (their
// formats are Pulse's own: "meta whatsapp 131026: ...", "twilio 21211: ...", the providers
// package's sentinel errors) and from the provider error codes in delivery receipts.
package reasons

import (
	"regexp"
	"strconv"
	"strings"
)

// Reason codes.
const (
	InvalidNumber    = "invalid_number"    // the phone number is invalid or can't take messages
	InvalidEmail     = "invalid_email"     // the mail server rejected the address
	NoAddress        = "no_address"        // the contact has no email / valid phone / push device
	NotOnChannel     = "not_on_channel"    // not a WhatsApp user, RCS not supported, handset unreachable
	Blocked          = "blocked"           // WhatsApp: undeliverable (possibly blocked by the user)
	InvalidToken     = "invalid_token"     // push: the device token or browser subscription is gone
	HardBounce       = "hard_bounce"       // email: permanent bounce
	SoftBounce       = "soft_bounce"       // email: temporary bounce (mailbox full, greylisted…)
	Complaint        = "complaint"         // email: marked as spam
	Consent          = "consent"           // opted out of the channel (in Pulse or at the provider)
	BouncedBefore    = "bounced_before"    // the address or number bounced earlier: not tried
	Capped           = "capped"            // frequency cap
	Deadline         = "deadline"          // the send deadline passed (quota or quiet hours held it too long)
	MissingValue     = "missing_value"     // a personalisation variable had no value (template set to skip)
	Template         = "template"          // template missing, wrong channel, failed to render, not approved
	NoProvider       = "no_provider"       // no provider set up for the channel
	ProviderAuth     = "provider_auth"     // the provider refused Pulse's credentials
	ProviderRejected = "provider_rejected" // the provider refused the message for another reason
	SpamPolicy       = "spam_policy"       // held back by the provider's or carrier's spam / frequency policy
	RateLimited      = "rate_limited"      // the provider kept answering "too many requests"
	Timeout          = "timeout"           // the provider didn't answer in time (after every retry)
	ProviderError    = "provider_error"    // the provider failed (5xx, network) after every retry
	Unknown          = "unknown"           // recorded before reason codes existed, or not classifiable
)

var labels = map[string]string{
	InvalidNumber: "Invalid phone number", InvalidEmail: "Invalid email address", NoAddress: "No address on this channel",
	NotOnChannel: "Not reachable on this channel", Blocked: "Undeliverable (possibly blocked)",
	InvalidToken: "Push device gone", HardBounce: "Hard bounce", SoftBounce: "Soft bounce", Complaint: "Spam complaint",
	Consent: "Opted out", BouncedBefore: "Bounced earlier", Capped: "Frequency cap", Deadline: "Send deadline passed",
	MissingValue: "Missing personalisation value", Template: "Template problem", NoProvider: "No provider set up",
	ProviderAuth: "Provider credentials refused", ProviderRejected: "Rejected by the provider",
	SpamPolicy: "Provider spam / frequency policy", RateLimited: "Provider rate limit", Timeout: "Provider timed out",
	ProviderError: "Provider error", Unknown: "Not recorded",
}

// Codes lists every code, in the order reports show them.
var Codes = []string{InvalidNumber, InvalidEmail, NoAddress, NotOnChannel, Blocked, InvalidToken, HardBounce, SoftBounce,
	Complaint, Consent, BouncedBefore, Capped, Deadline, MissingValue, Template, NoProvider, ProviderAuth, ProviderRejected,
	SpamPolicy, RateLimited, Timeout, ProviderError, Unknown}

// Label is a code's name for people ("" and unknown codes read "Not recorded").
func Label(code string) string {
	if l, ok := labels[code]; ok {
		return l
	}
	return labels[Unknown]
}

// Valid reports whether code is one of the codes.
func Valid(code string) bool { _, ok := labels[code]; return ok }

// Skip groups: the funnel's "skipped" step splits these out of failures.
var skipGroup = map[string]string{
	Consent: "consent", BouncedBefore: "consent", NoAddress: "reachability", NotOnChannel: "reachability",
	InvalidToken: "reachability", Capped: "cap", MissingValue: "missing_value", Deadline: "deadline",
}

// SkipGroup is the funnel group of a code that means the message was skipped rather than tried
// (consent, reachability, cap, missing_value, deadline), or "".
func SkipGroup(code string) string { return skipGroup[code] }

// Bounce types: what a failed message says about the contact's address on that channel, the same
// for every channel. Provider-side failures (credentials, timeouts, templates, rate limits) and
// skips are none of these: they aren't the contact's fault.
const (
	BounceHard      = "hard"      // permanent: the address or number doesn't exist or can't take messages
	BounceSoft      = "soft"      // temporary: mailbox full, handset unreachable, not on the channel now
	BounceComplaint = "complaint" // the person marked it as spam or blocked the sender
)

var bounceTypes = map[string]string{
	InvalidNumber: BounceHard, InvalidEmail: BounceHard, InvalidToken: BounceHard, HardBounce: BounceHard,
	SoftBounce: BounceSoft, NotOnChannel: BounceSoft,
	Complaint: BounceComplaint, Blocked: BounceComplaint,
}

// BounceType is the bounce type of a reason code ("" when it isn't a bounce or a complaint).
func BounceType(code string) string { return bounceTypes[code] }

// BounceCodes lists the reason codes of one bounce type, for queries.
func BounceCodes(bounceType string) []string {
	var out []string
	for _, c := range Codes {
		if bounceTypes[c] == bounceType {
			out = append(out, c)
		}
	}
	return out
}

// Meta WhatsApp Cloud API error codes.
var metaCodes = map[int]string{
	131026: Blocked, // message undeliverable: not on WhatsApp, old app, or the user blocked the business
	131021: InvalidNumber, 131030: InvalidNumber, 1006: InvalidNumber,
	131049: SpamPolicy, 131048: SpamPolicy, // per-user marketing limit, spam rate limit
	131050: Consent,          // the user stopped marketing messages
	131047: ProviderRejected, // outside the 24-hour customer service window
	131056: RateLimited, 130429: RateLimited, 80007: RateLimited, 4: RateLimited,
	190: ProviderAuth, 0: ProviderAuth, 10: ProviderAuth, 200: ProviderAuth,
	131051: ProviderRejected, 131052: ProviderRejected, 131053: ProviderRejected, 131042: ProviderRejected,
	131000: ProviderError, 131016: ProviderError, 2: ProviderError,
}

// Twilio error codes.
var twilioCodes = map[int]string{
	21211: InvalidNumber, 21214: InvalidNumber, 21612: InvalidNumber, 21614: InvalidNumber, 30005: InvalidNumber,
	30006: InvalidNumber, 21408: ProviderRejected,
	30003: NotOnChannel, 30004: Blocked, 30007: SpamPolicy, 21610: Consent,
	20003: ProviderAuth, 20404: ProviderRejected, 30008: ProviderError, 30001: RateLimited, 20429: RateLimited,
}

// MetaCode maps a WhatsApp Cloud API error code.
func MetaCode(code int) string {
	if c, ok := metaCodes[code]; ok {
		return c
	}
	if code >= 132000 && code <= 132015 {
		return Template
	}
	return ProviderRejected
}

// TwilioCode maps a Twilio error code.
func TwilioCode(code int) string {
	if c, ok := twilioCodes[code]; ok {
		return c
	}
	return ProviderRejected
}

var (
	metaRe   = regexp.MustCompile(`meta whatsapp (\d+)`)
	twilioRe = regexp.MustCompile(`twilio (?:error )?(\d+)`)
	httpRe   = regexp.MustCompile(`HTTP (\d{3})`)
	smtpRe   = regexp.MustCompile(`\b(5\d\d)[ -](5\.\d{1,3}\.\d{1,3})?`)
)

// Classify derives the code of an outcome from its reason text. outcome is the event's outcome
// (failed, suppressed, capped, bounced…); ch the channel. Outcomes that aren't failures return "".
func Classify(ch, outcome, reason string) string {
	switch outcome {
	case "suppressed":
		if strings.Contains(reason, "bounced") {
			return BouncedBefore
		}
		return Consent
	case "capped":
		return Capped
	case "failed":
	default:
		return ""
	}
	r := strings.TrimSpace(reason)
	low := strings.ToLower(r)
	switch {
	case r == "":
		return Unknown
	case r == "deadline":
		return Deadline
	case strings.HasPrefix(r, "skipped: no value for"):
		return MissingValue
	case strings.HasPrefix(low, "gave up after"):
		_, inner, _ := strings.Cut(r, ":")
		inner = strings.ToLower(inner)
		switch {
		case isTimeout(inner):
			return Timeout
		case strings.Contains(inner, "http 429") || strings.Contains(inner, "too many requests"):
			return RateLimited
		}
		if c := providerCode(strings.TrimSpace(inner)); c != "" && c != ProviderRejected {
			return c
		}
		return ProviderError
	case strings.HasPrefix(low, "template ") || strings.Contains(low, "attachment of the template") || strings.HasPrefix(low, "render") ||
		strings.Contains(low, "template:") || strings.Contains(low, "executing \""):
		return Template
	case strings.HasPrefix(low, "no ") && strings.HasSuffix(low, " provider configured"):
		return NoProvider
	case strings.HasPrefix(low, "contact has no "):
		return NoAddress
	case strings.HasPrefix(low, "delivery failed: "):
		return Classify(ch, outcome, r[len("delivery failed: "):])
	}
	if c := providerCode(r); c != "" {
		if c == NotOnChannel && ch == "push" {
			return InvalidToken
		}
		if c == ProviderRejected && ch == "email" {
			if m := smtpRe.FindStringSubmatch(r); m != nil && (strings.HasPrefix(m[2], "5.1.") || m[1] == "550" && strings.Contains(low, "user")) {
				return InvalidEmail
			}
		}
		return c
	}
	if isTimeout(low) {
		return Timeout
	}
	return ProviderError
}

// providerCode reads the codes adapters put in their errors.
func providerCode(r string) string {
	low := strings.ToLower(r)
	if m := metaRe.FindStringSubmatch(r); m != nil {
		n, _ := strconv.Atoi(m[1])
		return MetaCode(n)
	}
	if m := twilioRe.FindStringSubmatch(r); m != nil {
		n, _ := strconv.Atoi(m[1])
		return TwilioCode(n)
	}
	switch {
	case strings.Contains(low, "invalid or unreachable phone number"):
		return InvalidNumber
	case strings.Contains(low, "recipient unreachable on this channel"), strings.Contains(low, "unsubscribed from push"),
		strings.Contains(low, "unregistered"), strings.Contains(low, "not a valid browser push subscription"):
		return NotOnChannel
	}
	if m := httpRe.FindStringSubmatch(r); m != nil {
		switch m[1] {
		case "401", "403":
			return ProviderAuth
		case "429":
			return RateLimited
		}
	}
	if strings.Contains(low, "permanent delivery failure") {
		if strings.Contains(low, "auth") || strings.Contains(low, "credential") {
			return ProviderAuth
		}
		return ProviderRejected
	}
	return ""
}

func isTimeout(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "timeout") || strings.Contains(s, "timed out") || strings.Contains(s, "deadline exceeded")
}
