//go:build e2e

package e2e

// M2, personalisation: a cart-abandonment journey whose messages use the trigger event's
// properties ({{event.product_name}}), with the schema's personal properties left out, and a
// "Send marketing message" step that picks SMS for Indian numbers and WhatsApp otherwise.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCartAbandonPersonalisedJourney(t *testing.T) {
	c, _ := workspace(t, "Personalise")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "whatsapp", "provider": "sandbox_whatsapp", "is_default": true}, 200)

	// The starter cart_abandoned schema, plus a personal property that must never reach a message.
	jsonCall(t, c, "POST", "/v1/events/schemas/suggested", map[string]any{"names": []string{"cart_abandoned"}}, 200)
	schema := jsonCall(t, c, "GET", "/v1/events/cart_abandoned/schema", nil, 200)
	schema["props"] = append(schema["props"].([]any), map[string]any{"key": "buyer_email", "type": "string", "personal": true})
	jsonCall(t, c, "PUT", "/v1/events/cart_abandoned/schema", schema, 200)

	// Unknown pipes are refused when saving.
	jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "bad", "channel": "sms", "content": map[string]any{"text": "{{event.product_name | shout}}"}}, 400)

	email := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Cart reminder", "channel": "email",
		"subject": `{{contact.first_name | default:"Hi there"}}, your {{event.product_name}} is waiting`,
		"html": `<p>{{event.product_name}} for {{event.cart_value | money:"INR"}} in {{campaign.name}}.</p>` +
			`<p>[{{event.buyer_email}}]</p><a href="https://shop.example.com/cart?p={{event.product_code | urlencode}}">Back to your cart</a>`}, 201)["id"]
	sms := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Cart SMS", "channel": "sms",
		"content": map[string]any{"text": "{{event.product_name}} is still in your cart, {{contact.first_name | default:\"friend\"}}"}}, 201)["id"]
	wa := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Cart WhatsApp", "channel": "whatsapp",
		"content": map[string]any{"template_name": "cart_reminder", "body_params": []string{"{{contact.first_name}}", "{{event.product_name}}"}}}, 201)["id"]

	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Cart rescue", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "cart_abandoned"},
		"entry":   "mail",
		"nodes": map[string]any{
			"mail": map[string]any{"type": "send_email", "template_id": email, "next": "wait"},
			"wait": map[string]any{"type": "wait", "seconds": 2, "next": "msg"},
			"msg": map[string]any{"type": "send_message", "channels": []any{
				map[string]any{"channel": "sms", "template_id": sms, "when": []any{map[string]any{"prop": "contact.phone_country", "op": "eq", "value": "IN"}}},
				map[string]any{"channel": "whatsapp", "template_id": wa},
			}, "next": "x"},
			"x": map[string]any{"type": "exit"},
		},
	}}, 201)
	jpath := fmt.Sprintf("/v1/journeys/%v", j["id"])
	jsonCall(t, c, "POST", jpath+"/activate", nil, 200)
	time.Sleep(3 * time.Second)

	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000_000)
	in, us := "in-"+run, "us-"+run
	inPhone, usPhone := fmt.Sprintf("+9198%08s", run[len(run)-8:]), fmt.Sprintf("+1415%07s", run[len(run)-7:])
	inMail := "cart-" + run + "@example.com"
	cart := func(ext string) map[string]any {
		return map[string]any{"external_id": ext, "name": "cart_abandoned", "props": map[string]any{
			"product_code": "MAT 1", "product_name": "Yoga mat", "cart_value": 2499, "buyer_email": "secret-" + run + "@example.com"}}
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": in, "name": "identify", "email": inMail, "attrs": map[string]any{"first_name": "Asha", "phone": inPhone}},
		map[string]any{"external_id": us, "name": "identify", "attrs": map[string]any{"first_name": "Sam", "phone": usPhone}},
	}}, 202)
	waitFor(t, 10*time.Second, func() bool {
		return attrsOf(t, c, in)["phone_country"] == "IN" && attrsOf(t, c, us)["phone_country"] == "US"
	})
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{cart(in), cart(us)}}, 202)

	// The email: subject and body from the event; the personal property never arrives; the link is tracked.
	var search struct {
		Messages []struct{ ID, Subject string }
	}
	waitFor(t, 20*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+inMail), &search)
		return len(search.Messages) == 1
	})
	if got := search.Messages[0].Subject; got != "Asha, your Yoga mat is waiting" {
		t.Errorf("subject = %q", got)
	}
	var msg struct{ HTML string }
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID, &msg)
	if !strings.Contains(msg.HTML, "Yoga mat for ₹2,499.00 in Cart rescue.") || !strings.Contains(msg.HTML, "<p>[]</p>") {
		t.Errorf("email body:\n%s", msg.HTML)
	}
	if strings.Contains(msg.HTML, "secret-"+run) {
		t.Error("a personal event property reached the email")
	}
	if !strings.Contains(msg.HTML, apiURL+"/t/c/") || !strings.Contains(msg.HTML, url.QueryEscape("https://shop.example.com/cart?p=MAT+1")) {
		t.Errorf("the cart link isn't click-tracked with the encoded product code:\n%s", msg.HTML)
	}

	// The channel by condition: the Indian number gets the SMS, the US number WhatsApp, both with the product.
	type sbx struct {
		Channel   string          `json:"channel"`
		Recipient string          `json:"recipient"`
		Payload   json.RawMessage `json:"payload"`
	}
	find := func() map[string]sbx {
		var out struct{ Messages []sbx }
		b, _ := json.Marshal(jsonCall(t, c, "GET", "/v1/sandbox/messages", nil, 200))
		_ = json.Unmarshal(b, &out)
		got := map[string]sbx{}
		for _, m := range out.Messages {
			if m.Recipient == inPhone || m.Recipient == usPhone {
				got[m.Recipient] = m
			}
		}
		return got
	}
	waitFor(t, 20*time.Second, func() bool { return len(find()) == 2 })
	got := find()
	if m := got[inPhone]; m.Channel != "sms" || !strings.Contains(string(m.Payload), "Yoga mat is still in your cart, Asha") {
		t.Errorf("Indian number: %s %s", m.Channel, m.Payload)
	}
	if m := got[usPhone]; m.Channel != "whatsapp" || !strings.Contains(string(m.Payload), `"Sam","Yoga mat"`) {
		t.Errorf("US number: %s %s", m.Channel, m.Payload)
	}

	// The step stats say which channel each contact got.
	waitFor(t, 10*time.Second, func() bool {
		rep := jsonCall(t, c, "GET", jpath+"/report", nil, 200)
		steps, _ := rep["steps"].(map[string]any)
		st, _ := steps["msg"].(map[string]any)
		return st["ch:sms"] == 1.0 && st["ch:whatsapp"] == 1.0
	})

	// Preview with the contact's latest real event, as the editor does.
	sample := jsonCall(t, c, "GET", "/v1/templates/sample-event?event=cart_abandoned&external_id="+in, nil, 200)
	props, _ := sample["props"].(map[string]any)
	if sample["source"] != "latest" || props["product_name"] != "Yoga mat" || props["buyer_email"] != nil {
		t.Errorf("sample event = %v", sample)
	}
}
