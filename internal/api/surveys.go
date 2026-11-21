package api

// Surveys and NPS (O6, package survey).
//
//	GET/POST /v1/surveys, GET/PUT/DELETE /v1/surveys/{id}, GET /v1/surveys/{id}/report?days=90
//	GET/POST /s/{id}/{token}/{sig}  a message's link ({{survey.ID}}): the contact answers
//	GET/POST /s/{id}/p/{key}      the public link: anyone answers, without a contact
//
// GET only shows the page (?score=N pre-selects the NPS answer an email's 0-10 row linked to); the
// answer is recorded by the POST, so link scanners never answer for anyone.

import (
	"context"
	"crypto/subtle"
	"errors"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"time"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/store/pg"
	"pulse/internal/survey"
	"pulse/internal/tracking"
)

// SurveyStore keeps surveys and responses (pg.Store).
type SurveyStore interface {
	ListSurveys(ctx context.Context, tenant int64) ([]model.Survey, error)
	GetSurvey(ctx context.Context, tenant, id int64) (model.Survey, error)
	SaveSurvey(ctx context.Context, s model.Survey) (int64, error)
	DeleteSurvey(ctx context.Context, tenant, id int64) error
	SaveSurveyResponse(ctx context.Context, r model.SurveyResponse) error
	SurveyResponses(ctx context.Context, tenant, survey int64, since time.Time, limit int) ([]model.SurveyResponse, error)
}

func (s *Server) surveyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/surveys", s.tenant(s.listSurveys))
	mux.HandleFunc("POST /v1/surveys", s.tenant(s.saveSurvey))
	mux.HandleFunc("GET /v1/surveys/{id}", s.tenant(s.getSurvey))
	mux.HandleFunc("PUT /v1/surveys/{id}", s.tenant(s.saveSurvey))
	mux.HandleFunc("DELETE /v1/surveys/{id}", s.tenant(s.deleteSurvey))
	mux.HandleFunc("GET /v1/surveys/{id}/report", s.authed(s.surveyReport))
	mux.HandleFunc("GET /s/{id}/p/{key}", s.publicSurvey)
	mux.HandleFunc("POST /s/{id}/p/{key}", s.publicSurvey)
	mux.HandleFunc("GET /s/{id}/{token}/{sig}", s.messageSurvey)
	mux.HandleFunc("POST /s/{id}/{token}/{sig}", s.messageSurvey)
}

func (s *Server) surveyStore(w http.ResponseWriter) (SurveyStore, bool) {
	ss, ok := s.Store.(SurveyStore)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "surveys aren't available")
	}
	return ss, ok
}

func (s *Server) surveyLinks(sv model.Survey) map[string]string {
	return map[string]string{"public": s.publicBase() + "/s/" + strconv.FormatInt(sv.ID, 10) + "/p/" + sv.PublicKey,
		"variable": "{{survey." + strconv.FormatInt(sv.ID, 10) + "}}"}
}

func (s *Server) listSurveys(w http.ResponseWriter, r *http.Request, tenant int64) {
	ss, ok := s.surveyStore(w)
	if !ok {
		return
	}
	list, err := ss.ListSurveys(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"surveys": list})
}

func (s *Server) getSurvey(w http.ResponseWriter, r *http.Request, tenant int64) {
	ss, ok := s.surveyStore(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sv, err := ss.GetSurvey(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"survey": sv, "links": s.surveyLinks(sv)})
}

func (s *Server) saveSurvey(w http.ResponseWriter, r *http.Request, tenant int64) {
	ss, ok := s.surveyStore(w)
	if !ok {
		return
	}
	var sv model.Survey
	if !decode(w, r, &sv) {
		return
	}
	sv.TenantID, sv.ID = tenant, 0
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		sv.ID = id
	}
	if sv.Status == "" {
		sv.Status = survey.Draft
	}
	if err := survey.Validate(sv); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := ss.SaveSurvey(r.Context(), sv)
	if err != nil {
		s.fail(w, err)
		return
	}
	code := http.StatusOK
	if sv.ID == 0 {
		code = http.StatusCreated
	}
	writeJSON(w, code, map[string]any{"id": id})
}

