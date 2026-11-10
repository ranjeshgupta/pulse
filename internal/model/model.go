// Package model holds the row types shared by the stores, the API and the workers.
package model

import (
	"encoding/json"
	"time"

	"pulse/internal/auth"
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
	SSO          bool       `json:"sso"` // signs in through SSO
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

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
	Transactional bool      `json:"transactional"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Journey struct {
	ID       int64           `json:"id"`
	TenantID int64           `json:"tenant_id"`
	Name     string          `json:"name"`
	Graph    json.RawMessage `json:"graph"`
	Status   string          `json:"status"`
	StartAt  time.Time       `json:"start_at"`         // contacts enter from here...
	EndAt    *time.Time      `json:"end_at,omitempty"` // ...until here; nil = until stopped
}

type Enrollment struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	JourneyID int64  `json:"journey_id"`
	ContactID uint32 `json:"contact_id"`
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
	// Broadcast messages: which broadcast, run (unix seconds of its scheduled time) and A/B variant,
	// carried into every outcome and engagement event for reports.
	BroadcastID int64  `json:"broadcast_id,omitempty"`
	Run         int64  `json:"run,omitempty"`
	Variant     string `json:"variant,omitempty"`
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
}

// DWCSlot is a Dynamic Web Content slot: a placeholder on a page filled per visitor.
type DWCSlot struct {
	ID          int64        `json:"id"`
	TenantID    int64        `json:"tenant_id"`
	Slot        string       `json:"slot"` // the page's data-pulse-slot value
	Name        string       `json:"name"`
	DefaultHTML string       `json:"default_html"`
	Variants    []DWCVariant `json:"variants"` // first variant whose segment matches wins
	Status      string       `json:"status"`
	StartAt     time.Time    `json:"start_at"`
	EndAt       *time.Time   `json:"end_at,omitempty"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type DWCVariant struct {
	SegmentID int64  `json:"segment_id"`
	HTML      string `json:"html"`
}

// Broadcast sends one template to everyone in a segment, once or on a repeat.
type Broadcast struct {
	ID                 int64      `json:"id"`
	TenantID           int64      `json:"tenant_id"`
	Name               string     `json:"name"`
	Channel            string     `json:"channel"`
	TemplateID         int64      `json:"template_id"`
	FallbackTemplateID int64      `json:"fallback_template_id,omitempty"`
	SegmentID          int64      `json:"segment_id"`
	StartAt            time.Time  `json:"start_at"`
	EndAt              *time.Time `json:"end_at,omitempty"`
	Repeat             string     `json:"repeat"` // none | daily | weekly | monthly
	NextRunAt          *time.Time `json:"next_run_at,omitempty"`
	// draft | scheduled | running | testing (an A/B test waits for its winner) | completed | cancelled
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`

	AB              *ABTest `json:"ab,omitempty"`
	ConversionEvent string  `json:"conversion_event,omitempty"` // reports count recipients who do this...
	ConversionDays  int     `json:"conversion_days,omitempty"`  // ...within this many days of the send
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
	Key      string   `json:"key"` // the profile attribute it sets ("email" sets the contact's email)
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | email | tel | number | textarea | select | checkbox | hidden
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`     // select
	Default  string   `json:"default,omitempty"`     // hidden fields: the value sent
	Progress bool     `json:"progressive,omitempty"` // skip when the visitor's profile already has a value
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
	Secret    string    `json:"secret,omitempty"` // shown to admins; signs every delivery
	Sealed    []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
