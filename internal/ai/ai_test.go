package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fake struct {
	answer  string
	gotUser string
	schema  map[string]any
}

func (f *fake) JSON(_ context.Context, _, user string, schema map[string]any) ([]byte, error) {
	f.gotUser, f.schema = user, schema
	return []byte(f.answer), nil
}

func TestHelper(t *testing.T) {
	ctx := context.Background()
	f := &fake{answer: `{"subjects":["Your picks are back","  ","Last day: 20% off"]}`}
	h := Helper{C: f}
	subs, err := h.SubjectLines(ctx, "<p>Sale</p>", "Sale", 3)
	if err != nil || len(subs) != 2 || f.schema["additionalProperties"] != false {
		t.Fatalf("subjects %v %v", subs, err)
	}
	src := `<p>Hi {{contact.first_name | default:"there"}}, see {{reco.1.name}}</p>{{block.footer}}`
	f.answer = `{"text":"<p>Hey {{contact.first_name | default:\"there\"}}! Check {{reco.1.name}}</p>{{block.footer}}"}`
	if out, err := h.Rewrite(ctx, src, "friendlier", "email"); err != nil || !strings.Contains(out, "Hey") {
		t.Errorf("rewrite: %q %v", out, err)
	}
	f.answer = `{"text":"<p>Hey there! Check {{reco.1.name}}</p>{{block.footer}}"}` // dropped a placeholder
	if _, err := h.Rewrite(ctx, src, "friendlier", "email"); err == nil {
		t.Error("a rewrite that lost a placeholder was accepted")
	}
	if _, err := h.Rewrite(ctx, src, "louder", "email"); err == nil {
		t.Error("unknown style")
	}
	f.answer = `{"subject":"नमस्ते {{contact.first_name}}","text":"सेल"}`
	tr, err := h.Translate(ctx, map[string]string{"subject": "Hello {{contact.first_name}}", "text": "Sale", "html": ""}, "hi")
	if err != nil || tr["subject"] != "नमस्ते {{contact.first_name}}" || !strings.Contains(f.gotUser, `"hi"`) {
		t.Errorf("translate %v %v", tr, err)
	}
	f.answer = `{"rule":"{\"attr\":\"city\",\"op\":\"eq\",\"value\":\"Pune\"}","note":"People in Pune"}`
	rule, note, err := h.SegmentRule(ctx, "people in Pune", []string{"city"}, []string{"purchase"})
	if err != nil || note == "" || !json.Valid(rule) || !strings.Contains(f.gotUser, "city") {
		t.Errorf("segment %s %q %v", rule, note, err)
	}
	if !SameTags("{{ a }} {{b}}", "{{b}} x {{a}}") || SameTags("{{a}}", "{{a}}{{a}}") {
		t.Error("SameTags")
	}
}
