package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/assets"
	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/delivery"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/providers"
	"pulse/internal/store/pg"
	"pulse/internal/tracking"
)

func TestLimiter(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	now := time.UnixMilli(1_000_000)
	l := &Limiter{RDB: rdb, Rate: 10, Burst: 2, Now: func() time.Time { return now }}
	ctx := context.Background()

	for i := 0; i < 2; i++ { // burst
		if d, err := l.Reserve(ctx, "k"); err != nil || d != 0 {
			t.Fatalf("reserve %d: wait %v, err %v; want immediate", i, d, err)
		}
	}
	if d, _ := l.Reserve(ctx, "k"); d != 100*time.Millisecond {
		t.Errorf("third reserve: wait %v, want 100ms at 10/s", d)
	}
	now = now.Add(100 * time.Millisecond) // one token refilled
	if d, _ := l.Reserve(ctx, "k"); d != 0 {
		t.Errorf("after refill: wait %v, want 0", d)
	}
	if d, _ := l.Reserve(ctx, "other-tenant"); d != 0 {
		t.Errorf("separate key should have its own bucket, got wait %v", d)
	}
}

// ---- fakes ----

type memStore struct {
	contacts  map[uint32]model.Contact
	templates map[int64]model.Template
	devices   map[uint32][]model.Device
	providers map[string]model.Provider // by channel
	byID      map[int64]model.Provider  // other providers a message may choose
	deleted   []string
	rules     delivery.Rules
	inbox     []model.InboxMessage
	files     map[int64][]byte // asset id -> bytes
	fileLoads int
}

func (m *memStore) AssetFile(_ context.Context, tenant, id int64) (model.Asset, []byte, error) {
	b, ok := m.files[id]
	if !ok {
		return model.Asset{}, nil, pg.ErrNotFound
	}
	m.fileLoads++
	return model.Asset{ID: id, TenantID: tenant, PublicKey: "akey", Filename: "guide.pdf", ContentType: "application/pdf", Size: int64(len(b))}, b, nil
}

func (m *memStore) DeliveryRules(context.Context, int64) (delivery.Rules, error) { return m.rules, nil }
func (m *memStore) VAPIDKeys(context.Context, int64) (string, []byte, error)     { return "", nil, nil }
func (m *memStore) InsertInbox(_ context.Context, im model.InboxMessage) error {
	m.inbox = append(m.inbox, im)
	return nil
}

func (m *memStore) ContactByID(_ context.Context, _ int64, id uint32) (model.Contact, error) {
	if c, ok := m.contacts[id]; ok {
		return c, nil
	}
	return model.Contact{}, pg.ErrNotFound
}

func (m *memStore) Template(_ context.Context, _, id int64) (model.Template, error) {
	if t, ok := m.templates[id]; ok {
		return t, nil
	}
	return model.Template{}, pg.ErrNotFound
}

func (m *memStore) ContactDevices(_ context.Context, _ int64, c uint32) ([]model.Device, error) {
	return m.devices[c], nil
}

func (m *memStore) DeleteDevice(_ context.Context, _ int64, token string) error {
	m.deleted = append(m.deleted, token)
	return nil
}

func (m *memStore) DefaultProvider(_ context.Context, _ int64, ch string) (model.Provider, []byte, error) {
	if p, ok := m.providers[ch]; ok {
		return p, nil, nil
	}
	return model.Provider{}, nil, pg.ErrNotFound
}

func (m *memStore) ProviderToSend(_ context.Context, _ int64, id int64) (model.Provider, []byte, error) {
	if p, ok := m.byID[id]; ok {
		return p, nil, nil
	}
	return model.Provider{}, nil, pg.ErrNotFound
}

type memSink struct {
	msgs     []string // "channel:recipient"
	payloads []string
}

func (s *memSink) RecordSandbox(_ context.Context, _ int64, ch, to, _ string, payload []byte) error {
	s.msgs = append(s.msgs, ch+":"+to)
	s.payloads = append(s.payloads, string(payload))
	return nil
}

type memMail struct {
	sent []providers.Outbound
	err  error
}

