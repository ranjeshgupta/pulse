package channel

import (
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/model"
	"pulse/internal/personalize"
)

// Every templated field of every channel takes the standard variables, next to the older syntax.
func TestRenderWithStandardVariables(t *testing.T) {
	c := model.Contact{ExternalID: "u1", Email: "a@x.test", Attrs: map[string]any{"first_name": "Asha"}}
	v := Vars{Event: map[string]any{"product": "Yoga mat", "url": "https://shop.test/p/1"},
		Campaign: personalize.Campaign{Name: "Cart rescue"}, Links: Links{Unsubscribe: "https://p.test/u/t"}}
	all := `{{contact.first_name}}/{{.Attrs.first_name}}/{{event.product}}/{{campaign.name}}`
	want := "Asha/Asha/Yoga mat/Cart rescue"
	content, _ := json.Marshal(Content{Text: all, SenderID: "{{campaign.channel}}", BodyParams: []string{all}, HeaderImageURL: "{{event.url}}",
		Suggestions: []Suggestion{{Type: "url", Text: all, Value: "{{event.url}}"}}, Card: &Card{Title: all, Description: all},
		Title: all, Body: all, URL: "{{event.url}}", Data: map[string]string{"k": all}})
	r, err := RenderWith(model.Template{Name: "T", Channel: RCS, Subject: all, Content: content}, c, v)
	if err != nil {
		t.Fatal(err)
	}
	k := r.Content
	for name, got := range map[string]string{"subject": r.Subject, "text": k.Text, "param": k.BodyParams[0], "suggestion": k.Suggestions[0].Text,
		"card": k.Card.Title + "|" + k.Card.Description, "title": k.Title, "body": k.Body, "data": k.Data["k"]} {
		if strings.ReplaceAll(got, "|", "") != strings.Repeat(want, strings.Count(got, "|")+1) {
			t.Errorf("%s = %q", name, got)
		}
	}
	if k.SenderID != "rcs" || k.URL != "https://shop.test/p/1" || k.Suggestions[0].Value != "https://shop.test/p/1" || k.HeaderImageURL != "https://shop.test/p/1" {
		t.Errorf("content = %+v", k)
	}
	e, err := RenderWith(model.Template{Channel: Email, Subject: "s", HTML: `<a href="{{links.unsubscribe}}">u</a> {{.UnsubscribeURL}}`}, c, v)
	if err != nil || strings.Count(e.HTML, "https://p.test/u/t") != 2 {
		t.Errorf("email links: %v %s", err, e.HTML)
	}
}

func TestMissingValuesPolicyIsValidated(t *testing.T) {
	tpl := model.Template{Channel: SMS, Content: json.RawMessage(`{"text":"Hi {{contact.first_name}}"}`)}
	for policy, ok := range map[string]bool{"": true, MissingEmpty: true, MissingSkip: true, "drop": false} {
		tpl.MissingValues = policy
		if err := Validate(tpl); (err == nil) != ok {
			t.Errorf("%q: %v", policy, err)
		}
	}
	r, _ := Render(tpl, model.Contact{Attrs: map[string]any{}})
	if len(r.Missing) != 1 || r.Missing[0] != "contact.first_name" {
		t.Errorf("missing = %v", r.Missing)
	}
}
