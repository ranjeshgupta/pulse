package channel

import (
	"encoding/json"
	"testing"

	"pulse/internal/model"
)

// FuzzTemplate runs arbitrary template fields and translations through Validate, PrepareAMP,
// ForLocale, Render and TextFromHTML: nothing may panic or hang, and a template that validates
// must render for an empty contact and in every one of its languages.
func FuzzTemplate(f *testing.F) {
	seeds := []struct {
		ch, subject, html, content, trLocale, trSubject, trContent, locale string
	}{
		{Email, "Hi {{contact.first_name | default:\"there\"}}", "<p>Hello {{.Attrs.first_name}}</p>", `{"text":"plain","amp":"<!doctype html><html ⚡4email><head></head><body>x</body></html>"}`, "hi", "नमस्ते", `{}`, "hi-IN"},
		{SMS, "", "", `{"text":"Your OTP is {{.Attrs.otp}}","track_links":true}`, "ta", "", `{"text":"OTP {{.Attrs.otp}}"}`, "ta"},
		{WhatsApp, "", "", `{"template_name":"order_update","language":"en","body_params":["{{.Attrs.first_name}}"]}`, "hi", "", `{"body_params":["x"]}`, "hi"},
		{RCS, "", "", `{"text":"Sale!","suggestions":[{"type":"url","text":"Shop","value":"https://x.y"}],"card":{"title":"t"}}`, "", "", "", ""},
		{Push, "", "", `{"title":"Hi","body":"Back in stock","url":"https://x.y/p/1","data":{"k":"v"}}`, "en-GB", "", `{"title":""}`, "en_gb"},
		{Inbox, "", "", `{"title":"Hi","body":"b","expires_days":30,"category":"offers"}`, "", "", "", ""},
		{Email, "s", "<p>{{block.footer}}</p>", `{"amp_blocks":{"blocks":[]}}`, "fr", "", `null`, "fr"},
	}
	for _, s := range seeds {
		f.Add(s.ch, s.subject, s.html, s.content, s.trLocale, s.trSubject, s.trContent, s.locale)
	}
	f.Fuzz(func(t *testing.T, ch, subject, html, content, trLocale, trSubject, trContent, locale string) {
		tpl := model.Template{Name: "t", Channel: ch, Subject: subject, HTML: html, Content: json.RawMessage(content)}
		if trLocale != "" {
			tpl.Translations = []model.Translation{{Locale: trLocale, Subject: trSubject, HTML: html, Content: json.RawMessage(trContent)}}
		}
		TextFromHTML(html)
		_, _ = ParseContent(tpl.Content)
		prepared := tpl
		_ = PrepareAMP(&prepared)
		contact := model.Contact{Attrs: map[string]any{"locale": locale, "first_name": "Asha"}}
		l, used := ForLocale(tpl, ContactLocale(contact))
		if used == "" && len(l.Translations) != 0 {
			t.Fatal("ForLocale kept the translations")
		}
		if err := Validate(tpl); err != nil {
			return
		}
		if _, err := Render(tpl, model.Contact{Attrs: map[string]any{}}); err != nil {
			t.Fatalf("Validate accepted the template but Render failed: %v", err)
		}
		for _, tr := range tpl.Translations {
			lt, _ := ForLocale(tpl, tr.Locale)
			if _, err := Render(lt, model.Contact{Attrs: map[string]any{}}); err != nil {
				t.Fatalf("Validate accepted translation %s but Render failed: %v", tr.Locale, err)
			}
		}
	})
}