func (m *memMail) Send(_ context.Context, o providers.Outbound) (providers.Result, error) {
	if m.err != nil {
		return providers.Result{}, m.err
	}
	m.sent = append(m.sent, o)
	return providers.Result{ProviderMessageID: "smtp-" + o.MessageID}, nil
}

type memPub struct{ recs []*kgo.Record }

func (p *memPub) Publish(_ context.Context, recs ...*kgo.Record) error {
	p.recs = append(p.recs, recs...)
	return nil
}

// events returns the names of reported outcome events, and the messages published to channel topics.
func (p *memPub) take() (events []string, messages []model.Message) {
	for _, r := range p.recs {
		if r.Topic == event.TopicRaw {
			var e event.Event
			_ = json.Unmarshal(r.Value, &e)
			events = append(events, e.Name)
			continue
		}
		var m model.Message
		_ = json.Unmarshal(r.Value, &m)
		messages = append(messages, m)
	}
	p.recs = nil
	return events, messages
}

type fixture struct {
	d    *Dispatcher
	pub  *memPub
	mail *memMail
	sink *memSink
	st   *memStore
}

func setup(t *testing.T) fixture {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	sink, mail, pub := &memSink{}, &memMail{}, &memPub{}
	st := &memStore{
		contacts: map[uint32]model.Contact{
			1: {ID: 1, ExternalID: "u1", Email: "u1@example.com", Attrs: map[string]any{"first_name": "Asha", "phone": "+919800000001"}},
			2: {ID: 2, ExternalID: "u2", Attrs: map[string]any{"phone": "+919800000000"}}, // "not on WhatsApp/RCS" in the sandbox
			3: {ID: 3, ExternalID: "u3", Attrs: map[string]any{"phone": "98000"}},         // not E.164
		},
		templates: map[int64]model.Template{
			1: {ID: 1, Channel: channel.Email, Subject: "Hi {{.Attrs.first_name}}", HTML: "<p>Hi</p>"},
			2: {ID: 2, Channel: channel.WhatsApp, Content: json.RawMessage(`{"template_name":"welcome","body_params":["{{.Attrs.first_name}}"]}`)},
			3: {ID: 3, Channel: channel.SMS, Content: json.RawMessage(`{"text":"Hi {{.Attrs.first_name}}"}`)},
			4: {ID: 4, Channel: channel.Push, Content: json.RawMessage(`{"title":"Hey","body":"Back in stock"}`)},
		},
		devices: map[uint32][]model.Device{1: {{Token: "tok-ok", Platform: "android"}, {Token: "dead-0000", Platform: "ios"}}},
		rules:   delivery.Default(),
		providers: map[string]model.Provider{
			channel.WhatsApp: {Kind: "sandbox_whatsapp", Config: map[string]string{"unreachable_suffix": "0000"}},
			channel.SMS:      {Kind: "sandbox_sms", Config: map[string]string{}},
			channel.Push:     {Kind: "sandbox_push", Config: map[string]string{"unreachable_suffix": "0000"}},
		},
	}
	d := New(st, rdb, pub, Config{From: "Pulse <noreply@pulse.local>", EmailDefault: mail, Providers: providers.Deps{Sandbox: sink}})
	return fixture{d: d, pub: pub, mail: mail, sink: sink, st: st}
}

func msgRecord(m model.Message) *kgo.Record {
	b, _ := json.Marshal(m)
	return &kgo.Record{Topic: channel.Topic(m.Channel), Value: b}
}

