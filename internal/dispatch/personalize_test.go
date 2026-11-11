package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/model"
)

// M2: a journey message carries its trigger event and campaign, and every channel renders them;
// the fallback SMS keeps them; a template set to skip on missing values sends nothing.
func TestTriggerEventPersonalisesMessages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.st.templates[10] = model.Template{ID: 10, Name: "Cart", Channel: channel.Email,
		Subject: `{{contact.first_name | default:"there"}}, your {{event.items.0.name}} is waiting`,
		HTML:    `<p>{{event.items.0.name}} for {{event.total | money:"INR"}} in {{campaign.name}}</p><a href="https://shop.example.com/c/{{event.cart_id | urlencode}}">Back to cart</a>`}
	f.st.templates[11] = model.Template{ID: 11, Channel: channel.SMS, Content: json.RawMessage(`{"text":"{{event.items.0.name}} is waiting, {{contact.first_name}}"}`)}
	f.st.templates[12] = model.Template{ID: 12, Channel: channel.WhatsApp, Content: json.RawMessage(`{"template_name":"cart","body_params":["{{event.items.0.name}}"]}`)}
	ev := json.RawMessage(`{"cart_id":"c 9","total":2499,"items":[{"name":"Yoga <mat>"}]}`)

	m := model.Message{ID: "5:s1", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 10, JourneyID: 3,
		Event: ev, Campaign: "Cart rescue", Step: "s1"}
	if err := f.d.Handle(ctx, msgRecord(m)); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.sent) != 1 {
		t.Fatalf("sent %d emails", len(f.mail.sent))
	}
	r := f.mail.sent[0].Rendered
	if r.Subject != "Asha, your Yoga <mat> is waiting" {
		t.Errorf("subject = %q", r.Subject)
	}
	if !strings.Contains(r.HTML, "Yoga &lt;mat&gt; for ₹2,499.00 in Cart rescue") || !strings.Contains(r.HTML, "/c/c&#43;9") {
		t.Errorf("html = %s", r.HTML)
	}

	// SMS on its own, and as the fallback of a WhatsApp message (the fallback keeps the event).
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "5:s2", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 11, JourneyID: 3, Event: ev}))
	wa := model.Message{ID: "6:s3", TenantID: 1, ContactID: 2, Channel: channel.WhatsApp, TemplateID: 12, FallbackTemplateID: 11, JourneyID: 3, Event: ev, Campaign: "Cart rescue"}
	_ = f.d.Handle(ctx, msgRecord(wa))
	_, msgs := f.pub.take()
	if len(msgs) != 1 || !strings.Contains(string(msgs[0].Event), `"cart_id":"c 9"`) || msgs[0].Campaign != "Cart rescue" {
		t.Fatalf("fallback = %+v", msgs)
	}
	_ = f.d.Handle(ctx, msgRecord(msgs[0]))
	if len(f.sink.payloads) != 2 || !strings.Contains(f.sink.payloads[0], `Yoga \u003cmat\u003e is waiting, Asha`) ||
		!strings.Contains(f.sink.payloads[1], `Yoga \u003cmat\u003e is waiting`) {
		t.Errorf("sms payloads = %v", f.sink.payloads)
	}
}

func TestMissingValuePolicy(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.st.templates[20] = model.Template{ID: 20, Channel: channel.SMS, MissingValues: channel.MissingSkip,
		Content: json.RawMessage(`{"text":"Hi {{contact.first_name}}, {{event.product | default:\"your item\"}}"}`)}
	f.st.templates[21] = model.Template{ID: 21, Channel: channel.SMS,
		Content: json.RawMessage(`{"text":"Hi {{contact.first_name}}!"}`)}

	// Contact 2 has no first name: skipped with the reason, nothing sent.
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "k1", TenantID: 1, ContactID: 2, Channel: channel.SMS, TemplateID: 20}))
	var got event.Event
	for _, r := range f.pub.recs {
		_ = json.Unmarshal(r.Value, &got)
	}
	f.pub.recs = nil
	if got.Name != "sms_failed" || got.Props["skipped"] != "missing_value" || got.Props["reason"] != SkippedPrefix+"contact.first_name" {
		t.Errorf("outcome = %s %v", got.Name, got.Props)
	}
	if len(f.sink.msgs) != 0 {
		t.Errorf("sent anyway: %v", f.sink.msgs)
	}
	// Contact 1 has one: sent (the default covers the event). The "empty" policy sends to contact 2.
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "k2", TenantID: 1, ContactID: 1, Channel: channel.SMS, TemplateID: 20}))
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "k3", TenantID: 1, ContactID: 2, Channel: channel.SMS, TemplateID: 21}))
	if len(f.sink.payloads) != 2 || !strings.Contains(f.sink.payloads[0], "Hi Asha, your item") || !strings.Contains(f.sink.payloads[1], "Hi !") {
		t.Errorf("payloads = %v", f.sink.payloads)
	}
}
