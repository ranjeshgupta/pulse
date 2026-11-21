// Package automation holds a workspace's settings for Pulse's automatic behaviour (Settings →
// Automation & limits): how long each kind of log is kept, how failed sends are retried, when
// segments are swept and reachability is rebuilt, how long app inbox messages last, and the
// default sending limits of new providers. Every default equals what Pulse did before the
// settings existed, so a workspace that never opens the page behaves as before.
//
// The settings are stored as JSON in tenant_settings.automation; a field missing from the stored
// JSON takes its default (Load), so adding a setting never needs a data migration.
package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"pulse/internal/identity"
	"slices"
	"sort"
	"strings"
	"time"

	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/model"
)

// Settings are one workspace's automation settings.
type Settings struct {
	Retention     Retention                       `json:"retention"`
	Retries       Retries                         `json:"retries"`
	Schedules     Schedules                       `json:"schedules"`
	Messages      Messages                        `json:"messages"`
	DefaultLimits map[string]model.ProviderLimits `json:"default_limits"` // by channel
	// Bounces: what bounces and complaints do, per channel (bounces.go); DefaultBounces when unset.
	Bounces map[string]ChannelBounces `json:"bounces"`
	// Unsubscribe: the link's mode, the email policy and the topics (unsubscribe.go).
	Unsubscribe Unsubscribe `json:"unsubscribe"`
	// Identity: the keys that identify a person, in priority order (package identity).
	Identity Identity `json:"identity"`
}

// Identity is Settings → Contacts → Identity.
type Identity struct {
	Keys []identity.Key `json:"keys"`
}

// Retention says how long each kind of log is kept, in days (0 keeps forever).
type Retention struct {
	// EventDays: the event log (ClickHouse events), every name without a more specific setting.
	EventDays int `json:"event_days"`
	// MessageEventDays: message outcomes and engagement behind reports and dashboards
	// (ClickHouse message_events). nil: the same as EventDays.
	MessageEventDays *int `json:"message_event_days"`
	// WebViewDays: page and screen views (WebViewNames). nil: the same as EventDays.
	WebViewDays *int `json:"web_view_days"`
	// EventOverrides: days per event name; they win over EventDays and WebViewDays (0 = forever).
	EventOverrides map[string]int `json:"event_overrides"`
	// AnonymousDays: delete anonymous visitors (anon- ids) idle this long, with their events (0: off).
	AnonymousDays int `json:"anonymous_days"`
	AuditDays     int `json:"audit_days"`  // the audit log
	ImportDays    int `json:"import_days"` // the history of background imports (not the contacts)
	// WebhookDeliveryDays: the delivery log of outbound webhooks (always pruned).
	WebhookDeliveryDays int `json:"webhook_delivery_days"`
	// IntegrationLogDays: the activity log of integrations (always pruned).
	IntegrationLogDays int `json:"integration_log_days"`
	SandboxDays        int `json:"sandbox_days"` // the test inbox of the sandbox provider
	// LiveFeedSize: about how many recent events the live event feed keeps (a size, not days).
	LiveFeedSize int `json:"live_feed_size"`
}

// Retries are how temporarily failed sends are tried again (dispatch).
type Retries struct {
	Attempts          int `json:"attempts"`            // tries in all, the first included (1: no retries)
	MaxBackoffSeconds int `json:"max_backoff_seconds"` // the longest wait between tries, also the cap of a provider's Retry-After
}

// Schedules are when background work runs for the workspace.
type Schedules struct {
	// SweepMinutes: how often segments with time windows ("in the last 7 days") are re-checked
	// so members whose events grew too old leave. 0: the server's PULSE_SWEEP_INTERVAL.
	SweepMinutes int `json:"sweep_minutes"`
	// ReachRebuildHour: the UTC hour reachability counts are rebuilt from scratch each day; -1:
	// a day after the previous rebuild, whenever that was.
	ReachRebuildHour int `json:"reach_rebuild_hour"`
	// DailySegmentAt / DailySegmentTZ: the time and zone a new "refresh daily" segment starts
	// with in the app ("" zone: the browser's zone).
	DailySegmentAt string `json:"daily_segment_at"`
	DailySegmentTZ string `json:"daily_segment_tz"`
}