func TestEmailSentOnce(t *testing.T) {
	f := setup(t)
	rec := msgRecord(model.Message{ID: "9:send", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})
	for i := 0; i < 2; i++ { // redelivery must not resend
		if err := f.d.Handle(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.mail.sent) != 1 || f.mail.sent[0].Rendered.Subject != "Hi Asha" || f.mail.sent[0].To != "u1@example.com" {
		t.Fatalf("sent = %+v", f.mail.sent)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_sent" {
		t.Errorf("events = %v", evs)
	}
}

func TestWhatsAppFallsBackToSMSOnce(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wa := model.Message{ID: "7:wa", TenantID: 1, ContactID: 2, Channel: channel.WhatsApp, TemplateID: 2, FallbackTemplateID: 3}
	for i := 0; i < 2; i++ {
		if err := f.d.Handle(ctx, msgRecord(wa)); err != nil {
			t.Fatal(err)
		}
	}
	evs, msgs := f.pub.take()
	if len(evs) != 1 || evs[0] != "whatsapp_fallback" {
		t.Errorf("events = %v, want one whatsapp_fallback", evs)
	}
	if len(msgs) != 1 || msgs[0].Channel != channel.SMS || msgs[0].ID != "7:wa:sms" || msgs[0].TemplateID != 3 || !msgs[0].IsFallback {
		t.Fatalf("fallback messages = %+v", msgs)
	}
	// The SMS goes out through its own dispatch.
	if err := f.d.Handle(ctx, msgRecord(msgs[0])); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "sms_sent" {
		t.Errorf("sms events = %v", evs)
	}
	if len(f.sink.msgs) != 1 || f.sink.msgs[0] != "sms:+919800000000" {
		t.Errorf("sandbox inbox = %v", f.sink.msgs)
	}
}

func TestUnreachableWithoutFallbackFails(t *testing.T) {
	f := setup(t)
	_ = f.d.Handle(context.Background(), msgRecord(model.Message{ID: "a", TenantID: 1, ContactID: 2, Channel: channel.WhatsApp, TemplateID: 2}))
	if evs, msgs := f.pub.take(); len(evs) != 1 || evs[0] != "whatsapp_failed" || len(msgs) != 0 {
		t.Errorf("events = %v, messages = %v", evs, msgs)
	}
	// A contact without a valid phone is unreachable too.
	_ = f.d.Handle(context.Background(), msgRecord(model.Message{ID: "b", TenantID: 1, ContactID: 3, Channel: channel.SMS, TemplateID: 3}))
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "sms_failed" {
		t.Errorf("events = %v", evs)
	}
}

func TestReceiptTriggersFallbackOnce(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wa := model.Message{ID: "8:wa", TenantID: 1, ContactID: 1, Channel: channel.WhatsApp, TemplateID: 2, FallbackTemplateID: 3}
	if err := f.d.Handle(ctx, msgRecord(wa)); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "whatsapp_sent" {
		t.Fatalf("events = %v", evs)
	}
	receipt := func(status string, unreachable bool) *kgo.Record {
		b, _ := json.Marshal(ReceiptRecord{TenantID: 1, Receipt: providers.Receipt{
			ProviderMessageID: "sandbox-8:wa", Status: status, Unreachable: unreachable, Reason: "not on WhatsApp"}})
		return &kgo.Record{Topic: TopicReceipts, Value: b}
	}
	for i := 0; i < 2; i++ { // providers repeat webhooks
		if err := f.d.Handle(ctx, receipt("failed", true)); err != nil {
			t.Fatal(err)
		}
	}
	evs, msgs := f.pub.take()
	if len(msgs) != 1 || msgs[0].ID != "8:wa:sms" || len(evs) != 1 || evs[0] != "whatsapp_fallback" {
		t.Fatalf("after failed receipts: events %v, messages %+v", evs, msgs)
	}
	if err := f.d.Handle(ctx, receipt("delivered", false)); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "whatsapp_delivered" {
		t.Errorf("delivered receipt events = %v", evs)
	}
}

func TestPushDropsDeadTokens(t *testing.T) {
	f := setup(t)
	if err := f.d.Handle(context.Background(), msgRecord(model.Message{ID: "p", TenantID: 1, ContactID: 1, Channel: channel.Push, TemplateID: 4})); err != nil {
		t.Fatal(err)
	}
	// One live device is enough to send; the dead app token means the app was uninstalled there.
	if evs, _ := f.pub.take(); len(evs) != 2 || !slices.Contains(evs, "push_sent") || !slices.Contains(evs, "app_uninstalled") {
		t.Errorf("events = %v", evs)
	}
	if len(f.st.deleted) != 1 || f.st.deleted[0] != "dead-0000" {
		t.Errorf("deleted tokens = %v", f.st.deleted)
	}
}

// A temporary provider failure parks the message instead of returning an error (which would
// hold up the partition); it is re-published when due, with the tries so far, and sent once.
func TestTransientErrorsRetryLater(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.d.now = func() time.Time { return now }
	f.mail.err = errors.New("connection reset")
	rec := msgRecord(model.Message{ID: "t", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})
	if err := f.d.Handle(ctx, rec); err != nil {
		t.Fatalf("a temporary failure must not fail the record (it would block the partition): %v", err)
	}
	if evs, msgs := f.pub.take(); len(evs) != 0 || len(msgs) != 0 {
		t.Fatalf("after the first failure: events %v, messages %v", evs, msgs)
	}
	f.d.releaseRetries(ctx) // not due yet
	if len(f.pub.recs) != 0 {
		t.Fatal("released before the backoff")
	}
	now = now.Add(RetryBackoff[0])
	f.d.releaseRetries(ctx)
	if len(f.pub.recs) != 1 || f.pub.recs[0].Topic != channel.Topic(channel.Email) || triesOf(f.pub.recs[0]) != 1 {
		t.Fatalf("released = %+v", f.pub.recs)
	}
	retry := f.pub.recs[0]
	f.pub.recs = nil
	f.d.releaseRetries(ctx) // released once only
	if len(f.pub.recs) != 0 {
		t.Fatal("released twice")
	}
	f.mail.err = nil
	for i := 0; i < 2; i++ { // the retry isn't a duplicate, and a redelivery of it doesn't send again
		if err := f.d.Handle(ctx, retry); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.mail.sent) != 1 {
		t.Errorf("sent %d times, want 1", len(f.mail.sent))
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_sent" {
		t.Errorf("events = %v", evs)
	}
}

// When every try fails, the message is given up as <channel>_failed with the reason.
func TestRetriesGiveUpAsFailed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.d.now = func() time.Time { return now }
	f.mail.err = errors.New("smtp: 451 try again later")
	rec := msgRecord(model.Message{ID: "g", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})
	tries := 0
	for ; tries < 20; tries++ {
		if err := f.d.Handle(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if len(f.pub.recs) > 0 {
			break // the outcome event
		}
		now = now.Add(2 * time.Hour)
		f.d.releaseRetries(ctx)
		if len(f.pub.recs) != 1 {
			t.Fatalf("try %d: released %d records", tries+1, len(f.pub.recs))
		}
		rec, f.pub.recs = f.pub.recs[0], nil
	}
	if tries+1 != len(RetryBackoff)+1 {
		t.Errorf("gave up after %d tries, want %d", tries+1, len(RetryBackoff)+1)
	}
	if len(f.pub.recs) != 1 {
		t.Fatalf("records = %d", len(f.pub.recs))
	}
	var ev event.Event
	_ = json.Unmarshal(f.pub.recs[0].Value, &ev)
	reason, _ := ev.Props["reason"].(string)
	if ev.Name != "email_failed" || ev.ID != "msg:g:email_failed" || !strings.Contains(reason, "gave up after 8 tries") ||
		!strings.Contains(reason, "451 try again later") {
		t.Errorf("event = %s %s, reason %q", ev.Name, ev.ID, reason)
	}
	if n, _ := f.d.rdb.ZCard(ctx, retryKey).Result(); n != 0 {
		t.Errorf("%d messages still parked", n)
	}
	if err := f.d.Handle(ctx, rec); err != nil || len(f.pub.recs) != 1 { // done: a redelivery does nothing
		t.Errorf("redelivery after giving up: err %v, records %d", err, len(f.pub.recs))
	}
}

// Permanent failures are recorded at once, never parked.
func TestPermanentErrorsAreNotRetried(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.mail.err = providers.Permanent("550 mailbox does not exist")
	if err := f.d.Handle(ctx, msgRecord(model.Message{ID: "p", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_failed" {
		t.Errorf("events = %v", evs)
	}
	if n, _ := f.d.rdb.ZCard(ctx, retryKey).Result(); n != 0 {
		t.Errorf("a permanent failure was parked")
	}
}

func TestRetryDelay(t *testing.T) {
	plain := errors.New("HTTP 503")
	for i, want := range RetryBackoff {
		if got, ok := retryDelay(i+1, plain); !ok || got != want {
			t.Errorf("try %d: wait %v %v, want %v", i+1, got, ok, want)
		}
	}
	if _, ok := retryDelay(len(RetryBackoff)+1, plain); ok {
		t.Error("retries must run out")
	}
	var total time.Duration
	for _, w := range RetryBackoff {
		total += w
	}
	if total < time.Hour || total > 2*time.Hour {
		t.Errorf("retries span %v, want one to two hours", total)
	}
	ra := func(d time.Duration) error { return &providers.RetryAfterError{Err: plain, After: d} }
	cases := []struct {
		tries int
		err   error
		want  time.Duration
	}{
		{1, ra(5 * time.Minute), 5 * time.Minute},   // longer than the backoff: the provider wins
		{1, ra(10 * time.Second), 30 * time.Second}, // shorter: the backoff wins
		{1, ra(5 * time.Hour), time.Hour},           // capped
		{1, fmt.Errorf("send email: %w", ra(2*time.Minute)), 2 * time.Minute},
	}
	for _, c := range cases {
		if got, _ := retryDelay(c.tries, c.err); got != c.want {
			t.Errorf("retryDelay(%d, %v) = %v, want %v", c.tries, c.err, got, c.want)
		}
	}
}

// A provider's Retry-After holds the message at least that long.
func TestRetryAfterIsRespected(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.d.now = func() time.Time { return now }
	f.mail.err = providers.StatusError("ses", 429, "slow down", http.Header{"Retry-After": {"600"}})
	if err := f.d.Handle(ctx, msgRecord(model.Message{ID: "r", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})); err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Minute)
	f.d.releaseRetries(ctx)
	if len(f.pub.recs) != 0 {
		t.Fatal("released before Retry-After")
	}
	now = now.Add(time.Minute)
	f.d.releaseRetries(ctx)
	if len(f.pub.recs) != 1 {
		t.Fatalf("released %d after Retry-After", len(f.pub.recs))
	}
}

// An internal notification ("email to user") retries the same way.
func TestNotificationRetriesThenSends(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.d.now = func() time.Time { return now }
	f.mail.err = errors.New("timeout")
	m := model.Message{ID: "n", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1, To: []string{"ops@example.com"}}
	if err := f.d.Handle(ctx, msgRecord(m)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	f.d.releaseRetries(ctx)
	if len(f.pub.recs) != 1 {
		t.Fatalf("released %d", len(f.pub.recs))
	}
	retry := f.pub.recs[0]
	f.pub.recs, f.mail.err = nil, nil
	if err := f.d.Handle(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "notification_sent" {
		t.Errorf("events = %v", evs)
	}
}

func TestMisconfigurationsReportFailure(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// SMS template sent on the WhatsApp channel.
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "x", TenantID: 1, ContactID: 1, Channel: channel.WhatsApp, TemplateID: 3}))
	// No RCS provider configured.
	f.st.templates[5] = model.Template{ID: 5, Channel: channel.RCS, Content: json.RawMessage(`{"text":"hi"}`)}
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "y", TenantID: 1, ContactID: 1, Channel: channel.RCS, TemplateID: 5}))
	if evs, _ := f.pub.take(); len(evs) != 2 || evs[0] != "whatsapp_failed" || evs[1] != "rcs_failed" {
		t.Errorf("events = %v", evs)
	}
}

