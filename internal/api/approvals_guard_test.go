package api

import (
	"encoding/json"
	"testing"

	"pulse/internal/model"
)

func TestTemplateBlockKeys(t *testing.T) {
	tpl := model.Template{
		Subject: "{{ block.subj }}",
		HTML:    "<p>{{block.top}}</p>",
		// A line break inside the braces is escaped in the stored JSON; the renderer decodes it.
		Content:      json.RawMessage(`{"text":"Hi {{\nblock.promo\n}}","card":{"title":"{{block.card_title}}"}}`),
		Translations: []model.Translation{{Locale: "hi", Content: json.RawMessage(`{"text":"{{block.hi_only}}"}`)}},
	}
	got := templateBlockKeys(tpl)
	for _, k := range []string{"subj", "top", "promo", "card_title", "hi_only"} {
		if !got[k] {
			t.Errorf("missed %s: %v", k, got)
		}
	}
	if normalizeBlockKey("  promo ") != "promo" {
		t.Error("normalize")
	}
}
