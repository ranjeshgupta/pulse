package channel

import (
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/model"
)

func tpl(ch, content string) model.Template {
	return model.Template{Name: "t", Channel: ch, Content: json.RawMessage(content)}
}

func TestValidate(t *testing.T) {
	ok := []model.Template{
		{Name: "e", Channel: Email, Subject: "Hi {{.Attrs.first_name}}", HTML: "<p>x</p>"},
		tpl(SMS, `{"text":"Your OTP is {{.Attrs.otp}}"}`),
		tpl(WhatsApp, `{"template_name":"order_update","language":"en","body_params":["{{.Attrs.first_name}}"]}`),
		tpl(RCS, `{"text":"Sale!","suggestions":[{"type":"url","text":"Shop","value":"https://x.y"}]}`),
		tpl(RCS, `{"card":{"title":"New arrivals"}}`),
		tpl(Push, `{"title":"Hi","body":"Back in stock","url":"https://x.y/p/1"}`),
	}
	for _, x := range ok {
		if err := Validate(x); err != nil {
			t.Errorf("%s: %v", x.Channel, err)
		}
	}
	bad := map[string]model.Template{
		"unknown channel":    tpl("fax", `{}`),
		"subject":            {Channel: Email, HTML: "<p>x</p>"},
		"content.text":       tpl(SMS, `{}`),
		"1600":               tpl(SMS, `{"text":"`+strings.Repeat("x", 1601)+`"}`),
		"template_name":      tpl(WhatsApp, `{"language":"en"}`),
		"content.text or":    tpl(RCS, `{}`),
		"at most 4":          tpl(RCS, `{"text":"x","suggestions":[{"type":"reply","text":"1"},{"type":"reply","text":"2"},{"type":"reply","text":"3"},{"type":"reply","text":"4"},{"type":"reply","text":"5"}]}`),
		"reply, url or dial": tpl(RCS, `{"text":"x","suggestions":[{"type":"call","text":"1"}]}`),
		"content.body":       tpl(Push, `{"title":"x"}`),
		"unknown field":      tpl(SMS, `{"text":"x","colour":"red"}`),
		"function \"oops\"":  tpl(SMS, `{"text":"{{oops}}"}`),
	}
	for want, x := range bad {
		if err := Validate(x); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
}

func TestRender(t *testing.T) {
	c := model.Contact{Email: "a@b.c", Attrs: map[string]any{"first_name": "Asha", "bio": "<script>x</script>"}}
	r, err := Render(model.Template{Channel: Email, Subject: "Hi {{.Attrs.first_name}}\r\nBcc: evil@x.y",
		HTML: "<p>{{.Attrs.first_name}} {{.Attrs.bio}} [{{.Attrs.missing}}]</p>"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(r.Subject, "\r\n") || !strings.HasPrefix(r.Subject, "Hi Asha") {
		t.Errorf("subject = %q", r.Subject)
	}
	if strings.Contains(r.HTML, "<script>") || !strings.Contains(r.HTML, "&lt;script&gt;") || !strings.Contains(r.HTML, "[]") {
		t.Errorf("html = %s", r.HTML)
	}
	r, err = Render(tpl(WhatsApp, `{"template_name":"w","body_params":["{{.Attrs.first_name}}","{{.Attrs.missing}}"]}`), c)
	if err != nil || r.Content.BodyParams[0] != "Asha" || r.Content.BodyParams[1] != "" {
		t.Errorf("params = %q, err %v", r.Content.BodyParams, err)
	}
}