func TestOptedOutContactsAreSuppressed(t *testing.T) {
	f := setup(t)
	c := f.st.contacts[1]
	c.Attrs["optout_email"] = true
	f.st.contacts[1] = c
	if err := f.d.Handle(context.Background(), msgRecord(model.Message{ID: "u", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.sent) != 0 {
		t.Fatal("email sent to an unsubscribed contact")
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_suppressed" {
		t.Errorf("events = %v, want email_suppressed", evs)
	}
	// Opting out of email doesn't stop SMS.
	_ = f.d.Handle(context.Background(), msgRecord(model.Message{ID: "s", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3}))
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "sms_sent" {
		t.Errorf("sms events = %v", evs)
	}
}

func TestFrequencyCapAndTransactionalBypass(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.st.rules.Caps[channel.SMS] = delivery.Cap{Max: 1, Hours: 24}
	send := func(id string, tpl int64) {
		if err := f.d.Handle(ctx, msgRecord(model.Message{ID: id, TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: tpl})); err != nil {
			t.Fatal(err)
		}
	}
	send("a", 3)
	send("b", 3)
	if evs, _ := f.pub.take(); len(evs) != 2 || evs[0] != "sms_sent" || evs[1] != "sms_capped" {
		t.Errorf("events = %v, want sent then capped", evs)
	}
	// A transactional template (an OTP) still goes out.
	f.st.templates[6] = model.Template{ID: 6, Channel: channel.SMS, Transactional: true, Content: json.RawMessage(`{"text":"Your code is 1234"}`)}
	send("otp", 6)
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "sms_sent" {
		t.Errorf("transactional events = %v", evs)
	}
}

func TestQuietHoursDeferAndRelease(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.st.rules.Quiet.Enabled = true                         // 21:00-09:00 IST on SMS, WhatsApp, RCS, push
	night := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC) // 23:30 IST
	f.d.now = func() time.Time { return night }
	if err := f.d.Handle(ctx, msgRecord(model.Message{ID: "q", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3})); err != nil {
		t.Fatal(err)
	}
	if evs, msgs := f.pub.take(); len(evs) != 1 || evs[0] != "sms_deferred" || len(msgs) != 0 || len(f.sink.msgs) != 0 {
		t.Fatalf("at night: events %v, sent %v", evs, f.sink.msgs)
	}
	f.d.releaseDue(ctx) // still night: nothing released
	if _, msgs := f.pub.take(); len(msgs) != 0 {
		t.Fatal("released before quiet hours ended")
	}
	f.d.now = func() time.Time { return night.Add(10 * time.Hour) } // 09:30 IST
	f.d.releaseDue(ctx)
	_, msgs := f.pub.take()
	if len(msgs) != 1 || msgs[0].ID != "q" {
		t.Fatalf("morning release = %+v", msgs)
	}
	if err := f.d.Handle(ctx, msgRecord(msgs[0])); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "sms_sent" {
		t.Errorf("after release: %v", evs)
	}
}

// An email goes out with its generated text part (unsubscribe link added, links direct) and its
// attachments, loaded once and cached; a deleted attachment fails the message.
func TestEmailTextPartAndAttachments(t *testing.T) {
	f := setup(t)
	box, _ := auth.NewBox(strings.Repeat("cd", 32))
	f.d.cfg.Box = box
	f.d.cfg.Tracking = tracking.New(box, strings.Repeat("cd", 32), "https://pulse.test")
	f.st.files = map[int64][]byte{7: []byte("%PDF-1.7 guide")}
	f.st.templates[5] = model.Template{ID: 5, Channel: channel.Email, Subject: "Guide",
		HTML: `<p>Hi {{.Attrs.first_name}}, read <a href="https://shop.test/guide">the guide</a>.</p>`, Content: json.RawMessage(`{"attachments":[7]}`)}
	ctx := context.Background()
	for _, id := range []string{"att:1", "att:2"} {
		if err := f.d.Handle(ctx, msgRecord(model.Message{ID: id, TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 5})); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.mail.sent) != 2 || f.st.fileLoads != 1 {
		t.Fatalf("sent %d, file loads %d (want 2 and 1: cached)", len(f.mail.sent), f.st.fileLoads)
	}
	o := f.mail.sent[0]
	text := o.Rendered.Content.Text
	if !strings.HasPrefix(text, "Hi Asha, read the guide (https://shop.test/guide).") || !strings.Contains(text, "Unsubscribe: https://pulse.test/u/") {
		t.Errorf("text part = %q", text)
	}
	if !strings.Contains(o.Rendered.HTML, "/t/c/") || strings.Contains(text, "/t/c/") || strings.Contains(text, "/t/o/") {
		t.Error("the HTML links should be tracked and the text links direct, with no pixel")
	}
	if len(o.Attachments) != 1 || string(o.Attachments[0].Data) != "%PDF-1.7 guide" || o.Attachments[0].Filename != "guide.pdf" {
		t.Fatalf("attachments = %+v", o.Attachments)
	}
	u := o.Attachments[0].URL
	if !strings.HasPrefix(u, "https://pulse.test/a/akey/download?t=") {
		t.Fatalf("attachment link = %s", u)
	}
	tok, err := assets.Open(box, strings.TrimPrefix(u, "https://pulse.test/a/akey/download?t="), time.Now())
	if err != nil || tok.Purpose != assets.PurposeAttach || tok.AssetID != 7 || tok.TenantID != 1 {
		t.Errorf("attachment token = %+v, %v", tok, err)
	}
	f.pub.take()

	f.st.templates[6] = model.Template{ID: 6, Channel: channel.Email, Subject: "x", HTML: "<p>x</p>", Content: json.RawMessage(`{"attachments":[8]}`)}
	if err := f.d.Handle(ctx, msgRecord(model.Message{ID: "att:3", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 6})); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_failed" {
		t.Errorf("events = %v, want email_failed for the deleted attachment", evs)
	}
}