// Messages are defaults of messages themselves.
type Messages struct {
	// InboxExpiryDays: how long an app inbox message stays when its template doesn't say.
	InboxExpiryDays int `json:"inbox_expiry_days"`
}

// Today's behaviour, before these settings existed.
const (
	DefaultAttempts       = 8
	DefaultMaxBackoff     = time.Hour
	DefaultAuditDays      = 365
	DefaultWebhookDays    = 7
	DefaultIntegrationLog = 7
	DefaultLiveFeedSize   = 1000
	DefaultInboxExpiry    = 30
	DefaultDailyAt        = "06:00"
)

// WebViewNames are the page and screen view events the SDKs and landing pages record.
var WebViewNames = []string{"page_view", "screen_view", event.LandingPageViewed}

// LimitChannels are the channels whose providers take sending limits.
var LimitChannels = []string{channel.Email, channel.SMS, channel.WhatsApp, channel.RCS, channel.Push}

// Defaults are the settings of a workspace that never changed them: exactly what Pulse did before.
func Defaults() Settings {
	return Settings{
		Retention: Retention{AuditDays: DefaultAuditDays, WebhookDeliveryDays: DefaultWebhookDays,
			IntegrationLogDays: DefaultIntegrationLog, LiveFeedSize: DefaultLiveFeedSize},
		Retries:     Retries{Attempts: DefaultAttempts, MaxBackoffSeconds: int(DefaultMaxBackoff / time.Second)},
		Schedules:   Schedules{ReachRebuildHour: -1, DailySegmentAt: DefaultDailyAt},
		Messages:    Messages{InboxExpiryDays: DefaultInboxExpiry},
		Bounces:     DefaultBounces(),
		Unsubscribe: DefaultUnsubscribe(),
		Identity:    Identity{Keys: identity.Defaults()},
	}
}

// Load reads stored settings: fields missing from raw keep their defaults.
func Load(raw []byte) (Settings, error) {
	s := Defaults()
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Defaults(), err
	}
	return s, nil
}

// Sections are the top-level parts of Settings; a PUT replaces the sections it sends.
var Sections = []string{"retention", "retries", "schedules", "messages", "default_limits", "bounces", "unsubscribe", "identity"}

