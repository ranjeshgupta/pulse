package channel

import (
	"strings"
	"testing"

	"pulse/internal/model"
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