func TestSMSBounceMarksAndSuppresses(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.st.providers[channel.SMS] = model.Provider{Kind: "sandbox_sms", Config: map[string]string{"unreachable_suffix": "0001"}}
	if err := f.d.Handle(ctx, msgRecord(model.Message{ID: "b1", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3})); err != nil {
		t.Fatal(err)
	}
	if len(f.pub.recs) != 1 {
		t.Fatalf("published %d records", len(f.pub.recs))
	}
	var e event.Event
	_ = json.Unmarshal(f.pub.recs[0].Value, &e)
	if e.Name != "sms_failed" || e.Props["bounce"] != true || len(e.Attrs) != 0 { // the bounce rules set sms_bounced (profile)
		t.Errorf("bounce event = %+v", e)
	}
	f.pub.take()
	// The profile now says the number bounced: the next SMS is suppressed.
	c := f.st.contacts[1]
	c.Attrs["sms_bounced"] = true
	f.st.contacts[1] = c
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "b2", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3}))
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "sms_suppressed" {
		t.Errorf("events = %v, want sms_suppressed", evs)
	}
	// A missing number is not a bounce.
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "b3", TenantID: 1, ContactID: 3, Channel: channel.SMS, TemplateID: 3}))
	var e3 event.Event
	_ = json.Unmarshal(f.pub.recs[0].Value, &e3)
	if e3.Name != "sms_failed" || e3.Attrs != nil {
		t.Errorf("invalid number event = %+v", e3)
	}
}

