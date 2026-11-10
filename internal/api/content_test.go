package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// contentStore adds forms, assets and landing pages to memStore.
type contentStore struct {
	*memStore
	forms     map[int64]model.Form
	assets    map[int64]model.Asset
	files     map[int64][]byte
	pages     map[int64]model.LandingPage
	usedBy    map[int64][]string // asset -> template names
	templates map[int64]model.Template
}

func (c *contentStore) ListForms(_ context.Context, tenant int64) ([]model.Form, error) {
	var out []model.Form
	for _, f := range c.forms {
		if f.TenantID == tenant {
			out = append(out, f)
		}
	}
	return out, nil
}
func (c *contentStore) GetForm(_ context.Context, tenant, id int64) (model.Form, error) {
	if f, ok := c.forms[id]; ok && f.TenantID == tenant {
		return f, nil
	}
	return model.Form{}, pg.ErrNotFound
}
func (c *contentStore) FormByKey(_ context.Context, key string) (model.Form, error) {
	for _, f := range c.forms {
		if f.PublicKey == key {
			return f, nil
		}
	}
	return model.Form{}, pg.ErrNotFound
}
func (c *contentStore) SaveForm(context.Context, model.Form) (int64, error) { return 0, nil }
func (c *contentStore) DeleteForm(context.Context, int64, int64) error      { return nil }
func (c *contentStore) ContactByEmail(context.Context, int64, string) (model.Contact, error) {
	return model.Contact{}, pg.ErrNotFound
}
func (c *contentStore) ContactsByPhone(context.Context, int64, string) ([]model.Contact, error) {
	return nil, nil
}

func (c *contentStore) ListAssets(_ context.Context, tenant int64) ([]model.Asset, error) {
	var out []model.Asset
	for _, a := range c.assets {
		if a.TenantID == tenant {
			out = append(out, a)
		}
	}
	return out, nil
}
func (c *contentStore) GetAsset(_ context.Context, tenant, id int64) (model.Asset, error) {
	if a, ok := c.assets[id]; ok && a.TenantID == tenant {
		return a, nil
	}
	return model.Asset{}, pg.ErrNotFound
}
func (c *contentStore) AssetByKey(_ context.Context, key string) (model.Asset, error) {
	for _, a := range c.assets {
		if a.PublicKey == key {
			return a, nil
		}
	}
	return model.Asset{}, pg.ErrNotFound
}
func (c *contentStore) AssetsByIDs(_ context.Context, tenant int64, ids []int64) ([]model.Asset, error) {
	var out []model.Asset
	for _, id := range ids {
		if a, ok := c.assets[id]; ok && a.TenantID == tenant {
			out = append(out, a)
		}
	}
	return out, nil
}
func (c *contentStore) CreateAsset(_ context.Context, a model.Asset, data []byte, _ int64) (int64, error) {
	a.ID, a.Size = int64(len(c.assets)+1), int64(len(data))
	c.assets[a.ID], c.files[a.ID] = a, data
	return a.ID, nil
}
func (c *contentStore) UpdateAsset(_ context.Context, a model.Asset) error {
	cur, ok := c.assets[a.ID]
	if !ok || cur.TenantID != a.TenantID {
		return pg.ErrNotFound
	}
	cur.Title, cur.Description, cur.FormID = a.Title, a.Description, a.FormID
	c.assets[a.ID] = cur
	return nil
}
func (c *contentStore) DeleteAsset(_ context.Context, _, id int64) error {
	delete(c.assets, id)
	return nil
}
func (c *contentStore) AssetFile(ctx context.Context, tenant, id int64) (model.Asset, []byte, error) {
	a, err := c.GetAsset(ctx, tenant, id)
	return a, c.files[id], err
}
func (c *contentStore) CountAssetDownload(_ context.Context, _, id int64) error {
	a := c.assets[id]
	a.DownloadCount++
	c.assets[id] = a
	return nil
}
func (c *contentStore) TemplatesUsingAsset(_ context.Context, _, id int64) ([]string, error) {
	return c.usedBy[id], nil
}

