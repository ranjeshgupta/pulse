//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var e2ePDF = append([]byte("%PDF-1.4\n% Pulse e2e\n"), bytes.Repeat([]byte("binary\x00\xff"), 64)...)

// uploadAsset uploads a file as the web app does (multipart, session cookie, CSRF header).
func uploadAsset(t *testing.T, c *http.Client, filename string, data []byte, fields map[string]string, want int) map[string]any {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", apiURL+"/v1/assets", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Pulse-CSRF", "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("upload %s: %d %s, want %d", filename, resp.StatusCode, raw, want)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func checkDownload(t *testing.T, resp *http.Response, contentType, filename string, body []byte) {
	t.Helper()
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	disp, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != contentType || disp != "attachment" || params["filename"] != filename ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("download %s: %d %v", filename, resp.StatusCode, resp.Header)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("download %s: %d bytes, want %d", filename, len(got), len(body))
	}
}

// TestEmailTextPartAndAttachment: a template with its own plain-text part and an attached PDF arrives
// in Mailpit as multipart with both, the text links direct and the unsubscribe link added.
func TestEmailTextPartAndAttachment(t *testing.T) {
	c, _ := workspace(t, "Attachments")
	run := fmt.Sprint(time.Now().UnixNano())
	ext, email := "att-"+run, "att-"+run+"@example.com"
	asset := idOf(uploadAsset(t, c, "Price list.pdf", e2ePDF, map[string]string{"title": "Price list"}, 201)["id"])
	// Wrong content for the extension, and types outside the allowlist, are refused.
	uploadAsset(t, c, "evil.pdf", []byte("<html><script>alert(1)</script>"), nil, 400)
	uploadAsset(t, c, "page.html", []byte("<html>"), nil, 400)

	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "prices", "channel": "email", "subject": "Prices " + run,
		"html":    `<html><body><p>Hi {{.Attrs.first_name}}, see <a href="https://shop.test/prices">our prices</a>.</p></body></html>`,
		"content": map[string]any{"text": "Hi {{.Attrs.first_name}}, prices: https://shop.test/prices", "attachments": []any{asset}}}, 201)["id"]
	// The preview shows the text part and the attachment.
	pv := jsonCall(t, c, "POST", "/v1/templates/preview", map[string]any{"template": map[string]any{"channel": "email", "subject": "s",
		"html": "<p>x</p>", "content": map[string]any{"attachments": []any{asset}}}}, 200)
	if pv["text"] != "x" || pv["text_generated"] != true || len(pv["attachments"].([]any)) != 1 {
		t.Errorf("preview = %v", pv)
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "email": email, "attrs": map[string]any{"first_name": "Asha"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)

	var search struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	waitFor(t, 15*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+email), &search)
		return len(search.Messages) == 1
	})
	var msg struct {
		Text, HTML  string
		Attachments []struct {
			PartID, FileName, ContentType string
			Size                          int
		}
	}
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID, &msg)
	if !strings.HasPrefix(msg.Text, "Hi Asha, prices: https://shop.test/prices") || !strings.Contains(msg.Text, "Unsubscribe: "+apiURL+"/u/") ||
		strings.Contains(msg.Text, "/t/c/") {
		t.Errorf("text part = %q", msg.Text)
	}
	if !strings.Contains(msg.HTML, "/t/c/") || !strings.Contains(msg.HTML, "/t/o/") {
		t.Errorf("the HTML part should be tracked:\n%s", msg.HTML)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].FileName != "Price list.pdf" || msg.Attachments[0].ContentType != "application/pdf" {
		t.Fatalf("attachments = %+v", msg.Attachments)
	}
	resp, err := http.Get(mailpitURL + "/api/v1/message/" + search.Messages[0].ID + "/part/" + msg.Attachments[0].PartID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, e2ePDF) {
		t.Errorf("attachment bytes differ: %d, want %d", len(got), len(e2ePDF))
	}
	// An attached asset can't be deleted from under its template.
	if e := jsonCall(t, c, "DELETE", fmt.Sprint("/v1/assets/", asset), nil, 409)["error"].(string); !strings.Contains(e, "prices") {
		t.Errorf("delete attached asset: %s", e)
	}
}

