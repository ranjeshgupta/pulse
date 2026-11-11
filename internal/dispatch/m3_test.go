package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/providers"
)

// outcomes decodes the outcome events published so far.
func outcomes(p *memPub) []event.Event {
	var out []event.Event
	for _, r := range p.recs {
		if r.Topic == event.TopicRaw {
			var e event.Event
			_ = json.Unmarshal(r.Value, &e)
			out = append(out, e)
		}
	}
	return out
}

func TestDeadlineDropsLateMessages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC) // 11:30 in India (outside quiet hours)
	f.d.now = func() time.Time { return now }
	past, future := now.Add(-time.Minute), now.Add(time.Hour)

	if err := f.d.Send(ctx, model.Message{ID: "late", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3, Deadline: &past}); err != nil {
		t.Fatal(err)
	}
	evs := outcomes(f.pub)
	if len(evs) != 1 || evs[0].Name != "sms_failed" || evs[0].Props["reason"] != model.DeadlineReason || evs[0].Props["deadline"] == nil {
		t.Fatalf("late message: events %+v, want sms_failed with reason deadline", evs)
	}
	if len(f.sink.msgs) != 0 {
		t.Fatalf("a late message was sent: %v", f.sink.msgs)
	}
	f.pub.recs = nil
	if err := f.d.Send(ctx, model.Message{ID: "ontime", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3, Deadline: &future}); err != nil {
		t.Fatal(err)
	}
	if ev, _ := f.pub.take(); len(ev) != 1 || ev[0] != "sms_sent" || len(f.sink.msgs) != 1 {
		t.Fatalf("on-time message: events %v, sent %v", ev, f.sink.msgs)
	}

	// A local-time message's deadline is a clock time in the contact's zone.
	f.st.contacts[1] = model.Contact{ID: 1, ExternalID: "u1", Attrs: map[string]any{"phone": "+919800000001", "timezone": "Asia/Kolkata"}}
	// It is 11:30 in India: an 11:00 deadline there has passed (it wouldn't have in UTC).
	m := model.Message{ID: "local", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3,
		LocalAt: "2026-11-11T09:00", LocalZone: "UTC", DeadlineLocal: "2026-11-11T11:00"}
	if err := f.d.Send(ctx, m); err != nil {
		t.Fatal(err)
	}
	if ev, _ := f.pub.take(); len(ev) != 1 || ev[0] != "sms_failed" {
		t.Fatalf("local deadline: events %v, want sms_failed", ev)
	}
}

func TestHeldPastDeadlineIsDropped(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.d.now = func() time.Time { return now }
	dl := now.Add(30 * time.Minute)
	p := f.st.providers[channel.SMS]
	p.ID, p.Name, p.Limits = 7, "SMS sandbox", model.ProviderLimits{DailyQuota: 1}
	f.st.providers[channel.SMS] = p
	for i, id := range []string{"a", "b"} {
		if err := f.d.Send(ctx, model.Message{ID: id, TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3, Deadline: &dl}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// The second is over the quota; tomorrow is after its deadline, so it is dropped, not held.
	evs := outcomes(f.pub)
	if len(evs) != 2 || evs[0].Name != "sms_sent" || evs[1].Name != "sms_failed" || evs[1].Props["reason"] != model.DeadlineReason {
		t.Fatalf("events %+v", evs)
	}
	if n, _ := f.d.rdb.ZCard(ctx, deferredKey).Result(); n != 0 {
		t.Fatalf("%d messages held, want none", n)
	}
}

// Two dispatchers (two instances) share one provider's daily quota through Redis: messages over it
// wait for the next day in the provider's time zone.
func TestDailyQuotaAcrossInstances(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC) // 20:30 in India
	p := f.st.providers[channel.SMS]
	p.ID, p.Name, p.Limits = 7, "SMS sandbox", model.ProviderLimits{DailyQuota: 3, Timezone: "Asia/Kolkata"}
	f.st.providers[channel.SMS] = p
	other := New(f.st, f.d.rdb, f.pub, Config{Providers: providers.Deps{Sandbox: f.sink}})
	for _, d := range []*Dispatcher{f.d, other} {
		d.now = func() time.Time { return now }
	}
	for i := range 5 {
		d := []*Dispatcher{f.d, other}[i%2]
		if err := d.Send(ctx, model.Message{ID: "q" + string(rune('a'+i)), TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3}); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sink.msgs) != 3 {
		t.Fatalf("sent %d, want the quota of 3", len(f.sink.msgs))
	}
	ev, _ := f.pub.take()
	if strings.Count(strings.Join(ev, ","), "sms_deferred") != 2 {
		t.Fatalf("events %v, want 2 sms_deferred", ev)
	}
	held, _ := f.d.rdb.ZRangeWithScores(ctx, deferredKey, 0, -1).Result()
	nextDay := time.Date(2026, 11, 12, 0, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))
	if len(held) != 2 || int64(held[0].Score) != nextDay.UnixMilli() {
		t.Fatalf("held %v, want 2 until %v", held, nextDay)
	}
	if n, _ := UsageToday(ctx, f.d.rdb, 7, p.Limits, now); n != 3 {
		t.Fatalf("usage today %d, want 3", n)
	}
	// The next day the quota starts again.
	now = nextDay.Add(10 * time.Hour) // after quiet hours
	if err := f.d.Send(ctx, model.Message{ID: "tomorrow", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 3}); err != nil {
		t.Fatal(err)
	}
	if len(f.sink.msgs) != 4 {
		t.Fatalf("next day: sent %d, want 4", len(f.sink.msgs))
	}
}

