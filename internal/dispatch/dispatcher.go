// Package dispatch delivers messages from the channel topics (messages.email|sms|whatsapp|rcs|push).
//
// Per message: skip if already sent, resolve the recipient (email, phone, push tokens), render the
// template, rate limit per workspace and channel, send through the workspace's default provider,
// then report the outcome as an event (email_sent, whatsapp_fallback, ...) so journeys, segments and
// the dashboard see deliveries. WhatsApp and RCS messages that can't reach the contact fall back to
// an SMS template, exactly once, whether the failure is immediate or arrives later as a receipt.
// A send that fails temporarily is retried later for about two hours without holding up the
// partition (retry.go), then recorded as <channel>_failed.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/amp"
	"pulse/internal/assets"
	"pulse/internal/attr"
	"pulse/internal/auth"
	"pulse/internal/broadcast"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/delivery"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/personalize"
	"pulse/internal/providers"
	"pulse/internal/reach"
	"pulse/internal/reasons"
	"pulse/internal/replies"
	"pulse/internal/safehttp"
	"pulse/internal/store/pg"
	"pulse/internal/tracking"
)

const (
	sentTTL       = 7 * 24 * time.Hour
	receiptTTL    = 3 * 24 * time.Hour
	cacheTTL      = 30 * time.Second
	TopicReceipts = "delivery.receipts"
)

// DefaultRates are per-workspace send rates (messages/second) by channel.
var DefaultRates = map[string]float64{
	channel.Email: 200, channel.SMS: 100, channel.WhatsApp: 80, channel.RCS: 50, channel.Push: 500, channel.Inbox: 2000,
}

// EffectiveRates are the per-workspace send rates by channel: DefaultRates with the configured
// ones (PULSE_<CHANNEL>_RATE) on top.
func EffectiveRates(configured map[string]float64) map[string]float64 {
	rates := make(map[string]float64, len(DefaultRates))
	for ch, r := range DefaultRates {
		rates[ch] = r
	}
	for ch, r := range configured {
		if r > 0 {
			rates[ch] = r
		}
	}
	return rates
}

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

type Store interface {
	ContactByID(ctx context.Context, tenant int64, id uint32) (model.Contact, error)
	Template(ctx context.Context, tenant, id int64) (model.Template, error)
	ContactDevices(ctx context.Context, tenant int64, contact uint32) ([]model.Device, error)
	DeleteDevice(ctx context.Context, tenant int64, token string) error
	DefaultProvider(ctx context.Context, tenant int64, ch string) (model.Provider, []byte, error)
	DeliveryRules(ctx context.Context, tenant int64) (delivery.Rules, error)
	// VAPIDKeys returns the workspace's web push key pair (private key sealed); empty when unset.
	VAPIDKeys(ctx context.Context, tenant int64) (public string, sealedPrivate []byte, err error)
	// InsertInbox stores an app inbox message (once per message id).
	InsertInbox(ctx context.Context, m model.InboxMessage) error
	// AssetFile returns an asset with its bytes (email attachments).
	AssetFile(ctx context.Context, tenant, id int64) (model.Asset, []byte, error)
}

type Config struct {
	From         string             // default email sender
	EmailDefault providers.Provider // used when a workspace has no email provider (the server's SMTP)
	Rates        map[string]float64
	Box          *auth.Box // opens provider secrets
	Providers    providers.Deps
	Tracking     *tracking.Codec // email open/click tracking and unsubscribe links; nil disables
	PushSubject  string          // web push contact (mailto: or https:), required by push services
	Log          *slog.Logger    // optional
}

type Dispatcher struct {
	cfg      Config
	store    Store
	rdb      redis.Cmdable
	limiters map[string]*Limiter // per channel
	pub      kafka.Publisher
	now      func() time.Time

	mu        sync.Mutex
	templates map[[2]int64]cached[model.Template]
	senders   map[string]cached[providers.Provider] // web push
	vias      map[string]cached[sendVia]            // default providers with their limits
	rules     map[int64]cached[delivery.Rules]
	files     map[[2]int64]cached[attachment] // attachment bytes; assets never change their file
	autoCache sync.Map                        // tenant -> cached[automation.Settings] (automation.go)
	fileBytes int
}

type attachment struct {
	asset model.Asset
	data  []byte
}

const (
	fileCacheTTL   = 10 * time.Minute
	fileCacheBytes = 128 << 20
	// attachLinkTTL is how long the download links handed to the custom gateway work.
	attachLinkTTL = 7 * 24 * time.Hour
)

// deferredKey is a sorted set of messages held back by quiet hours, scored by when they may go.
const deferredKey = "deferred:messages"

type cached[T any] struct {
	v   T
	exp time.Time
}

func New(store Store, rdb redis.Cmdable, pub kafka.Publisher, cfg Config) *Dispatcher {
	rates := EffectiveRates(cfg.Rates)
	d := &Dispatcher{cfg: cfg, store: store, rdb: rdb, limiters: map[string]*Limiter{}, pub: pub, now: time.Now,
		templates: map[[2]int64]cached[model.Template]{}, senders: map[string]cached[providers.Provider]{}, vias: map[string]cached[sendVia]{},
		rules: map[int64]cached[delivery.Rules]{}, files: map[[2]int64]cached[attachment]{}}
	for ch, r := range rates {
		d.limiters[ch] = &Limiter{RDB: rdb, Rate: r, Burst: r, Now: time.Now}
	}
	return d
}

// Topics lists everything the dispatcher consumes.
func Topics() []string {
	out := []string{TopicReceipts}
	for _, c := range channel.All {
		out = append(out, channel.Topic(c))
	}
	return out
}

// Handle processes one record from a channel topic or the receipts topic.
func (d *Dispatcher) Handle(ctx context.Context, r *kgo.Record) error {
	if r.Topic == TopicReceipts {
		return d.handleReceipt(ctx, r)
	}
	var m model.Message
	if err := json.Unmarshal(r.Value, &m); err != nil {
		return fmt.Errorf("decode message: %w", err)
	}
	if m.Channel == "" {
		m.Channel = channel.Email // records written before channels existed
	}
	if channel.Topic(m.Channel) != r.Topic {
		return fmt.Errorf("message for %s on topic %s", m.Channel, r.Topic)
	}
	return d.send(ctx, m, triesOf(r))
}

type target struct{ to, platform string }

// Send delivers one message. Returning an error means "retry"; final outcomes are reported as events.
func (d *Dispatcher) Send(ctx context.Context, m model.Message) error { return d.send(ctx, m, 0) }

