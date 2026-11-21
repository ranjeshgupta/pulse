// Package model holds the row types shared by the stores, the API and the workers.
package model

import (
	"encoding/json"
	"slices"
	"time"

	"pulse/internal/auth"
	"pulse/internal/segment"
)

type Tenant struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	SiteKey string `json:"site_key"`

	IdentitySecret        string `json:"-"` // signs user ids for the web SDK; see /v1/settings/identity
	RequireSignedIdentify bool   `json:"require_signed_identify"`
}

type User struct {
	ID           int64      `json:"id"`
	TenantID     int64      `json:"tenant_id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	PasswordHash string     `json:"-"`
	SSOSubject   string     `json:"-"`
	SSO          bool       `json:"sso"`        // has signed in through SSO
	Password     bool       `json:"password"`   // has a password
	TwoFactor    bool       `json:"two_factor"` // has two-step sign-in with an authenticator app
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// TwoFactor is a user's authenticator-app setup. The secrets are sealed with the server key.
type TwoFactor struct {
	Secret       []byte // confirmed secret (sealed); nil until set up
	Pending      []byte // a new secret not yet confirmed (sealed)
	EnabledAt    *time.Time
	LastStep     int64 // the last 30-second step accepted
	RecoveryLeft int   // unused recovery codes
}

// Enabled reports whether two-step sign-in is on.
func (t TwoFactor) Enabled() bool { return len(t.Secret) > 0 }

// Principal is who is calling: a signed-in user (UserID > 0) or a server holding the API key.
type Principal struct {
	TenantID int64
	UserID   int64
	Role     string
	Perms    auth.Perms
	Actor    string // for the audit log: the user's email, or "API key"
}

// Role is a workspace's custom role.
type Role struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Permissions auth.Perms `json:"permissions"`
	Users       int        `json:"users"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SSOConfig is a workspace's OIDC settings. The client secret is write-only through the API.
type SSOConfig struct {
	Enabled         bool   `json:"enabled"`
	Issuer          string `json:"issuer"`
	DiscoveryURL    string `json:"discovery_url"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret,omitempty"`
	HasClientSecret bool   `json:"has_client_secret"`
	AutoCreateUsers bool   `json:"auto_create_users"`
	DefaultRole     string `json:"default_role"`
	// IdPVerifiesEmail: the identity provider only issues verified addresses, so an ID token
	// without email_verified is accepted. Off: such tokens are refused.
	IdPVerifiesEmail bool `json:"idp_verifies_email"`
}

type Contact struct {
	TenantID   int64          `json:"tenant_id"`
	ID         uint32         `json:"id"`
	ExternalID string         `json:"external_id"`
	Email      string         `json:"email"`
	Attrs      map[string]any `json:"attrs"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type Segment struct {
	ID        int64           `json:"id"`
	TenantID  int64           `json:"tenant_id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"` // rule | static (a list whose members are added by hand or import)
	Rule      json.RawMessage `json:"rule"`
	HasWindow bool            `json:"has_window"`
	Status    string          `json:"status"`
	BuiltAt   *time.Time      `json:"built_at,omitempty"`
	Size      int64           `json:"size"`
	// Lists only: offered on the preference center, where contacts join or leave them themselves.
	Public      bool   `json:"public"`
	Description string `json:"description"`
	// Refresh: live (nil or mode live) follows every event; daily is rebuilt once a day.
	Refresh       *segment.Refresh `json:"refresh,omitempty"`
	NextRefreshAt *time.Time       `json:"next_refresh_at,omitempty"`
}

type Template struct {
	ID       int64           `json:"id"`
	TenantID int64           `json:"tenant_id"`
	Name     string          `json:"name"`
	Channel  string          `json:"channel"`
	Subject  string          `json:"subject"` // email
	HTML     string          `json:"html"`    // email
	Content  json.RawMessage `json:"content"` // other channels, see channel.Content
	Builder  json.RawMessage `json:"builder,omitempty"`
	// Transactional templates (OTPs, receipts, password resets) skip frequency caps and quiet hours.
	Transactional bool `json:"transactional"`
	// MissingValues is what happens when a variable ({{contact.first_name}}) has no value and no
	// default: "empty" (the default) renders it empty, "skip" sends nothing to that contact.
	MissingValues string `json:"missing_values,omitempty"`
	// Hidden: written inside a one-time campaign and not offered in template lists and pickers
	// (saving it as a reusable template clears it).
	Hidden bool `json:"hidden,omitempty"`
	// Translations are the template in other languages (O6): a contact whose locale matches one
	// (exactly, or by language: hi-IN takes hi) gets it; everyone else the default.
	Translations []Translation `json:"translations,omitempty"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// Translation is a template's text in one locale; empty fields keep the default's. Content holds
// only the content fields it changes (text, title, body…).
type Translation struct {
	Locale  string          `json:"locale"` // e.g. hi, hi-IN, ta
	Subject string          `json:"subject,omitempty"`
	HTML    string          `json:"html,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type Journey struct {
	ID       int64           `json:"id"`
	TenantID int64           `json:"tenant_id"`
	Name     string          `json:"name"`
	Graph    json.RawMessage `json:"graph"`
	Status   string          `json:"status"`
	StartAt  time.Time       `json:"start_at"`         // contacts enter from here...
	EndAt    *time.Time      `json:"end_at,omitempty"` // ...until here; nil = until stopped
	// SplitWinners are automatic A/B winners of split steps, by step id (journey.SplitDecision),
	// recorded by the journey workers. Read-only through the API.
	SplitWinners json.RawMessage `json:"split_winners,omitempty"`
	// ArchivedAt hides a journey that has run from the list; its enrollments and reports are kept.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// Tags are free labels for the Campaigns hub.
	Tags []string `json:"tags"`
	// Problem says why a live journey can't run (its saved steps no longer pass the checks, e.g.
	// after a rule changed); the journey workers skip it until it's fixed. Set by the API, not stored.
	Problem string `json:"problem,omitempty"`
}

type Enrollment struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	JourneyID int64  `json:"journey_id"`
	ContactID uint32 `json:"contact_id"`
	// Jumps counts the jump steps this enrollment has taken (capped); step keys carry it.
	Jumps int `json:"jumps,omitempty"`
}

// Timer is a due wait: resume the enrollment at Node.
type Timer struct {
	Enrollment Enrollment
	Node       string // resume here
	WaitNode   string // the step parked on (empty for timers created before this was recorded)
	DueAt      time.Time
}

// Message is a send request on messages.<channel>. ID is the idempotency key "{enrollment}:{node}"
// (a fallback send gets "{id}:sms").
type Message struct {
	ID         string `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	ContactID  uint32 `json:"contact_id"`
	Channel    string `json:"channel"`
	TemplateID int64  `json:"template_id"`
	JourneyID  int64  `json:"journey_id,omitempty"`
	// FallbackTemplateID is an SMS template sent if a WhatsApp or RCS message can't reach the contact.
	FallbackTemplateID int64 `json:"fallback_template_id,omitempty"`
	IsFallback         bool  `json:"is_fallback,omitempty"`
	// ProviderID sends through this provider of the channel instead of the default (chosen on the
	// campaign, step or test send); if it was removed since, the default is used.
	ProviderID int64 `json:"provider_id,omitempty"`
	// SentVia is the provider the message actually went through (set by the dispatcher at send
	// time; the channel's default resolved), recorded on its outcome events for reports.
	SentVia int64 `json:"sent_via,omitempty"`
	// Topic is the subscription topic the message belongs to (Settings → Bounces & unsubscribes):
	// contacts who left it are skipped, and its unsubscribe link leaves that topic only.
	Topic string `json:"topic,omitempty"`
	// Broadcast messages: which broadcast, run (unix seconds of its scheduled time) and A/B variant,
	// carried into every outcome and engagement event for reports.
	BroadcastID int64  `json:"broadcast_id,omitempty"`
	Run         int64  `json:"run,omitempty"`
	Variant     string `json:"variant,omitempty"`
	// To, when set, sends an internal notification (journey "send email to user"): the template is
	// rendered with the contact's data and sent to these addresses instead of the contact. It isn't
	// consent-gated, capped or tracked, and records notification_sent / notification_failed.
	To []string `json:"to,omitempty"`
	// LocalAt (2006-01-02T15:04) holds the message until that clock time in the contact's time zone
	// (their timezone or geo_timezone attribute, else LocalZone: the zone the broadcast was set in).
	LocalAt   string `json:"local_at,omitempty"`
	LocalZone string `json:"local_zone,omitempty"`
	// NotBefore holds the message until then (a campaign's sending speed spreads its sends).
	NotBefore *time.Time `json:"not_before,omitempty"`
	// Holdout: the contact is in the campaign's control group: recorded as kept back, not sent.
	Holdout bool `json:"holdout,omitempty"`
	// BestTime holds the message until its contact's best hour after the run's start (Run).
	BestTime bool `json:"best_time,omitempty"`
	// Personalisation context: Event holds the properties of the event that started the journey
	// (personal properties removed, at most 16 KB) for {{event.x}}; Campaign is the journey's or
	// broadcast's name and Step the journey step, for {{campaign.name}} and {{campaign.step}}.
	Event    json.RawMessage `json:"event,omitempty"`
	Campaign string          `json:"campaign,omitempty"`
	Step     string          `json:"step,omitempty"`
	// Deadline: a message that can't go out by then is dropped as <channel>_failed with reason
	// "deadline" (instead of a late burst). DeadlineLocal is the same for a local-time broadcast: a
	// clock time (LocalClockLayout) in the contact's zone, like LocalAt.
	Deadline      *time.Time `json:"deadline,omitempty"`
	DeadlineLocal string     `json:"deadline_local,omitempty"`
	// TestTo sends a test to a bare address (a saved test contact without a contact): ContactID is 0,
	// TestName fills {{contact.first_name}}. Consent, caps and tracking don't apply; no events are recorded.
	TestTo   string `json:"test_to,omitempty"`
	TestName string `json:"test_name,omitempty"`
}

// Provider is a configured delivery provider of a workspace. Secrets are write-only.
type Provider struct {
	ID         int64             `json:"id"`
	TenantID   int64             `json:"tenant_id"`
	Channel    string            `json:"channel"`
	Kind       string            `json:"provider"`
	Name       string            `json:"name"`
	Config     map[string]string `json:"config"`
	Secrets    map[string]string `json:"secrets,omitempty"`
	HasSecrets bool              `json:"has_secrets"`
	IsDefault  bool              `json:"is_default"`
	WebhookURL string            `json:"webhook_url,omitempty"` // where the provider posts delivery receipts
	Limits     ProviderLimits    `json:"limits"`
	// UsageToday is how many messages went through it today (in Limits.Timezone), from Redis.
	UsageToday int64 `json:"usage_today"`
}

// ProviderLimits caps what a provider sends, shared by every dispatcher: messages per second (0:
// only the workspace's channel rate applies) and a daily quota (0: none), whose day runs in Timezone
// (UTC when empty). Messages over the quota wait for the next day, or their deadline.
type ProviderLimits struct {
	RatePerSecond float64 `json:"rate_per_second,omitempty"`
	DailyQuota    int64   `json:"daily_quota,omitempty"`
	Timezone      string  `json:"timezone,omitempty"`
}

// InboxMessage is a message in a contact's app inbox.
type InboxMessage struct {
	ID        int64             `json:"id"`
	TenantID  int64             `json:"-"`
	ContactID uint32            `json:"-"`
	MessageID string            `json:"message_id"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	ImageURL  string            `json:"image_url,omitempty"`
	URL       string            `json:"url,omitempty"`
	Category  string            `json:"category,omitempty"`
	Data      map[string]string `json:"data,omitempty"`
	Meta      map[string]any    `json:"-"`
	CreatedAt time.Time         `json:"created_at"`
	ReadAt    *time.Time        `json:"read_at,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// Device is a push token of a contact.
type Device struct {
	Token    string `json:"token"`
	Platform string `json:"platform"` // android | ios | web
}

// SandboxMessage is a message recorded by the sandbox provider.
type SandboxMessage struct {
	ID        int64           `json:"id"`
	Channel   string          `json:"channel"`
	Recipient string          `json:"recipient"`
	MessageID string          `json:"message_id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// MembershipChange is a record on segment.membership.
type MembershipChange struct {
	TenantID  int64     `json:"tenant_id"`
	SegmentID int64     `json:"segment_id"`
	ContactID uint32    `json:"contact_id"`
	Entered   bool      `json:"entered"`
	Source    string    `json:"source"` // stream | backfill | sweep
	At        time.Time `json:"at"`
}

// FocusItem is an on-site message (modal, top bar or slide-in) shown by the web SDK.
type FocusItem struct {
	ID        int64          `json:"id"`
	TenantID  int64          `json:"tenant_id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"` // modal | bar | slide
	Content   FocusContent   `json:"content"`
	Targeting FocusTargeting `json:"targeting"`
	Status    string         `json:"status"` // draft | active
	StartAt   time.Time      `json:"start_at"`
	EndAt     *time.Time     `json:"end_at,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type FocusContent struct {
	Title       string `json:"title"`
	BodyHTML    string `json:"body_html"`
	ImageURL    string `json:"image_url,omitempty"`
	CTAText     string `json:"cta_text,omitempty"`
	CTAURL      string `json:"cta_url,omitempty"`
	Background  string `json:"background,omitempty"`
	TextColor   string `json:"text_color,omitempty"`
	ButtonColor string `json:"button_color,omitempty"`
	Position    string `json:"position,omitempty"` // bar: top|bottom; slide: bottom-right|bottom-left
}

// FocusTargeting decides who sees an item, where and when.
type FocusTargeting struct {
	SegmentIDs        []int64  `json:"segment_ids,omitempty"`         // show to members of any (empty: everyone)
	ExcludeSegmentIDs []int64  `json:"exclude_segment_ids,omitempty"` // never to members of any
	URLContains       []string `json:"url_contains,omitempty"`        // page URL contains any (empty: every page)
	Trigger           string   `json:"trigger"`                       // load | exit_intent | scroll | delay
	TriggerValue      int      `json:"trigger_value,omitempty"`       // scroll %, delay seconds
	Frequency         string   `json:"frequency"`                     // once | session | daily | always
	// M6: more audience rules (see OnsiteAudience) and the order among items of the same kind:
	// the web SDK shows one pop-up, one bar and one slide-in at a time, the first allowed in order.
	URLNotContains []string          `json:"url_not_contains,omitempty"`
	Devices        []string          `json:"devices,omitempty"`
	Conditions     []OnsiteCondition `json:"conditions,omitempty"`
	Priority       int               `json:"priority,omitempty"` // lower first; ties: newest first
}

// Audience returns the item's audience rules.
func (t FocusTargeting) Audience() OnsiteAudience {
	return OnsiteAudience{SegmentIDs: t.SegmentIDs, ExcludeSegmentIDs: t.ExcludeSegmentIDs, URLContains: t.URLContains,
		URLNotContains: t.URLNotContains, Devices: t.Devices, Conditions: t.Conditions}
}

// OnsiteAudience says who sees a content variant or focus item: members of any SegmentIDs (none:
// everyone) and of no ExcludeSegmentIDs, on pages whose URL contains any URLContains (none: every
// page) and none of URLNotContains, on one of Devices (none: any), whose contact fields pass every
// condition (only for verified identities: conditions read the contact's data).
type OnsiteAudience struct {
	SegmentIDs        []int64           `json:"segment_ids,omitempty"`
	ExcludeSegmentIDs []int64           `json:"exclude_segment_ids,omitempty"`
	URLContains       []string          `json:"url_contains,omitempty"`
	URLNotContains    []string          `json:"url_not_contains,omitempty"`
	Devices           []string          `json:"devices,omitempty"` // desktop | mobile | tablet
	Conditions        []OnsiteCondition `json:"conditions,omitempty"`
}

// OnsiteCondition compares one contact field (a path, as segment property filters), with the
// operators of segment rules.
type OnsiteCondition = segment.PropFilter

// DWCSlot is a Dynamic Web Content slot: a placeholder on a page filled per visitor.
type DWCSlot struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"tenant_id"`
	Slot     string `json:"slot"` // the page's data-pulse-slot value, or the app's slot id
	Name     string `json:"name"`
	// Platform is where the slot lives: "web" (a page element, HTML) or "app" (a place in a mobile
	// app, filled by the SDKs with a card or HTML). "" is web.
	Platform    string `json:"platform"`
	DefaultHTML string `json:"default_html"`
	// DefaultCard is an app slot's default content as a card (nil: DefaultHTML, or nothing).
	DefaultCard *AppCard     `json:"default_card,omitempty"`
	Variants    []DWCVariant `json:"variants"` // first variant whose segment matches wins
	Status      string       `json:"status"`
	StartAt     time.Time    `json:"start_at"`
	EndAt       *time.Time   `json:"end_at,omitempty"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// DWCVariant is one version of a slot's content. Variants are tried in order (their priority);
// the first whose audience matches is shown, else the slot's default.
type DWCVariant struct {
	ID   string `json:"id,omitempty"` // stable within the slot (assigned on save)
	Name string `json:"name,omitempty"`
	// SegmentID is the variant's audience before M6 (one segment); folded into Audience on save.
	SegmentID int64          `json:"segment_id,omitempty"`
	Audience  OnsiteAudience `json:"audience"`
	HTML      string         `json:"html"`
	Card      *AppCard       `json:"card,omitempty"` // app slots: a card instead of HTML
}

// AppCard is app slot content the app draws natively: any part may be empty, and Data carries
// custom key-values for apps that draw the slot themselves.
type AppCard struct {
	Title      string            `json:"title,omitempty"`
	Body       string            `json:"body,omitempty"`
	ImageURL   string            `json:"image_url,omitempty"`
	ButtonText string            `json:"button_text,omitempty"`
	DeepLink   string            `json:"deep_link,omitempty"` // https link or app scheme://
	Data       map[string]string `json:"data,omitempty"`
}

// Rules returns the variant's audience, with the pre-M6 segment folded in.
func (v DWCVariant) Rules() OnsiteAudience {
	a := v.Audience
	if v.SegmentID > 0 && !slices.Contains(a.SegmentIDs, v.SegmentID) {
		a.SegmentIDs = append(slices.Clone(a.SegmentIDs), v.SegmentID)
	}
	return a
}

// Broadcast sends one template to everyone in a segment, once or on a repeat.
type Broadcast struct {
	ID                 int64  `json:"id"`
	TenantID           int64  `json:"tenant_id"`
	Name               string `json:"name"`
	Channel            string `json:"channel"`
	TemplateID         int64  `json:"template_id"`
	FallbackTemplateID int64  `json:"fallback_template_id,omitempty"`
	// ProviderID: the channel's provider to send through; 0 is the channel's default.
	ProviderID int64 `json:"provider_id,omitempty"`
	// Topic: the subscription topic (its unsubscribers are skipped); "" for none.
	Topic string `json:"topic,omitempty"`
	// SendPerHour paces a run (O6, sending speed): at most this many messages an hour, spread
	// evenly from the run's start (0: as fast as the channel and provider allow).
	SendPerHour int `json:"send_per_hour,omitempty"`
	// HoldoutPercent keeps this share of the audience (0-50 %) from getting the campaign, the same
	// people every run, so its effect can be measured against them (uplift).
	HoldoutPercent int `json:"holdout_percent,omitempty"`
	// BestTime sends each message at its contact's best hour (package besttime), within a day of
	// the run's start (one-time campaigns; not with local time or a sending speed).
	BestTime bool `json:"best_time,omitempty"`
	// ArchivePublic lists the campaign's sends on the workspace's public archive page and RSS.
	ArchivePublic bool `json:"archive_public,omitempty"`
	// FeedURL makes it an RSS-to-email campaign: each run sends the feed's items published since
	// the last run as {{block.feed}}, and skips the run when nothing is new (repeating campaigns).
	FeedURL   string     `json:"feed_url,omitempty"`
	SegmentID int64      `json:"segment_id"`
	StartAt   time.Time  `json:"start_at"`
	EndAt     *time.Time `json:"end_at,omitempty"`
	Repeat    string     `json:"repeat"` // none | daily | weekly | monthly
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	// draft | scheduled | running | testing (an A/B test waits for its winner) | completed | cancelled
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`

	AB              *ABTest `json:"ab,omitempty"`
	ConversionEvent string  `json:"conversion_event,omitempty"` // reports count recipients who do this...
	ConversionDays  int     `json:"conversion_days,omitempty"`  // ...within this many days of the send

	// ExcludeSegmentIDs: members of any of these segments get nothing, even if in SegmentID.
	ExcludeSegmentIDs []int64 `json:"exclude_segment_ids,omitempty"`
	// LocalTime sends at StartAt's clock time in each contact's own time zone (one-time broadcasts).
	// Timezone is the zone StartAt was chosen in: it gives the clock time, and is the zone used for
	// contacts without one.
	LocalTime bool   `json:"local_time,omitempty"`
	Timezone  string `json:"timezone,omitempty"`

	// IncludeSegmentIDs: members of these segments get it too (the audience is the union).
	IncludeSegmentIDs []int64 `json:"include_segment_ids,omitempty"`
	// Deadline: don't send after a time of day or after hours late (see Deadline).
	Deadline *Deadline `json:"deadline,omitempty"`
	Tags     []string  `json:"tags"`
}

// Survey is a set of questions answered on a hosted page (package survey).
type Survey struct {
	ID        int64            `json:"id"`
	TenantID  int64            `json:"-"`
	Name      string           `json:"name"`
	Status    string           `json:"status"` // draft | live | closed
	Questions []SurveyQuestion `json:"questions"`
	Intro     string           `json:"intro,omitempty"`
	ThankYou  string           `json:"thank_you,omitempty"`
	// PublicKey makes a link anyone can answer without being a contact (/s/{id}/p/{key}).
	PublicKey string    `json:"public_key,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SurveyResponse is one answer to a survey (ExternalID "" for a public one).
type SurveyResponse struct {
	SurveyID   int64          `json:"survey_id"`
	TenantID   int64          `json:"-"`
	ExternalID string         `json:"external_id,omitempty"`
	MessageID  string         `json:"message_id,omitempty"`
	Answers    map[string]any `json:"answers"`
	NPS        *int           `json:"nps,omitempty"`
	At         time.Time      `json:"at"`
}

// SurveyQuestion is one question: nps (0-10), rating (1-5), choice (Options) or text.
type SurveyQuestion struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Text     string   `json:"text"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// Product is one item of a workspace's catalog (package catalog).
type Product struct {
	Code      string         `json:"code"`
	Name      string         `json:"name"`
	URL       string         `json:"url,omitempty"`
	ImageURL  string         `json:"image_url,omitempty"`
	Price     *float64       `json:"price,omitempty"`
	SalePrice *float64       `json:"sale_price,omitempty"`
	Currency  string         `json:"currency,omitempty"`
	Category  string         `json:"category,omitempty"`
	InStock   bool           `json:"in_stock"`
	Attrs     map[string]any `json:"attrs,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// ArchiveEntry is one send in a workspace's public campaign archive.
type ArchiveEntry struct {
	RunID       int64     `json:"run_id"`
	BroadcastID int64     `json:"broadcast_id"`
	Name        string    `json:"name"`
	TemplateID  int64     `json:"template_id"`
	ScheduledAt time.Time `json:"scheduled_at"`
	SentAt      time.Time `json:"sent_at"`
	FeedHTML    string    `json:"-"`
	FeedText    string    `json:"-"`
}

// ABTest sends 2-5 variants to a test group, then the best one to everyone else.
type ABTest struct {
	Variants []ABVariant `json:"variants"`
	// TestPercent of the audience is split evenly between the variants; 100 means a plain split
	// with no winner step.
	TestPercent int    `json:"test_percent"`
	Metric      string `json:"metric"`     // opens | clicks (email), manual
	WaitHours   int    `json:"wait_hours"` // after the test send, for opens and clicks
}

type ABVariant struct {
	Label      string `json:"label"` // A, B, ...
	TemplateID int64  `json:"template_id"`
}

type BroadcastRun struct {
	ID          int64      `json:"id"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Recipients  int        `json:"recipients"`

	// A/B runs: the test group, then the winner's send to everyone else.
	TestRecipients   int        `json:"test_recipients"`
	WinnerDueAt      *time.Time `json:"winner_due_at,omitempty"`
	Winner           string     `json:"winner,omitempty"`
	WinnerReason     string     `json:"winner_reason,omitempty"`
	WinnerSentAt     *time.Time `json:"winner_sent_at,omitempty"`
	WinnerRecipients int        `json:"winner_recipients"`
	// Audience is the run's audience when it started (reach counts for its channel: audience,
	// after_exclude, after_consent, reachable), for the report's funnel. Empty for runs before M4.
	Audience json.RawMessage `json:"audience,omitempty"`
}

// MessageCounts are unique contacts per outcome of a set of messages (a broadcast run, a variant,
// a journey step). Opened and clicked are email only; Read is WhatsApp and RCS.
type MessageCounts struct {
	Sent         uint64 `json:"sent"`
	Delivered    uint64 `json:"delivered"`
	Read         uint64 `json:"read"`
	Failed       uint64 `json:"failed"`
	Fallback     uint64 `json:"fallback"`
	Suppressed   uint64 `json:"suppressed"`
	Capped       uint64 `json:"capped"`
	Deferred     uint64 `json:"deferred"`
	Opened       uint64 `json:"opened"`
	Clicked      uint64 `json:"clicked"`
	Unsubscribed uint64 `json:"unsubscribed"`
	Converted    uint64 `json:"converted"`
	// M4: email bounces and spam complaints linked to the message, and clicks on links to files
	// (Pulse assets and documents: counted in Clicked too).
	Bounced    uint64 `json:"bounced"`
	Complained uint64 `json:"complained"`
	Downloaded uint64 `json:"downloaded"`
}

// Form collects contact details on a hosted page or a customer's website (web SDK).
type Form struct {
	ID        int64        `json:"id"`
	TenantID  int64        `json:"tenant_id"`
	Name      string       `json:"name"`
	PublicKey string       `json:"public_key"` // in the hosted URL /f/{public_key} and the embed snippet
	Fields    []FormField  `json:"fields"`
	Settings  FormSettings `json:"settings"`
	Status    string       `json:"status"` // draft | active
	UpdatedAt time.Time    `json:"updated_at"`
}

type FormField struct {
	Key   string `json:"key"` // the profile attribute it sets ("email" sets the contact's email)
	Label string `json:"label"`
	// text | email | tel | number | textarea | select | checkbox | hidden | file | page_break
	// (page_break starts a new page; its label titles that page; it has no value)
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`     // select
	Default  string   `json:"default,omitempty"`     // hidden fields: the value sent
	Progress bool     `json:"progressive,omitempty"` // skip when the visitor's profile already has a value
	Accept   []string `json:"accept,omitempty"`      // file: allowed extensions (.pdf, .png...); empty: every upload type
	MaxMB    int      `json:"max_mb,omitempty"`      // file: size limit in MB (1-10; 0 means 5)
}

type FormSettings struct {
	SubmitText     string   `json:"submit_text,omitempty"`
	SuccessMessage string   `json:"success_message,omitempty"`
	RedirectURL    string   `json:"redirect_url,omitempty"`
	ListIDs        []int64  `json:"list_ids,omitempty"`     // join these lists
	Tags           []string `json:"tags,omitempty"`         // add these tags
	ConsentText    string   `json:"consent_text,omitempty"` // shown as a required checkbox when set
	Resubscribe    []string `json:"resubscribe,omitempty"`  // channels the submitter opts in to (lifts an opt-out)
	Description    string   `json:"description,omitempty"`  // shown above the fields
	Theme          string   `json:"theme,omitempty"`        // accent color, e.g. #2747d6
	// Double opt-in (O6): the submitter gets ConfirmTemplateID (an email with {{event.confirm_url}})
	// and joins lists, gets tags and is opted in only when they click it. ConfirmedMessage or
	// ConfirmedRedirect is what they see then.
	DoubleOptIn       bool   `json:"double_optin,omitempty"`
	ConfirmTemplateID int64  `json:"confirm_template_id,omitempty"`
	ConfirmedMessage  string `json:"confirmed_message,omitempty"`
	ConfirmedRedirect string `json:"confirmed_redirect,omitempty"`
}

// Funnel is an ordered list of steps a contact completes within WindowDays.
type Funnel struct {
	ID         int64        `json:"id"`
	TenantID   int64        `json:"tenant_id"`
	Name       string       `json:"name"`
	Steps      []FunnelStep `json:"steps"`
	WindowDays int          `json:"window_days"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

type FunnelStep struct {
	Event string          `json:"event"`
	Where json.RawMessage `json:"where,omitempty"` // []segment.PropFilter
	Label string          `json:"label,omitempty"`
}

// SavedReport is a saved analytics report: kind "cohort" (Spec is an analytics.CohortSpec) or
// "custom" (an analytics.ReportSpec). Results are computed on demand.
type SavedReport struct {
	ID        int64           `json:"id"`
	TenantID  int64           `json:"tenant_id"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Spec      json.RawMessage `json:"spec"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// AuditEntry is one change or sign-in in a workspace's audit log.
type AuditEntry struct {
	ID         int64           `json:"id"`
	At         time.Time       `json:"at"`
	UserID     int64           `json:"user_id,omitempty"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"` // "PUT /v1/broadcasts/{id}", "sign-in", ...
	ObjectType string          `json:"object_type"`
	ObjectID   string          `json:"object_id"`
	Status     int             `json:"status"`
	IP         string          `json:"ip"`
	Detail     json.RawMessage `json:"detail,omitempty"` // the request, secrets removed
}

type AuditFilter struct {
	Before     int64 // entries older than this id (paging)
	UserID     int64
	ObjectType string
	From, To   time.Time // optional: entries at or after From and before To
	Limit      int
}

// Webhook is an outbound webhook: events posted to the customer's URL.
type Webhook struct {
	ID        int64     `json:"id"`
	TenantID  int64     `json:"tenant_id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"` // event names, or "*"
	Active    bool      `json:"active"`
	Secret    string    `json:"secret,omitempty"` // signs every delivery; the API shows it masked except when created or rotated
	HasSecret bool      `json:"has_secret"`
	Sealed    []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// REST hook subscriptions (Zapier, Make) belong to an integration and may narrow segment
	// events to one segment and form_submitted to one form.
	IntegrationID int64 `json:"integration_id,omitempty"`
	SegmentID     int64 `json:"segment_id,omitempty"`
	FormID        int64 `json:"form_id,omitempty"`
}

type WebhookDelivery struct {
	ID         int64     `json:"id"`
	EventID    string    `json:"event_id"`
	EventName  string    `json:"event_name"`
	At         time.Time `json:"at"`
	Attempt    int       `json:"attempt"`
	Status     int       `json:"status"`
	Error      string    `json:"error,omitempty"`
	DurationMS int       `json:"duration_ms"`
	Final      bool      `json:"final"`
	Test       bool      `json:"test,omitempty"` // a "Send test" from the webhook's page
}

// Import is a background CSV import.
type Import struct {
	ID         int64           `json:"id"`
	Filename   string          `json:"filename"`
	ListID     int64           `json:"list_id,omitempty"`
	Status     string          `json:"status"` // uploading | queued | running | done | failed | cancelled
	Bytes      int64           `json:"bytes"`
	RowsDone   int64           `json:"rows_done"`
	RowsFailed int64           `json:"rows_failed"`
	Errors     json.RawMessage `json:"errors"`
	Message    string          `json:"message,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// InAppMessage is an in-app campaign: a modal, banner or full-screen message the app SDK shows when
// a trigger event or screen happens, to contacts in its segments.
type InAppMessage struct {
	ID        int64          `json:"id"`
	TenantID  int64          `json:"tenant_id"`
	Name      string         `json:"name"`
	Layout    string         `json:"layout"` // modal | banner | fullscreen
	Content   InAppContent   `json:"content"`
	Targeting InAppTargeting `json:"targeting"`
	Priority  int            `json:"priority"` // higher shows first when several match
	Status    string         `json:"status"`   // draft | active
	StartAt   time.Time      `json:"start_at"`
	EndAt     *time.Time     `json:"end_at,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type InAppContent struct {
	Title       string        `json:"title"`
	Body        string        `json:"body"`
	ImageURL    string        `json:"image_url,omitempty"`
	Buttons     []InAppButton `json:"buttons,omitempty"` // up to 2
	Background  string        `json:"background,omitempty"`
	TextColor   string        `json:"text_color,omitempty"`
	ButtonColor string        `json:"button_color,omitempty"`
	Position    string        `json:"position,omitempty"` // banner: top | bottom
}

type InAppButton struct {
	Text   string `json:"text"`
	Action string `json:"action"`          // url (link or deep link) | dismiss
	Value  string `json:"value,omitempty"` // url: where to go
}

type InAppTargeting struct {
	SegmentIDs        []int64  `json:"segment_ids,omitempty"`         // members of any (empty: everyone)
	ExcludeSegmentIDs []int64  `json:"exclude_segment_ids,omitempty"` // never members of any
	Platforms         []string `json:"platforms,omitempty"`           // android | ios (empty: both); websites use focus items
	TriggerEvent      string   `json:"trigger_event"`                 // e.g. app_open, screen_view, add_to_cart
	Screen            string   `json:"screen,omitempty"`              // screen_view only: this screen name
	DelaySeconds      int      `json:"delay_seconds,omitempty"`       // wait after the trigger
	Frequency         string   `json:"frequency"`                     // once | session | daily | always
}

// DataSettings are a workspace's tracking and retention choices.
type DataSettings struct {
	Tracking  TrackingSettings  `json:"tracking"`
	Retention RetentionSettings `json:"retention"`
}

type TrackingSettings struct {
	Anonymous bool `json:"anonymous"` // accept events from anonymous visitors (anon- ids)
	GeoIP     bool `json:"geoip"`     // locate web and app visitors by IP (needs the server's MaxMind database)
}

// RetentionSettings: 0 keeps forever.
type RetentionSettings struct {
	AnonymousDays int `json:"anonymous_days"` // delete anonymous visitors idle this long, with their events
	EventDays     int `json:"event_days"`     // delete events older than this
	AuditDays     int `json:"audit_days"`     // delete audit log entries older than this
}

// DefaultDataSettings: track anonymous visitors and locations, keep data forever, audit log a year.
func DefaultDataSettings() DataSettings {
	return DataSettings{Tracking: TrackingSettings{Anonymous: true, GeoIP: true}, Retention: RetentionSettings{AuditDays: 365}}
}

// Asset is an uploaded file: a download at /a/{public_key} (gated by a form when FormID is set)
// and an email attachment. The file itself is fetched separately (AssetData).
type Asset struct {
	ID            int64     `json:"id"`
	TenantID      int64     `json:"tenant_id"`
	PublicKey     string    `json:"public_key"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Filename      string    `json:"filename"`
	ContentType   string    `json:"content_type"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	FormID        *int64    `json:"form_id,omitempty"`
	DownloadCount int64     `json:"download_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// LandingPage is a workspace page served at /p/{workspace slug}/{slug} while published and live.
type LandingPage struct {
	ID              int64           `json:"id"`
	TenantID        int64           `json:"tenant_id"`
	Slug            string          `json:"slug"`
	Title           string          `json:"title"`
	MetaDescription string          `json:"meta_description"`
	HTML            string          `json:"html"`
	Builder         json.RawMessage `json:"builder,omitempty"`
	Status          string          `json:"status"` // draft | published
	StartAt         time.Time       `json:"start_at"`
	EndAt           *time.Time      `json:"end_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// ContentBlock is a reusable piece of content (O6): {{block.<Key>}} in any template. HTML goes into
// email bodies; Text into subjects and every other channel.
type ContentBlock struct {
	ID        int64     `json:"id"`
	TenantID  int64     `json:"-"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	HTML      string    `json:"html"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Approval is a request to launch a campaign (O6 approvals).
type Approval struct {
	ID           int64      `json:"id"`
	TenantID     int64      `json:"-"`
	Kind         string     `json:"kind"` // broadcast | journey
	CampaignID   int64      `json:"campaign_id"`
	Version      string     `json:"-"`
	RequestedBy  int64      `json:"requested_by"`
	Requester    string     `json:"requester"`
	Note         string     `json:"note"`
	Status       string     `json:"status"` // pending | approved | rejected | withdrawn
	DecidedBy    int64      `json:"decided_by,omitempty"`
	Decider      string     `json:"decider,omitempty"`
	DecisionNote string     `json:"decision_note,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	// CampaignName is filled for lists.
	CampaignName string `json:"campaign_name,omitempty"`
}
