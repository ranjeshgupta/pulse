package journey

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/onsite"
)

// triggerStore is fakeStore plus the trigger event an enrollment started with.
type triggerStore struct {
	*fakeStore
	trigger json.RawMessage
	reads   int
}

func (s *triggerStore) EnrollmentTrigger(context.Context, int64) (json.RawMessage, error) {
	s.reads++
	return s.trigger, nil
}

const cartJourney = `{"trigger":{"type":"event","event":"cart_abandoned"},"entry":"e1","nodes":{
	"e1":{"type":"send_email","template_id":1,"next":"w"},
	"w":{"type":"wait","seconds":3600,"next":"m"},
	"m":{"type":"send_message","channels":[
		{"channel":"sms","template_id":2,"when":[{"prop":"contact.phone_country","op":"eq","value":"IN"}]},
		{"channel":"whatsapp","template_id":3,"fallback_template_id":2}],"next":"x"},
	"x":{"type":"exit"}}}`

func findEvent(f *fakeEvents, name string) *event.Event {
	for i := range f.evs {
		if f.evs[i].Name == name {
			return &f.evs[i]
		}
	}
	return nil
}

func TestMessagesCarryTheTriggerEvent(t *testing.T) {
	x, store, sender, _, _ := builderExecutor(t, nil)
	ts := &triggerStore{fakeStore: store, trigger: json.RawMessage(`{"items":[{"name":"Yoga mat"}]}`)}
	x.Store = ts
	g := graph(t, cartJourney)
	g.Name = "Cart rescue"
	if err := x.Run(context.Background(), g, en, g.Entry); err != nil {
		t.Fatal(err)
	}
	// Later steps (after the wait, from a timer) carry it too.
	if err := x.Run(context.Background(), g, en, "m"); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("sent %+v", sender.sent)
	}
	for _, m := range sender.sent {
		if string(m.Event) != string(ts.trigger) || m.Campaign != "Cart rescue" || m.Step == "" {
			t.Errorf("message %s: event %s, campaign %q, step %q", m.ID, m.Event, m.Campaign, m.Step)
		}
	}

	// A journey started by a segment has no trigger event and doesn't look for one.
	ts.reads, sender.sent = 0, nil
	seg := graph(t, strings.Replace(cartJourney, `{"type":"event","event":"cart_abandoned"}`, `{"type":"segment_entered","segment_id":4}`, 1))
	if err := x.Run(context.Background(), seg, en, seg.Entry); err != nil {
		t.Fatal(err)
	}
	if ts.reads != 0 || len(sender.sent) != 1 || sender.sent[0].Event != nil {
		t.Errorf("segment journey: reads %d, sent %+v", ts.reads, sender.sent)
	}
}

func TestSendMessageChoosesTheChannelByCondition(t *testing.T) {
	g := graph(t, cartJourney)
	for _, tc := range []struct {
		name  string
		attrs map[string]any
		want  string // channel sent, or "" for none
		event string
	}{
		{"Indian number", map[string]any{"phone": "+919812345678", "phone_country": "IN"}, "sms", ""},
		{"other number", map[string]any{"phone": "+14155550100", "phone_country": "US"}, "whatsapp", ""},
		{"Indian but SMS opted out", map[string]any{"phone": "+919812345678", "phone_country": "IN", consent.Key(channel.SMS): true}, "whatsapp", ""},
		{"no phone", map[string]any{"phone": ""}, "", "message_unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, _, sender, evs, _ := builderExecutor(t, tc.attrs)
			stats := x.Stats.(fakeStats)
			if err := x.Run(context.Background(), g, en, "m"); err != nil {
				t.Fatal(err)
			}
			got := ""
			if len(sender.sent) == 1 {
				got = sender.sent[0].Channel
				if got == "whatsapp" && sender.sent[0].FallbackTemplateID != 2 {
					t.Errorf("fallback = %d", sender.sent[0].FallbackTemplateID)
				}
			}
			if got != tc.want {
				t.Fatalf("sent on %q, want %q (%+v)", got, tc.want, sender.sent)
			}
			if stats["m|"+ChannelOutput(tc.want)] != 1 {
				t.Errorf("stats = %v", stats)
			}
			if tc.event != "" && findEvent(evs, tc.event) == nil {
				t.Errorf("events = %+v", evs.evs)
			}
		})
	}
}

func TestSendMessageWithoutAMatchingRuleSkips(t *testing.T) {
	// Conditions on the trigger event; no "otherwise" rule.
	g := graph(t, `{"trigger":{"type":"event","event":"cart_abandoned"},"entry":"m","nodes":{
		"m":{"type":"send_message","channels":[
			{"channel":"email","template_id":1,"when":[{"prop":"event.total","op":"gte","value":5000}]},
			{"channel":"sms","template_id":2,"when":[{"prop":"event.items[].name","op":"contains","value":"mat"}]}],"next":"x"},
		"x":{"type":"exit"}}}`)
	for _, tc := range []struct {
		trigger, want string
	}{
		{`{"total":9000}`, "email"},
		{`{"total":100,"items":[{"name":"Yoga mat"}]}`, "sms"},
		{`{"total":100}`, ""},
	} {
		x, store, sender, evs, _ := builderExecutor(t, nil)
		x.Store = &triggerStore{fakeStore: store, trigger: json.RawMessage(tc.trigger)}
		if err := x.Run(context.Background(), g, en, "m"); err != nil {
			t.Fatal(err)
		}
		got := ""
		if len(sender.sent) == 1 {
			got = sender.sent[0].Channel
		}
		if got != tc.want {
			t.Errorf("%s: sent on %q, want %q", tc.trigger, got, tc.want)
		}
		if tc.want == "" {
			if e := findEvent(evs, EventMessageSkipped); e == nil || !strings.Contains(fmt.Sprint(e.Props["reason"]), "no channel rule") {
				t.Errorf("events = %+v", evs.evs)
			}
		}
	}
}

