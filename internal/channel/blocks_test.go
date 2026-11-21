package channel

import (
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/model"
	"pulse/internal/personalize"
)

func TestBlocksRender(t *testing.T) {
	blocks := map[string]Block{"footer": {HTML: `<p>Bye {{contact.first_name}} <b>&amp;</b></p>`, Text: `Bye {{contact.first_name}} "x"`}}
	c := model.Contact{Attrs: map[string]any{"first_name": "Asha"}}
	r, err := RenderWith(model.Template{Channel: Email, Subject: "Hi {{ block.footer }}", HTML: "<p>Body</p>{{block.footer}}{{block.gone}}"}, c, Vars{Blocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.HTML, "<p>Bye Asha <b>&amp;</b></p>") || strings.Contains(r.HTML, "block.") {
		t.Errorf("html: %s", r.HTML)
	}
	if r.Subject != `Hi Bye Asha "x"` {
		t.Errorf("subject: %q", r.Subject)
	}
	sms, err := RenderWith(model.Template{Channel: SMS, Content: []byte(`{"text":"Sale! {{block.footer}}"}`)}, c, Vars{Blocks: blocks})
	if err != nil || sms.Content.Text != `Sale! Bye Asha "x"` {
		t.Errorf("sms: %+v %v", sms.Content, err)
	}
}

// A template repeating a big block can't grow a message without bound: all its fields share one
// allowance of block content.
func TestBlocksExpansionIsBounded(t *testing.T) {
	big := map[string]Block{"big": {HTML: strings.Repeat("x", 200_000), Text: strings.Repeat("y", 20_000)}}
	params := make([]string, 2000)
	for i := range params {
		params[i] = "{{block.big}}"
	}
	content, _ := json.Marshal(map[string]any{"template_name": "t", "body_params": params})
	out := withBlocks(model.Template{Channel: WhatsApp, HTML: strings.Repeat("{{block.big}}", 100), Content: content}, big)
	if n := len(out.HTML) + len(out.Content); n > personalize.MaxExpanded+len(content)+2000 {
		t.Fatalf("expanded to %d bytes", n)
	}
}
