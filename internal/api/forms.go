package api

// Forms: managed in the app, filled in on a hosted page (/f/{key}, which can sit in an iframe) or
// rendered by the web SDK on the customer's site (/v1/web/forms/{key}).
//
// A submission identifies the contact by the signed-in visitor (web SDK, identity verified when the
// workspace requires it), else by email, else by phone; a web SDK visitor's anonymous history is
// merged in. Bots are kept out by a honeypot field, a minimum fill time and a per-IP rate limit.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pulse/internal/assets"
	"pulse/internal/auth"
	"pulse/internal/forms"
	"pulse/internal/model"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

const (
	formSubmitsPerMin = 10              // per IP and workspace
	formMinFill       = 2 * time.Second // faster than this is a bot
)

type FormStore interface {
	ListForms(ctx context.Context, tenant int64) ([]model.Form, error)
	GetForm(ctx context.Context, tenant, id int64) (model.Form, error)
	FormByKey(ctx context.Context, key string) (model.Form, error)
	SaveForm(ctx context.Context, f model.Form) (int64, error)
	DeleteForm(ctx context.Context, tenant, id int64) error
	ContactByEmail(ctx context.Context, tenant int64, email string) (model.Contact, error)
	ContactsByPhone(ctx context.Context, tenant int64, phone string) ([]model.Contact, error)
}

// FormUploadStore keeps files uploaded through forms (private assets).
type FormUploadStore interface {
	CreateFormUpload(ctx context.Context, a model.Asset, data []byte, maxTotal, formID int64, externalID string) (int64, error)
	FormUpload(ctx context.Context, tenant, formID, id int64) (model.Asset, []byte, error)
}

type FormAnalytics interface {
	FormSubmissions(ctx context.Context, tenant, form int64, limit int) (ch.FormStats, error)
}

func (s *Server) formRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/forms", s.tenant(s.listForms))
	mux.HandleFunc("POST /v1/forms", s.tenant(s.saveForm))
	mux.HandleFunc("GET /v1/forms/{id}", s.tenant(s.getForm))
	mux.HandleFunc("PUT /v1/forms/{id}", s.tenant(s.saveForm))
	mux.HandleFunc("DELETE /v1/forms/{id}", s.tenant(s.deleteForm))
	mux.HandleFunc("GET /v1/forms/{id}/submissions", s.tenant(s.formSubmissions))
	mux.HandleFunc("GET /v1/forms/{id}/uploads/{upload}", s.tenant(s.formUpload))

	mux.HandleFunc("GET /f/{key}", s.hostedForm)
	mux.HandleFunc("POST /f/{key}", s.hostedFormSubmit)
	mux.HandleFunc("OPTIONS /v1/web/forms/{key}", s.corsPreflight)
	mux.HandleFunc("GET /v1/web/forms/{key}", s.webForm)
	mux.HandleFunc("POST /v1/web/forms/{key}", s.webFormSubmit)
}

func (s *Server) formStore() FormStore { return s.Store.(FormStore) }

// ---- management ----

func (s *Server) listForms(w http.ResponseWriter, r *http.Request, tenant int64) {
	fs, err := s.formStore().ListForms(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if fs == nil {
		fs = []model.Form{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"forms": fs, "base_url": s.publicBase()})
}

func (s *Server) getForm(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	f, err := s.formStore().GetForm(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"form": f, "hosted_url": s.publicBase() + "/f/" + f.PublicKey})
}