func TestSMSTrackedLinks(t *testing.T) {
	f := setup(t)
	box, _ := auth.NewBox("9f1c2b7e4a6d8053c1e2f4a7b9d0c3e5f6a8b1c2d4e7f9a0b3c5d6e8f1a2b4c6")
	f.d.cfg.Tracking = tracking.New(box, "9f1c2b7e4a6d8053c1e2f4a7b9d0c3e5f6a8b1c2d4e7f9a0b3c5d6e8f1a2b4c6", "https://p.example")
	f.st.templates[7] = model.Template{ID: 7, Channel: channel.SMS, Content: json.RawMessage(`{"text":"Hi {{.Attrs.first_name}}, sale: https://shop.in/x","track_links":true}`)}
	f.st.templates[8] = model.Template{ID: 8, Channel: channel.SMS, Content: json.RawMessage(`{"text":"Untracked https://shop.in/x"}`)}
	ctx := context.Background()
	for _, id := range []int64{7, 8} {
		if err := f.d.Handle(ctx, msgRecord(model.Message{ID: fmt.Sprint("l", id), TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: id})); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sink.payloads) != 2 {
		t.Fatalf("sent %d", len(f.sink.payloads))
	}
	tracked, plain := f.sink.payloads[0], f.sink.payloads[1]
	if !strings.Contains(tracked, "Hi Asha, sale: https://p.example/t/s/") || strings.Contains(tracked, "shop.in") {
		t.Errorf("tracked text = %s", tracked)
	}
	if !strings.Contains(plain, "Untracked https://shop.in/x") {
		t.Errorf("untracked text = %s", plain)
	}
	i := strings.Index(tracked, "/t/s/") + 5
	tok, dest, err := tracking.ResolveShort(ctx, f.d.rdb, tracked[i:i+11])
	if err != nil || dest != "https://shop.in/x" || tok.MessageID != "l7" || tok.ExternalID != "u1" || tok.Channel != "sms" {
		t.Errorf("resolve = %+v %q %v", tok, dest, err)
	}
}

