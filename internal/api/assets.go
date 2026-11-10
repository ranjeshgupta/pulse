package api

// Assets: files uploaded in the app, downloaded at /a/{key} and attached to emails.
//
// Files live in Postgres (asset_files, up to 25 MB each, 2 GB per workspace), so any API instance
// serves any file without a shared disk. Downloads are always attachments with the stored type
// (checked against the extension's signature on upload), nosniff and a sandboxing CSP, so nothing
// uploaded is ever rendered as a page on Pulse's origin.
//
// An asset with a form is gated: /a/{key} shows the form; a valid submission gets a sealed link
// valid for 15 minutes, and following it records asset_downloaded for the submitter. Ungated
// downloads are counted (download_count) without knowing who downloaded.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pulse/internal/assets"
	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

const (
	assetDownloadsPerMin = 60 // per IP and workspace
	gateLinkTTL          = 15 * time.Minute
	maxAssetUpload       = assets.MaxSize + 1<<20 // the file plus the form's other fields
)

type AssetStore interface {
	ListAssets(ctx context.Context, tenant int64) ([]model.Asset, error)
	GetAsset(ctx context.Context, tenant, id int64) (model.Asset, error)
	AssetByKey(ctx context.Context, key string) (model.Asset, error)
	AssetsByIDs(ctx context.Context, tenant int64, ids []int64) ([]model.Asset, error)
	CreateAsset(ctx context.Context, a model.Asset, data []byte, maxTotal int64) (int64, error)
	UpdateAsset(ctx context.Context, a model.Asset) error
	DeleteAsset(ctx context.Context, tenant, id int64) error
	AssetFile(ctx context.Context, tenant, id int64) (model.Asset, []byte, error)
	CountAssetDownload(ctx context.Context, tenant, id int64) error
	TemplatesUsingAsset(ctx context.Context, tenant, id int64) ([]string, error)
}

type AssetAnalytics interface {
	AssetDownloads(ctx context.Context, tenant, asset int64) (downloads, contacts uint64, err error)
}

func (s *Server) assetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/assets", s.tenant(s.listAssets))
	mux.HandleFunc("POST /v1/assets", s.tenant(s.uploadAsset))
	mux.HandleFunc("GET /v1/assets/{id}", s.tenant(s.getAsset))
	mux.HandleFunc("PUT /v1/assets/{id}", s.tenant(s.updateAsset))
	mux.HandleFunc("DELETE /v1/assets/{id}", s.tenant(s.deleteAsset))
	mux.HandleFunc("GET /v1/assets/{id}/file", s.tenant(s.assetFileForUser))

	mux.HandleFunc("GET /a/{key}", s.assetPage)
	mux.HandleFunc("POST /a/{key}", s.assetFormSubmit)
	mux.HandleFunc("GET /a/{key}/download", s.assetDownload)
}

func (s *Server) assetStore() AssetStore { return s.Store.(AssetStore) }

// ---- management ----