// Apply replaces the sections present in body (a JSON object) on top of cur; within a section,
// fields left out take their defaults. Unknown sections and fields are refused.
func Apply(cur Settings, body []byte) (Settings, error) {
	var parts map[string]json.RawMessage
	if err := json.Unmarshal(body, &parts); err != nil {
		return cur, errors.New("send a JSON object with any of: " + strings.Join(Sections, ", "))
	}
	def := Defaults()
	out := cur
	out.Retention.EventOverrides = maps.Clone(cur.Retention.EventOverrides)
	out.DefaultLimits = maps.Clone(cur.DefaultLimits)
	out.Bounces = maps.Clone(cur.Bounces)
	for k, v := range parts {
		var dst any
		switch k {
		case "retention":
			out.Retention = def.Retention
			dst = &out.Retention
		case "retries":
			out.Retries = def.Retries
			dst = &out.Retries
		case "schedules":
			out.Schedules = def.Schedules
			dst = &out.Schedules
		case "messages":
			out.Messages = def.Messages
			dst = &out.Messages
		case "default_limits":
			out.DefaultLimits = nil
			dst = &out.DefaultLimits
		case "unsubscribe":
			out.Unsubscribe = def.Unsubscribe
			dst = &out.Unsubscribe
		case "identity":
			out.Identity = def.Identity
			dst = &out.Identity
		case "bounces":
			// The channels sent replace theirs; the others keep theirs.
			var b map[string]ChannelBounces
			dec := json.NewDecoder(strings.NewReader(string(v)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&b); err != nil {
				return cur, fmt.Errorf("bounces: %v", err)
			}
			if out.Bounces == nil {
				out.Bounces = DefaultBounces()
			}
			for ch, r := range b {
				out.Bounces[ch] = r
			}
			continue
		default:
			return cur, fmt.Errorf("unknown section %q (sections: %s)", k, strings.Join(Sections, ", "))
		}
		dec := json.NewDecoder(strings.NewReader(string(v)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(dst); err != nil {
			return cur, fmt.Errorf("%s: %v", k, err)
		}
	}
	return out, nil
}

// Limits are the allowed range of every setting (min and max; days settings also take 0 for
// forever where noted). The app shows them next to each field.
type Limit struct {
	Min  int  `json:"min"`
	Max  int  `json:"max"`
	Zero bool `json:"zero"` // 0 is allowed too (forever / off / server default)
}

var Limits = map[string]Limit{
	"retention.event_days":            {30, 3650, true},
	"retention.message_event_days":    {30, 3650, true},
	"retention.web_view_days":         {7, 3650, true},
	"retention.event_overrides":       {7, 3650, true},
	"retention.anonymous_days":        {7, 3650, true},
	"retention.audit_days":            {30, 3650, true},
	"retention.import_days":           {7, 3650, true},
	"retention.webhook_delivery_days": {1, 90, false},
	"retention.integration_log_days":  {1, 90, false},
	"retention.sandbox_days":          {1, 3650, true},
	"retention.live_feed_size":        {100, 10000, false},
	"retries.attempts":                {1, 20, false},
	"retries.max_backoff_seconds":     {60, 86400, false},
	"schedules.sweep_minutes":         {1, 1440, true},
	"schedules.reach_rebuild_hour":    {-1, 23, false},
	"messages.inbox_expiry_days":      {1, 365, false},
	"default_limits.rate_per_second":  {0, 100000, false},
	"default_limits.daily_quota":      {0, 1000000000, false},
}

// MaxOverrides caps the per-event-name retention settings.
const MaxOverrides = 100

func check(key string, v int) error {
	l := Limits[key]
	if l.Zero && v == 0 {
		return nil
	}
	if v < l.Min || v > l.Max {
		field := key[strings.IndexByte(key, '.')+1:]
		if l.Zero {
			return fmt.Errorf("%s must be %d to %d, or 0", field, l.Min, l.Max)
		}
		return fmt.Errorf("%s must be %d to %d", field, l.Min, l.Max)
	}
	return nil
}

// Validate checks every value against Limits and normalises the settings (zones, names).
func (s *Settings) Validate() error {
	r := &s.Retention
	ints := []struct {
		key string
		v   int
	}{
		{"retention.event_days", r.EventDays}, {"retention.anonymous_days", r.AnonymousDays},
		{"retention.audit_days", r.AuditDays}, {"retention.import_days", r.ImportDays},
		{"retention.webhook_delivery_days", r.WebhookDeliveryDays}, {"retention.integration_log_days", r.IntegrationLogDays},
		{"retention.sandbox_days", r.SandboxDays}, {"retention.live_feed_size", r.LiveFeedSize},
		{"retries.attempts", s.Retries.Attempts}, {"retries.max_backoff_seconds", s.Retries.MaxBackoffSeconds},
		{"schedules.sweep_minutes", s.Schedules.SweepMinutes}, {"schedules.reach_rebuild_hour", s.Schedules.ReachRebuildHour},
		{"messages.inbox_expiry_days", s.Messages.InboxExpiryDays},
	}
	if r.MessageEventDays != nil {
		ints = append(ints, struct {
			key string
			v   int
		}{"retention.message_event_days", *r.MessageEventDays})
	}
	if r.WebViewDays != nil {
		ints = append(ints, struct {
			key string
			v   int
		}{"retention.web_view_days", *r.WebViewDays})
	}
	for _, x := range ints {
		if err := check(x.key, x.v); err != nil {
			return err
		}
	}
	if len(r.EventOverrides) > MaxOverrides {
		return fmt.Errorf("at most %d event-name retention settings", MaxOverrides)
	}
	for name, days := range r.EventOverrides {
		if !event.ValidName(name) {
			return fmt.Errorf("event_overrides: %q is not an event name", name)
		}
		if err := check("retention.event_overrides", days); err != nil {
			return fmt.Errorf("event_overrides[%s]: keep for 7 to 3650 days, or 0 for forever", name)
		}
	}
	if len(r.EventOverrides) == 0 {
		r.EventOverrides = nil
	}
	sc := &s.Schedules
	if _, err := time.Parse("15:04", sc.DailySegmentAt); err != nil || len(sc.DailySegmentAt) != 5 {
		return errors.New("daily_segment_at must be a time like 06:00")
	}
	if sc.DailySegmentTZ != "" {
		if _, err := time.LoadLocation(sc.DailySegmentTZ); err != nil || sc.DailySegmentTZ == "Local" {
			return fmt.Errorf("daily_segment_tz: unknown time zone %q", sc.DailySegmentTZ)
		}
	}
	for ch, l := range s.DefaultLimits {
		if !slices.Contains(LimitChannels, ch) {
			return fmt.Errorf("default_limits: %q is not a channel with providers (%s)", ch, strings.Join(LimitChannels, ", "))
		}
		if l.RatePerSecond < 0 || l.RatePerSecond > float64(Limits["default_limits.rate_per_second"].Max) {
			return fmt.Errorf("default_limits[%s]: rate_per_second must be 0 (no limit) to 100000", ch)
		}
		if l.DailyQuota < 0 || l.DailyQuota > int64(Limits["default_limits.daily_quota"].Max) {
			return fmt.Errorf("default_limits[%s]: daily_quota must be 0 (no quota) to 1000000000", ch)
		}
		if l.Timezone != "" {
			if _, err := time.LoadLocation(l.Timezone); err != nil || l.Timezone == "Local" {
				return fmt.Errorf("default_limits[%s]: unknown time zone %q", ch, l.Timezone)
			}
		}
		if l == (model.ProviderLimits{}) {
			delete(s.DefaultLimits, ch) // nothing set: no default
		}
	}
	if len(s.DefaultLimits) == 0 {
		s.DefaultLimits = nil
	}
	if len(s.Identity.Keys) == 0 {
		s.Identity.Keys = identity.Defaults()
	}
	if err := identity.Validate(s.Identity.Keys); err != nil {
		return err
	}
	if err := validateUnsubscribe(&s.Unsubscribe); err != nil {
		return err
	}
	return validateBounces(s.Bounces)
}

// MessageEvents is the effective message-events retention.
func (r Retention) MessageEvents() int {
	if r.MessageEventDays != nil {
		return *r.MessageEventDays
	}
	return r.EventDays
}

// WebViews is the effective page/screen-view retention.
func (r Retention) WebViews() int {
	if r.WebViewDays != nil {
		return *r.WebViewDays
	}
	return r.EventDays
}

// EventDaysFor is how long events of one name are kept (0: forever).
func (r Retention) EventDaysFor(name string) int {
	if d, ok := r.EventOverrides[name]; ok {
		return d
	}
	if slices.Contains(WebViewNames, name) {
		return r.WebViews()
	}
	return r.EventDays
}

// overrideNames lists the override names, sorted.
func (r Retention) overrideNames() []string {
	names := slices.Collect(maps.Keys(r.EventOverrides))
	sort.Strings(names)
	return names
}

// MaxBackoff is the longest wait between tries.
func (r Retries) MaxBackoff() time.Duration { return time.Duration(r.MaxBackoffSeconds) * time.Second }

// LimitsFor returns the default limits of new providers of a channel (zero when none).
func (s Settings) LimitsFor(ch string) model.ProviderLimits { return s.DefaultLimits[ch] }

// ProviderLimits picks the limits a new provider starts with: its own when it brings any, else
// its kind's preset (e.g. Gmail's daily quota), else the workspace default for the channel.
func ProviderLimits(explicit, preset *model.ProviderLimits, workspace model.ProviderLimits) model.ProviderLimits {
	if explicit != nil && *explicit != (model.ProviderLimits{}) {
		return *explicit
	}
	if preset != nil && *preset != (model.ProviderLimits{}) {
		return *preset
	}
	return workspace
}