func (c *contentStore) ListLandingPages(_ context.Context, tenant int64) ([]model.LandingPage, error) {
	var out []model.LandingPage
	for _, p := range c.pages {
		if p.TenantID == tenant {
			out = append(out, p)
		}
	}
	return out, nil
}
func (c *contentStore) GetLandingPage(_ context.Context, tenant, id int64) (model.LandingPage, error) {
	if p, ok := c.pages[id]; ok && p.TenantID == tenant {
		return p, nil
	}
	return model.LandingPage{}, pg.ErrNotFound
}
func (c *contentStore) LandingPageBySlug(_ context.Context, tenant int64, slug string) (model.LandingPage, error) {
	for _, p := range c.pages {
		if p.TenantID == tenant && p.Slug == slug {
			return p, nil
		}
	}
	return model.LandingPage{}, pg.ErrNotFound
}
func (c *contentStore) SaveLandingPage(_ context.Context, p model.LandingPage) (int64, error) {
	for _, o := range c.pages {
		if o.TenantID == p.TenantID && o.Slug == p.Slug && o.ID != p.ID {
			return 0, pg.ErrConflict
		}
	}
	if p.ID == 0 {
		p.ID = int64(len(c.pages) + 1)
	}
	c.pages[p.ID] = p
	return p.ID, nil
}
func (c *contentStore) DeleteLandingPage(_ context.Context, _, id int64) error {
	delete(c.pages, id)
	return nil
}

func (c *contentStore) Template(_ context.Context, _, id int64) (model.Template, error) {
	if t, ok := c.templates[id]; ok {
		return t, nil
	}
	return model.Template{}, pg.ErrNotFound
}
func (c *contentStore) UpdateTemplate(_ context.Context, t model.Template) error {
	c.templates[t.ID] = t
	return nil
}

func contentServer(t *testing.T) (*Server, *contentStore, *memPub) {
	s, mem, pub := newServer()
	mem.tenants["acme"] = model.Tenant{ID: 1, Slug: "acme", SiteKey: "sk_acme"}
	cs := &contentStore{memStore: mem, assets: map[int64]model.Asset{}, files: map[int64][]byte{},
		pages: map[int64]model.LandingPage{}, usedBy: map[int64][]string{}, templates: map[int64]model.Template{},
		forms: map[int64]model.Form{
			3: {ID: 3, TenantID: 1, PublicKey: "fGuide", Status: "active", Name: "Get the guide",
				Fields: []model.FormField{{Key: "email", Label: "Email", Type: "email", Required: true}}},
			4: {ID: 4, TenantID: 1, PublicKey: "fDraft", Status: "draft", Name: "Draft",
				Fields: []model.FormField{{Key: "email", Label: "Email", Type: "email", Required: true}}},
		}}
	s.Store = cs
	box, err := auth.NewBox(strings.Repeat("ef", 32))
	if err != nil {
		t.Fatal(err)
	}
	s.Box = box
	return s, cs, pub
}

func upload(t *testing.T, h http.Handler, filename string, data []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/v1/assets", &b)
	req.Header.Set("Authorization", "Bearer key-1")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

var pdfBytes = []byte("%PDF-1.7\nhello")

func TestAssetUploadValidation(t *testing.T) {
	s, cs, _ := contentServer(t)
	h := s.Handler()
	w := upload(t, h, "../../Q3 report.pdf", pdfBytes, map[string]string{"description": "Numbers"})
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	a := cs.assets[1]
	if a.Filename != "Q3 report.pdf" || a.Title != "Q3 report" || a.ContentType != "application/pdf" || a.Size != int64(len(pdfBytes)) || len(a.PublicKey) < 20 || a.SHA256 == "" {
		t.Errorf("stored asset = %+v", a)
	}
	for _, tc := range []struct {
		name string
		data []byte
		code int
		msg  string
	}{
		{"page.html", []byte("<html>"), 400, "isn't allowed"},
		{"fake.pdf", []byte("<html><script>alert(1)</script>"), 400, "isn't a PDF"},
		{"big.pdf", append(append([]byte{}, pdfBytes...), make([]byte, 25<<20)...), 413, "25 MB"},
	} {
		if w := upload(t, h, tc.name, tc.data, nil); w.Code != tc.code || !strings.Contains(w.Body.String(), tc.msg) {
			t.Errorf("%s: %d %s", tc.name, w.Code, w.Body)
		}
	}
	if w := upload(t, h, "x.pdf", pdfBytes, map[string]string{"form_id": "99"}); w.Code != 400 || !strings.Contains(w.Body.String(), "gating form") {
		t.Errorf("unknown form: %d %s", w.Code, w.Body)
	}
	// Assets used by templates can't be deleted.
	cs.usedBy[1] = []string{"Welcome"}
	if w := do(t, h, "DELETE", "/v1/assets/1", "key-1", ""); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Welcome") {
		t.Errorf("delete used asset: %d %s", w.Code, w.Body)
	}
}