func (s *Server) saveForm(w http.ResponseWriter, r *http.Request, tenant int64) {
	var f model.Form
	if !decode(w, r, &f) {
		return
	}
	f.TenantID, f.ID = tenant, 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		f.ID = id
	} else {
		f.PublicKey = auth.NewToken("f")[:20]
	}
	if f.Status != "active" {
		f.Status = "draft"
	}
	f.Name = strings.TrimSpace(f.Name)
	lists, err := s.audience().ListSegmentsOfKind(r.Context(), tenant, "static")
	if err != nil {
		s.fail(w, err)
		return
	}
	isList := map[int64]bool{}
	for _, l := range lists {
		isList[l.ID] = true
	}
	if err := forms.Validate(f, func(id int64) bool { return isList[id] }); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.formStore().SaveForm(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteForm(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.formStore().DeleteForm(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) formSubmissions(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.formStore().GetForm(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	fa, ok := s.Analytics.(FormAnalytics)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "submissions need the analytics database")
		return
	}
	st, err := fa.FormSubmissions(r.Context(), tenant, id, 50)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// formUpload downloads a file a contact uploaded through the form, for signed-in users with
// contacts access (personal data): always as an attachment, never rendered.
func (s *Server) formUpload(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	upload, err := strconv.ParseInt(r.PathValue("upload"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid upload id")
		return
	}
	us, ok := s.Store.(FormUploadStore)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	a, data, err := us.FormUpload(r.Context(), tenant, id, upload)
	if err != nil {
		s.fail(w, err)
		return
	}
	serveAsset(w, a, data)
}

// ---- submitting ----

// activeForm loads a form by its public key with its workspace; drafts are not served.
func (s *Server) activeForm(ctx context.Context, key string) (model.Form, model.Tenant, error) {
	f, err := s.formStore().FormByKey(ctx, key)
	if err != nil {
		return f, model.Tenant{}, err
	}
	if f.Status != "active" {
		return f, model.Tenant{}, pg.ErrNotFound
	}
	t, err := s.Store.Tenant(ctx, f.TenantID)
	return f, t, err
}

// visitorProfile returns what the visitor's profile already has (progressive profiling), for a
// web SDK visitor: an identified user (verified if the workspace requires it), or an anonymous
// visitor who was merged into a contact before.
func (s *Server) visitorProfile(ctx context.Context, t model.Tenant, visitor, hash string) map[string]bool {
	if visitor == "" || !webIdentityOK(t, visitor, hash) {
		return nil
	}
	c, err := s.Store.ContactByExternalID(ctx, t.ID, visitor)
	if err != nil {
		return nil
	}
	if into, _ := c.Attrs["merged_into"].(string); into != "" {
		if c, err = s.Store.ContactByExternalID(ctx, t.ID, into); err != nil {
			return nil
		}
	}
	return forms.Known(c)
}

// submitterID decides who submitted: the signed-in visitor, else the contact with this email or
// phone (a new contact keyed by it when there is none). It also returns an anonymous visitor id
// to merge in.
func (s *Server) submitterID(ctx context.Context, t model.Tenant, sub forms.Submission, visitor, hash string) (string, string, error) {
	anon := ""
	if strings.HasPrefix(visitor, "anon-") {
		anon = visitor
	} else if visitor != "" && validUserHashOrOptional(t, visitor, hash) {
		return visitor, "", nil
	}
	if sub.Email != "" {
		c, err := s.formStore().ContactByEmail(ctx, t.ID, sub.Email)
		if err == nil {
			return c.ExternalID, anon, nil
		} else if !errors.Is(err, pg.ErrNotFound) {
			return "", "", err
		}
		return sub.Email, anon, nil
	}
	cs, err := s.formStore().ContactsByPhone(ctx, t.ID, sub.Phone)
	if err != nil {
		return "", "", err
	}
	if len(cs) > 0 {
		return cs[0].ExternalID, anon, nil
	}
	return sub.Phone, anon, nil
}

// validUserHashOrOptional: a known visitor id counts when identity verification is off, or its hash checks out.
func validUserHashOrOptional(t model.Tenant, visitor, hash string) bool {
	return !t.RequireSignedIdentify || validUserHash(t.IdentitySecret, visitor, hash)
}

type formInput struct {
	Values   map[string]string
	Visitor  string
	UserHash string
	PageURL  string
	Source   string // hosted | sdk
	Honeypot string
	Uploads  map[string]forms.Upload // files of file fields, by field key
	Elapsed  time.Duration           // since the form was shown
	Meta     map[string]any          // extra form_submitted properties (e.g. asset_id)
	// Attribution: the web SDK's first- and last-touch UTM attributes, added to the submitter's
	// profile (fields of the form win). Only the attribution keys are taken.
	Attribution map[string]string
}

// attributionKeys are the profile attributes the web SDK keeps for first- and last-touch UTM
// attribution (first_* are kept once set; see event.SetOnceAttr).
var attributionKeys = func() map[string]bool {
	m := map[string]bool{"first_referrer": true, "last_referrer": true}
	for _, p := range []string{"first_utm_", "last_utm_"} {
		for _, k := range []string{"source", "medium", "campaign", "term", "content"} {
			m[p+k] = true
		}
	}
	return m
}()

// submit validates and records a submission, and returns the submitter's external id. Bot-looking
// submissions get a normal-looking success (so bots learn nothing) but record nothing (and no id).
func (s *Server) submit(ctx context.Context, f model.Form, t model.Tenant, in formInput, r *http.Request) ([]forms.FieldError, int, string, error) {
	if !s.allowRate(ctx, "rlform", t.ID, r, 1, formSubmitsPerMin) {
		return nil, http.StatusTooManyRequests, "", errors.New("too many submissions from this network; try again in a minute")
	}
	if in.Honeypot != "" || in.Elapsed < formMinFill {
		s.Log.Info("form submission dropped as a bot", "form", f.ID, "honeypot", in.Honeypot != "", "elapsed", in.Elapsed)
		return nil, http.StatusOK, "", nil
	}
	visible := forms.Visible(f, s.visitorProfile(ctx, t, in.Visitor, in.UserHash))
	sub, ferrs := forms.Parse(f, visible, in.Values)
	files, fileErrs := forms.ParseFiles(visible, in.Uploads)
	if ferrs = append(ferrs, fileErrs...); len(ferrs) > 0 {
		return ferrs, http.StatusUnprocessableEntity, "", nil
	}
	for k, v := range in.Attribution {
		if _, set := sub.Attrs[k]; attributionKeys[k] && !set && v != "" && len(v) <= 200 {
			sub.Attrs[k] = v
		}
	}
	ext, anon, err := s.submitterID(ctx, t, sub, in.Visitor, in.UserHash)
	if err != nil {
		return nil, 0, "", err
	}
	if len(files) > 0 {
		if code, err := s.storeUploads(ctx, f, t, ext, files, sub.Values); err != nil {
			return nil, code, "", err
		}
	}
	meta := map[string]any{"source": in.Source}
	for k, v := range in.Meta {
		meta[k] = v
	}
	if in.PageURL != "" && len(in.PageURL) < 2000 {
		meta["page_url"] = in.PageURL
		if id := s.landingPageOf(ctx, t, in.PageURL); id != 0 {
			meta["landing_page_id"] = id // the form sits on one of the workspace's landing pages
		}
	}
	if sub.Consent {
		meta["consent"], meta["consent_text"] = true, f.Settings.ConsentText
	}
	if err := s.publishEvents(ctx, t.ID, forms.Events(f, sub, ext, anon, meta)); err != nil {
		return nil, http.StatusServiceUnavailable, "", errors.New("we couldn't save that; please try again")
	}
	return nil, http.StatusOK, ext, nil
}

// storeUploads keeps the accepted files (private assets, within the workspace's storage cap) and
// puts a reference to each in the submission's values: {upload_id, filename, size, content_type}.
func (s *Server) storeUploads(ctx context.Context, f model.Form, t model.Tenant, ext string, files []forms.File, values map[string]any) (int, error) {
	us, ok := s.Store.(FormUploadStore)
	if !ok {
		return http.StatusServiceUnavailable, errors.New("this form can't take files right now")
	}
	for _, fl := range files {
		sum := sha256.Sum256(fl.Data)
		a := model.Asset{TenantID: t.ID, PublicKey: auth.NewToken("u")[:24], Title: fl.Filename, Filename: fl.Filename,
			ContentType: fl.ContentType, SHA256: hex.EncodeToString(sum[:])}
		id, err := us.CreateFormUpload(ctx, a, fl.Data, assets.MaxWorkspaceBytes, f.ID, ext)
		if errors.Is(err, pg.ErrQuota) {
			return http.StatusRequestEntityTooLarge, errors.New("this form can't take more files right now; please try again later")
		} else if err != nil {
			return 0, err
		}
		values[fl.Key] = map[string]any{"upload_id": id, "filename": fl.Filename, "size": len(fl.Data), "content_type": fl.ContentType}
	}
	return 0, nil
}

// maxFormBody is the most a submission may send: the form's file limits plus 1 MB for the rest.
func maxFormBody(f model.Form) int64 {
	n := int64(1 << 20)
	for _, fd := range f.Fields {
		if fd.Type == forms.TypeFile {
			n += forms.MaxBytes(fd) + 4<<10
		}
	}
	return n
}

// readFormPost reads a hosted (or asset gate) form post: URL-encoded, or multipart when the form
// takes files. Only the form's own file fields are read.
func readFormPost(w http.ResponseWriter, r *http.Request, f model.Form) (map[string]string, map[string]forms.Upload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody(f))
	values := map[string]string{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			return nil, nil, err
		}
		defer r.MultipartForm.RemoveAll()
		for k, vs := range r.MultipartForm.Value {
			if len(vs) > 0 {
				values[k] = vs[0]
			}
		}
		uploads, err := readUploads(r.MultipartForm, f)
		return values, uploads, err
	}
	if err := r.ParseForm(); err != nil {
		return nil, nil, err
	}
	for k := range r.PostForm {
		values[k] = r.PostForm.Get(k)
	}
	return values, nil, nil
}

// readUploads takes the first file of each of the form's file fields from a parsed multipart form.
func readUploads(mf *multipart.Form, f model.Form) (map[string]forms.Upload, error) {
	uploads := map[string]forms.Upload{}
	for _, fd := range f.Fields {
		if fd.Type != forms.TypeFile || len(mf.File[fd.Key]) == 0 {
			continue
		}
		fh := mf.File[fd.Key][0]
		if fh.Size == 0 && fh.Filename == "" {
			continue // the browser sends an empty part for a file input left empty
		}
		fl, err := fh.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(fl, forms.MaxBytes(fd)+1))
		fl.Close()
		if err != nil {
			return nil, err
		}
		uploads[fd.Key] = forms.Upload{Filename: fh.Filename, Data: data}
	}
	return uploads, nil
}