func (s *Server) deleteSurvey(w http.ResponseWriter, r *http.Request, tenant int64) {
	ss, ok := s.surveyStore(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := ss.DeleteSurvey(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- report ----

type questionReport struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Answers int            `json:"answers"`
	Average float64        `json:"average,omitempty"`
	Counts  map[string]int `json:"counts,omitempty"`
	Latest  []textAnswer   `json:"latest,omitempty"`
}

type textAnswer struct {
	At         time.Time `json:"at"`
	ExternalID string    `json:"external_id,omitempty"`
	Text       string    `json:"text"`
	NPS        *int      `json:"nps,omitempty"`
}

// surveyReport: comments show who wrote them only to roles that may view contacts.
func (s *Server) surveyReport(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	showWho := p.Perms.Allows(auth.AreaContacts, auth.View)
	ss, ok := s.surveyStore(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sv, err := ss.GetSurvey(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 730 {
		days = 90
	}
	const cap = 20000
	rs, err := ss.SurveyResponses(r.Context(), tenant, id, s.now().AddDate(0, 0, -days), cap)
	if err != nil {
		s.fail(w, err)
		return
	}
	var dist [11]int
	weeks := map[string]*[11]int{}
	qs := make([]*questionReport, len(sv.Questions))
	for i, q := range sv.Questions {
		qs[i] = &questionReport{ID: q.ID, Type: q.Type, Text: q.Text, Counts: map[string]int{}}
	}
	for _, x := range rs {
		if x.NPS != nil && *x.NPS >= 0 && *x.NPS <= 10 {
			dist[*x.NPS]++
			y, wk := x.At.UTC().ISOWeek()
			key := strconv.Itoa(y) + "-W" + strconv.Itoa(100 + wk)[1:]
			if weeks[key] == nil {
				weeks[key] = &[11]int{}
			}
			weeks[key][*x.NPS]++
		}
		for i, q := range sv.Questions {
			v, ok := x.Answers[q.ID]
			if !ok {
				continue
			}
			qr := qs[i]
			qr.Answers++
			switch q.Type {
			case survey.NPS, survey.Rating:
				n, _ := v.(float64)
				qr.Average += n
				qr.Counts[strconv.Itoa(int(n))]++
			case survey.Choice:
				qr.Counts[attrString(v)]++
			case survey.Text:
				if len(qr.Latest) < 50 {
					who := ""
					if showWho {
						who = x.ExternalID
					}
					qr.Latest = append(qr.Latest, textAnswer{At: x.At, ExternalID: who, Text: attrString(v), NPS: x.NPS})
				}
			}
		}
	}
	for _, qr := range qs {
		if (qr.Type == survey.NPS || qr.Type == survey.Rating) && qr.Answers > 0 {
			qr.Average /= float64(qr.Answers)
		}
	}
	score, n := survey.Score(dist)
	var trend []map[string]any
	keys := make([]string, 0, len(weeks))
	for k := range weeks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ws, wn := survey.Score(*weeks[k])
		trend = append(trend, map[string]any{"week": k, "nps": ws, "answers": wn})
	}
	pro, pas, det := 0, 0, 0
	for sc, c := range dist {
		switch survey.Group(sc) {
		case "promoter":
			pro += c
		case "passive":
			pas += c
		default:
			det += c
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"survey": sv, "links": s.surveyLinks(sv), "days": days, "responses": len(rs), "capped": len(rs) == cap,
		"nps":       map[string]any{"score": score, "answers": n, "distribution": dist, "promoters": pro, "passives": pas, "detractors": det, "trend": trend},
		"questions": qs})
}

func attrString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---- the hosted page ----

type surveyView struct {
	Survey   model.Survey
	Score    int // pre-selected NPS answer, -1 for none
	Values   map[string]string
	Problems map[string]string
	Done     bool
	Closed   bool
	Nonce    string
	Scale    []int
}

func (s *Server) messageSurvey(w http.ResponseWriter, r *http.Request) {
	if s.Tracking == nil {
		http.NotFound(w, r)
		return
	}
	tok, err := s.Tracking.Decode(r.PathValue("token"))
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tok.TenantID == 0 || tok.ExternalID == "" || id <= 0 || !s.Tracking.VerifySurvey(id, r.PathValue("token"), r.PathValue("sig")) {
		http.Error(w, "This survey link isn't valid.", http.StatusNotFound)
		return
	}
	s.answerSurvey(w, r, tok.TenantID, id, &tok)
}

func (s *Server) publicSurvey(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.Store.(SurveyStore)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !ok || id <= 0 {
		http.NotFound(w, r)
		return
	}
	sv, err := ss.GetSurvey(r.Context(), 0, id)
	if err != nil || subtle.ConstantTimeCompare([]byte(sv.PublicKey), []byte(r.PathValue("key"))) != 1 {
		http.Error(w, "This survey isn't available.", http.StatusNotFound)
		return
	}
	s.answerSurvey(w, r, sv.TenantID, id, nil)
}

// answerSurvey shows the survey (GET) or records an answer (POST); tok is nil for a public link.
func (s *Server) answerSurvey(w http.ResponseWriter, r *http.Request, tenant, id int64, tok *tracking.Token) {
	ss, ok := s.Store.(SurveyStore)
	if !ok {
		http.NotFound(w, r)
		return
	}
	sv, err := ss.GetSurvey(r.Context(), tenant, id)
	if err != nil {
		if !errors.Is(err, pg.ErrNotFound) {
			s.Log.Error("survey page", "err", err)
		}
		http.Error(w, "This survey isn't available.", http.StatusNotFound)
		return
	}
	if !s.allowRate(r.Context(), "rlsurvey", tenant, r, 1, pageViewsPerMin) {
		http.Error(w, "Too many requests from this network; try again in a minute.", http.StatusTooManyRequests)
		return
	}
	v := surveyView{Survey: sv, Score: -1, Values: map[string]string{}, Problems: map[string]string{}, Scale: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}}
	if sv.Status != survey.Live {
		v.Closed = true
		renderSurvey(w, http.StatusOK, v)
		return
	}
	if r.Method == http.MethodGet {
		if n, err := strconv.Atoi(r.URL.Query().Get("score")); err == nil && n >= 0 && n <= 10 {
			v.Score = n
		}
		renderSurvey(w, http.StatusOK, v)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The answers couldn't be read.", http.StatusBadRequest)
		return
	}
	form := map[string]string{}
	for k := range r.PostForm {
		form[k] = r.PostForm.Get(k)
		v.Values[k] = form[k]
	}
	answers, problems := survey.Answers(sv, form)
	if len(problems) > 0 {
		v.Problems = problems
		if n, err := strconv.Atoi(form["q_"+survey.NPSQuestion(sv)]); err == nil {
			v.Score = n
		}
		renderSurvey(w, http.StatusUnprocessableEntity, v)
		return
	}
	resp := model.SurveyResponse{SurveyID: sv.ID, TenantID: tenant, Answers: answers}
	props := map[string]any{"survey_id": sv.ID, "survey": sv.Name}
	if q := survey.NPSQuestion(sv); q != "" {
		if n, ok := answers[q].(int); ok {
			resp.NPS = &n
			props["nps"], props["nps_group"] = n, survey.Group(n)
		}
	}
	if tok != nil {
		resp.ExternalID, resp.MessageID = tok.ExternalID, tok.MessageID
		for k, val := range tok.Props() {
			props[k] = val
		}
		for k, val := range answers {
			props["q_"+k] = val
		}
	}
	if err := ss.SaveSurveyResponse(r.Context(), resp); err != nil {
		s.Log.Error("survey answer", "err", err)
		http.Error(w, "Your answer couldn't be saved. Please try again.", http.StatusInternalServerError)
		return
	}
	if tok != nil {
		if err := s.publishEvents(r.Context(), tenant, []event.Event{{ExternalID: tok.ExternalID, Name: event.SurveyAnswered, Props: props}}); err != nil {
			s.Log.Error("survey answered event", "err", err) // the answer is stored; the event is best effort
		}
	}
	v.Done = true
	renderSurvey(w, http.StatusOK, v)
}

func renderSurvey(w http.ResponseWriter, code int, v surveyView) {
	v.Nonce = auth.NewToken("")[:22]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+v.Nonce+"'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(code)
	_ = surveyPage.Execute(w, v)
}

var surveyPage = template.Must(template.New("survey").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>{{.Survey.Name}}</title>
<style nonce="{{.Nonce}}">
:root{color-scheme:light dark;--ink:#1d2029;--muted:#5b6070;--line:#dfe2ea;--bg:#f6f7fb;--card:#fff;--accent:#2b4bd1;--bad:#c62828}
@media(prefers-color-scheme:dark){:root{--ink:#e7e9ee;--muted:#a0a6b4;--line:#2a2e38;--bg:#101217;--card:#181b22;--accent:#8ea6ff;--bad:#ff8a80}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.5 system-ui,sans-serif}
main{max-width:640px;margin:0 auto;padding:32px 16px}.card{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:24px}
h1{font-size:24px;margin:0 0 8px}p.intro{color:var(--muted);margin:0 0 20px}fieldset{border:0;padding:0;margin:0 0 22px}
legend{font-weight:600;margin-bottom:10px}.scale{display:grid;grid-template-columns:repeat(11,1fr);gap:4px}
.scale label,.opts label{display:block;cursor:pointer}.scale input,.opts input{position:absolute;opacity:0}
.scale span{display:block;text-align:center;padding:10px 0;border:1px solid var(--line);border-radius:8px;font-weight:600}
.scale input:checked+span,.opts input:checked+span{background:var(--accent);color:#fff;border-color:var(--accent)}
.scale input:focus-visible+span,.opts input:focus-visible+span{outline:2px solid var(--accent);outline-offset:2px}
.ends{display:flex;justify-content:space-between;color:var(--muted);font-size:13px;margin-top:6px}
.opts{display:flex;flex-wrap:wrap;gap:8px}.opts span{display:inline-block;padding:8px 14px;border:1px solid var(--line);border-radius:999px}
textarea{width:100%;min-height:96px;padding:10px;border:1px solid var(--line);border-radius:8px;background:transparent;color:inherit;font:inherit}
.err{color:var(--bad);font-size:14px;margin-top:6px}button{background:var(--accent);color:#fff;border:0;border-radius:10px;padding:12px 22px;font:inherit;font-weight:600;cursor:pointer}
@media(max-width:480px){.scale{grid-template-columns:repeat(6,1fr)}}
</style></head><body><main><div class="card">
{{if .Closed}}<h1>{{.Survey.Name}}</h1><p class="intro">This survey isn't taking answers right now. Thank you for your time.</p>
{{else if .Done}}<h1>Thank you!</h1><p class="intro">{{if .Survey.ThankYou}}{{.Survey.ThankYou}}{{else}}Your answer was saved.{{end}}</p>
{{else}}<h1>{{.Survey.Name}}</h1>{{if .Survey.Intro}}<p class="intro">{{.Survey.Intro}}</p>{{end}}
<form method="post">
{{range .Survey.Questions}}{{$q := .}}<fieldset><legend>{{.Text}}{{if .Required}} *{{end}}</legend>
{{if eq .Type "nps"}}<div class="scale">{{range $.Scale}}<label><input type="radio" name="q_{{$q.ID}}" value="{{.}}"{{if eq . $.Score}} checked{{end}}><span>{{.}}</span></label>{{end}}</div>
<div class="ends"><span>Not at all likely</span><span>Extremely likely</span></div>
{{else if eq .Type "rating"}}<div class="opts">{{range $i, $n := (slice $.Scale 1 6)}}<label><input type="radio" name="q_{{$q.ID}}" value="{{$n}}"{{if eq (index $.Values (printf "q_%s" $q.ID)) (printf "%d" $n)}} checked{{end}}><span>{{$n}} ★</span></label>{{end}}</div>
{{else if eq .Type "choice"}}<div class="opts">{{range .Options}}<label><input type="radio" name="q_{{$q.ID}}" value="{{.}}"{{if eq (index $.Values (printf "q_%s" $q.ID)) .}} checked{{end}}><span>{{.}}</span></label>{{end}}</div>
{{else}}<textarea name="q_{{.ID}}" maxlength="2000" aria-label="{{.Text}}">{{index $.Values (printf "q_%s" .ID)}}</textarea>{{end}}
{{with index $.Problems .ID}}<div class="err" role="alert">{{.}}</div>{{end}}</fieldset>
{{end}}<button type="submit">Send</button></form>{{end}}
</div></main></body></html>`))
