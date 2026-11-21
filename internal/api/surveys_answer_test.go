package api

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/survey"
	"pulse/internal/tracking"
)

// surveyMem keeps one answer per (contact, message), changed only when the answers differ (as pg).
type surveyMem struct {
	*memStore
	sv      model.Survey
	answers map[string]map[string]any
}

func (m *surveyMem) ListSurveys(context.Context, int64) ([]model.Survey, error) { return nil, nil }
func (m *surveyMem) SurveyByIDPublic(ctx context.Context, id int64) (model.Survey, error) {
	return m.GetSurvey(ctx, 0, id)
}

func (m *surveyMem) GetSurvey(context.Context, int64, int64) (model.Survey, error) {
	return m.sv, nil
}
func (m *surveyMem) SaveSurvey(context.Context, model.Survey) (int64, error) { return 0, nil }
func (m *surveyMem) DeleteSurvey(context.Context, int64, int64) error        { return nil }
func (m *surveyMem) SurveyResponses(context.Context, int64, int64, time.Time, int) ([]model.SurveyResponse, error) {
	return nil, nil
}
func (m *surveyMem) SaveSurveyResponse(_ context.Context, r model.SurveyResponse) (bool, error) {
	k := r.ExternalID + "/" + r.MessageID
	if old, ok := m.answers[k]; ok && reflect.DeepEqual(old, r.Answers) {
		return false, nil
	}
	m.answers[k] = r.Answers
	return true, nil
}

func TestSurveyAnswerEventOnlyWhenChanged(t *testing.T) {
	_, base, _ := newServer()
	box, _ := auth.NewBox(strings.Repeat("cd", 32))
	codec := tracking.New(box, strings.Repeat("cd", 32), "https://pulse.test")
	st := &surveyMem{memStore: base, answers: map[string]map[string]any{},
		sv: model.Survey{ID: 4, TenantID: 1, Name: "NPS", Status: survey.Live, Questions: []model.SurveyQuestion{{ID: "n", Type: survey.NPS, Text: "How likely?"}}}}
	pub := &memPub{}
	s := New(Deps{Store: st, Pub: pub, Box: box, Tracking: codec, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	h := s.Handler()
	tok := codec.Encode(tracking.Token{TenantID: 1, ExternalID: "ann", MessageID: "m1", Channel: "email"})
	link, _ := url.Parse(codec.SurveyURL(4, tok))
	post := func(score string) int {
		req := httptest.NewRequest("POST", link.Path, strings.NewReader("q_n="+score))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for _, sc := range []string{"9", "9", "7"} {
		if c := post(sc); c != 200 {
			t.Fatalf("answer %s: %d", sc, c)
		}
	}
	if len(pub.recs) != 2 {
		t.Errorf("survey_answered events: %d, want 2 (the repeat records nothing)", len(pub.recs))
	}
}