// ---- hosted page ----

// formTemplates holds the parts shared by the hosted form page and the gated download page.
var formTemplates = template.Must(template.New("base").Funcs(template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"pct": func(a, b int) int { return a * 100 / max(b, 1) },
	"accept": func(exts []string) string {
		if len(exts) == 0 {
			exts = assets.UploadExtensions()
		}
		return strings.Join(exts, ",")
	},
	"maxBytes": forms.MaxBytes,
	"mb":       func(fd model.FormField) int64 { return forms.MaxBytes(fd) >> 20 },
	"accent": func(c string) template.CSS {
		if c == "" {
			c = "#2747d6"
		}
		return template.CSS(c) // validated as #rrggbb when saved
	},
}).Parse(`{{define "css"}}:root{--accent:{{accent .Form.Settings.Theme}}}*{box-sizing:border-box}
body{font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;color:#152238;margin:0;background:transparent}
main{max-width:520px;margin:0 auto;padding:20px 16px}h1{font-size:22px;margin:0 0 6px}.desc{color:#5b6474;margin:0 0 18px}
label{display:block;margin:0 0 14px;font-weight:600;font-size:14px}label .req{color:#b42318}
input[type=text],input[type=email],input[type=tel],input[type=number],textarea,select{display:block;width:100%;margin-top:6px;font:inherit;font-weight:400;padding:10px 12px;border:1px solid #c3cbd6;border-radius:6px;background:#fff;color:inherit}
textarea{min-height:96px}input:focus,textarea:focus,select:focus{outline:2px solid var(--accent);outline-offset:1px;border-color:var(--accent)}
.check{display:flex;gap:10px;align-items:flex-start;font-weight:400}.check input{margin-top:4px;width:18px;height:18px;accent-color:var(--accent)}
.err{display:block;color:#b42318;font-weight:400;font-size:13px;margin-top:4px}.alert{background:#fdecea;color:#7a1a12;border-radius:6px;padding:10px 12px;margin:0 0 14px}
button{font:inherit;font-weight:600;background:var(--accent);color:#fff;border:0;border-radius:6px;padding:11px 18px;cursor:pointer;width:100%}
.hp{position:absolute;left:-9999px;width:1px;height:1px;overflow:hidden}.done{padding:28px 0;text-align:center}.done h1{margin-bottom:8px}
fieldset.page{border:0;margin:0 0 6px;padding:0}fieldset.page legend{font-size:17px;font-weight:700;padding:0;margin:0 0 12px}
input[type=file]{display:block;width:100%;margin-top:6px;font:inherit;font-weight:400;font-size:14px}.hint{display:block;color:#5b6474;font-weight:400;font-size:13px;margin-top:4px}
.steps{margin:0 0 18px;font-size:13px;color:#5b6474}.bar{height:6px;background:#e4e8ee;border-radius:3px;margin-top:6px;overflow:hidden}.bar i{display:block;height:100%;background:var(--accent)}
.nav{display:flex;gap:10px}.nav .back{background:#fff;color:#152238;border:1px solid #c3cbd6}form:not(.js) [data-next],form:not(.js) .back,form:not(.js) .steps{display:none}{{end}}
{{define "fields"}}<input type="hidden" name="_ts" value="{{.Shown}}">
<div class="hp" aria-hidden="true"><label>Leave this empty <input type="text" name="_hp" tabindex="-1" autocomplete="off"></label></div>
{{$n := len .Pages}}{{if gt $n 1}}<div class="steps"><span data-step-label>Page 1 of {{$n}}</span><div class="bar" role="progressbar" aria-valuemin="1" aria-valuemax="{{$n}}"><i data-bar style="width:{{pct 1 $n}}%"></i></div></div>{{end}}
{{range $i, $p := .Pages}}<fieldset class="page" data-page="{{$i}}">{{with $p.Title}}<legend>{{.}}</legend>{{end}}
{{range $p.Fields}}{{$v := index $.Values .Key}}{{$e := index $.Errors .Key}}
{{if eq .Type "hidden"}}<input type="hidden" name="{{.Key}}" value="{{if $v}}{{$v}}{{else}}{{.Default}}{{end}}">
{{else if eq .Type "checkbox"}}<label class="check"><input type="checkbox" name="{{.Key}}"{{if $v}} checked{{end}}{{if .Required}} required{{end}}> <span>{{.Label}}{{if .Required}} <span class="req">*</span>{{end}}{{with $e}}<span class="err">{{.}}</span>{{end}}</span></label>
{{else}}<label>{{.Label}}{{if .Required}} <span class="req" aria-hidden="true">*</span>{{end}}
{{if eq .Type "textarea"}}<textarea name="{{.Key}}"{{if .Required}} required{{end}}>{{$v}}</textarea>
{{else if eq .Type "select"}}<select name="{{.Key}}"{{if .Required}} required{{end}}><option value="">Choose…</option>{{range .Options}}<option{{if eq . $v}} selected{{end}}>{{.}}</option>{{end}}</select>
{{else if eq .Type "file"}}<input type="file" name="{{.Key}}" accept="{{accept .Accept}}" data-max="{{maxBytes .}}"{{if .Required}} required{{end}}><span class="hint">{{if $.Problem}}Choose the file again. {{end}}Up to {{mb .}} MB.</span>
{{else}}<input type="{{.Type}}" name="{{.Key}}" value="{{$v}}"{{if .Required}} required{{end}}{{if eq .Type "email"}} autocomplete="email"{{else if eq .Type "tel"}} autocomplete="tel" placeholder="+91 98123 45678"{{end}}>{{end}}
{{with $e}}<span class="err">{{.}}</span>{{end}}</label>{{end}}{{end}}
{{if lt (add $i 1) $n}}<div class="nav">{{if $i}}<button type="button" class="back" data-back>Back</button>{{end}}<button type="button" data-next>Next</button></div>{{end}}
</fieldset>{{end}}
<div data-last>{{with .Form.Settings.ConsentText}}<label class="check"><input type="checkbox" name="_consent"{{if index $.Values "_consent"}} checked{{end}} required> <span>{{.}}{{with index $.Errors "_consent"}}<span class="err">{{.}}</span>{{end}}</span></label>{{end}}
<div class="nav">{{if gt $n 1}}<button type="button" class="back" data-back>Back</button>{{end}}<button type="submit">{{if .Form.Settings.SubmitText}}{{.Form.Settings.SubmitText}}{{else}}Submit{{end}}</button></div></div>{{end}}
{{define "form"}}<form method="post" novalidate data-paged{{if .Multipart}} enctype="multipart/form-data"{{end}}>{{template "fields" .}}</form>
<script nonce="{{.Nonce}}">(function(){var f=document.querySelector("form[data-paged]");if(!f)return;f.classList.add("js");
var pages=[].slice.call(f.querySelectorAll("fieldset.page")),last=f.querySelector("[data-last]"),lab=f.querySelector("[data-step-label]"),bar=f.querySelector("[data-bar]"),cur=0;
function show(i){cur=i;pages.forEach(function(p,j){p.hidden=j!==i});last.hidden=i!==pages.length-1;if(lab)lab.textContent="Page "+(i+1)+" of "+pages.length;if(bar)bar.style.width=Math.round((i+1)*100/pages.length)+"%";var x=pages[i].querySelector("input:not([type=hidden]),select,textarea");if(x&&i!==start)x.focus()}
function check(p){var ok=true;[].forEach.call(p.querySelectorAll("input,select,textarea"),function(el){var m="";if(el.type==="file"&&el.files&&el.files.length){var x=el.files[0],mx=+el.getAttribute("data-max"),ext=(x.name.match(/\.[^.]+$/)||[""])[0].toLowerCase(),acc=(el.getAttribute("accept")||"").split(",");if(mx&&x.size>mx)m="The file is too big: "+Math.round(mx/1048576)+" MB at most.";else if(acc[0]&&acc.indexOf(ext)<0)m="This type of file isn't accepted here.";}if(el.setCustomValidity)el.setCustomValidity(m);if(el.checkValidity&&!el.checkValidity())ok=false;});if(!ok){var b=p.querySelector(":invalid");if(b)b.reportValidity();}return ok}
f.addEventListener("click",function(e){var t=e.target;if(t.hasAttribute("data-next")){if(check(pages[cur]))show(cur+1)}else if(t.hasAttribute("data-back"))show(cur-1)});
f.addEventListener("change",function(e){if(e.target.setCustomValidity)e.target.setCustomValidity("")});
f.addEventListener("submit",function(e){if(!check(pages[cur])||(last&&!check(last)))e.preventDefault()});
var start=0,err=f.querySelector(".err");if(err)pages.forEach(function(p,j){if(p.contains(err))start=j});show(start)})();</script>{{end}}`))