// A local-time broadcast message waits for its clock time in the contact's zone (their timezone
// attribute, else the zone the broadcast was scheduled in), then goes.
func TestLocalTimeHold(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	c := f.st.contacts[1]
	c.Attrs["timezone"] = "America/New_York"
	f.st.contacts[1] = c
	early := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC) // 05:00 in New York, 15:30 in India
	f.d.now = func() time.Time { return early }
	m := model.Message{ID: "bc1:1:1", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1, LocalAt: "2026-11-12T09:00", LocalZone: "Asia/Kolkata"}
	if err := f.d.Handle(ctx, msgRecord(m)); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_deferred" || len(f.mail.sent) != 0 {
		t.Fatalf("before 9:00 in New York: %v, sent %d", evs, len(f.mail.sent))
	}
	f.d.now = func() time.Time { return early.Add(4*time.Hour + time.Minute) } // 09:01 in New York
	f.d.releaseDue(ctx)
	_, msgs := f.pub.take()
	if len(msgs) != 1 {
		t.Fatalf("released %v", msgs)
	}
	if err := f.d.Handle(ctx, msgRecord(msgs[0])); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_sent" {
		t.Errorf("after release: %v", evs)
	}

	// No zone of their own: the broadcast's zone, where 9:00 has passed, so it goes right away.
	m.ID, m.ContactID = "bc1:1:2", 2
	c2 := f.st.contacts[2]
	c2.Email = "u2@example.com"
	f.st.contacts[2] = c2
	if err := f.d.Handle(ctx, msgRecord(m)); err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.pub.take(); len(evs) != 1 || evs[0] != "email_sent" {
		t.Errorf("no zone, time passed in India: %v", evs)
	}
}

