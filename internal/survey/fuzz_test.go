package survey

import (
	"strings"
	"testing"
	"unicode/utf8"

	"pulse/internal/model"
)

func TestLongTextAnswerKeepsWholeCharacters(t *testing.T) {
	s := model.Survey{Questions: []model.SurveyQuestion{{ID: "why", Type: Text, Text: "Why?"}, {ID: "plan", Type: Choice, Text: "Plan", Options: []string{" Pro ", "Free"}}}}
	a, p := Answers(s, map[string]string{"q_why": "a" + strings.Repeat("é", MaxText), "q_plan": "Pro"})
	why, _ := a["why"].(string)
	if len(why) > MaxText || !utf8.ValidString(why) || len(why) < MaxText-1 {
		t.Errorf("cut answer: %d bytes, valid %v", len(why), utf8.ValidString(why))
	}
	if a["plan"] != "Pro" || len(p) != 0 {
		t.Errorf("an option with spaces around it can't be picked: %v %v", a, p)
	}
}

// FuzzAnswers checks survey validation and answer parsing with arbitrary input: no panics, scores
// in range, text answers capped and valid UTF-8 when the input was.
func FuzzAnswers(f *testing.F) {
	f.Add("nps", "nps", "9", "Free\nPro", "why")
	f.Add("rating", "q", "6", "", "x")
	f.Add("choice", "plan", "Pro", "Free\nPro", "")
	f.Add("text", "why", strings.Repeat("é", 1500), "", "")
	f.Fuzz(func(t *testing.T, typ, id, answer, options, name string) {
		q := model.SurveyQuestion{ID: id, Type: typ, Text: "Q", Options: strings.Split(options, "\n"), Required: true}
		s := model.Survey{Name: name, Status: Live, Questions: []model.SurveyQuestion{q}}
		_ = Validate(s)
		a, p := Answers(s, map[string]string{"q_" + id: answer})
		if len(a)+len(p) > 1 {
			t.Fatalf("one question gave %v and %v", a, p)
		}
		switch v := a[id].(type) {
		case int:
			if v < 0 || v > 10 {
				t.Fatalf("score %d", v)
			}
		case string:
			if len(v) > MaxText || (utf8.ValidString(answer) && !utf8.ValidString(v)) {
				t.Fatalf("text answer %q", v)
			}
		}
		NPSQuestion(s)
	})
}