func TestSendMessageRuleValidation(t *testing.T) {
	bad := map[string]string{
		`{"type":"send_message","channels":[{"channel":"sms","template_id":1,"when":[{"prop":"phone_country","op":"eq","value":"IN"}]}],"next":"x"}`:            "contact.<field> or event.<property>",
		`{"type":"send_message","channels":[{"channel":"sms","template_id":1,"when":[{"prop":"contact.phone_country","op":"magic","value":"IN"}]}],"next":"x"}`: "magic",
		`{"type":"send_message","channels":[{"channel":"sms","template_id":1,"fallback_template_id":2}],"next":"x"}`:                                            "only WhatsApp and RCS",
	}
	for node, want := range bad {
		_, err := ParseGraph([]byte(fmt.Sprintf(start, node)))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", node, err, want)
		}
	}
	// The same channel may appear twice when conditions tell the rules apart.
	ok := `{"type":"send_message","channels":[
		{"channel":"sms","template_id":1,"when":[{"prop":"contact.phone_country","op":"eq","value":"IN"}]},
		{"channel":"sms","template_id":2}],"next":"x"}`
	if _, err := ParseGraph([]byte(fmt.Sprintf(start, ok))); err != nil {
		t.Error(err)
	}
}

func TestWebhookBodyAndUpdateAttrUseVariables(t *testing.T) {
	if _, err := ParseGraph([]byte(fmt.Sprintf(start, `{"type":"webhook","url":"https://x.test/h","body":"{\"n\": {{contact.first_name | shout}}}","next":"x"}`))); err == nil ||
		!strings.Contains(err.Error(), "unknown pipe") {
		t.Errorf("webhook body pipes aren't checked: %v", err)
	}
	c := model.Contact{ExternalID: "u1", Attrs: map[string]any{"first_name": "asha"}}
	got := ResolveAttrsWith(map[string]any{"last_product": "{{event.items.0.name}}", "greeting": `{{contact.first_name | title}} / {{.Attrs.first_name}}`},
		c, map[string]any{"items": []any{map[string]any{"name": "Yoga mat"}}}, time.Now())
	if got["last_product"] != "Yoga mat" || got["greeting"] != "Asha / asha" {
		t.Errorf("resolved = %v", got)
	}
	if _, err := ParseGraph([]byte(fmt.Sprintf(start, `{"type":"update_attr","attrs":{"x":"{{contact.a | nope}}"},"next":"x"}`))); err == nil {
		t.Error("update_attr pipes aren't checked")
	}
}

// Personalised dynamic content keeps an anonymous version for visitors whose identity isn't verified.
func TestPushDWCKeepsAnAnonymousVersion(t *testing.T) {
	x, _, _, _, _ := builderExecutor(t, nil)
	g := one(t, `{"type":"push_dwc","slot":"hero","html":"<b>Hi {{contact.first_name | default:\"there\"}}</b>","next":"x"}`)
	if err := x.Run(context.Background(), g, en, "s"); err != nil {
		t.Fatal(err)
	}
	stored := x.Onsite.(*fakeOnsite).dwc[0]
	stored = stored[strings.Index(stored, "hero:")+5 : strings.LastIndex(stored, ":")]
	if onsite.ResolveDWC(stored, true) != "<b>Hi Asha</b>" || onsite.ResolveDWC(stored, false) != "<b>Hi there</b>" {
		t.Errorf("stored %q", stored)
	}
}

// Properties of an event nobody proved they sent never reach the contact's messages.
func TestUnverifiedTriggerIsNotKept(t *testing.T) {
	ev := &event.Event{Name: "cart_abandoned", Props: map[string]any{"product_name": "Yoga mat"}}
	if got := messageTrigger(ev, nil); !strings.Contains(string(got), "Yoga mat") {
		t.Errorf("verified: %s", got)
	}
	ev.Unverified = true
	if got := messageTrigger(ev, nil); got != nil {
		t.Errorf("unverified kept: %s", got)
	}
}

// Webhook bodies escape every value, so an event property can't add fields to the JSON.
func TestWebhookBodyEscapesValues(t *testing.T) {
	x, _, _, _, _ := builderExecutor(t, nil)
	var got string
	x.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	x.Store = &triggerStore{fakeStore: x.Store.(*fakeStore), trigger: json.RawMessage(`{"name":"x\", \"admin\": true, \"y\": \"","items":[1,2]}`)}
	g := graph(t, `{"trigger":{"type":"event","event":"cart_abandoned"},"entry":"s","nodes":{
		"s":{"type":"webhook","url":"https://x.test/h","body":"{\"p\": \"{{event.name}}\", \"old\": \"{{.Attrs.first_name}}\", \"items\": {{event.items | json}}, \"all\": {{json .Attrs}}}","next":"x"},
		"x":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "s"); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got), &body); err != nil {
		t.Fatalf("body %q: %v", got, err)
	}
	if _, injected := body["admin"]; injected || !strings.Contains(attrString(body["p"]), `"admin"`) {
		t.Errorf("value escaped into fields: %q", got)
	}
	if items, _ := body["items"].([]any); len(items) != 2 {
		t.Errorf("json pipe: %q", got)
	}
	if _, ok := body["all"].(map[string]any); !ok {
		t.Errorf("{{json .Attrs}}: %q", got)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func attrString(v any) string { s, _ := v.(string); return s }
