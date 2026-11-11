package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/providers"
	"pulse/internal/tracking"
)

func outcome(t *testing.T, p *memPub) []event.Event {
	t.Helper()
	var out []event.Event
	for _, r := range p.recs {
		if r.Topic == event.TopicRaw {
			var e event.Event
			_ = json.Unmarshal(r.Value, &e)
			out = append(out, e)
		}
	}
	p.recs = nil
	return out
}

// Every email carries its message reference; every failure carries a reason code.
func TestReferencesAndReasonCodes(t *testing.T) {
	f := setup(t)
	box, _ := auth.NewBox(strings.Repeat("cd", 32))
	codec := tracking.New(box, strings.Repeat("cd", 32), "https://pulse.test")
	f.d.cfg.Tracking = codec
	ctx := context.Background()
	m := model.Message{ID: "bc3:77:1", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1, BroadcastID: 3, Run: 77}
	if err := f.d.Handle(ctx, msgRecord(m)); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.sent) != 1 {
		t.Fatal("not sent")
	}
	ref, err := codec.ParseRef(f.mail.sent[0].Ref)
	if err != nil || ref.TenantID != 1 || ref.ContactID != 1 || ref.BroadcastID != 3 || ref.MessageID != "bc3:77:1" || ref.TemplateID != 1 {
		t.Fatalf("reference %q: %+v %v", f.mail.sent[0].Ref, ref, err)
	}
	outcome(t, f.pub)

	// No address: a failure with reason code no_address; a missing template: template.
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "x1", TenantID: 1, ContactID: 2, Channel: channel.Email, TemplateID: 1}))
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "x2", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 99}))
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "x3", TenantID: 1, ContactID: 3, Channel: channel.SMS, TemplateID: 3}))
	evs := outcome(t, f.pub)
	want := []string{"no_address", "template", "no_address"}
	if len(evs) != 3 {
		t.Fatalf("events %+v", evs)
	}
	for i, e := range evs {
		if e.Props["reason_code"] != want[i] {
			t.Errorf("%s: reason_code %v, want %s (%v)", e.Name, e.Props["reason_code"], want[i], e.Props["reason"])
		}
	}
	// A failed WhatsApp receipt with Meta's "undeliverable" code: blocked.
	f.d.remember(ctx, model.Message{ID: "w1", TenantID: 1, ContactID: 1, Channel: channel.WhatsApp, TemplateID: 2}, "wamid.1")
	rr, _ := json.Marshal(ReceiptRecord{TenantID: 1, Receipt: providers.Receipt{ProviderMessageID: "wamid.1", Status: "failed",
		Reason: "meta whatsapp 131026: Message undeliverable", Code: "blocked"}})
	if err := f.d.Handle(ctx, recordOn(TopicReceipts, rr)); err != nil {
		t.Fatal(err)
	}
	if evs := outcome(t, f.pub); len(evs) != 1 || evs[0].Name != "whatsapp_failed" || evs[0].Props["reason_code"] != "blocked" {
		t.Errorf("receipt: %+v", evs)
	}
	// Email bounce and complaint receipts (custom gateway, sandbox).
	f.d.remember(ctx, m, "gw-1")
	for _, rc := range []providers.Receipt{{ProviderMessageID: "gw-1", Status: "bounced", Kind: "hard", Reason: "550"},
		{ProviderMessageID: "gw-1", Status: "complained"}} {
		b, _ := json.Marshal(ReceiptRecord{TenantID: 1, Receipt: rc})
		if err := f.d.Handle(ctx, recordOn(TopicReceipts, b)); err != nil {
			t.Fatal(err)
		}
	}
	evs = outcome(t, f.pub)
	if len(evs) != 3 || evs[0].Name != "email_bounced" || evs[0].Attrs["email_bounced"] != true || evs[0].Props["broadcast_id"] != float64(3) ||
		evs[0].Props["reason_code"] != "hard_bounce" || evs[1].Name != "email_complained" || evs[2].Name != "unsubscribed" {
		t.Errorf("feedback receipts: %+v", evs)
	}
}

func recordOn(topic string, b []byte) *kgo.Record { return &kgo.Record{Topic: topic, Value: b} }