// send is Send for a message already tried tries times (a parked retry, see retry.go). A
// temporary provider failure parks the message for a later try instead of returning an error.
func (d *Dispatcher) send(ctx context.Context, m model.Message, tries int) error {
	sentKey := sentKeyOf(m)
	if n, err := d.rdb.Exists(ctx, sentKey).Result(); err != nil {
		return err
	} else if n == 1 {
		return nil // already handled (redelivered record or re-run journey step)
	}
	if m.TestTo != "" {
		return d.sendTest(ctx, sentKey, m)
	}
	c, err := d.store.ContactByID(ctx, m.TenantID, m.ContactID)
	if err != nil {
		return fmt.Errorf("load contact: %w", err)
	}
	if len(m.To) > 0 {
		return d.sendInternal(ctx, sentKey, m, c, tries)
	}
	// A message that can no longer go out on time is dropped rather than sent late.
	if dl, ok := d.deadline(m, c); ok && !d.now().Before(dl) {
		return d.late(ctx, m, c)
	}
	t, err := d.template(ctx, m.TenantID, m.TemplateID)
	if errors.Is(err, pg.ErrNotFound) {
		return d.finish(ctx, sentKey, m, c, "failed", "template not found")
	} else if err != nil {
		return fmt.Errorf("load template %d: %w", m.TemplateID, err)
	}
	// Consent first: an opted-out contact gets nothing on this channel, and no fallback either
	// (the SMS fallback is a separate message and checks SMS consent itself).
	if consent.OptedOut(c.Attrs, m.Channel) {
		return d.finish(ctx, sentKey, m, c, "suppressed", "contact unsubscribed from "+m.Channel)
	}
	if m.Channel == channel.Email && attr.String(c.Attrs[consent.AttrEmailBounced]) == "true" {
		return d.finish(ctx, sentKey, m, c, "suppressed", "the email address bounced")
	}
	if m.Channel == channel.SMS && attr.String(c.Attrs[consent.AttrSMSBounced]) == "true" {
		return d.finish(ctx, sentKey, m, c, "suppressed", "the phone number bounced")
	}
	if t.Channel != m.Channel {
		return d.finish(ctx, sentKey, m, c, "failed", fmt.Sprintf("template %d is a %s template", t.ID, t.Channel))
	}
	// A local-time broadcast waits for its clock time in the contact's own zone.
	if m.LocalAt != "" {
		if due, ok := broadcast.LocalDue(m.LocalAt, localZone(c, m.LocalZone)); ok && due.After(d.now()) {
			return d.holdUntil(ctx, m, c, due, "local", "sends at "+strings.Replace(m.LocalAt, "T", " ", 1)+" in the contact's time zone")
		}
	}
	// Marketing messages respect the workspace's quiet hours and frequency caps; transactional ones don't.
	if !t.Transactional {
		rules, err := d.deliveryRules(ctx, m.TenantID)
		if err != nil {
			return err
		}
		tz, _ := c.Attrs["timezone"].(string)
		if tz == "" {
			tz, _ = c.Attrs["geo_timezone"].(string) // located by IP (GeoIP)
		}
		if until, quiet := rules.QuietUntil(m.Channel, tz, d.now()); quiet {
			return d.deferUntil(ctx, m, c, until)
		}
		if cp, ok := rules.Caps[m.Channel]; ok {
			n, err := d.recentSends(ctx, m, cp)
			if err != nil {
				return err
			}
			if n >= cp.Max {
				return d.finish(ctx, sentKey, m, c, "capped", fmt.Sprintf("already sent %d %s messages in %d hours", n, m.Channel, cp.Hours))
			}
		}
	}

	targets, err := d.recipients(ctx, m.Channel, c)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return d.unreachable(ctx, sentKey, m, c, "contact has no "+addressName(m.Channel))
	}
	var token, ref string
	var headers map[string]string
	unsub := ""
	if m.Channel == channel.Email && d.cfg.Tracking != nil {
		tok := tracking.Token{TenantID: m.TenantID, ExternalID: c.ExternalID, MessageID: m.ID,
			Channel: m.Channel, TemplateID: m.TemplateID, JourneyID: m.JourneyID,
			BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant}
		token = d.cfg.Tracking.Encode(tok)
		// The message reference links bounces, complaints and delivery reports back to this message.
		ref = d.cfg.Tracking.Ref(tracking.RefFromToken(tok, c.ID))
		unsub, headers = d.cfg.Tracking.UnsubscribeURL(token), d.cfg.Tracking.Headers(token)
		if err := d.replyTo(ctx, m, token, headers); err != nil {
			return err
		}
	}
	prefs := ""
	if token != "" {
		prefs = d.cfg.Tracking.PreferencesURL(token)
	}
	rendered, err := channel.RenderWith(t, c, vars(m, channel.Links{Unsubscribe: unsub, Preferences: prefs}))
	if err != nil {
		return d.finish(ctx, sentKey, m, c, "failed", err.Error())
	}
	if t.MissingValues == channel.MissingSkip && len(rendered.Missing) > 0 {
		return d.finish(ctx, sentKey, m, c, "failed", SkippedPrefix+strings.Join(rendered.Missing, ", "))
	}
	var ampTok tracking.Token
	if token != "" {
		rendered.HTML = d.cfg.Tracking.Instrument(rendered.HTML, token)
		rendered.Content.Text = d.cfg.Tracking.InstrumentText(rendered.Content.Text, token)
		ampTok, _ = d.cfg.Tracking.Decode(token)
	}
	if m.Channel == channel.Email {
		rendered.Content.AMP = d.prepareAMP(rendered.Content.AMP, token, ampTok, c.ID, false)
	}
	if err := d.shortenLinks(ctx, m, c, &rendered); err != nil {
		return err
	}
	if m.Channel == channel.Push && d.cfg.Tracking != nil {
		// The service worker and the app SDKs post clicks and dismissals to this link.
		tok := d.cfg.Tracking.Encode(tracking.Token{TenantID: m.TenantID, ExternalID: c.ExternalID, MessageID: m.ID,
			Channel: m.Channel, TemplateID: m.TemplateID, JourneyID: m.JourneyID, BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant})
		data := maps.Clone(rendered.Content.Data)
		if data == nil {
			data = map[string]string{}
		}
		data[tracking.PushTrackKey] = d.cfg.Tracking.PushURL(tok)
		rendered.Content.Data = data
	}
	var files []providers.Attachment
	if m.Channel == channel.Email && len(rendered.Content.Attachments) > 0 {
		files, err = d.attachments(ctx, m.TenantID, rendered.Content.Attachments)
		if errors.Is(err, pg.ErrNotFound) {
			return d.finish(ctx, sentKey, m, c, "failed", "an attachment of the template was deleted")
		} else if err != nil {
			return fmt.Errorf("load attachments: %w", err)
		}
	}
	// Browser push subscriptions go through Web Push with the workspace's VAPID keys; everything
	// else through the channel's default provider (fetched only when needed).
	var p providers.Provider
	var via sendVia
	if m.Channel == channel.Inbox {
		p = &inboxSender{store: d.store, contact: c, msg: m, expiryDays: d.automation(ctx, m.TenantID).Messages.InboxExpiryDays}
	}
	for _, tg := range targets {
		if p != nil {
			break
		}
		if m.Channel == channel.Push && providers.IsWebPushSubscription(tg.to) {
			continue
		}
		var err error
		via, err = d.senderVia(ctx, m.TenantID, m.Channel)
		if errors.Is(err, pg.ErrNotFound) {
			return d.finish(ctx, sentKey, m, c, "failed", "no "+m.Channel+" provider configured")
		} else if err != nil {
			return err
		}
		p = via.p
		break
	}
	// The provider's daily quota: over it, the message waits for the next day (or is dropped at
	// its deadline).
	quotaTaken := false
	if via.id != 0 && via.limits.DailyQuota > 0 {
		ok, next, err := TakeQuota(ctx, d.rdb, via.id, via.limits, d.now())
		if err != nil {
			return err
		}
		if !ok {
			return d.holdUntil(ctx, m, c, next, "quota", fmt.Sprintf("the daily quota of %d messages for %s is used up; sends after %s",
				via.limits.DailyQuota, via.name, next.UTC().Format(time.RFC3339)))
		}
		quotaTaken = true
	}
	if err := d.limiters[m.Channel].Wait(ctx, "rl:"+m.Channel+":"+strconv.FormatInt(m.TenantID, 10)); err != nil {
		return err
	}
	if err := d.waitProviderRate(ctx, via); err != nil {
		return err
	}

	var sent, unreachable int
	var lastErr, transient error
	for _, tg := range targets {
		via := p
		if m.Channel == channel.Push && providers.IsWebPushSubscription(tg.to) {
			wp, err := d.webPusher(ctx, m.TenantID)
			if err != nil {
				lastErr = err
				if providers.Transient(err) {
					transient = err
				}
				continue
			}
			via = wp
		}
		res, err := via.Send(ctx, providers.Outbound{
			TenantID: m.TenantID, MessageID: m.ID, Channel: m.Channel, To: tg.to, Platform: tg.platform,
			From: d.cfg.From, Rendered: rendered, Headers: headers, Attachments: files, Ref: ref,
			Vars: outboundVars(m, c, unsub, prefs),
		})
		switch {
		case err == nil:
			sent++
			if res.ProviderMessageID != "" {
				d.remember(ctx, m, res.ProviderMessageID)
			}
		case errors.Is(err, providers.ErrUnreachable):
			unreachable++
			lastErr = err
			if m.Channel == channel.Push {
				_ = d.store.DeleteDevice(ctx, m.TenantID, tg.to) // dead token
				d.pushGone(ctx, c)
			}
		case errors.Is(err, providers.ErrPermanent):
			lastErr = err
		default: // transient: network error, timeout, 429, 5xx
			lastErr, transient = err, err
		}
	}
	if sent == 0 && quotaTaken {
		refundQuota(ctx, d.rdb, via.id, via.limits, d.now())
	}
	switch {
	case sent > 0:
		if !t.Transactional {
			d.countSend(ctx, m)
		}
		if m.Channel == channel.RCS { // an RCS reply is attributed to the latest RCS message (package replies)
			tok := tracking.Token{TenantID: m.TenantID, ExternalID: c.ExternalID, MessageID: m.ID, Channel: m.Channel,
				TemplateID: m.TemplateID, JourneyID: m.JourneyID, BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant}
			_ = replies.SaveLast(ctx, d.rdb, c.ID, tok) // best effort: without it a reply is still recorded, just not matched

		}
		return d.finish(ctx, sentKey, m, c, "sent", "")
	case transient != nil:
		// Nothing went out and at least one target may work later: try the whole message again.
		return d.retryLater(ctx, m, tries+1, fmt.Errorf("send %s: %w", m.Channel, transient), func(reason string) error {
			return d.finish(ctx, sentKey, m, c, "failed", reason)
		})
	case lastErr == nil:
		return d.finish(ctx, sentKey, m, c, "failed", "nothing was sent")
	case m.Channel == channel.SMS && providers.Bounced(lastErr):
		return d.bounce(ctx, sentKey, m, c, lastErr.Error())
	case unreachable == len(targets):
		return d.unreachable(ctx, sentKey, m, c, lastErr.Error())
	default:
		return d.finish(ctx, sentKey, m, c, "failed", lastErr.Error())
	}
}

