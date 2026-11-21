//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestContentBlocks (O6): a block used as {{block.key}} goes into sends and previews; editing it
// changes the next send; bad keys are refused; usage lists the templates.
func TestContentBlocks(t *testing.T) {
	c, _ := workspace(t, "Blocks")
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)
	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	phone := "+9198166" + fmt.Sprintf("%05d", time.Now().UnixNano()%100000)
	ext := "blk-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": phone, "first_name": "Asha"}}, 202)
	jsonCall(t, c, "POST", "/v1/content-blocks", map[string]any{"key": "Bad Key", "name": "x"}, 400)
	id := idOf(jsonCall(t, c, "POST", "/v1/content-blocks", map[string]any{"key": "sign", "name": "Sign-off", "html": "<p>Team Acme</p>", "text": "- Team Acme, {{contact.first_name}}"}, 200)["id"])
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "blk", "channel": "sms", "content": map[string]any{"text": "Sale today {{block.sign}}"}}, 201)["id"]
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	waitFor(t, 20*time.Second, func() bool {
		m := smsTo(t, c, phone)
		return len(m) == 1 && m[0] == "Sale today - Team Acme, Asha"
	})
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/content-blocks/", id), map[string]any{"key": "sign", "name": "Sign-off", "text": "- The Acme crew"}, 200)
	time.Sleep(31 * time.Second) // the dispatcher's 30-second cache
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	waitFor(t, 20*time.Second, func() bool {
		m := smsTo(t, c, phone)
		return len(m) == 2 && (m[0] == "Sale today - The Acme crew" || m[1] == "Sale today - The Acme crew")
	})
	u := jsonCall(t, c, "GET", fmt.Sprintf("/v1/content-blocks/%v/usage", id), nil, 200)
	if ts, _ := u["templates"].([]any); len(ts) != 1 {
		t.Errorf("usage %v", u)
	}
}

// TestSpamAndDomainChecks (O6): the content check scores and explains; the domain check reads DNS.
func TestSpamAndDomainChecks(t *testing.T) {
	c, _ := workspace(t, "Checks")
	r := jsonCall(t, c, "POST", "/v1/templates/spam-check", map[string]any{"subject": "FREE MONEY!!!", "html": `<img src="x"><a href="https://bit.ly/x">go</a>`}, 200)
	if r["rating"] != "spam" || !strings.Contains(fmt.Sprint(r["issues"]), "shortener") {
		t.Errorf("spam check %v", r)
	}
	ok := jsonCall(t, c, "POST", "/v1/templates/spam-check", map[string]any{"subject": "Your order", "html": "<p>" + strings.Repeat("Your order is on its way and arrives soon. ", 8) + "</p>", "text": "x"}, 200)
	if ok["rating"] != "good" {
		t.Errorf("clean email %v", ok)
	}
	jsonCall(t, c, "POST", "/v1/channels/domain-check", map[string]any{"domain": "not a domain"}, 400)
	d := jsonCall(t, c, "POST", "/v1/channels/domain-check", map[string]any{"domain": "pulse-nonexistent-domain.invalid", "service": "ses"}, 200)
	if d["bulk_ready"] != false || len(d["checks"].([]any)) != 5 {
		t.Errorf("domain check %v", d)
	}
}
