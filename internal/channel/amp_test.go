package channel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"pulse/internal/amp"
	"pulse/internal/model"
)

func ampTemplate(t *testing.T, doc *amp.Doc) model.Template {
	t.Helper()
	blocks, _ := json.Marshal(doc)
	content, _ := json.Marshal(map[string]any{"amp_blocks": json.RawMessage(blocks)})
	tpl := model.Template{Name: "amp", Channel: Email, Subject: "s", HTML: "<p>x</p>", Content: content}
	if err := PrepareAMP(&tpl); err != nil {
		t.Fatal(err)
	}
	return tpl
}

// Personalisation in the AMP part is escaped for where each value lands, the client's mustache
// templates are left alone, and the document keeps its ⚡4email marker.
func TestAMPPersonalisationEscaping(t *testing.T) {
	tpl := ampTemplate(t, &amp.Doc{Blocks: []amp.Block{
		{ID: "h", Type: amp.TypeHeading, Text: `Hi {{contact.first_name | default:"there"}}`},
		{ID: "b", Type: amp.TypeButton, Text: "Go", URL: "https://shop.example/?c={{contact.city | urlencode}}"},
		{ID: "poll-1", Type: amp.TypePoll, Text: "Q?", Options: []string{"A", "B"}, ShowResults: true},
	}})
	if err := Validate(tpl); err != nil {
		t.Fatal(err)
	}
	c := model.Contact{Attrs: map[string]any{"first_name": `<script>alert("x")</script>`, "city": `a&b "c"`}}
	r, err := Render(tpl, c)
	if err != nil {
		t.Fatal(err)
	}
	a := r.Content.AMP
	for _, want := range []string{"⚡4email", "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;", "?c=a%26b&#43;%22c%22", "{{message}}", "{{#items}}", `action-xhr="pulse:submit/poll-1"`} {
		if !strings.Contains(a, want) {
			t.Errorf("rendered AMP lacks %s\n%s", want, a)
		}
	}
	if strings.Contains(a, "<script>alert") {
		t.Error("contact data unescaped")
	}
	if len(r.Content.AMPBlocks) != 0 {
		t.Error("builder document carried into the message")
	}
	if ps := amp.Errors(amp.Validate(a)); len(ps) != 0 {
		t.Errorf("rendered AMP invalid: %v", ps)
	}
	// Default when the contact has no name.
	r, _ = Render(tpl, model.Contact{Attrs: map[string]any{}})
	if !strings.Contains(r.Content.AMP, "Hi there") {
		t.Error("default not applied")
	}
}

func TestAMPValidationOnSave(t *testing.T) {
	content, _ := json.Marshal(map[string]any{"amp": "<!doctype html>\n<html>\n<body><img src=\"x\"></body></html>"})
	err := Validate(model.Template{Name: "x", Channel: Email, Subject: "s", HTML: "<p>x</p>", Content: content})
	var ae AMPError
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), "line 2:") || !strings.Contains(err.Error(), "line 3:") {
		t.Fatalf("err = %v", err)
	}
	sms, _ := json.Marshal(map[string]any{"text": "x", "amp": "y"})
	if err := Validate(model.Template{Name: "x", Channel: SMS, Content: sms}); err == nil {
		t.Error("an SMS template took an AMP part")
	}
	bad, _ := json.Marshal(map[string]any{"amp_blocks": map[string]any{"blocks": []any{}}})
	if err := PrepareAMP(&model.Template{Channel: Email, Content: bad}); err == nil {
		t.Error("empty builder document built")
	}
}