// SkippedPrefix starts the reason of a message not sent because a variable had no value (the
// template's missing-value policy is "skip"); the outcome is <channel>_failed with props.skipped.
const SkippedPrefix = "skipped: no value for "

// vars is a message's personalisation context: its trigger event, its campaign and its links.
func vars(m model.Message, links channel.Links) channel.Vars {
	v := channel.Vars{Links: links}
	if len(m.Event) > 0 {
		_ = json.Unmarshal(m.Event, &v.Event) // written by Pulse; a bad one just renders empty
	}
	v.Campaign.Name, v.Campaign.Step, v.Campaign.Channel = m.Campaign, m.Step, m.Channel
	switch {
	case m.BroadcastID != 0:
		v.Campaign.Type, v.Campaign.ID = "broadcast", m.BroadcastID
	case m.JourneyID != 0:
		v.Campaign.Type, v.Campaign.ID = "journey", m.JourneyID
	case strings.HasPrefix(m.ID, "test:"):
		v.Campaign.Type = "test"
	}
	return v
}

// replyTo adds a Reply-To with a reply code when the workspace tracks replies (delivery rules
// reply_to), so a reply can be matched to this message (package replies).
func (d *Dispatcher) replyTo(ctx context.Context, m model.Message, token string, headers map[string]string) error {
	rules, err := d.deliveryRules(ctx, m.TenantID)
	if err != nil || rules.ReplyTo == "" || headers == nil {
		return err
	}
	code := replies.NewCode()
	addr, ok := replies.Address(rules.ReplyTo, code)
	if !ok {
		return nil
	}
	if err := replies.Save(ctx, d.rdb, code, token); err != nil {
		return err
	}
	headers["Reply-To"] = addr
	return nil
}