func TestQuotaRefundWhenNothingSent(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l := model.ProviderLimits{DailyQuota: 2}
	for range 2 {
		if ok, _, err := TakeQuota(ctx, rdb, 1, l, now); !ok || err != nil {
			t.Fatalf("take: %v %v", ok, err)
		}
	}
	if ok, next, _ := TakeQuota(ctx, rdb, 1, l, now); ok || !next.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("over quota: ok %v next %v", ok, next)
	}
	refundQuota(ctx, rdb, 1, l, now)
	if ok, _, _ := TakeQuota(ctx, rdb, 1, l, now); !ok {
		t.Fatal("a refunded message should be available again")
	}
	if ok, _, _ := TakeQuota(ctx, rdb, 2, l, now); !ok {
		t.Fatal("another provider has its own quota")
	}
	if ok, _, _ := TakeQuota(ctx, rdb, 3, model.ProviderLimits{}, now); !ok {
		t.Fatal("no quota means no limit")
	}
}

func TestQuotaDayZones(t *testing.T) {
	now := time.Date(2026, 3, 31, 20, 0, 0, 0, time.UTC)
	if d, _ := QuotaDay("", now); d != "20260331" {
		t.Errorf("UTC day %s", d)
	}
	if d, next := QuotaDay("Asia/Kolkata", now); d != "20260401" || next.UTC() != time.Date(2026, 4, 1, 18, 30, 0, 0, time.UTC) {
		t.Errorf("India day %s next %v", d, next.UTC())
	}
	if ValidateLimits(model.ProviderLimits{RatePerSecond: -1}) == nil || ValidateLimits(model.ProviderLimits{Timezone: "Mars/Base"}) == nil ||
		ValidateLimits(model.ProviderLimits{RatePerSecond: 50, DailyQuota: 10000, Timezone: "Asia/Kolkata"}) != nil {
		t.Error("limit validation")
	}
}

func TestProviderRateSharedAcrossInstances(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	v := sendVia{id: 9, limits: model.ProviderLimits{RatePerSecond: 2}}
	other := New(f.st, f.d.rdb, f.pub, Config{})
	start := time.Now()
	for i := range 4 { // a burst of 2, then 2 more at 2/s across both instances: about a second
		if err := []*Dispatcher{f.d, other}[i%2].waitProviderRate(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 800*time.Millisecond {
		t.Fatalf("4 messages at 2/s took %v; the bucket isn't shared", el)
	}
	if ok, _ := f.d.rdb.Exists(ctx, providerRateKey(9)).Result(); ok != 1 {
		t.Fatal("no shared bucket in Redis")
	}
}

func TestTestSendToBareAddress(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if err := f.d.Send(ctx, model.Message{ID: "test:x", TenantID: 1, Channel: channel.Email, TemplateID: 1,
		TestTo: "qa@example.com", TestName: "Quinn Tester"}); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.sent) != 1 || f.mail.sent[0].To != "qa@example.com" || f.mail.sent[0].Rendered.Subject != "Hi Quinn" {
		t.Fatalf("sent %+v", f.mail.sent)
	}
	if ev := outcomes(f.pub); len(ev) != 0 {
		t.Fatalf("a test to a bare address recorded events: %+v", ev)
	}
	if err := f.d.Send(ctx, model.Message{ID: "test:y", TenantID: 1, Channel: channel.SMS, TemplateID: 3,
		TestTo: "+919811111111", TestName: "Quinn"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sink.msgs) != 1 || f.sink.msgs[0] != "sms:+919811111111" {
		t.Fatalf("sms test: %v", f.sink.msgs)
	}
}