func checkDownloadHeaders(t *testing.T, w *httptest.ResponseRecorder, contentType, filename string) {
	t.Helper()
	h := w.Result().Header
	disp, params, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	if w.Code != 200 || h.Get("Content-Type") != contentType || disp != "attachment" || params["filename"] != filename ||
		h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("download: %d %v", w.Code, h)
	}
}

func TestOpenAssetDownload(t *testing.T) {
	s, cs, pub := contentServer(t)
	h := s.Handler()
	cs.assets[1] = model.Asset{ID: 1, TenantID: 1, PublicKey: "kOpen", Title: "Logo", Filename: "logo.svg", ContentType: "image/svg+xml"}
	cs.files[1] = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	w := do(t, h, "GET", "/a/kOpen", "", "")
	checkDownloadHeaders(t, w, "image/svg+xml", "logo.svg") // SVG is only ever a download
	if w.Body.String() != string(cs.files[1]) || cs.assets[1].DownloadCount != 1 || len(pub.recs) != 0 {
		t.Errorf("open download: count %d, events %d", cs.assets[1].DownloadCount, len(pub.recs))
	}
	if w := do(t, h, "GET", "/a/nope", "", ""); w.Code != 404 {
		t.Errorf("unknown key: %d", w.Code)
	}
}

func TestGatedAssetDownload(t *testing.T) {
	s, cs, pub := contentServer(t)
	h := s.Handler()
	form := int64(3)
	cs.assets[1] = model.Asset{ID: 1, TenantID: 1, PublicKey: "kGated", Title: "The guide", Filename: "guide.pdf", ContentType: "application/pdf", FormID: &form}
	cs.files[1] = pdfBytes

	// The page shows the form, not the file.
	w := do(t, h, "GET", "/a/kGated", "", "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `name="email"`) ||
		strings.Contains(w.Body.String(), "%PDF") || w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("gated page: %d %s", w.Code, w.Body)
	}
	post := func(values url.Values) *httptest.ResponseRecorder {
		values.Set("_ts", fmt.Sprint(time.Now().Add(-5*time.Second).UnixMilli()))
		req := httptest.NewRequest("POST", "/a/kGated", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := post(url.Values{"email": {"nope"}}); w.Code != 422 || strings.Contains(w.Body.String(), "/download?t=") {
		t.Errorf("invalid submission: %d", w.Code)
	}
	w = post(url.Values{"email": {"asha@example.com"}})
	m := regexp.MustCompile(`href="(/a/kGated/download\?t=[^"]+)"`).FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil {
		t.Fatalf("accepted submission: %d %s", w.Code, w.Body)
	}
	link := strings.ReplaceAll(m[1], "&amp;", "&")
	var submitted bool
	for _, e := range published(pub) {
		if e.Name == "form_submitted" && e.ExternalID == "asha@example.com" && fmt.Sprint(e.Props["asset_id"]) == "1" && e.Props["source"] == "asset" {
			submitted = true
		}
	}
	if !submitted {
		t.Errorf("form_submitted with asset_id not recorded: %+v", published(pub))
	}
	pub.recs = nil

	w = do(t, h, "GET", link, "", "")
	checkDownloadHeaders(t, w, "application/pdf", "guide.pdf")
	evs := published(pub)
	if w.Body.String() != string(pdfBytes) || len(evs) != 1 || evs[0].Name != event.AssetDownloaded || evs[0].ExternalID != "asha@example.com" ||
		fmt.Sprint(evs[0].Props["asset_id"]) != "1" || cs.assets[1].DownloadCount != 1 {
		t.Errorf("gated download: events %+v, count %d", evs, cs.assets[1].DownloadCount)
	}

	// Tampered or foreign links don't work; a link for one asset doesn't open another.
	if w := do(t, h, "GET", link[:len(link)-3]+"abc", "", ""); w.Code != http.StatusForbidden {
		t.Errorf("tampered link: %d", w.Code)
	}
	cs.assets[2] = model.Asset{ID: 2, TenantID: 1, PublicKey: "kOther", Filename: "o.pdf", ContentType: "application/pdf"}
	if w := do(t, h, "GET", strings.Replace(link, "kGated", "kOther", 1), "", ""); w.Code != 404 {
		t.Errorf("link reused for another asset: %d", w.Code)
	}
	// A gated asset whose form is a draft (or gone) isn't served at all.
	draft := int64(4)
	a := cs.assets[1]
	a.FormID = &draft
	cs.assets[1] = a
	if w := do(t, h, "GET", "/a/kGated", "", ""); w.Code != 404 {
		t.Errorf("draft gate: %d", w.Code)
	}
}

func TestTemplateAttachmentsAndPreview(t *testing.T) {
	s, cs, _ := contentServer(t)
	h := s.Handler()
	cs.assets[1] = model.Asset{ID: 1, TenantID: 1, Title: "Guide", Filename: "guide.pdf", ContentType: "application/pdf", Size: 6 << 20}
	cs.assets[2] = model.Asset{ID: 2, TenantID: 1, Title: "Deck", Filename: "deck.pptx", ContentType: "application/zip", Size: 5 << 20}
	cs.assets[3] = model.Asset{ID: 3, TenantID: 2, Title: "Theirs", Filename: "x.pdf", ContentType: "application/pdf", Size: 10}
	tpl := func(att string) string {
		return `{"name":"t","channel":"email","subject":"s","html":"<p>Hi <a href=\"https://a.test\">there</a></p>","content":{"attachments":` + att + `}}`
	}
	for att, want := range map[string]string{"[1,2]": "10 MB", "[3]": "doesn't exist", "[9]": "doesn't exist"} {
		if w := do(t, h, "POST", "/v1/templates", "key-1", tpl(att)); w.Code != 400 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("attachments %s: %d %s", att, w.Code, w.Body)
		}
	}
	if w := do(t, h, "POST", "/v1/templates", "key-1", tpl("[1]")); w.Code != 201 {
		t.Errorf("valid attachments: %d %s", w.Code, w.Body)
	}
	w := do(t, h, "POST", "/v1/templates/preview", "key-1", `{"template":`+tpl("[1]")+`}`)
	var out struct {
		Text          string `json:"text"`
		TextGenerated bool   `json:"text_generated"`
		Attachments   []struct {
			Filename string `json:"filename"`
			Size     int64  `json:"size"`
		} `json:"attachments"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out.Text != "Hi there (https://a.test)" || !out.TextGenerated || len(out.Attachments) != 1 || out.Attachments[0].Filename != "guide.pdf" {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}
	// Generating the text to edit keeps personalization tags.
	w = do(t, h, "POST", "/v1/templates/text", "key-1", `{"html":"<p>Hi {{.Attrs.first_name}}</p>"}`)
	if !strings.Contains(w.Body.String(), `"text":"Hi {{.Attrs.first_name}}"`) {
		t.Errorf("text: %s", w.Body)
	}
}

func TestLandingPages(t *testing.T) {
	s, cs, pub := contentServer(t)
	h := s.Handler()
	start := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := func(slug, html, status string) string {
		b, _ := json.Marshal(map[string]any{"slug": slug, "title": "Spring sale", "meta_description": "Big", "html": html,
			"status": status, "start_at": start})
		return string(b)
	}
	for _, tc := range []struct{ body, want string }{
		{body("Spring Sale", "<p>x</p>", "published"), "lowercase"},
		{body("spring", "<p>{form:fMissing}</p>", "published"), "fMissing"},
		{`{"slug":"x","title":"t","html":"<p>x</p>","status":"published","start_at":"` + start + `","end_at":"2000-01-01T00:00:00Z"}`, "after the start"},
	} {
		if w := do(t, h, "POST", "/v1/landing-pages", "key-1", tc.body); w.Code != 400 || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("save: %d %s, want %q", w.Code, w.Body, tc.want)
		}
	}
	w := do(t, h, "POST", "/v1/landing-pages", "key-1", body("spring", `<h1 onclick="x()">Sale</h1>{form:fGuide}<script>alert(1)</script>`, "published"))
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/landing-pages", "key-1", body("spring", "<p>x</p>", "draft")); w.Code != http.StatusConflict {
		t.Errorf("duplicate slug: %d", w.Code)
	}
	w = do(t, h, "GET", "/v1/landing-pages/1", "key-1", "")
	if !strings.Contains(w.Body.String(), `"url":"http://localhost:7080/p/acme/spring"`) {
		t.Errorf("get: %s", w.Body)
	}

	// The live page: SDK scripts with the response's nonce, nothing else runs, frameable.
	w = do(t, h, "GET", "/p/acme/spring", "", "")
	csp := w.Header().Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(csp)
	page := w.Body.String()
	if w.Code != 200 || nonce == nil || w.Header().Get("X-Frame-Options") != "" || !strings.Contains(csp, "frame-ancestors *") {
		t.Fatalf("page: %d %v", w.Code, w.Header())
	}
	if strings.Count(page, `<script nonce="`+nonce[1]+`"`) != 2 || strings.Count(page, "<script") != 2 || strings.Contains(page, "onclick") ||
		!strings.Contains(page, `data-site-key="sk_acme"`) || !strings.Contains(page, `<div data-pulse-form="fGuide"></div>`) {
		t.Errorf("page body:\n%s", page)
	}
	if w2 := do(t, h, "GET", "/p/acme/spring", "", ""); strings.Contains(w2.Header().Get("Content-Security-Policy"), nonce[1]) {
		t.Error("the nonce must change per response")
	}
	if w := do(t, h, "GET", "/p/other/spring", "", ""); w.Code != 404 {
		t.Errorf("other workspace: %d", w.Code)
	}

	// A submission of the embedded form counts for the page.
	jsonBody := fmt.Sprintf(`{"values":{"email":"lp@example.com"},"page_url":"http://localhost:7080/p/acme/spring?utm_source=x","shown":%d}`,
		time.Now().Add(-5*time.Second).UnixMilli())
	if w := do(t, h, "POST", "/v1/web/forms/fGuide", "", jsonBody); w.Code != 200 {
		t.Fatalf("form on page: %d %s", w.Code, w.Body)
	}
	var onPage bool
	for _, e := range published(pub) {
		onPage = onPage || (e.Name == "form_submitted" && fmt.Sprint(e.Props["landing_page_id"]) == "1")
	}
	if !onPage {
		t.Errorf("form_submitted lacks landing_page_id: %+v", published(pub))
	}

	// Drafts and pages outside their schedule aren't served.
	p := cs.pages[1]
	p.StartAt = time.Now().Add(time.Hour)
	cs.pages[1] = p
	if w := do(t, h, "GET", "/p/acme/spring", "", ""); w.Code != 404 {
		t.Errorf("scheduled page: %d", w.Code)
	}

	// Preview renders unsaved HTML without any script.
	w = do(t, h, "POST", "/v1/landing-pages/preview", "key-1", `{"page":{"title":"T","html":"<p>Draft</p>{form:fGuide}<script>x()</script>"}}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script") || !strings.Contains(w.Body.String(), "Email *") ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/landing-pages/preview", "", `{"page":{}}`); w.Code != http.StatusUnauthorized {
		t.Errorf("preview signed out: %d", w.Code)
	}
}

func TestContentRoutePermissions(t *testing.T) {
	viewer, _ := auth.BuiltinPerms(auth.RoleViewer)
	editor, _ := auth.BuiltinPerms(auth.RoleEditor)
	for _, tc := range []struct {
		pattern string
		perms   auth.Perms
		want    bool
	}{
		{"GET /v1/assets", viewer, true},
		{"POST /v1/assets", viewer, false},
		{"POST /v1/assets", editor, true},
		{"DELETE /v1/assets/{id}", viewer, false},
		{"GET /v1/landing-pages/{id}/stats", viewer, true},
		{"POST /v1/landing-pages/preview", viewer, true},
		{"PUT /v1/landing-pages/{id}", viewer, false},
		{"PUT /v1/landing-pages/{id}", editor, true},
	} {
		method, path, _ := strings.Cut(tc.pattern, " ")
		r, _ := http.NewRequest(method, path, nil)
		r.Pattern = tc.pattern
		n := required(r)
		if got := tc.perms.Allows(n.area, n.level); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}