// sendInternal sends a journey's "email to user": the email template rendered with the contact's
// data, to workspace users or fixed addresses. It isn't the contact's message, so consent, caps,
// quiet hours and tracking don't apply; it records notification_sent or notification_failed.
func (d *Dispatcher) sendInternal(ctx context.Context, sentKey string, m model.Message, c model.Contact, tries int) error {
	done := func(outcome, reason string) error {
		props := map[string]any{"message_id": m.ID, "template_id": m.TemplateID, "journey_id": m.JourneyID, "recipients": len(m.To)}
		if reason != "" {
			props["reason"] = reason
		}
		ev := event.Event{ID: "msg:" + m.ID + ":notification_" + outcome, TenantID: m.TenantID, ExternalID: c.ExternalID,
			Name: "notification_" + outcome, Ts: d.now().UTC(), Props: props}
		rec, err := kafka.JSON(event.TopicRaw, ev.RawKey(), ev)
		if err != nil {
			return err
		}
		if err := d.mark(ctx, sentKey, outcome); err != nil {
			return err
		}
		return d.pub.Publish(ctx, rec)
	}
	t, err := d.template(ctx, m.TenantID, m.TemplateID)
	if errors.Is(err, pg.ErrNotFound) {
		return done("failed", "template not found")
	} else if err != nil {
		return fmt.Errorf("load template %d: %w", m.TemplateID, err)
	}
	if t.Channel != channel.Email {
		return done("failed", fmt.Sprintf("template %d is a %s template", t.ID, t.Channel))
	}
	rendered, err := channel.RenderWith(t, c, vars(m, channel.Links{}))
	if err != nil {
		return done("failed", err.Error())
	}
	rendered.Content.AMP = "" // internal notifications go without the AMP part (no recipient to post back as)
	if t.MissingValues == channel.MissingSkip && len(rendered.Missing) > 0 {
		return done("failed", SkippedPrefix+strings.Join(rendered.Missing, ", "))
	}
	p, err := d.sender(ctx, m.TenantID, channel.Email)
	if errors.Is(err, pg.ErrNotFound) {
		return done("failed", "no email provider configured")
	} else if err != nil {
		return err
	}
	if err := d.limiters[channel.Email].Wait(ctx, "rl:"+channel.Email+":"+strconv.FormatInt(m.TenantID, 10)); err != nil {
		return err
	}
	sent := 0
	var last, transient error
	for i, to := range m.To {
		_, err := p.Send(ctx, providers.Outbound{TenantID: m.TenantID, MessageID: m.ID + ":" + strconv.Itoa(i), Channel: channel.Email,
			To: to, From: d.cfg.From, Rendered: rendered})
		if err != nil {
			last = err
			if providers.Transient(err) {
				transient = err
			}
			continue
		}
		sent++
	}
	switch {
	case sent > 0:
		return done("sent", "")
	case transient != nil:
		return d.retryLater(ctx, m, tries+1, fmt.Errorf("send notification: %w", transient), func(reason string) error {
			return done("failed", reason)
		})
	default:
		return done("failed", last.Error())
	}
}

