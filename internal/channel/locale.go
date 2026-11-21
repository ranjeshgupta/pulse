package channel

// Multi-language templates (O6): a template's translations and which one a contact gets.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"pulse/internal/model"
)

// MaxTranslations is how many languages a template can have besides its default.
const MaxTranslations = 20

var localeRe = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)

// ValidLocale reports whether l is a language code a translation can have (hi, ta, hi-IN).
func ValidLocale(l string) bool { return localeRe.MatchString(l) }

// ContactLocale is the locale a contact's messages are written in: their locale attribute (set by
// the SDKs, or by a server), else language.
func ContactLocale(c model.Contact) string {
	for _, k := range []string{"locale", "language"} {
		if v, ok := c.Attrs[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func lang(l string) string {
	l = strings.ToLower(strings.ReplaceAll(l, "_", "-"))
	before, _, _ := strings.Cut(l, "-")
	return before
}

// PickTranslation is the translation for a locale: the exact one, else the first of the same
// language; nil for the default.
func PickTranslation(trs []model.Translation, locale string) *model.Translation {
	if locale == "" || len(trs) == 0 {
		return nil
	}
	norm := strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	for i := range trs {
		if strings.ToLower(trs[i].Locale) == norm {
			return &trs[i]
		}
	}
	for i := range trs {
		if lang(trs[i].Locale) == lang(norm) {
			return &trs[i]
		}
	}
	return nil
}

// ForLocale is the template as a contact with locale gets it: the matching translation's fields
// over the default's. It returns the locale used ("" for the default).
func ForLocale(t model.Template, locale string) (model.Template, string) {
	tr := PickTranslation(t.Translations, locale)
	t.Translations = nil
	if tr == nil {
		return t, ""
	}
	return applyTranslation(t, *tr), tr.Locale
}

func applyTranslation(t model.Template, tr model.Translation) model.Template {
	if strings.TrimSpace(tr.Subject) != "" {
		t.Subject = tr.Subject
	}
	if strings.TrimSpace(tr.HTML) != "" {
		t.HTML = tr.HTML
		t.Builder = nil // the drag-and-drop layout is the default's
	}
	if len(tr.Content) > 0 && string(tr.Content) != "null" {
		base := map[string]any{}
		_ = json.Unmarshal(t.Content, &base)
		over := map[string]any{}
		if json.Unmarshal(tr.Content, &over) == nil {
			for k, v := range over {
				if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
					continue
				}
				base[k] = v
			}
			t.Content, _ = json.Marshal(base)
		}
	}
	return t
}

// ValidateTranslations checks a template's translations: locales, and each translated template as a
// whole (required fields, personalisation syntax).
func ValidateTranslations(t model.Template) error {
	if len(t.Translations) > MaxTranslations {
		return fmt.Errorf("a template has at most %d languages besides its default", MaxTranslations)
	}
	seen := map[string]bool{}
	for _, tr := range t.Translations {
		l := strings.ToLower(tr.Locale)
		if !localeRe.MatchString(tr.Locale) {
			return fmt.Errorf("language %q: use a code like hi, ta or hi-IN", tr.Locale)
		}
		if seen[l] {
			return fmt.Errorf("language %s is there twice", tr.Locale)
		}
		seen[l] = true
		if len(tr.HTML) > 2<<20 || len(tr.Content) > 256<<10 {
			return fmt.Errorf("language %s: the content is too big", tr.Locale)
		}
		base := t
		base.Translations = nil
		if err := Validate(applyTranslation(base, tr)); err != nil {
			return fmt.Errorf("language %s: %w", tr.Locale, err)
		}
	}
	return nil
}
