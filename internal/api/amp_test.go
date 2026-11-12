package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/amp"
	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/model"
	"pulse/internal/store/pg"
	"pulse/internal/tracking"
)

// ampMem adds templates and email providers to memStore.
type ampMem struct {
	*memStore
	templates map[int64]model.Template
	providers []model.Provider
}

func (m *ampMem) Template(_ context.Context, tenant, id int64) (model.Template, error) {
	if t, ok := m.templates[id]; ok && t.TenantID == tenant {
		return t, nil
	}
	return model.Template{}, pg.ErrNotFound
}

func (m *ampMem) ListProviders(_ context.Context, tenant int64) ([]model.Provider, error) {
	var out []model.Provider
	for _, p := range m.providers {
		if p.TenantID == tenant {
			out = append(out, p)
		}
	}
	return out, nil
}

func ampFixture(t *testing.T) (http.Handler, *tracking.Codec, *memPub, *miniredis.Miniredis, *ampMem) {
	t.Helper()
	_, base, _ := newServer()
	box, _ := auth.NewBox(strings.Repeat("ab", 32))
	codec := tracking.New(box, strings.Repeat("ab", 32), "https://pulse.test")
	doc := &amp.Doc{Blocks: []amp.Block{
		{ID: "poll-1", Type: amp.TypePoll, Text: "Colour?", Options: []string{"Red", "Blue"}, ShowResults: true},
		{ID: "signup", Type: amp.TypeForm, Fields: []amp.FormField{
			{Key: "city", Label: "City", Type: "text", Required: true, SaveTo: "city"},
			{Key: "mail", Label: "Mail", Type: "email", SaveTo: "email"}, // saved as a template before the check: still refused at post time
			{Key: "size", Label: "Size", Type: "select", Options: []string{"S", "M"}},
		}},
	}}
	blocks, _ := json.Marshal(doc)
	content, _ := json.Marshal(map[string]any{"amp_blocks": json.RawMessage(blocks)})
	tpl := model.Template{ID: 7, TenantID: 1, Channel: channel.Email, Subject: "s", HTML: "<p>x</p>", Content: content}
	if err := channel.PrepareAMP(&tpl); err != nil {
		t.Fatal(err)
	}
	st := &ampMem{memStore: base, templates: map[int64]model.Template{7: tpl},
		providers: []model.Provider{{TenantID: 1, Channel: "email", Kind: "smtp", Name: "Main", Config: map[string]string{"from": "Acme <news@acme.test>"}},
			{TenantID: 2, Channel: "email", Kind: "smtp", Config: map[string]string{"from": "x@other.test"}}}}
	mr := miniredis.RunT(t)
	pub := &memPub{}
	s := New(Deps{Store: st, Pub: pub, Box: box, Tracking: codec, RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		DefaultFrom: "Pulse <noreply@pulse.local>", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return s.Handler(), codec, pub, mr, st
}

func ampTok(c *tracking.Codec, block string, mod func(*tracking.AMPToken)) string {
	t := tracking.AMPToken{Token: tracking.Token{TenantID: 1, ExternalID: "u1", MessageID: "bc1:1:1", Channel: "email", TemplateID: 7, BroadcastID: 1, Run: 1},
		ContactID: 1, Block: block, Exp: time.Now().Add(time.Hour).Unix()}
	if mod != nil {
		mod(&t)
	}
	return c.EncodeAMP(t)
}

func ampPost(h http.Handler, tok string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/amp/"+tok, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

var v2 = map[string]string{"AMP-Email-Sender": "news@acme.test"}

func TestAMPPollPostBack(t *testing.T) {
	h, codec, pub, _, _ := ampFixture(t)
	tok := ampTok(codec, "poll-1", nil)
	w := ampPost(h, tok, url.Values{"answer": {"Blue"}}, v2)
	if w.Code != 200 || w.Header().Get("AMP-Email-Allow-Sender") != "news@acme.test" {
		t.Fatalf("%d %s %v", w.Code, w.Body, w.Header())
	}
	var out struct {
		Message string     `json:"message"`
		Items   []pollItem `json:"items"`
		Total   int64      `json:"total"`
		Already bool       `json:"already"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Total != 1 || out.Items[1].Label != "Blue" || out.Items[1].Percent != 100 || out.Already {
		t.Errorf("results %+v", out)
	}
	evs := pubEvents(t, pub)
	if len(evs) != 1 || evs[0].Name != "amp_poll_answered" || evs[0].ExternalID != "u1" || evs[0].TenantID != 1 ||
		evs[0].Props["answer"] != "Blue" || evs[0].Props["block"] != "poll-1" || evs[0].Props["broadcast_id"] != float64(1) {
		t.Fatalf("events %+v", evs)
	}
	// Replay (or a second answer): the first counts, nothing more is recorded.
	w = ampPost(h, tok, url.Values{"answer": {"Red"}}, v2)
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || !out.Already || out.Total != 1 || !strings.Contains(out.Message, "Blue") || len(pubEvents(t, pub)) != 0 {
		t.Errorf("replay: %d %s", w.Code, w.Body)
	}
	// Not one of the answers.
	if w := ampPost(h, ampTok(codec, "poll-1", func(a *tracking.AMPToken) { a.ContactID = 2; a.ExternalID = "u2" }), url.Values{"answer": {"Green"}}, v2); w.Code != 400 {
		t.Errorf("bad answer: %d", w.Code)
	}
	// Results for amp-list.
	req := httptest.NewRequest("GET", "/v1/amp/"+tok+"/results", nil)
	req.Header.Set("AMP-Email-Sender", "news@acme.test")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), `"label":"Blue","count":1,"percent":100`) {
		t.Errorf("results: %d %s", rw.Code, rw.Body)
	}
	// A test send's token: answered, nothing recorded or counted.
	w = ampPost(h, ampTok(codec, "poll-1", func(a *tracking.AMPToken) { a.Test, a.ContactID, a.ExternalID = true, 0, "" }), url.Values{"answer": {"Red"}}, v2)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Test email") || len(pubEvents(t, pub)) != 0 {
		t.Errorf("test token: %d %s", w.Code, w.Body)
	}
}

func TestAMPTokenAndCORS(t *testing.T) {
	h, codec, pub, _, _ := ampFixture(t)
	tok := ampTok(codec, "poll-1", nil)
	forged := "x" + tok[1:]
	if tok[0] == 'x' { // tokens start with a random character: always change it
		forged = "y" + tok[1:]
	}
	answer := url.Values{"answer": {"Red"}}
	for name, c := range map[string]struct {
		tok  string
		hdr  map[string]string
		q    string
		want int
	}{
		"forged token":         {tok: forged, hdr: v2, want: 400},
		"tracking token":       {tok: codec.Encode(tracking.Token{TenantID: 1, ExternalID: "u1", MessageID: "m"}), hdr: v2, want: 400},
		"expired":              {tok: ampTok(codec, "poll-1", func(a *tracking.AMPToken) { a.Exp = time.Now().Add(-time.Minute).Unix() }), hdr: v2, want: 410},
		"no AMP headers":       {tok: tok, want: 403},
		"other sender domain":  {tok: tok, hdr: map[string]string{"AMP-Email-Sender": "news@evil.test"}, want: 403},
		"other workspace's":    {tok: tok, hdr: map[string]string{"AMP-Email-Sender": "x@other.test"}, want: 403},
		"v1 unknown origin":    {tok: tok, hdr: map[string]string{"Origin": "https://evil.test"}, q: "?__amp_source_origin=news%40acme.test", want: 403},
		"v1 origin, no source": {tok: tok, hdr: map[string]string{"Origin": "https://mail.google.com"}, want: 403},
		"unknown block":        {tok: ampTok(codec, "nope", nil), hdr: v2, want: 404},
		"other template":       {tok: ampTok(codec, "poll-1", func(a *tracking.AMPToken) { a.TemplateID = 99 }), hdr: v2, want: 404},
	} {
		req := httptest.NewRequest("POST", "/v1/amp/"+c.tok+c.q, strings.NewReader(answer.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range c.hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, c.want)
		}
		if c.want == 403 && (w.Header().Get("AMP-Email-Allow-Sender") != "" || w.Header().Get("Access-Control-Allow-Origin") != "") {
			t.Errorf("%s: CORS allowed on refusal", name)
		}
	}
	if len(pubEvents(t, pub)) != 0 {
		t.Error("a refused request recorded an event")
	}
	// Version 1: a known email client's Origin and the sender as __amp_source_origin. The
	// server's default sender domain counts too.
	req := httptest.NewRequest("POST", "/v1/amp/"+tok+"?__amp_source_origin=noreply%40pulse.local", strings.NewReader(answer.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://mail.google.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://mail.google.com" ||
		w.Header().Get("AMP-Access-Control-Allow-Source-Origin") != "noreply@pulse.local" ||
		w.Header().Get("Access-Control-Expose-Headers") != "AMP-Access-Control-Allow-Source-Origin" {
		t.Errorf("v1: %d %v", w.Code, w.Header())
	}
}

func TestAMPFormFieldsAndRateLimit(t *testing.T) {
	h, codec, pub, mr, _ := ampFixture(t)
	tok := ampTok(codec, "signup", nil)
	if w := ampPost(h, tok, url.Values{"size": {"S"}}, v2); w.Code != 400 || !strings.Contains(w.Body.String(), "City") {
		t.Fatalf("required: %d %s", w.Code, w.Body)
	}
	if w := ampPost(h, tok, url.Values{"city": {"Pune"}, "size": {"XL"}}, v2); w.Code != 400 {
		t.Fatalf("select: %d", w.Code)
	}
	// Extra fields (attrs, consent, tenant ids) in the body are ignored: only the form's fields count,
	// and only custom fields are updated.
	w := ampPost(h, tok, url.Values{"city": {"Pune"}, "mail": {"new@x.test"}, "size": {"M"}, "optout_email": {"false"},
		"tenant_id": {"2"}, "external_id": {"someone"}}, v2)
	if w.Code != 200 {
		t.Fatalf("submit: %d %s", w.Code, w.Body)
	}
	evs := pubEvents(t, pub)
	if len(evs) != 1 {
		t.Fatalf("events %+v", evs)
	}
	e := evs[0]
	if e.Name != "amp_form_submitted" || e.TenantID != 1 || e.ExternalID != "u1" || e.Props["city"] != "Pune" || e.Props["size"] != "M" ||
		e.Props["optout_email"] != nil || e.Props["tenant_id"] != nil {
		t.Errorf("event %+v", e)
	}
	if len(e.Attrs) != 1 || e.Attrs["city"] != "Pune" {
		t.Errorf("attrs %v: only the custom field city may change", e.Attrs)
	}
	// The same post again gets the same event id (recorded once downstream).
	ampPost(h, tok, url.Values{"city": {"Pune"}, "mail": {"new@x.test"}, "size": {"M"}}, v2)
	if again := pubEvents(t, pub); len(again) != 1 || again[0].ID != e.ID {
		t.Errorf("replay ids: %v vs %s", again, e.ID)
	}
	// Rate limit: 10 answers per token per minute.
	mr.FlushAll()
	codes := map[int]int{}
	for range 12 {
		codes[ampPost(h, tok, url.Values{"city": {"Pune"}}, v2).Code]++
	}
	if codes[429] != 2 {
		t.Errorf("rate limit: %v", codes)
	}
}

func TestAMPCheckAndSaveTargets(t *testing.T) {
	h, _, _, _, _ := ampFixture(t)
	body := `{"amp_blocks":{"blocks":[{"id":"f","type":"form","fields":[{"key":"a","label":"A","type":"text","save_to":"optout_email"}]}]}}`
	w := do(t, h, "POST", "/v1/templates/amp/check", "key-1", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "can't save into") || !strings.Contains(w.Body.String(), `"name":"Main","amp":true`) {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	w = do(t, h, "POST", "/v1/templates/amp/check", "key-1", `{"amp":"<!doctype html>\n<html>\n</html>"}`)
	if !strings.Contains(w.Body.String(), `"line":2`) {
		t.Errorf("lines: %s", w.Body)
	}
	w = do(t, h, "POST", "/v1/templates/amp/check", "key-1", `{"amp_blocks":{"blocks":[{"id":"t","type":"text","text":"Hi {{contact.first_name}}"}]}}`)
	var out struct {
		Problems []amp.Problem `json:"problems"`
		Preview  string        `json:"preview"`
		AMP      string        `json:"amp"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Problems) != 0 || !strings.Contains(out.AMP, "⚡4email") || !strings.Contains(out.Preview, "Hi Asha") || strings.Contains(out.Preview, "<script") {
		t.Errorf("built: %+v", out)
	}
}