// TestLandingPage: a published page serves under a nonce CSP with the web SDK, its views (sent by the
// SDK) and an embedded form's submissions count for it; drafts aren't served; the preview runs nothing.
func TestLandingPage(t *testing.T) {
	c, siteKey := workspace(t, "Landing")
	run := fmt.Sprint(time.Now().UnixNano())
	formID := idOf(jsonCall(t, c, "POST", "/v1/forms", map[string]any{"name": "Sale signup", "status": "active",
		"fields": []any{map[string]any{"key": "email", "label": "Email", "type": "email", "required": true}}}, 200)["id"])
	formKey := jsonCall(t, c, "GET", fmt.Sprint("/v1/forms/", formID), nil, 200)["form"].(map[string]any)["public_key"].(string)
	page := map[string]any{"slug": "sale-" + run, "title": "Spring sale", "meta_description": "Everything 20% off",
		"html":   `<h1 onclick="steal()">Spring sale</h1><p>Sign up:</p>{form:` + formKey + `}<script>alert(1)</script>`,
		"status": "published", "start_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}
	id := idOf(jsonCall(t, c, "POST", "/v1/landing-pages", page, 200)["id"])
	pageURL := jsonCall(t, c, "GET", fmt.Sprint("/v1/landing-pages/", id), nil, 200)["url"].(string)

	resp, err := http.Get(pageURL + "?utm_source=newsletter")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`script-src 'nonce-([^']+)'`).FindStringSubmatch(csp)
	if resp.StatusCode != 200 || nonce == nil || resp.Header.Get("X-Frame-Options") != "" || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("page: %d %v", resp.StatusCode, resp.Header)
	}
	doc := string(body)
	if strings.Count(doc, "<script") != 2 || strings.Count(doc, `<script nonce="`+nonce[1]+`"`) != 2 || strings.Contains(doc, "onclick") ||
		!strings.Contains(doc, `data-site-key="`+siteKey+`"`) || !strings.Contains(doc, `<div data-pulse-form="`+formKey+`"></div>`) ||
		!strings.Contains(doc, `<meta name="description" content="Everything 20% off">`) {
		t.Errorf("page:\n%s", doc)
	}
	for _, js := range []string{"/sdk/pulse.js", "/sdk/lp.js"} {
		if r, err := http.Get(apiURL + js); err != nil || r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "javascript") {
			t.Errorf("%s: %v %v", js, err, r)
		}
	}

	// What the SDK (lp.js) sends on load, then a submission of the embedded form from the page.
	visitor := "lp-" + run
	req, _ := http.NewRequest("POST", apiURL+"/v1/web/events", strings.NewReader(fmt.Sprintf(
		`{"events":[{"external_id":%q,"name":"landing_page_viewed","props":{"page_id":%d,"utm_source":"newsletter"}}]}`, visitor, id)))
	req.Header.Set("X-Pulse-Site-Key", siteKey)
	req.Header.Set("Content-Type", "application/json")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 202 {
		t.Fatalf("page view event: %v %v", err, r)
	}
	sub, _ := json.Marshal(map[string]any{"values": map[string]string{"email": "lp-" + run + "@example.com"}, "id": visitor,
		"page_url": pageURL + "?utm_source=newsletter", "shown": time.Now().Add(-5 * time.Second).UnixMilli()})
	if r, err := http.Post(apiURL+"/v1/web/forms/"+formKey, "application/json", bytes.NewReader(sub)); err != nil || r.StatusCode != 200 {
		t.Fatalf("form on page: %v %v", err, r)
	}
	took := waitFor(t, 20*time.Second, func() bool {
		st := jsonCall(t, c, "GET", fmt.Sprint("/v1/landing-pages/", id, "/stats"), nil, 200)
		return st["views"] == 1.0 && st["visitors"] == 1.0 && st["submissions"] == 1.0
	})
	t.Logf("landing page view and form submission in its stats after %v", took)
	if evs := contactEvents(t, c, visitor); !slices.Contains(evs, "landing_page_viewed") {
		t.Errorf("visitor timeline = %v", evs)
	}

	// The preview renders unsaved HTML for signed-in users without any script.
	req, _ = http.NewRequest("POST", apiURL+"/v1/landing-pages/preview", strings.NewReader(`{"page":{"title":"x","html":"<p>Draft</p><script>x()</script>"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pulse-CSRF", "1")
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pv, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || strings.Contains(string(pv), "<script") || !strings.Contains(string(pv), "<p>Draft</p>") {
		t.Errorf("preview: %d %s", r.StatusCode, pv)
	}
	if r, _ := http.Post(apiURL+"/v1/landing-pages/preview", "application/json", strings.NewReader(`{"page":{}}`)); r.StatusCode != 401 {
		t.Errorf("preview signed out: %d", r.StatusCode)
	}

	// Unpublished, it's gone.
	page["status"] = "draft"
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/landing-pages/", id), page, 200)
	if r, _ := http.Get(pageURL); r.StatusCode != 404 {
		t.Errorf("draft page: %d", r.StatusCode)
	}
}

// TestGatedAsset: a gated asset shows its form; a valid submission gets a short-lived link that
// downloads the file with safe headers and records asset_downloaded for the submitter. Open assets
// download directly, SVG included, always as attachments.
func TestGatedAsset(t *testing.T) {
	c, _ := workspace(t, "Assets")
	run := fmt.Sprint(time.Now().UnixNano())
	formID := idOf(jsonCall(t, c, "POST", "/v1/forms", map[string]any{"name": "Get the guide", "status": "active",
		"fields": []any{map[string]any{"key": "email", "label": "Work email", "type": "email", "required": true}}}, 200)["id"])
	up := uploadAsset(t, c, "guide.pdf", e2ePDF, map[string]string{"title": "The guide", "form_id": fmt.Sprint(formID)}, 201)
	assetID := idOf(up["id"])
	link := up["url"].(string)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := noRedirect.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Disposition") != "" || !strings.Contains(string(pageBody), "Work email") || bytes.Contains(pageBody, []byte("%PDF")) {
		t.Fatalf("gated page: %d %v", resp.StatusCode, resp.Header)
	}
	post := func(email string) (int, string) {
		v := url.Values{"email": {email}, "_ts": {fmt.Sprint(time.Now().Add(-5 * time.Second).UnixMilli())}}
		r, err := noRedirect.PostForm(link, v)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		return r.StatusCode, string(b)
	}
	if code, body := post("not-an-email"); code != 422 || strings.Contains(body, "/download?t=") {
		t.Errorf("invalid submission: %d", code)
	}
	email := "guide-" + run + "@example.com"
	code, body := post(email)
	m := regexp.MustCompile(`href="(/a/[^"]+/download\?t=[^"]+)"`).FindStringSubmatch(body)
	if code != 200 || m == nil {
		t.Fatalf("submission: %d %s", code, body)
	}
	dl := apiURL + html.UnescapeString(m[1])
	resp, err = http.Get(dl)
	if err != nil {
		t.Fatal(err)
	}
	checkDownload(t, resp, "application/pdf", "guide.pdf", e2ePDF)
	if r, _ := http.Get(dl[:len(dl)-4] + "AAAA"); r.StatusCode != 403 {
		t.Errorf("tampered link: %d", r.StatusCode)
	}

	took := waitFor(t, 20*time.Second, func() bool {
		return slices.Contains(contactEvents(t, c, email), "asset_downloaded")
	})
	t.Logf("gated download recorded for the submitter after %v", took)
	waitFor(t, 20*time.Second, func() bool {
		st := jsonCall(t, c, "GET", fmt.Sprint("/v1/assets/", assetID), nil, 200)["stats"].(map[string]any)
		return st["downloads"] == 1.0 && st["known_downloads"] == 1.0 && st["contacts"] == 1.0
	})

	// Open assets download straight away; SVG is served as a download, never rendered.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.cookie)</script></svg>`)
	open := uploadAsset(t, c, "logo.svg", svg, nil, 201)["url"].(string)
	resp, err = http.Get(open)
	if err != nil {
		t.Fatal(err)
	}
	checkDownload(t, resp, "image/svg+xml", "logo.svg", svg)

	// Turning the gate's form into a draft closes the download rather than opening it.
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/forms/", formID), map[string]any{"name": "Get the guide", "status": "draft",
		"fields": []any{map[string]any{"key": "email", "label": "Work email", "type": "email", "required": true}}}, 200)
	if r, _ := http.Get(link); r.StatusCode != 404 {
		t.Errorf("gate with a draft form: %d", r.StatusCode)
	}
}