// shortenLinks replaces the links of an SMS or RCS message whose template tracks links with short
// tracked links (tracking.ShortenText), and stores what they stand for.
func (d *Dispatcher) shortenLinks(ctx context.Context, m model.Message, c model.Contact, r *channel.Rendered) error {
	if !r.Content.TrackLinks || d.cfg.Tracking == nil || (m.Channel != channel.SMS && m.Channel != channel.RCS) {
		return nil
	}
	base, code := d.cfg.Tracking.Base(), tracking.NewShortCode()
	var urls []string
	r.Content.Text, urls = tracking.ShortenText(r.Content.Text, base, code, urls)
	for i, sg := range r.Content.Suggestions {
		if sg.Type == "url" && sg.Value != "" {
			r.Content.Suggestions[i].Value, urls = tracking.ShortenText(sg.Value, base, code, urls)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	tok := tracking.Token{TenantID: m.TenantID, ExternalID: c.ExternalID, MessageID: m.ID, Channel: m.Channel,
		TemplateID: m.TemplateID, JourneyID: m.JourneyID, BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant}
	return tracking.SaveShort(ctx, d.rdb, code, tracking.ShortLink{Token: tok, URLs: urls})
}

// bounce records an SMS that failed because of the phone number: sms_failed with props.bounce,
// and sms_bounced on the contact, so later SMS are suppressed until the number changes.
func (d *Dispatcher) bounce(ctx context.Context, sentKey string, m model.Message, c model.Contact, reason string) error {
	rec, err := d.eventRecordWith("msg:"+m.ID+":"+m.Channel+"_failed", m, c, "failed", reason, true)
	if err != nil {
		return err
	}
	if err := d.mark(ctx, sentKey, "failed"); err != nil {
		return err
	}
	return d.pub.Publish(ctx, rec)
}

func addressName(ch string) string {
	switch ch {
	case channel.Email:
		return "email address"
	case channel.Push:
		return "push device"
	}
	return "valid phone number (E.164, e.g. +919812345678)"
}

func (d *Dispatcher) recipients(ctx context.Context, ch string, c model.Contact) ([]target, error) {
	switch ch {
	case channel.Email:
		if c.Email == "" {
			return nil, nil
		}
		return []target{{to: c.Email}}, nil
	case channel.Inbox:
		return []target{{to: c.ExternalID}}, nil
	case channel.Push:
		devs, err := d.store.ContactDevices(ctx, c.TenantID, c.ID)
		if err != nil {
			return nil, err
		}
		out := make([]target, len(devs))
		for i, dv := range devs {
			out[i] = target{to: dv.Token, platform: dv.Platform}
		}
		return out, nil
	default:
		phone, _ := c.Attrs["phone"].(string)
		if !e164.MatchString(phone) {
			return nil, nil
		}
		return []target{{to: phone}}, nil
	}
}

// unreachable falls back to SMS when the message allows it, otherwise records a failure.
func (d *Dispatcher) unreachable(ctx context.Context, sentKey string, m model.Message, c model.Contact, reason string) error {
	if channel.FallsBackToSMS(m.Channel) && m.FallbackTemplateID > 0 && !m.IsFallback {
		if err := d.fallback(ctx, m, reason); err != nil {
			return err
		}
		return d.mark(ctx, sentKey, "fallback")
	}
	return d.finish(ctx, sentKey, m, c, "failed", reason)
}

// fallback publishes the SMS version of a message. The guard key makes it happen once even if an
// immediate failure and a failed delivery receipt both arrive.
func (d *Dispatcher) fallback(ctx context.Context, m model.Message, reason string) error {
	won, err := d.rdb.SetNX(ctx, fmt.Sprintf("fallback:%d:%s", m.TenantID, m.ID), 1, sentTTL).Result()
	if err != nil || !won {
		return err
	}
	sms := model.Message{ID: m.ID + ":sms", TenantID: m.TenantID, ContactID: m.ContactID, Channel: channel.SMS,
		TemplateID: m.FallbackTemplateID, JourneyID: m.JourneyID, IsFallback: true,
		BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant, Event: m.Event, Campaign: m.Campaign, Step: m.Step}
	rec, err := kafka.JSON(channel.Topic(channel.SMS), event.ContactKey(m.TenantID, m.ContactID), sms)
	if err != nil {
		return err
	}
	c, err := d.store.ContactByID(ctx, m.TenantID, m.ContactID)
	if err != nil {
		return err
	}
	report, err := d.outcomeEvent(m, c, "fallback", reason)
	if err != nil {
		return err
	}
	return d.pub.Publish(ctx, rec, report)
}

func (d *Dispatcher) mark(ctx context.Context, sentKey, outcome string) error {
	return d.rdb.Set(ctx, sentKey, outcome, sentTTL).Err()
}

// finish marks the message done and reports the outcome as an event, e.g. sms_sent or push_failed.
func (d *Dispatcher) finish(ctx context.Context, sentKey string, m model.Message, c model.Contact, outcome, reason string) error {
	rec, err := d.outcomeEvent(m, c, outcome, reason)
	if err != nil {
		return err
	}
	if err := d.mark(ctx, sentKey, outcome); err != nil {
		return err
	}
	return d.pub.Publish(ctx, rec)
}

func (d *Dispatcher) outcomeEvent(m model.Message, c model.Contact, outcome, reason string) (*kgo.Record, error) {
	return d.eventRecord("msg:"+m.ID+":"+m.Channel+"_"+outcome, m, c, outcome, reason)
}

func (d *Dispatcher) eventRecord(id string, m model.Message, c model.Contact, outcome, reason string) (*kgo.Record, error) {
	return d.eventRecordWith(id, m, c, outcome, reason, false)
}

// eventRecordWith is eventRecord; bounce marks an SMS bounce (props.bounce, attribute sms_bounced).
func (d *Dispatcher) eventRecordWith(id string, m model.Message, c model.Contact, outcome, reason string, bounce bool) (*kgo.Record, error) {
	return d.eventRecordCode(id, m, c, outcome, reason, "", bounce)
}

// eventRecordCode is eventRecordWith with a known reason code (else it is derived from the reason).
func (d *Dispatcher) eventRecordCode(id string, m model.Message, c model.Contact, outcome, reason, code string, bounce bool) (*kgo.Record, error) {
	props := map[string]any{"message_id": m.ID, "template_id": m.TemplateID, "journey_id": m.JourneyID}
	if code == "" || !reasons.Valid(code) {
		code = reasons.Classify(m.Channel, outcome, reason)
	}
	if code != "" {
		props["reason_code"] = code
	}
	if reason != "" {
		props["reason"] = reason
		if strings.HasPrefix(reason, SkippedPrefix) {
			props["skipped"] = "missing_value"
		}
		if reason == model.DeadlineReason {
			if m.Deadline != nil {
				props["deadline"] = m.Deadline.UTC().Format(time.RFC3339)
			} else {
				props["deadline"] = m.DeadlineLocal + " (contact's time)"
			}
		}
	}
	if m.IsFallback {
		props["fallback"] = true
	}
	if m.BroadcastID != 0 {
		props["broadcast_id"], props["run"] = m.BroadcastID, m.Run
		if m.Variant != "" {
			props["variant"] = m.Variant
		}
	}
	name := m.Channel + "_" + outcome
	ev := event.Event{ID: id, TenantID: m.TenantID, ExternalID: c.ExternalID,
		Name: name, Ts: d.now().UTC(), Props: props}
	if bounce {
		props["bounce"] = true
		ev.Attrs = map[string]any{consent.AttrSMSBounced: true}
	}
	return kafka.JSON(event.TopicRaw, ev.RawKey(), ev)
}

// ---- delivery rules ----

func capKey(m model.Message) string { return CapKey(m.TenantID, m.ContactID, m.Channel) }

// CapKey is a contact's sorted set of recent marketing messages on a channel (frequency caps).
func CapKey(tenant int64, contact uint32, ch string) string {
	return fmt.Sprintf("cap:%d:%d:%s", tenant, contact, ch)
}

// recentSends counts the contact's marketing messages on this channel within the cap's window.
func (d *Dispatcher) recentSends(ctx context.Context, m model.Message, cp delivery.Cap) (int, error) {
	key, now := capKey(m), d.now()
	from := now.Add(-time.Duration(cp.Hours) * time.Hour).UnixMilli()
	pipe := d.rdb.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(from, 10))
	n := pipe.ZCard(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int(n.Val()), nil
}

func (d *Dispatcher) countSend(ctx context.Context, m model.Message) {
	key := capKey(m)
	pipe := d.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(d.now().UnixMilli()), Member: m.ID})
	pipe.Expire(ctx, key, 32*24*time.Hour)
	_, _ = pipe.Exec(ctx)
}

// deferUntil holds a message until quiet hours end; RunDeferred sends it again then.
func (d *Dispatcher) deferUntil(ctx context.Context, m model.Message, c model.Contact, until time.Time) error {
	return d.holdUntil(ctx, m, c, until, "deferred", "quiet hours until "+until.UTC().Format(time.RFC3339))
}

// localZone is the zone a local-time message follows: the contact's own (timezone, or
// geo_timezone from GeoIP), else the zone the broadcast was scheduled in.
func localZone(c model.Contact, scheduled string) string {
	for _, k := range []string{"timezone", "geo_timezone"} {
		if tz, _ := c.Attrs[k].(string); tz != "" {
			if _, err := time.LoadLocation(tz); err == nil {
				return tz
			}
		}
	}
	return scheduled
}

// holdUntil parks a message until a time (RunDeferred sends it again then) and records a
// <channel>_deferred event; kind keeps the event ids of different holds apart.
func (d *Dispatcher) holdUntil(ctx context.Context, m model.Message, c model.Contact, until time.Time, kind, reason string) error {
	if dl, ok := d.deadline(m, c); ok && until.After(dl) {
		return d.late(ctx, m, c) // it would go out after its deadline
	}
	b, _ := json.Marshal(m)
	if err := d.rdb.ZAdd(ctx, deferredKey, redis.Z{Score: float64(until.UnixMilli()), Member: string(b)}).Err(); err != nil {
		return err
	}
	rec, err := d.eventRecord("msg:"+m.ID+":"+kind, m, c, "deferred", reason)
	if err != nil {
		return err
	}
	return d.pub.Publish(ctx, rec)
}