func (s *Server) assetURL(a model.Asset) string { return s.publicBase() + "/a/" + a.PublicKey }

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request, tenant int64) {
	as, err := s.assetStore().ListAssets(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if as == nil {
		as = []model.Asset{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": as, "base_url": s.PublicURL, "max_size": assets.MaxSize,
		"max_attachment_bytes": assets.MaxAttachmentBytes, "extensions": assets.Extensions()})
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := s.assetStore().GetAsset(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	stats := map[string]any{"downloads": a.DownloadCount}
	if aa, ok := s.Analytics.(AssetAnalytics); ok {
		if n, c, err := aa.AssetDownloads(r.Context(), tenant, id); err == nil {
			stats["known_downloads"], stats["contacts"] = n, c
		} else {
			s.Log.Warn("asset download stats", "err", err)
		}
	}
	used, err := s.assetStore().TemplatesUsingAsset(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if used == nil {
		used = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"asset": a, "url": s.assetURL(a), "stats": stats, "templates": used})
}

// assetMeta is what can be set on an asset besides its file.
type assetMeta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	FormID      *int64 `json:"form_id"`
}

func (s *Server) validAssetMeta(ctx context.Context, tenant int64, m *assetMeta) error {
	m.Title, m.Description = strings.TrimSpace(m.Title), strings.TrimSpace(m.Description)
	switch {
	case m.Title == "":
		return errors.New("a title is required")
	case len(m.Title) > 200:
		return errors.New("the title is longer than 200 characters")
	case len(m.Description) > 2000:
		return errors.New("the description is longer than 2,000 characters")
	}
	if m.FormID != nil && *m.FormID == 0 {
		m.FormID = nil
	}
	if m.FormID != nil {
		if _, err := s.formStore().GetForm(ctx, tenant, *m.FormID); errors.Is(err, pg.ErrNotFound) {
			return errors.New("the gating form doesn't exist")
		} else if err != nil {
			return err
		}
	}
	return nil
}

// uploadAsset takes multipart/form-data: file, title, description, form_id (optional).
func (s *Server) uploadAsset(w http.ResponseWriter, r *http.Request, tenant int64) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send the file as multipart/form-data")
		return
	}
	var meta assetMeta
	var filename string
	var data []byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.uploadErr(w, err)
			return
		}
		switch part.FormName() {
		case "file":
			filename = assets.SafeFilename(part.FileName())
			data, err = io.ReadAll(io.LimitReader(part, assets.MaxSize+1))
			if err != nil {
				s.uploadErr(w, err)
				return
			}
			if len(data) > assets.MaxSize {
				writeErr(w, http.StatusRequestEntityTooLarge, "files can be at most 25 MB")
				return
			}
		case "title", "description", "form_id":
			b, err := io.ReadAll(io.LimitReader(part, 4<<10))
			if err != nil {
				s.uploadErr(w, err)
				return
			}
			switch v := string(b); part.FormName() {
			case "title":
				meta.Title = v
			case "description":
				meta.Description = v
			case "form_id":
				if v != "" && v != "0" {
					id, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						writeErr(w, http.StatusBadRequest, "invalid form_id")
						return
					}
					meta.FormID = &id
				}
			}
		}
		part.Close()
	}
	if filename == "" || data == nil {
		writeErr(w, http.StatusBadRequest, "choose a file to upload")
		return
	}
	ct, err := assets.Detect(filename, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if meta.Title == "" {
		meta.Title = strings.TrimSuffix(filename, filename[strings.LastIndexByte(filename, '.'):])
	}
	if err := s.validAssetMeta(r.Context(), tenant, &meta); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sum := sha256.Sum256(data)
	a := model.Asset{TenantID: tenant, PublicKey: auth.NewToken("")[:22], Title: meta.Title, Description: meta.Description,
		Filename: filename, ContentType: ct, SHA256: hex.EncodeToString(sum[:]), FormID: meta.FormID}
	id, err := s.assetStore().CreateAsset(r.Context(), a, data, assets.MaxWorkspaceBytes)
	if errors.Is(err, pg.ErrQuota) {
		writeErr(w, http.StatusRequestEntityTooLarge, "the workspace's files would go over 2 GB; delete some first")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	a, err = s.assetStore().GetAsset(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "asset": a, "url": s.assetURL(a)})
}

func (s *Server) uploadErr(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, http.StatusRequestEntityTooLarge, "files can be at most 25 MB")
		return
	}
	writeErr(w, http.StatusBadRequest, "the upload was interrupted or malformed")
}

