//go:build e2e

package e2e

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestSurveys (O6): an NPS survey linked from an email ({{survey.ID}}?score=9) opens with the score
// picked but records nothing until the contact sends it; the answer is stored, recorded as
// survey_answered on the contact and counted in the report; the public link takes anonymous answers.
func TestSurveys(t *testing.T) {
	c, _ := workspace(t, "Surveys")
	run := fmt.Sprint(time.Now().UnixNano())
	jsonCall(t, c, "POST", "/v1/surveys", map[string]any{"name": "x", "questions": []any{map[string]any{"id": "a", "type": "nps", "text": "?"}, map[string]any{"id": "b", "type": "nps", "text": "?"}}}, 400)
	id := jsonCall(t, c, "POST", "/v1/surveys", map[string]any{"name": "How did we do " + run, "status": "live", "thank_you": "Thanks a lot", "questions": []any{
		map[string]any{"id": "nps", "type": "nps", "text": "How likely are you to recommend us?", "required": true},
		map[string]any{"id": "why", "type": "text", "text": "Why?"},
		map[string]any{"id": "plan", "type": "choice", "text": "Your plan", "options": []string{"Free", "Pro"}},
	}}, 201)["id"]
	sv := jsonCall(t, c, "GET", fmt.Sprintf("/v1/surveys/%v", id), nil, 200)
	public := sv["links"].(map[string]any)["public"].(string)

	ext, email := "sv-"+run, "sv-"+run+"@example.com"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "email": email}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "nps", "channel": "email", "subject": "Quick question " + run,
		"html": fmt.Sprintf(`<html><body><a href="{{survey.%v}}?score=9">9</a></body></html>`, id)}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	var search struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	waitFor(t, 20*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+email), &search)
		return len(search.Messages) == 1
	})
	var msg struct{ HTML string }
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID, &msg)
	m := regexp.MustCompile(`<a href="([^"]+)">9</a>`).FindStringSubmatch(msg.HTML)
	if m == nil {
		t.Fatalf("no survey link:\n%s", msg.HTML)
	}
	resp, err := http.Get(html.UnescapeString(m[1])) // through the click tracker
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	page := resp.Request.URL.String()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `value="9" checked`) || !strings.Contains(page, fmt.Sprintf("/s/%v/", id)) {
		t.Fatalf("survey page %d %s:\n%s", resp.StatusCode, page, body)
	}
	report := func() map[string]any {
		return jsonCall(t, c, "GET", fmt.Sprintf("/v1/surveys/%v/report", id), nil, 200)
	}
	if r := report(); r["responses"] != float64(0) {
		t.Fatalf("opening the link answered: %v", r)
	}
	post := func(u string, v url.Values) (int, string) {
		resp, err := http.PostForm(strings.Split(u, "?")[0], v)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := post(page, url.Values{"q_why": {"no score"}}); code != 422 {
		t.Errorf("missing required answer: %d", code)
	}
	if code, b := post(page, url.Values{"q_nps": {"9"}, "q_why": {"Fast delivery"}, "q_plan": {"Pro"}}); code != 200 || !strings.Contains(b, "Thanks a lot") {
		t.Fatalf("answer: %d %s", code, b)
	}
	post(page, url.Values{"q_nps": {"10"}, "q_why": {"Even better"}}) // answering again updates it
	if code, _ := post(public, url.Values{"q_nps": {"3"}}); code != 200 {
		t.Errorf("public answer: %d", code)
	}
	var r map[string]any
	waitFor(t, 10*time.Second, func() bool { r = report(); return r["responses"] == float64(2) })
	nps := r["nps"].(map[string]any)
	if nps["score"] != float64(0) || nps["promoters"] != float64(1) || nps["detractors"] != float64(1) {
		t.Errorf("nps: %v", nps)
	}
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "survey_answered") })
	if code, _ := post(strings.Replace(page, fmt.Sprintf("/s/%v/", id), fmt.Sprintf("/s/%v/", id)+"x", 1), url.Values{"q_nps": {"1"}}); code != 404 {
		t.Errorf("forged token: %d", code)
	}
	// The link is bound to its survey: the same token can't answer another one.
	other := jsonCall(t, c, "POST", "/v1/surveys", map[string]any{"name": "Other " + run, "status": "live", "questions": []any{
		map[string]any{"id": "nps", "type": "nps", "text": "?"}}}, 201)["id"]
	if code, _ := post(strings.Replace(page, fmt.Sprintf("/s/%v/", id), fmt.Sprintf("/s/%v/", other), 1), url.Values{"q_nps": {"1"}}); code != 404 {
		t.Errorf("token reused for another survey: %d", code)
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "survey_answered", "props": map[string]any{"nps": 10}}, 400)
	// Closed: the page says so and takes nothing.
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/surveys/%v", id), map[string]any{"name": "How did we do " + run, "status": "closed", "questions": sv["survey"].(map[string]any)["questions"]}, 200)
	if _, b := post(public, url.Values{"q_nps": {"3"}}); !strings.Contains(b, "isn't taking answers") {
		t.Errorf("closed survey: %s", b)
	}
}
