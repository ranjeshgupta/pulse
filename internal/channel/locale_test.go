package channel

import (
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/model"
)

func TestTranslations(t *testing.T) {
	tpl := model.Template{Channel: Push, Content: json.RawMessage(`{"title":"Sale","body":"50% off","url":"https://x.test"}`),
		Translations: []model.Translation{
			{Locale: "hi", Content: json.RawMessage(`{"title":"सेल","body":""}`)},
			{Locale: "ta-IN", Content: json.RawMessage(`{"title":"விற்பனை","body":"50% தள்ளுபடி"}`)},
		}}
	if err := Validate(tpl); err != nil {
		t.Fatal(err)
	}
	got, loc := ForLocale(tpl, "hi-IN") // same language
	c, _ := ParseContent(got.Content)
	if loc != "hi" || c.Title != "सेल" || c.Body != "50% off" || c.URL != "https://x.test" || got.Translations != nil {
		t.Errorf("hi-IN: %s %+v", loc, c)
	}
	if _, loc := ForLocale(tpl, "ta_in"); loc != "ta-IN" {
		t.Errorf("ta_in: %s", loc)
	}
	if got, loc := ForLocale(tpl, "fr"); loc != "" || string(got.Content) != string(tpl.Content) {
		t.Errorf("fr: %s", loc)
	}
	if ContactLocale(model.Contact{Attrs: map[string]any{"language": "hi"}}) != "hi" {
		t.Error("language attribute")
	}
	bad := tpl
	bad.Translations = []model.Translation{{Locale: "Hindi"}}
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "code like") {
		t.Errorf("bad locale: %v", err)
	}
	bad.Translations = []model.Translation{{Locale: "hi", Content: json.RawMessage(`{"title":"{{contact.first_name | nope}}"}`)}}
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "language hi") {
		t.Errorf("bad syntax: %v", err)
	}
	email := model.Template{Channel: Email, Subject: "Hello", HTML: "<p>Hi</p>", Builder: json.RawMessage(`{}`),
		Translations: []model.Translation{{Locale: "hi", Subject: "नमस्ते", HTML: "<p>नमस्ते</p>"}}}
	if got, _ := ForLocale(email, "hi"); got.Subject != "नमस्ते" || got.Builder != nil {
		t.Errorf("email: %+v", got)
	}
}