func (s *Server) updateAsset(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var m assetMeta
	if !decode(w, r, &m) {
		return
	}
	if err := s.validAssetMeta(r.Context(), tenant, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.assetStore().UpdateAsset(r.Context(), model.Asset{ID: id, TenantID: tenant, Title: m.Title, Description: m.Description, FormID: m.FormID}); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	used, err := s.assetStore().TemplatesUsingAsset(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(used) > 0 {
		writeErr(w, http.StatusConflict, "these email templates attach this file: "+strings.Join(used, ", ")+". Remove it from them first.")
		return
	}
	if err := s.assetStore().DeleteAsset(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// assetFileForUser lets people in the app download a file (to check it) without counting it.
func (s *Server) assetFileForUser(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, data, err := s.assetStore().AssetFile(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	serveAsset(w, a, data)
}

// checkAttachments verifies that an email template's attachments are the workspace's assets and fit
// the size cap together.
func (s *Server) checkAttachments(ctx context.Context, tenant int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	as, err := s.assetStore().AssetsByIDs(ctx, tenant, ids)
	if err != nil {
		return err
	}
	if len(as) != len(ids) {
		return errUser("an attachment doesn't exist in this workspace")
	}
	var total int64
	for _, a := range as {
		total += a.Size
	}
	if total > assets.MaxAttachmentBytes {
		return errUser("attachments can be at most 10 MB together")
	}
	return nil
}

// errUser is a validation failure to show as it is (400).
type errUser string

func (e errUser) Error() string { return string(e) }

// ---- public download ----

// serveAsset sends a file as a download: its stored type, never rendered inline, never sniffed.
func serveAsset(w http.ResponseWriter, a model.Asset, data []byte) {
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	h.Set("Content-Disposition", assets.Disposition(a.Filename))
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func assetUnavailable(w http.ResponseWriter) {
	http.Error(w, "This download isn't available.", http.StatusNotFound)
}

// publicAsset loads an asset by key with its workspace and, when gated, its form (which must be
// active: a gated asset whose form was deleted or is a draft isn't served at all).
func (s *Server) publicAsset(w http.ResponseWriter, r *http.Request) (model.Asset, model.Tenant, *model.Form, bool) {
	a, err := s.assetStore().AssetByKey(r.Context(), r.PathValue("key"))
	if err != nil {
		assetUnavailable(w)
		return a, model.Tenant{}, nil, false
	}
	t, err := s.Store.Tenant(r.Context(), a.TenantID)
	if err != nil {
		assetUnavailable(w)
		return a, t, nil, false
	}
	if !s.allowRate(r.Context(), "rlasset", t.ID, r, 1, assetDownloadsPerMin) {
		http.Error(w, "Too many downloads from this network; try again in a minute.", http.StatusTooManyRequests)
		return a, t, nil, false
	}
	if a.FormID == nil {
		return a, t, nil, true
	}
	f, err := s.formStore().GetForm(r.Context(), t.ID, *a.FormID)
	if err != nil || f.Status != "active" {
		assetUnavailable(w)
		return a, t, nil, false
	}
	return a, t, &f, true
}

// assetPage downloads an open asset, or shows a gated asset's form.
func (s *Server) assetPage(w http.ResponseWriter, r *http.Request) {
	a, t, f, ok := s.publicAsset(w, r)
	if !ok {
		return
	}
	if f == nil {
		s.sendAsset(w, r, a, t, "")
		return
	}
	renderAssetPage(w, http.StatusOK, assetView{formView: formView{Form: *f, Fields: f.Fields, Values: map[string]string{},
		Errors: map[string]string{}, Shown: s.now().UnixMilli()}, Asset: a})
}

// sendAsset loads and sends the file, counts it, and records asset_downloaded for a known downloader.
func (s *Server) sendAsset(w http.ResponseWriter, r *http.Request, a model.Asset, t model.Tenant, ext string) {
	_, data, err := s.assetStore().AssetFile(r.Context(), t.ID, a.ID)
	if err != nil {
		if !errors.Is(err, pg.ErrNotFound) {
			s.Log.Error("asset download", "asset", a.ID, "err", err)
		}
		assetUnavailable(w)
		return
	}
	if err := s.assetStore().CountAssetDownload(r.Context(), t.ID, a.ID); err != nil {
		s.Log.Warn("count asset download", "asset", a.ID, "err", err)
	}
	if ext != "" {
		// One event per download link, however often it is followed.
		sum := sha256.Sum256([]byte(r.URL.Query().Get("t")))
		ev := event.Event{ID: "asset:" + hex.EncodeToString(sum[:12]), ExternalID: ext, Name: event.AssetDownloaded,
			Props: map[string]any{"asset_id": a.ID, "asset_title": a.Title, "filename": a.Filename}}
		if err := s.publishEvents(r.Context(), t.ID, []event.Event{ev}); err != nil {
			s.Log.Warn("asset_downloaded event", "asset", a.ID, "err", err)
		}
	}
	serveAsset(w, a, data)
}

func (s *Server) assetFormSubmit(w http.ResponseWriter, r *http.Request) {
	a, t, f, ok := s.publicAsset(w, r)
	if !ok {
		return
	}
	if f == nil {
		http.Redirect(w, r, "/a/"+url.PathEscape(a.PublicKey), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}
	values := map[string]string{}
	for k := range r.PostForm {
		values[k] = r.PostForm.Get(k)
	}
	shown, _ := strconv.ParseInt(values["_ts"], 10, 64)
	in := formInput{Values: values, PageURL: r.Referer(), Source: "asset", Honeypot: values["_hp"],
		Elapsed: s.now().Sub(time.UnixMilli(shown)), Meta: map[string]any{"asset_id": a.ID}}
	ferrs, code, ext, err := s.submit(r.Context(), *f, t, in, r)
	view := assetView{formView: formView{Form: *f, Fields: f.Fields, Values: values, Errors: map[string]string{}, Shown: shown}, Asset: a}
	switch {
	case err != nil && code == 0:
		s.Log.Error("asset form submission", "asset", a.ID, "err", err)
		view.Problem = "Something went wrong. Please try again."
		renderAssetPage(w, http.StatusInternalServerError, view)
	case err != nil:
		view.Problem = err.Error()
		renderAssetPage(w, code, view)
	case len(ferrs) > 0:
		for _, fe := range ferrs {
			view.Errors[fe.Field] = fe.Message
		}
		view.Problem = "Please check the highlighted fields."
		renderAssetPage(w, code, view)
	default:
		view.Ready = true
		view.Download = "/a/" + url.PathEscape(a.PublicKey) // bots (nothing recorded) get no file
		if ext != "" && s.Box != nil {
			tok := assets.Seal(s.Box, assets.Token{TenantID: t.ID, AssetID: a.ID, ExternalID: ext, Purpose: assets.PurposeGate,
				Expires: s.now().Add(gateLinkTTL).Unix()})
			view.Download += "/download?t=" + url.QueryEscape(tok)
		}
		renderAssetPage(w, http.StatusOK, view)
	}
}

// assetDownload serves a file through a sealed link: a gated asset's link after its form, or an
// email attachment's link handed to the custom HTTP gateway.
func (s *Server) assetDownload(w http.ResponseWriter, r *http.Request) {
	tok, err := assets.Open(s.Box, r.URL.Query().Get("t"), s.now())
	if err != nil {
		http.Error(w, "This download link isn't valid any more. Go back to the download page to get a new one.", http.StatusForbidden)
		return
	}
	a, err := s.assetStore().AssetByKey(r.Context(), r.PathValue("key"))
	if err != nil || a.ID != tok.AssetID || a.TenantID != tok.TenantID {
		assetUnavailable(w)
		return
	}
	if !s.allowRate(r.Context(), "rlasset", a.TenantID, r, 1, assetDownloadsPerMin) {
		http.Error(w, "Too many downloads from this network; try again in a minute.", http.StatusTooManyRequests)
		return
	}
	if tok.Purpose == assets.PurposeAttach {
		_, data, err := s.assetStore().AssetFile(r.Context(), a.TenantID, a.ID)
		if err != nil {
			assetUnavailable(w)
			return
		}
		serveAsset(w, a, data)
		return
	}
	s.sendAsset(w, r, a, model.Tenant{ID: a.TenantID}, tok.ExternalID)
}

type assetView struct {
	formView
	Asset    model.Asset
	Ready    bool // the form was accepted: show the download
	Download string
}

var assetPageTpl = template.Must(template.Must(formTemplates.Clone()).New("a").Funcs(template.FuncMap{
	"size": func(n int64) string {
		switch {
		case n >= 1<<20:
			return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
		case n >= 1<<10:
			return strconv.FormatInt(n>>10, 10) + " KB"
		}
		return strconv.FormatInt(n, 10) + " bytes"
	},
	"kind": func(ct string) string {
		t, _, _ := mime.ParseMediaType(ct)
		if exts, _ := mime.ExtensionsByType(t); len(exts) > 0 {
			return strings.ToUpper(strings.TrimPrefix(exts[len(exts)-1], "."))
		}
		return "File"
	},
}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>{{.Asset.Title}}</title>
{{if .Ready}}<meta http-equiv="refresh" content="1;url={{.Download}}">{{end}}
<style>{{template "css" .}}.file{display:flex;gap:12px;align-items:center;background:#f4f6f8;border-radius:8px;padding:12px 14px;margin:0 0 18px;font-size:14px;color:#5b6474}
.file b{color:#152238}.dl{display:block;text-align:center;text-decoration:none;font-weight:600;background:var(--accent);color:#fff;border-radius:6px;padding:11px 18px}</style></head>
<body><main><h1>{{.Asset.Title}}</h1>{{with .Asset.Description}}<p class="desc">{{.}}</p>{{end}}
<div class="file"><span><b>{{.Asset.Filename}}</b><br>{{kind .Asset.ContentType}} · {{size .Asset.Size}}</span></div>
{{if .Ready}}<div class="done" role="status"><h1>Thanks! Your download is starting.</h1><p class="desc">If it doesn't, use the button below. The link works for 15 minutes.</p>
<a class="dl" href="{{.Download}}">Download {{.Asset.Filename}}</a></div>
{{else}}{{with .Form.Settings.Description}}<p class="desc">{{.}}</p>{{end}}
{{with .Problem}}<div class="alert" role="alert">{{.}}</div>{{end}}
<form method="post" novalidate>{{template "fields" .}}</form>{{end}}</main></body></html>`))

func renderAssetPage(w http.ResponseWriter, code int, v assetView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	var b bytes.Buffer
	if err := assetPageTpl.Execute(&b, v); err != nil {
		http.Error(w, "page error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(code)
	_, _ = w.Write(b.Bytes())
}