var formPage = template.Must(template.Must(formTemplates.Clone()).New("f").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>{{.Form.Name}}</title>
<style>{{template "css" .}}</style></head>
<body><main>{{if .Done}}<div class="done" role="status"><h1>{{.Done}}</h1></div>{{else}}
<h1>{{.Form.Name}}</h1>{{with .Form.Settings.Description}}<p class="desc">{{.}}</p>{{end}}
{{with .Problem}}<div class="alert" role="alert">{{.}}</div>{{end}}
{{template "form" .}}{{end}}</main></body></html>`))

type formView struct {
	Form    model.Form
	Fields  []model.FormField
	Values  map[string]string
	Errors  map[string]string
	Problem string
	Done    string
	Shown   int64
	Nonce   string // for the page's one script (pages, file checks)
}

// Pages are the form's pages (page breaks split them).
func (v formView) Pages() []forms.Page { return forms.Pages(v.Fields) }

// Multipart: the form takes files, so the browser must post multipart/form-data.
func (v formView) Multipart() bool {
	for _, fd := range v.Fields {
		if fd.Type == forms.TypeFile {
			return true
		}
	}
	return false
}

func renderForm(w http.ResponseWriter, code int, v formView, redirect string) {
	formAction := "'self'"
	if u, err := url.Parse(redirect); err == nil && u.Host != "" {
		formAction += " " + u.Scheme + "://" + u.Host // browsers check the redirect after the post too
	}
	v.Nonce = auth.NewToken("")[:22]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-"+v.Nonce+"'; form-action "+formAction+"; base-uri 'none'; frame-ancestors *")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = formPage.Execute(w, v)
}

func notFoundPage(w http.ResponseWriter) {
	http.Error(w, "This form isn't available.", http.StatusNotFound)
}

func (s *Server) hostedForm(w http.ResponseWriter, r *http.Request) {
	f, _, err := s.activeForm(r.Context(), r.PathValue("key"))
	if err != nil {
		notFoundPage(w)
		return
	}
	values := map[string]string{}
	for _, fd := range f.Fields {
		if v := r.URL.Query().Get(fd.Key); v != "" && fd.Type == "hidden" { // e.g. ?utm_source=newsletter
			values[fd.Key] = v
		}
	}
	renderForm(w, http.StatusOK, formView{Form: f, Fields: f.Fields, Values: values, Shown: s.now().UnixMilli()}, f.Settings.RedirectURL)
}

func (s *Server) hostedFormSubmit(w http.ResponseWriter, r *http.Request) {
	f, t, err := s.activeForm(r.Context(), r.PathValue("key"))
	if err != nil {
		notFoundPage(w)
		return
	}
	values, uploads, err := readFormPost(w, r, f)
	if err != nil {
		http.Error(w, "The form data couldn't be read (are the files too big?).", http.StatusBadRequest)
		return
	}
	shown, _ := strconv.ParseInt(values["_ts"], 10, 64)
	in := formInput{Values: values, Uploads: uploads, PageURL: r.Referer(), Source: "hosted", Honeypot: values["_hp"],
		Elapsed: s.now().Sub(time.UnixMilli(shown))}
	ferrs, code, _, err := s.submit(r.Context(), f, t, in, r)
	view := formView{Form: f, Fields: f.Fields, Values: values, Errors: map[string]string{}, Shown: shown}
	switch {
	case err != nil && code == 0:
		s.Log.Error("form submission", "form", f.ID, "err", err)
		view.Problem = "Something went wrong. Please try again."
		renderForm(w, http.StatusInternalServerError, view, f.Settings.RedirectURL)
	case err != nil:
		view.Problem = err.Error()
		renderForm(w, code, view, f.Settings.RedirectURL)
	case len(ferrs) > 0:
		for _, fe := range ferrs {
			view.Errors[fe.Field] = fe.Message
		}
		view.Problem = "Please check the highlighted fields."
		renderForm(w, code, view, f.Settings.RedirectURL)
	case f.Settings.RedirectURL != "":
		http.Redirect(w, r, f.Settings.RedirectURL, http.StatusSeeOther)
	default:
		view.Done = f.Settings.SuccessMessage
		if view.Done == "" {
			view.Done = "Thanks! We've got your details."
		}
		renderForm(w, http.StatusOK, view, "")
	}
}

// ---- web SDK ----

// webForm returns a form for the SDK to render, without the fields a known visitor already filled.
func (s *Server) webForm(w http.ResponseWriter, r *http.Request) {
	cors(w)
	f, t, err := s.activeForm(r.Context(), r.PathValue("key"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "this form isn't available")
		return
	}
	q := r.URL.Query()
	fields := forms.Visible(f, s.visitorProfile(r.Context(), t, q.Get("id"), q.Get("user_hash")))
	st := f.Settings
	writeJSON(w, http.StatusOK, map[string]any{"key": f.PublicKey, "name": f.Name, "fields": fields, "shown": s.now().UnixMilli(),
		"settings": map[string]any{"submit_text": st.SubmitText, "success_message": st.SuccessMessage, "redirect_url": st.RedirectURL,
			"consent_text": st.ConsentText, "description": st.Description, "theme": st.Theme}})
}

func (s *Server) webFormSubmit(w http.ResponseWriter, r *http.Request) {
	cors(w)
	f, t, err := s.activeForm(r.Context(), r.PathValue("key"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "this form isn't available")
		return
	}
	var req struct {
		Values   map[string]string `json:"values"`
		ID       string            `json:"id"` // the SDK's current id: user id or anon-...
		UserHash string            `json:"user_hash"`
		PageURL  string            `json:"page_url"`
		Shown    int64             `json:"shown"` // from GET, unix ms
		HP       string            `json:"hp"`
		// first_utm_* / last_utm_* / first_referrer / last_referrer kept by the SDK
		Attribution map[string]string `json:"attribution"`
	}
	var uploads map[string]forms.Upload
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		// With files the SDK posts multipart/form-data: the JSON body as "payload", and each file
		// under its field's key.
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBody(f))
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeErr(w, http.StatusRequestEntityTooLarge, "the files are too big for this form")
			return
		}
		defer r.MultipartForm.RemoveAll()
		if err := json.Unmarshal([]byte(r.FormValue("payload")), &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if uploads, err = readUploads(r.MultipartForm, f); err != nil {
			writeErr(w, http.StatusBadRequest, "unreadable file")
			return
		}
	} else if !decode(w, r, &req) {
		return
	}
	if len(req.Attribution) > len(attributionKeys) {
		writeErr(w, http.StatusBadRequest, "too many attribution values")
		return
	}
	ferrs, code, _, err := s.submit(r.Context(), f, t, formInput{Values: req.Values, Uploads: uploads, Visitor: req.ID, UserHash: req.UserHash,
		PageURL: req.PageURL, Source: "sdk", Honeypot: req.HP, Elapsed: s.now().Sub(time.UnixMilli(req.Shown)), Attribution: req.Attribution}, r)
	switch {
	case err != nil && code == 0:
		s.fail(w, err)
	case err != nil:
		writeErr(w, code, err.Error())
	case len(ferrs) > 0:
		writeJSON(w, code, map[string]any{"error": "Please check the highlighted fields.", "fields": ferrs})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": f.Settings.SuccessMessage, "redirect_url": f.Settings.RedirectURL})
	}
}
