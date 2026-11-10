package api

// Landing pages: built in the app (drag-and-drop or HTML), served at
// /p/{workspace slug}/{page slug} while published and inside their schedule.
//
// The page carries the web SDK with the workspace's site key: it records page_view and
// landing_page_viewed (page_id, utm_*) for the visitor, anonymous until they identify or submit an
// embedded form (which merges their views into the contact). Stats come from those events.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"pulse/internal/landing"
	"pulse/internal/model"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

const pageViewsPerMin = 300 // per IP and workspace

type LandingStore interface {
	ListLandingPages(ctx context.Context, tenant int64) ([]model.LandingPage, error)
	GetLandingPage(ctx context.Context, tenant, id int64) (model.LandingPage, error)
	LandingPageBySlug(ctx context.Context, tenant int64, slug string) (model.LandingPage, error)
	SaveLandingPage(ctx context.Context, p model.LandingPage) (int64, error)
	DeleteLandingPage(ctx context.Context, tenant, id int64) error
}

type PageAnalytics interface {
	LandingPageStats(ctx context.Context, tenant, page int64) (ch.PageStats, error)
}

func (s *Server) landingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/landing-pages", s.tenant(s.listPages))
	mux.HandleFunc("POST /v1/landing-pages", s.tenant(s.savePage))
	mux.HandleFunc("POST /v1/landing-pages/preview", s.tenant(s.previewPage))
	mux.HandleFunc("GET /v1/landing-pages/{id}", s.tenant(s.getPage))
	mux.HandleFunc("PUT /v1/landing-pages/{id}", s.tenant(s.savePage))
	mux.HandleFunc("DELETE /v1/landing-pages/{id}", s.tenant(s.deletePage))
	mux.HandleFunc("GET /v1/landing-pages/{id}/stats", s.tenant(s.pageStats))

	mux.HandleFunc("GET /p/{workspace}/{slug}", s.servePage)
}

func (s *Server) pageStore() LandingStore { return s.Store.(LandingStore) }

// pagesBase is where the workspace's pages live: {PublicURL}/p/{workspace slug}/.
func (s *Server) pagesBase(ctx context.Context, tenant int64) (string, error) {
	t, err := s.Store.Tenant(ctx, tenant)
	if err != nil {
		return "", err
	}
	return s.PublicURL + "/p/" + url.PathEscape(t.Slug) + "/", nil
}

func (s *Server) listPages(w http.ResponseWriter, r *http.Request, tenant int64) {
	ps, err := s.pageStore().ListLandingPages(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ps == nil {
		ps = []model.LandingPage{}
	}
	base, err := s.pagesBase(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, len(ps))
	now := s.now()
	for i, p := range ps {
		out[i] = map[string]any{"page": p, "live": landing.Live(p, now)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": out, "base_url": base})
}

func (s *Server) getPage(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := s.pageStore().GetLandingPage(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	base, err := s.pagesBase(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"page": p, "url": base + p.Slug, "base_url": base, "live": landing.Live(p, s.now())})
}

// workspaceForms maps the workspace's forms by public key.
func (s *Server) workspaceForms(ctx context.Context, tenant int64) (map[string]model.Form, error) {
	fs, err := s.formStore().ListForms(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make(map[string]model.Form, len(fs))
	for _, f := range fs {
		out[f.PublicKey] = f
	}
	return out, nil
}

func (s *Server) savePage(w http.ResponseWriter, r *http.Request, tenant int64) {
	var p model.LandingPage
	if !decode(w, r, &p) {
		return
	}
	p.TenantID, p.ID = tenant, 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		p.ID = id
	}
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	p.Title = strings.TrimSpace(p.Title)
	if p.Status == "" {
		p.Status = landing.Draft
	}
	if p.StartAt.IsZero() {
		p.StartAt = s.now() // API callers that omit it start now; the app always sends it
	}
	if err := landing.Validate(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	forms, err := s.workspaceForms(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, k := range landing.FormKeys(p.HTML) {
		if _, ok := forms[k]; !ok {
			writeErr(w, http.StatusBadRequest, "the page embeds form "+k+", which isn't a form of this workspace")
			return
		}
	}
	id, err := s.pageStore().SaveLandingPage(r.Context(), p)
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, "another page already uses the address "+p.Slug)
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": p.Status, "live": landing.Live(p, s.now())})
}

func (s *Server) deletePage(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.pageStore().DeleteLandingPage(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewPage renders a page, saved or not, as the app's preview: no scripts at all, embedded forms
// drawn as static copies. The app shows it in a sandboxed iframe.
func (s *Server) previewPage(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Page model.LandingPage `json:"page"`
	}
	if !decode(w, r, &req) {
		return
	}
	forms, err := s.workspaceForms(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if strings.TrimSpace(req.Page.Title) == "" {
		req.Page.Title = "Untitled page"
	}
	doc := landing.Render(req.Page, landing.Page{Preview: true, Forms: forms})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", landing.PreviewCSP)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(doc))
}

func (s *Server) pageStats(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.pageStore().GetLandingPage(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	pa, ok := s.Analytics.(PageAnalytics)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "stats need the analytics database")
		return
	}
	st, err := pa.LandingPageStats(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func pageNotFound(w http.ResponseWriter) {
	http.Error(w, "This page isn't available.", http.StatusNotFound)
}

// servePage serves a live landing page under a nonce-based CSP: only Pulse's own SDK scripts run.
func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.TenantBySlug(r.Context(), r.PathValue("workspace"))
	if err != nil {
		pageNotFound(w)
		return
	}
	if !s.allowRate(r.Context(), "rlpage", t.ID, r, 1, pageViewsPerMin) {
		http.Error(w, "Too many requests from this network; try again in a minute.", http.StatusTooManyRequests)
		return
	}
	p, err := s.pageStore().LandingPageBySlug(r.Context(), t.ID, r.PathValue("slug"))
	if err != nil || !landing.Live(p, s.now()) {
		if err != nil && !errors.Is(err, pg.ErrNotFound) {
			s.Log.Error("landing page", "err", err)
		}
		pageNotFound(w)
		return
	}
	nonce := make([]byte, 18)
	_, _ = rand.Read(nonce)
	n := base64.RawStdEncoding.EncodeToString(nonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", landing.CSP(n))
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(landing.Render(p, landing.Page{SiteKey: t.SiteKey, Nonce: n})))
}

// landingPageOf returns the id of the workspace's landing page at pageURL (a form submission's page),
// or 0 when the URL isn't one.
func (s *Server) landingPageOf(ctx context.Context, t model.Tenant, pageURL string) int64 {
	ls, ok := s.Store.(LandingStore)
	if !ok {
		return 0
	}
	u, err := url.Parse(pageURL)
	base, berr := url.Parse(s.PublicURL)
	if err != nil || berr != nil || !strings.EqualFold(u.Host, base.Host) {
		return 0
	}
	rest, ok := strings.CutPrefix(u.Path, "/p/"+t.Slug+"/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return 0
	}
	p, err := ls.LandingPageBySlug(ctx, t.ID, rest)
	if err != nil {
		return 0
	}
	return p.ID
}