func TestChosenProvider(t *testing.T) {
	f := setup(t)
	f.st.providers[channel.SMS] = model.Provider{ID: 1, Channel: channel.SMS, Name: "Default SMS", Kind: "sandbox_sms", Config: map[string]string{}}
	f.st.byID = map[int64]model.Provider{
		7: {ID: 7, Channel: channel.SMS, Name: "Backup SMS", Kind: "sandbox_sms", Config: map[string]string{}},
		8: {ID: 8, Channel: channel.WhatsApp, Name: "A WhatsApp", Kind: "sandbox_whatsapp", Config: map[string]string{}},
	}
	ctx := context.Background()
	for _, c := range []struct {
		chosen int64
		want   string
	}{{0, "Default SMS"}, {7, "Backup SMS"}, {8, "Default SMS"}, {99, "Default SMS"}} { // another channel's, or removed: the default
		v, err := f.d.senderVia(ctx, 1, channel.SMS, c.chosen)
		if err != nil || v.name != c.want {
			t.Errorf("chosen %d: via %q, %v; want %q", c.chosen, v.name, err, c.want)
		}
	}
	// The SMS fallback of a WhatsApp message goes through the SMS default, not the WhatsApp choice.
	m := model.Message{ID: "x", TenantID: 1, ContactID: 2, Channel: channel.WhatsApp, TemplateID: 2, FallbackTemplateID: 3, ProviderID: 8}
	if err := f.d.fallback(ctx, m, "unreachable"); err != nil {
		t.Fatal(err)
	}
	var sms model.Message
	for _, r := range f.pub.recs {
		if r.Topic == channel.Topic(channel.SMS) {
			_ = json.Unmarshal(r.Value, &sms)
		}
	}
	if sms.Channel != channel.SMS || sms.ProviderID != 0 {
		t.Errorf("fallback = %+v", sms)
	}
}