// RunDeferred releases held messages whose quiet hours are over, and parked retries that are due
// (retry.go), until ctx is done.
func (d *Dispatcher) RunDeferred(ctx context.Context) error {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		d.releaseDue(ctx)
		d.releaseRetries(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// releaseDue re-publishes every held message that may go now (up to 20,000 a round, so a day's
// quota-held messages drain quickly). ZREM decides which worker owns each.
func (d *Dispatcher) releaseDue(ctx context.Context) {
	for range 40 {
		if n := d.releaseBatch(ctx); n < 500 || ctx.Err() != nil {
			return
		}
	}
}

func (d *Dispatcher) releaseBatch(ctx context.Context) int {
	due, err := d.rdb.ZRangeByScore(ctx, deferredKey, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(d.now().UnixMilli(), 10), Count: 500}).Result()
	if err != nil {
		return 0
	}
	for _, raw := range due {
		if n, _ := d.rdb.ZRem(ctx, deferredKey, raw).Result(); n != 1 {
			continue // another worker took it
		}
		var m model.Message
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		rec, err := kafka.JSON(channel.Topic(m.Channel), event.ContactKey(m.TenantID, m.ContactID), m)
		if err == nil {
			err = d.pub.Publish(ctx, rec)
		}
		if err != nil { // put it back for the next round
			d.rdb.ZAdd(ctx, deferredKey, redis.Z{Score: float64(d.now().UnixMilli()), Member: raw})
		}
	}
	return len(due)
}

func (d *Dispatcher) deliveryRules(ctx context.Context, tenant int64) (delivery.Rules, error) {
	d.mu.Lock()
	c, ok := d.rules[tenant]
	d.mu.Unlock()
	if ok && d.now().Before(c.exp) {
		return c.v, nil
	}
	r, err := d.store.DeliveryRules(ctx, tenant)
	if err != nil {
		return r, err
	}
	d.mu.Lock()
	d.rules[tenant] = cached[delivery.Rules]{v: r, exp: d.now().Add(cacheTTL)}
	d.mu.Unlock()
	return r, nil
}

// ---- delivery receipts ----

// ReceiptRecord is what the API publishes to delivery.receipts for each parsed webhook entry.
type ReceiptRecord struct {
	TenantID int64             `json:"tenant_id"`
	Receipt  providers.Receipt `json:"receipt"`
}

func receiptKey(tenant int64, providerMsgID string) string {
	return fmt.Sprintf("pmsg:%d:%s", tenant, providerMsgID)
}

// remember maps the provider's message id back to ours, so receipts can be matched.
func (d *Dispatcher) remember(ctx context.Context, m model.Message, providerMsgID string) {
	b, _ := json.Marshal(m)
	d.rdb.Set(ctx, receiptKey(m.TenantID, providerMsgID), b, receiptTTL)
}

func (d *Dispatcher) handleReceipt(ctx context.Context, r *kgo.Record) error {
	var rr ReceiptRecord
	if err := json.Unmarshal(r.Value, &rr); err != nil {
		return fmt.Errorf("decode receipt: %w", err)
	}
	raw, err := d.rdb.Get(ctx, receiptKey(rr.TenantID, rr.Receipt.ProviderMessageID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil // unknown or expired message
	} else if err != nil {
		return err
	}
	var m model.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	c, err := d.store.ContactByID(ctx, m.TenantID, m.ContactID)
	if err != nil {
		return err
	}
	switch {
	case rr.Receipt.Status == "failed" && rr.Receipt.Unreachable && channel.FallsBackToSMS(m.Channel) && m.FallbackTemplateID > 0 && !m.IsFallback:
		return d.fallback(ctx, m, "delivery failed: "+rr.Receipt.Reason)
	case rr.Receipt.Status == "delivered" || rr.Receipt.Status == "failed" || rr.Receipt.Status == "read":
		// The id makes it one event per (message, status), even if the provider repeats the webhook.
		bounce := m.Channel == channel.SMS && rr.Receipt.Status == "failed" && rr.Receipt.Unreachable
		rec, err := d.eventRecordCode("rcpt:"+m.ID+":"+rr.Receipt.Status, m, c, rr.Receipt.Status, rr.Receipt.Reason, rr.Receipt.Code, bounce)
		if err != nil {
			return err
		}
		return d.pub.Publish(ctx, rec)
	case m.Channel == channel.Email && (rr.Receipt.Status == "bounced" || rr.Receipt.Status == "complained"):
		return d.emailFeedback(ctx, m, c, rr.Receipt)
	}
	return nil
}

// emailFeedback records a bounce or complaint receipt (the custom gateway, the sandbox) like the
// bounce webhooks do (package bounces): email_bounced (a hard bounce also marks the address
// bounced), or email_complained plus an email unsubscribe.
func (d *Dispatcher) emailFeedback(ctx context.Context, m model.Message, c model.Contact, rc providers.Receipt) error {
	props := map[string]any{"message_id": m.ID, "template_id": m.TemplateID, "journey_id": m.JourneyID,
		"reason": rc.Reason, "source": "receipt", "email": c.Email}
	if m.BroadcastID != 0 {
		props["broadcast_id"], props["run"] = m.BroadcastID, m.Run
		if m.Variant != "" {
			props["variant"] = m.Variant
		}
	}
	now := d.now().UTC()
	var evs []event.Event
	if rc.Status == "complained" {
		props["kind"], props["reason_code"] = "complaint", reasons.Complaint
		evs = append(evs, event.Event{ID: "rcpt:" + m.ID + ":complained", Name: "email_complained", Props: props},
			event.Event{ID: "rcpt:" + m.ID + ":unsub", Name: consent.Unsubscribed, Props: map[string]any{"channel": "email", "source": "complaint"}})
	} else {
		kind := rc.Kind
		if kind != "soft" {
			kind = "hard"
		}
		props["kind"], props["reason_code"] = kind, kind+"_bounce"
		ev := event.Event{ID: "rcpt:" + m.ID + ":bounced", Name: "email_bounced", Props: props}
		if kind == "hard" {
			ev.Attrs = map[string]any{consent.AttrEmailBounced: true}
		}
		evs = append(evs, ev)
	}
	recs := make([]*kgo.Record, 0, len(evs))
	for _, ev := range evs {
		ev.TenantID, ev.ExternalID, ev.Ts = m.TenantID, c.ExternalID, now
		rec, err := kafka.JSON(event.TopicRaw, ev.RawKey(), ev)
		if err != nil {
			return err
		}
		recs = append(recs, rec)
	}
	return d.pub.Publish(ctx, recs...)
}

// ---- caches ----

func (d *Dispatcher) template(ctx context.Context, tenant, id int64) (model.Template, error) {
	key := [2]int64{tenant, id}
	d.mu.Lock()
	c, ok := d.templates[key]
	d.mu.Unlock()
	if ok && d.now().Before(c.exp) {
		return c.v, nil
	}
	t, err := d.store.Template(ctx, tenant, id)
	if err != nil {
		return model.Template{}, err
	}
	d.mu.Lock()
	d.templates[key] = cached[model.Template]{v: t, exp: d.now().Add(cacheTTL)}
	d.mu.Unlock()
	return t, nil
}

// attachments loads a template's files (cached: an asset's file never changes) with a signed
// download link for providers that take links.
func (d *Dispatcher) attachments(ctx context.Context, tenant int64, ids []int64) ([]providers.Attachment, error) {
	out := make([]providers.Attachment, 0, len(ids))
	for _, id := range ids {
		key := [2]int64{tenant, id}
		d.mu.Lock()
		f, ok := d.files[key]
		d.mu.Unlock()
		if !ok || !d.now().Before(f.exp) {
			a, data, err := d.store.AssetFile(ctx, tenant, id)
			if err != nil {
				return nil, err
			}
			f = cached[attachment]{v: attachment{asset: a, data: data}, exp: d.now().Add(fileCacheTTL)}
			d.mu.Lock()
			if d.fileBytes+len(data) > fileCacheBytes { // simple bound: start over rather than track recency
				d.files, d.fileBytes = map[[2]int64]cached[attachment]{}, 0
			}
			if old, had := d.files[key]; had {
				d.fileBytes -= len(old.v.data)
			}
			d.files[key] = f
			d.fileBytes += len(data)
			d.mu.Unlock()
		}
		a := f.v.asset
		pa := providers.Attachment{Filename: a.Filename, ContentType: a.ContentType, Data: f.v.data}
		if d.cfg.Box != nil && d.cfg.Tracking != nil {
			tok := assets.Seal(d.cfg.Box, assets.Token{TenantID: tenant, AssetID: a.ID, Purpose: assets.PurposeAttach,
				Expires: d.now().Add(attachLinkTTL).Unix()})
			pa.URL = d.cfg.Tracking.Base() + "/a/" + a.PublicKey + "/download?t=" + tok
		}
		out = append(out, pa)
	}
	return out, nil
}

// sender returns the workspace's default provider for a channel (the server SMTP for email when none).
func (d *Dispatcher) sender(ctx context.Context, tenant int64, ch string) (providers.Provider, error) {
	key := strconv.FormatInt(tenant, 10) + ":" + ch
	d.mu.Lock()
	c, ok := d.senders[key]
	d.mu.Unlock()
	if ok && d.now().Before(c.exp) {
		return c.v, nil
	}
	p, err := d.buildSender(ctx, tenant, ch)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.senders[key] = cached[providers.Provider]{v: p, exp: d.now().Add(cacheTTL)}
	d.mu.Unlock()
	return p, nil
}

func (d *Dispatcher) buildSender(ctx context.Context, tenant int64, ch string) (providers.Provider, error) {
	v, err := d.buildVia(ctx, tenant, ch)
	return v.p, err
}

func (d *Dispatcher) buildVia(ctx context.Context, tenant int64, ch string) (sendVia, error) {
	cfg, sealed, err := d.store.DefaultProvider(ctx, tenant, ch)
	if errors.Is(err, pg.ErrNotFound) && ch == channel.Email && d.cfg.EmailDefault != nil {
		return sendVia{p: d.cfg.EmailDefault, name: "the server's SMTP"}, nil
	} else if err != nil {
		return sendVia{}, err
	}
	secrets, err := OpenSecrets(d.cfg.Box, sealed)
	if err != nil {
		return sendVia{}, err
	}
	deps := d.cfg.Providers
	deps.Scope = providers.TokenScope(tenant, cfg.ID) // custom HTTP auth tokens are per provider
	p, err := providers.New(cfg.Kind, deps, cfg.Config, secrets)
	return sendVia{p: p, id: cfg.ID, name: cfg.Name, limits: cfg.Limits}, err
}

// outboundVars is the personalisation context handed to providers whose requests are templates
// (the gateway's custom-request mode).
func outboundVars(m model.Message, c model.Contact, unsub, prefs string) *personalize.Vars {
	v := vars(m, channel.Links{Unsubscribe: unsub, Preferences: prefs})
	links := map[string]string{}
	if unsub != "" {
		links["unsubscribe"] = unsub
	}
	if prefs != "" {
		links["preferences"] = prefs
	}
	return &personalize.Vars{Contact: c, Event: v.Event, Campaign: v.Campaign, Links: links}
}

// senderVia is sender with the provider's id and limits (cached like sender).
func (d *Dispatcher) senderVia(ctx context.Context, tenant int64, ch string) (sendVia, error) {
	key := strconv.FormatInt(tenant, 10) + ":" + ch
	d.mu.Lock()
	c, ok := d.vias[key]
	d.mu.Unlock()
	if ok && d.now().Before(c.exp) {
		return c.v, nil
	}
	v, err := d.buildVia(ctx, tenant, ch)
	if err != nil {
		return v, err
	}
	d.mu.Lock()
	d.vias[key] = cached[sendVia]{v: v, exp: d.now().Add(cacheTTL)}
	d.mu.Unlock()
	return v, nil
}

// inboxSender "delivers" an inbox message by storing it, carrying the message's ids so reads and
// clicks count in its journey or broadcast report.
type inboxSender struct {
	store   Store
	contact model.Contact
	msg     model.Message
	// expiryDays applies when the template doesn't set its own (the workspace's setting, 30 by default)
	expiryDays int
}

func (s *inboxSender) Send(ctx context.Context, o providers.Outbound) (providers.Result, error) {
	c := o.Rendered.Content
	days := c.ExpiresDays
	if days == 0 {
		days = s.expiryDays
	}
	if days <= 0 {
		days = 30
	}
	meta := map[string]any{"template_id": s.msg.TemplateID, "journey_id": s.msg.JourneyID}
	if s.msg.BroadcastID != 0 {
		meta["broadcast_id"], meta["run"] = s.msg.BroadcastID, s.msg.Run
		if s.msg.Variant != "" {
			meta["variant"] = s.msg.Variant
		}
	}
	err := s.store.InsertInbox(ctx, model.InboxMessage{TenantID: o.TenantID, ContactID: s.contact.ID, MessageID: o.MessageID,
		Title: c.Title, Body: c.Body, ImageURL: c.ImageURL, URL: c.URL, Category: c.Category, Data: c.Data, Meta: meta,
		ExpiresAt: time.Now().AddDate(0, 0, days)})
	return providers.Result{}, err
}

// webPusher sends to browser push subscriptions with the workspace's VAPID keys.
func (d *Dispatcher) webPusher(ctx context.Context, tenant int64) (providers.Provider, error) {
	key := strconv.FormatInt(tenant, 10) + ":webpush"
	d.mu.Lock()
	c, ok := d.senders[key]
	d.mu.Unlock()
	if ok && d.now().Before(c.exp) {
		return c.v, nil
	}
	pub, sealed, err := d.store.VAPIDKeys(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if pub == "" || d.cfg.Box == nil {
		return nil, providers.Permanent("web push isn't set up for this workspace")
	}
	priv, err := d.cfg.Box.Open(sealed)
	if err != nil {
		return nil, providers.Permanent("web push keys can't be opened: " + err.Error())
	}
	wp := &providers.WebPush{HTTP: d.cfg.Providers.HTTP, PublicKey: pub, PrivateKey: string(priv), Subject: d.cfg.PushSubject}
	if wp.HTTP == nil {
		// Endpoints come from browsers: never let one reach a private address.
		wp.HTTP = safehttp.Client(15*time.Second, d.cfg.Providers.AllowPrivate)
	}
	d.mu.Lock()
	d.senders[key] = cached[providers.Provider]{v: wp, exp: d.now().Add(cacheTTL)}
	d.mu.Unlock()
	return wp, nil
}

// OpenSecrets decrypts a provider's stored secrets (a sealed JSON object).
func OpenSecrets(box *auth.Box, sealed []byte) (map[string]string, error) {
	secrets := map[string]string{}
	if len(sealed) == 0 {
		return secrets, nil
	}
	if box == nil {
		return nil, errors.New("server has no secret key configured")
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("open provider secrets: %w", err)
	}
	return secrets, json.Unmarshal(plain, &secrets)
}

// pushGone clears the contact's push reachability bit when its last device is gone.
func (d *Dispatcher) pushGone(ctx context.Context, c model.Contact) {
	hd, ok := d.store.(interface {
		HasDevices(ctx context.Context, tenant int64, contact uint32) (bool, error)
	})
	if !ok {
		return
	}
	if has, err := hd.HasDevices(ctx, c.TenantID, c.ID); err == nil && !has {
		_ = reach.SetPush(ctx, d.rdb, c.TenantID, c.ID, false)
	}
}

// sendTest sends a test to a saved test contact's bare address (no contact behind it): the template
// is rendered with the test contact's name, nothing is tracked, consent and caps don't apply, and
// no events are recorded (the Test inbox and the provider show the result).
func (d *Dispatcher) sendTest(ctx context.Context, sentKey string, m model.Message) error {
	t, err := d.template(ctx, m.TenantID, m.TemplateID)
	if errors.Is(err, pg.ErrNotFound) {
		return d.mark(ctx, sentKey, "failed")
	} else if err != nil {
		return fmt.Errorf("load template %d: %w", m.TemplateID, err)
	}
	c := model.Contact{TenantID: m.TenantID, ExternalID: "test-contact", Attrs: map[string]any{}}
	if m.TestName != "" {
		first, _, _ := strings.Cut(m.TestName, " ")
		c.Attrs["name"], c.Attrs["first_name"] = m.TestName, first
	}
	tg := target{to: m.TestTo}
	switch m.Channel {
	case channel.Email:
		c.Email = m.TestTo
		c.Attrs["email"] = m.TestTo
	case channel.Push:
		tg.platform = "android"
		if providers.IsWebPushSubscription(m.TestTo) {
			tg.platform = "web"
		}
	default:
		c.Attrs["phone"] = m.TestTo
	}
	rendered, err := channel.RenderWith(t, c, vars(m, channel.Links{}))
	if err != nil {
		d.log("test send failed", m, err)
		return d.mark(ctx, sentKey, "failed")
	}
	if m.Channel == channel.Email {
		// Answers from a test to a bare address are checked and answered, never recorded.
		rendered.Content.AMP = d.prepareAMP(rendered.Content.AMP, "", tracking.Token{TenantID: m.TenantID, MessageID: m.ID,
			Channel: m.Channel, TemplateID: m.TemplateID}, 0, true)
	}
	var p providers.Provider
	if m.Channel == channel.Push && tg.platform == "web" {
		p, err = d.webPusher(ctx, m.TenantID)
	} else {
		var v sendVia
		v, err = d.senderVia(ctx, m.TenantID, m.Channel)
		p = v.p
	}
	if err != nil {
		d.log("test send failed", m, err)
		return d.mark(ctx, sentKey, "failed")
	}
	if err := d.limiters[m.Channel].Wait(ctx, "rl:"+m.Channel+":"+strconv.FormatInt(m.TenantID, 10)); err != nil {
		return err
	}
	if _, err := p.Send(ctx, providers.Outbound{TenantID: m.TenantID, MessageID: m.ID, Channel: m.Channel, To: tg.to,
		Platform: tg.platform, From: d.cfg.From, Rendered: rendered}); err != nil {
		d.log("test send failed", m, err)
		return d.mark(ctx, sentKey, "failed")
	}
	return d.mark(ctx, sentKey, "sent")
}

// prepareAMP finishes a rendered AMP part for one recipient: form and poll placeholders become
// signed post-back links (tracking.AMPToken), and with tracking on, links are tracked and the
// open pixel added. Without a server key the forms can't post back, and a part over the size
// clients show would be dropped by them, so in both cases the AMP part is left out.
func (d *Dispatcher) prepareAMP(doc, token string, tok tracking.Token, contact uint32, test bool) string {
	if doc == "" {
		return ""
	}
	if d.cfg.Tracking == nil {
		if len(amp.Blocks(doc)) > 0 || strings.Contains(doc, amp.ResultsPrefix) {
			return ""
		}
		return doc
	}
	exp := d.now().Add(tracking.AMPTTL).Unix()
	doc = amp.Fill(doc, func(kind, block string) string {
		return d.cfg.Tracking.AMPURL(d.cfg.Tracking.EncodeAMP(tracking.AMPToken{Token: tok, ContactID: contact, Block: block, Exp: exp, Test: test}), kind)
	})
	if token != "" {
		doc = d.cfg.Tracking.InstrumentAMP(doc, token)
	}
	if len(doc) > amp.MaxBytes {
		if d.cfg.Log != nil {
			d.cfg.Log.Warn("AMP part left out: larger than 200 KB once personalised", "tenant", tok.TenantID, "message", tok.MessageID)
		}
		return ""
	}
	return doc
}

func (d *Dispatcher) log(msg string, m model.Message, err error) {
	if d.cfg.Log != nil {
		d.cfg.Log.Warn(msg, "tenant", m.TenantID, "message", m.ID, "channel", m.Channel, "err", err)
	}
}
