package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// uploadStore keeps form uploads in memory on top of contentStore.
type uploadStore struct {
	*contentStore
	uploads map[int64]model.Asset
	data    map[int64][]byte
	form    map[int64]int64
	owner   map[int64]string
}

func (u *uploadStore) CreateFormUpload(_ context.Context, a model.Asset, data []byte, _, formID int64, ext string) (int64, error) {
	a.ID, a.Size = int64(100+len(u.uploads)), int64(len(data))
	u.uploads[a.ID], u.data[a.ID], u.form[a.ID], u.owner[a.ID] = a, data, formID, ext
	return a.ID, nil
}
func (u *uploadStore) FormUpload(_ context.Context, tenant, formID, id int64) (model.Asset, []byte, error) {
	a, ok := u.uploads[id]
	if !ok || a.TenantID != tenant || u.form[id] != formID {
		return model.Asset{}, nil, pg.ErrNotFound
	}
	return a, u.data[id], nil
}

func uploadServer(t *testing.T) (*Server, *uploadStore, *memPub) {
	s, cs, pub := contentServer(t)
	cs.forms[5] = model.Form{ID: 5, TenantID: 1, PublicKey: "fJobs", Status: "active", Name: "Apply",
		Fields: []model.FormField{
			{Key: "email", Label: "Email", Type: "email", Required: true},
			{Type: "page_break", Label: "Your CV"},
			{Key: "cv", Label: "CV", Type: "file", Required: true, Accept: []string{".pdf", ".docx"}, MaxMB: 1},
		}}
	us := &uploadStore{contentStore: cs, uploads: map[int64]model.Asset{}, data: map[int64][]byte{}, form: map[int64]int64{}, owner: map[int64]string{}}
	s.Store = us
	return s, us, pub
}

