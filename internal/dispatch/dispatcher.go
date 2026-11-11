// Package dispatch delivers messages from the channel topics (messages.email|sms|whatsapp|rcs|push).
//
// Per message: skip if already sent, resolve the recipient (email, phone, push tokens), render the
// template, rate limit per workspace and channel, send through the workspace's default provider,
// then report the outcome as an event (email_sent, whatsapp_fallback, ...) so journeys, segments and
// the dashboard see deliveries. WhatsApp and RCS messages that can't reach the contact fall back to
// an SMS template, exactly once, whether the failure is immediate or arrives later as a receipt.
package dispatch

import (
	"maps"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/assets"
	"pulse/internal/attr"
	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/delivery"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/providers"
	"pulse/internal/replies"
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
	senders   map[string]cached[providers.Provider]
	rules     map[int64]cached[delivery.Rules]
	files     map[[2]int64]cached[attachment] // attachment bytes; assets never change their file
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
	rates := map[string]float64{}
	for ch, r := range DefaultRates {
		rates[ch] = r
	}
	for ch, r := range cfg.Rates {
		rates[ch] = r
	}
	d := &Dispatcher{cfg: cfg, store: store, rdb: rdb, limiters: map[string]*Limiter{}, pub: pub, now: time.Now,
		templates: map[[2]int64]cached[model.Template]{}, senders: map[string]cached[providers.Provider]{},
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
	return d.Send(ctx, m)
}

type target struct{ to, platform string }

// Send delivers one message. Returning an error means "retry"; final outcomes are reported as events.
func (d *Dispatcher) Send(ctx context.Context, m model.Message) error {
	sentKey := fmt.Sprintf("sent:%d:%s", m.TenantID, m.ID)
	if n, err := d.rdb.Exists(ctx, sentKey).Result(); err != nil {
		return err
	} else if n == 1 {
		return nil // already handled (redelivered record or re-run journey step)
	}
	c, err := d.store.ContactByID(ctx, m.TenantID, m.ContactID)
	if err != nil {
		return fmt.Errorf("load contact: %w", err)
	}
	if len(m.To) > 0 {
		return d.sendInternal(ctx, sentKey, m, c)
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
	var token string
	var headers map[string]string
	unsub := ""
	if m.Channel == channel.Email && d.cfg.Tracking != nil {
		token = d.cfg.Tracking.Encode(tracking.Token{TenantID: m.TenantID, ExternalID: c.ExternalID, MessageID: m.ID,
			Channel: m.Channel, TemplateID: m.TemplateID, JourneyID: m.JourneyID,
			BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant})
		unsub, headers = d.cfg.Tracking.UnsubscribeURL(token), d.cfg.Tracking.Headers(token)
		if err := d.replyTo(ctx, m, token, headers); err != nil {
			return err
		}
	}
	prefs := ""
	if token != "" {
		prefs = d.cfg.Tracking.PreferencesURL(token)
	}
	rendered, err := channel.RenderFor(t, c, channel.Links{Unsubscribe: unsub, Preferences: prefs})
	if err != nil {
		return d.finish(ctx, sentKey, m, c, "failed", err.Error())
	}
	if token != "" {
		rendered.HTML = d.cfg.Tracking.Instrument(rendered.HTML, token)
		rendered.Content.Text = d.cfg.Tracking.InstrumentText(rendered.Content.Text, token)
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
	if m.Channel == channel.Inbox {
		p = &inboxSender{store: d.store, contact: c, msg: m}
	}
	for _, tg := range targets {
		if p != nil {
			break
		}
		if m.Channel == channel.Push && providers.IsWebPushSubscription(tg.to) {
			continue
		}
		var err error
		p, err = d.sender(ctx, m.TenantID, m.Channel)
		if errors.Is(err, pg.ErrNotFound) {
			return d.finish(ctx, sentKey, m, c, "failed", "no "+m.Channel+" provider configured")
		} else if err != nil {
			return err
		}
		break
	}
	if err := d.limiters[m.Channel].Wait(ctx, "rl:"+m.Channel+":"+strconv.FormatInt(m.TenantID, 10)); err != nil {
		return err
	}

	var sent, unreachable int
	var lastErr error
	for _, tg := range targets {
		via := p
		if m.Channel == channel.Push && providers.IsWebPushSubscription(tg.to) {
			wp, err := d.webPusher(ctx, m.TenantID)
			if err != nil {
				lastErr = err
				continue
			}
			via = wp
		}
		res, err := via.Send(ctx, providers.Outbound{
			TenantID: m.TenantID, MessageID: m.ID, Channel: m.Channel, To: tg.to, Platform: tg.platform,
			From: d.cfg.From, Rendered: rendered, Headers: headers, Attachments: files,
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
			}
		case errors.Is(err, providers.ErrPermanent):
			lastErr = err
		default:
			if len(targets) == 1 {
				return fmt.Errorf("send %s: %w", m.Channel, err) // transient: retry the record
			}
			lastErr = err
		}
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
func (d *Dispatcher) sendInternal(ctx context.Context, sentKey string, m model.Message, c model.Contact) error {
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
	rendered, err := channel.RenderFor(t, c, channel.Links{})
	if err != nil {
		return done("failed", err.Error())
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
	var last error
	for i, to := range m.To {
		_, err := p.Send(ctx, providers.Outbound{TenantID: m.TenantID, MessageID: m.ID + ":" + strconv.Itoa(i), Channel: channel.Email,
			To: to, From: d.cfg.From, Rendered: rendered})
		if err != nil {
			last = err
			continue
		}
		sent++
	}
	switch {
	case sent > 0:
		return done("sent", "")
	case len(m.To) == 1 && !errors.Is(last, providers.ErrPermanent) && !errors.Is(last, providers.ErrUnreachable):
		return fmt.Errorf("send notification: %w", last) // transient: retry
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
		BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant}
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
	props := map[string]any{"message_id": m.ID, "template_id": m.TemplateID, "journey_id": m.JourneyID}
	if reason != "" {
		props["reason"] = reason
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

func capKey(m model.Message) string {
	return fmt.Sprintf("cap:%d:%d:%s", m.TenantID, m.ContactID, m.Channel)
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
	b, _ := json.Marshal(m)
	if err := d.rdb.ZAdd(ctx, deferredKey, redis.Z{Score: float64(until.UnixMilli()), Member: string(b)}).Err(); err != nil {
		return err
	}
	rec, err := d.eventRecord("msg:"+m.ID+":deferred", m, c, "deferred", "quiet hours until "+until.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	return d.pub.Publish(ctx, rec)
}

// RunDeferred releases held messages whose quiet hours are over, until ctx is done.
func (d *Dispatcher) RunDeferred(ctx context.Context) error {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		d.releaseDue(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// releaseDue re-publishes every held message that may go now. ZREM decides which worker owns each.
func (d *Dispatcher) releaseDue(ctx context.Context) {
	due, err := d.rdb.ZRangeByScore(ctx, deferredKey, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(d.now().UnixMilli(), 10), Count: 500}).Result()
	if err != nil {
		return
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
		rec, err := d.eventRecordWith("rcpt:"+m.ID+":"+rr.Receipt.Status, m, c, rr.Receipt.Status, rr.Receipt.Reason, bounce)
		if err != nil {
			return err
		}
		return d.pub.Publish(ctx, rec)
	}
	return nil
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
	cfg, sealed, err := d.store.DefaultProvider(ctx, tenant, ch)
	if errors.Is(err, pg.ErrNotFound) && ch == channel.Email && d.cfg.EmailDefault != nil {
		return d.cfg.EmailDefault, nil
	} else if err != nil {
		return nil, err
	}
	secrets, err := OpenSecrets(d.cfg.Box, sealed)
	if err != nil {
		return nil, err
	}
	return providers.New(cfg.Kind, d.cfg.Providers, cfg.Config, secrets)
}

// inboxSender "delivers" an inbox message by storing it, carrying the message's ids so reads and
// clicks count in its journey or broadcast report.
type inboxSender struct {
	store   Store
	contact model.Contact
	msg     model.Message
}

func (s *inboxSender) Send(ctx context.Context, o providers.Outbound) (providers.Result, error) {
	c := o.Rendered.Content
	days := c.ExpiresDays
	if days == 0 {
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
		wp.HTTP = &http.Client{Timeout: 15 * time.Second}
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
