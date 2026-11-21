package feed

import (
	"strings"
	"testing"

	"pulse/internal/channel"
	"pulse/internal/model"
)

// Feed text is data: placeholders in it are shown as written, never run, and a lone "{{" doesn't
// break the send.
func TestFeedTextIsNotTemplateCode(t *testing.T) {
	h, txt := Render([]Item{
		{Title: "{{ print 1 }} and {{contact.email}}", Link: "https://acme.test/{{x}}", Summary: "a lone {{ here"},
		{Title: "{{{triple"},
	})
	c := model.Contact{ExternalID: "u1", Email: "a@x.test", Attrs: map[string]any{"email": "a@x.test"}}
	tpl := model.Template{Channel: channel.Email, Subject: "News", HTML: "<p>Hi</p>{{block.feed}}"}
	r, err := channel.RenderWith(tpl, c, channel.Vars{Blocks: map[string]channel.Block{"feed": {HTML: h, Text: txt}}})
	if err != nil {
		t.Fatalf("send broken by feed text: %v", err)
	}
	if strings.Contains(r.HTML, "a@x.test") || !strings.Contains(r.HTML, "&#123;&#123; print 1 }}") || !strings.Contains(r.HTML, "lone &#123;&#123; here") {
		t.Errorf("html: %s", r.HTML)
	}
	sms := model.Template{Channel: channel.SMS, Content: []byte(`{"text":"{{block.feed}}"}`)}
	r, err = channel.RenderWith(sms, c, channel.Vars{Blocks: map[string]channel.Block{"feed": {HTML: h, Text: txt}}})
	if err != nil || strings.Contains(r.Content.Text, "a@x.test") || !strings.Contains(r.Content.Text, "print 1") {
		t.Errorf("text: %q %v", r.Content.Text, err)
	}
}
