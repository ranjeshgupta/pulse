package survey

import (
	"testing"

	"pulse/internal/model"
)

func TestSurvey(t *testing.T) {
	s := model.Survey{Name: "NPS", Status: Live, Questions: []model.SurveyQuestion{
		{ID: "nps", Type: NPS, Text: "How likely…?", Required: true},
		{ID: "why", Type: Text, Text: "Why?"},
		{ID: "plan", Type: Choice, Text: "Plan", Options: []string{"Free", "Pro"}},
		{ID: "ease", Type: Rating, Text: "Ease"},
	}}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	bad := s
	bad.Questions = append([]model.SurveyQuestion{}, s.Questions...)
	bad.Questions = append(bad.Questions, model.SurveyQuestion{ID: "nps", Type: NPS, Text: "again"})
	if Validate(bad) == nil {
		t.Error("duplicate NPS accepted")
	}
	a, p := Answers(s, map[string]string{"q_nps": "9", "q_why": " fast ", "q_plan": "Pro", "q_ease": "6"})
	if a["nps"] != 9 || a["why"] != "fast" || a["plan"] != "Pro" || p["ease"] == "" || len(p) != 1 {
		t.Errorf("answers %v problems %v", a, p)
	}
	if _, p := Answers(s, map[string]string{"q_plan": "Gold"}); p["nps"] == "" || p["plan"] == "" {
		t.Errorf("required/invalid: %v", p)
	}
	if Group(9) != "promoter" || Group(7) != "passive" || Group(6) != "detractor" {
		t.Error("groups")
	}
	var d [11]int
	d[10], d[8], d[3] = 5, 3, 2 // 50% promoters, 20% detractors
	if nps, n := Score(d); nps != 30 || n != 10 {
		t.Errorf("score %v %d", nps, n)
	}
}