func multipartPost(t *testing.T, h http.Handler, path string, fields map[string]string, files map[string][2]string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for k, f := range files {
		fw, _ := mw.CreateFormFile(k, f[0])
		_, _ = fw.Write([]byte(f[1]))
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", path, &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHostedFormPagesAndUploads(t *testing.T) {
	s, us, pub := uploadServer(t)
	h := s.Handler()
	w := do(t, h, "GET", "/f/fJobs", "", "")
	body := w.Body.String()
	nonce := regexp.MustCompile(`script-src 'nonce-([^']+)'`).FindStringSubmatch(w.Header().Get("Content-Security-Policy"))
	if w.Code != 200 || nonce == nil || !strings.Contains(body, `<script nonce="`+nonce[1]+`">`) {
		t.Fatalf("page %d, CSP %q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	for _, want := range []string{`enctype="multipart/form-data"`, `data-page="1"`, "Page 1 of 2", "<legend>Your CV</legend>",
		`type="file" name="cv" accept=".pdf,.docx" data-max="1048576" required`, "data-next"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	ts := fmt.Sprint(time.Now().Add(-5 * time.Second).UnixMilli())

	// A web page renamed .pdf, a file over the limit, a type the field doesn't take: refused.
	for name, file := range map[string][2]string{
		"html":  {"cv.pdf", "<!DOCTYPE html><html><script>alert(1)</script></html>"},
		"big":   {"cv.pdf", "%PDF-1.7\n" + strings.Repeat("x", 1<<20)},
		"type":  {"cv.png", "\x89PNG\r\n\x1a\nxxxx"},
		"magic": {"cv.docx", "%PDF-1.7 not a zip"},
	} {
		w := multipartPost(t, h, "/f/fJobs", map[string]string{"email": "a@example.com", "_ts": ts}, map[string][2]string{"cv": file})
		if w.Code != 422 || !strings.Contains(w.Body.String(), `class="err"`) {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if len(us.uploads) != 0 {
		t.Fatal("a refused file was stored")
	}
	if w := multipartPost(t, h, "/f/fJobs", map[string]string{"email": "a@example.com", "_ts": ts}, nil); w.Code != 422 || !strings.Contains(w.Body.String(), "Choose a file") {
		t.Errorf("required file missing: %d", w.Code)
	}

	w = multipartPost(t, h, "/f/fJobs", map[string]string{"email": "Asha@Example.com", "_ts": ts}, map[string][2]string{"cv": {"../../My CV.pdf", "%PDF-1.7\nhello"}})
	if w.Code != 200 || len(us.uploads) != 1 {
		t.Fatalf("accepted: %d %s", w.Code, w.Body.String())
	}
	var id int64
	for k, a := range us.uploads {
		id = k
		if a.Filename != "My CV.pdf" || a.ContentType != "application/pdf" || us.owner[k] != "asha@example.com" || us.form[k] != 5 {
			t.Errorf("stored %+v by %q", a, us.owner[k])
		}
	}
	var found bool
	for _, e := range published(pub) {
		if e.Name == "form_submitted" {
			cv, _ := e.Props["fields"].(map[string]any)["cv"].(map[string]any)
			found = fmt.Sprint(cv["upload_id"]) == fmt.Sprint(id) && cv["filename"] == "My CV.pdf"
		}
		if e.Name == "identify" && e.Attrs["cv"] != nil {
			t.Error("a file field must not become a profile attribute")
		}
	}
	if !found {
		t.Errorf("form_submitted lacks the upload: %+v", published(pub))
	}

	// Only signed-in users (here the API key) download it, from its own form.
	w = do(t, h, "GET", fmt.Sprintf("/v1/forms/5/uploads/%d", id), "key-1", "")
	checkDownloadHeaders(t, w, "application/pdf", "My CV.pdf")
	if w := do(t, h, "GET", fmt.Sprintf("/v1/forms/3/uploads/%d", id), "key-1", ""); w.Code != 404 {
		t.Errorf("through another form: %d", w.Code)
	}
	if w := do(t, h, "GET", fmt.Sprintf("/v1/forms/5/uploads/%d", id), "", ""); w.Code != 401 {
		t.Errorf("signed out: %d", w.Code)
	}
}

func TestWebSDKFormUpload(t *testing.T) {
	s, us, _ := uploadServer(t)
	h := s.Handler()
	w := do(t, h, "GET", "/v1/web/forms/fJobs", "", "")
	if !strings.Contains(w.Body.String(), `"type":"page_break"`) || !strings.Contains(w.Body.String(), `"max_mb":1`) {
		t.Errorf("SDK form: %s", w.Body.String())
	}
	payload, _ := json.Marshal(map[string]any{"values": map[string]string{"email": "b@example.com"}, "shown": time.Now().Add(-5 * time.Second).UnixMilli()})
	w = multipartPost(t, h, "/v1/web/forms/fJobs", map[string]string{"payload": string(payload)}, map[string][2]string{"cv": {"cv.docx", "PK\x03\x04rest"}})
	if w.Code != 200 || len(us.uploads) != 1 {
		t.Fatalf("SDK upload: %d %s", w.Code, w.Body.String())
	}
	w = multipartPost(t, h, "/v1/web/forms/fJobs", map[string]string{"payload": string(payload)}, map[string][2]string{"cv": {"cv.pdf", "MZ\x90\x00 an exe"}})
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"cv"`) {
		t.Errorf("SDK bad file: %d %s", w.Code, w.Body.String())
	}
}

func TestFormUploadPermission(t *testing.T) {
	r, _ := http.NewRequest("GET", "/v1/forms/1/uploads/2", nil)
	r.Pattern = "GET /v1/forms/{id}/uploads/{upload}"
	if n := required(r); n.area != "contacts" {
		t.Errorf("uploads need contacts access, got %s", required(r).area)
	}
}

// A submission without the time the form was shown (or with an unreadable one) counts as too fast:
// it gets the usual success answer but nothing is recorded.
func TestFormWithoutShownTimeIsABot(t *testing.T) {
	s, us, pub := uploadServer(t)
	h := s.Handler()
	file := map[string][2]string{"cv": {"cv.pdf", "%PDF-1.7\nhello"}}
	for name, ts := range map[string]string{"missing": "", "garbage": "soon", "zero": "0", "future": fmt.Sprint(time.Now().Add(time.Hour).UnixMilli())} {
		fields := map[string]string{"email": "bot@example.com"}
		if ts != "" {
			fields["_ts"] = ts
		}
		if w := multipartPost(t, h, "/f/fJobs", fields, file); w.Code != 200 {
			t.Errorf("hosted %s: %d", name, w.Code)
		}
	}
	payload, _ := json.Marshal(map[string]any{"values": map[string]string{"email": "bot@example.com"}})
	if w := multipartPost(t, h, "/v1/web/forms/fJobs", map[string]string{"payload": string(payload)}, file); w.Code != 200 {
		t.Errorf("SDK without shown: %d %s", w.Code, w.Body.String())
	}
	if len(us.uploads) != 0 || len(pub.recs) != 0 {
		t.Fatalf("a submission without a shown time was recorded: %d uploads, %d events", len(us.uploads), len(pub.recs))
	}
}
