package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
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

func TestTooLongAndMaxTokens(t *testing.T) {
	h := Helper{C: &fake{answer: `{"text":"x"}`}}
	var tl *TooLongError
	if _, err := h.Rewrite(context.Background(), strings.Repeat("a", MaxRewrite+1), "fix", "email"); !errors.As(err, &tl) || !strings.Contains(err.Error(), "the limit is 60 KB") {
		t.Errorf("long rewrite: %v", err)
	}
	if _, err := h.Translate(context.Background(), map[string]string{"html": strings.Repeat("a", MaxTranslate)}, "hi"); !errors.As(err, &tl) {
		t.Errorf("long translation: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"{\"text\":\"cut"}],"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	c := &Claude{api: anthropic.NewClient(option.WithAPIKey("k"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))}
	if _, err := c.JSON(context.Background(), "s", "u", map[string]any{"type": "object"}); !errors.Is(err, ErrAnswerTooLong) {
		t.Errorf("max tokens: %v", err)
	}
}
