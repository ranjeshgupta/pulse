package api

// Public campaign archive and RSS-to-email (O6). Email campaigns marked "show in the public
// archive" list each send at /p/{workspace}/archive (newest first), each viewable at
// /p/{workspace}/archive/{run} as everyone would see it (no contact's details: the template is
// rendered for an empty contact), with an RSS feed at /p/{workspace}/archive.rss. They live on the
// pages origin under the landing pages' CSP, so stored scripts never run.
//
// POST /v1/broadcasts/feed-preview {"url": "...", "repeat": "weekly"} reads an RSS/Atom feed and
// shows what the next run of an RSS-to-email campaign would send.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/broadcast"
	"pulse/internal/channel"
	"pulse/internal/feed"
	"pulse/internal/landing"
	"pulse/internal/model"
	"pulse/internal/personalize"
	"pulse/internal/safehttp"
	"pulse/internal/store/pg"
)

// ArchiveStore reads the public archive (pg.Store).
type ArchiveStore interface {
	ArchiveEntries(ctx context.Context, tenant int64, limit int) ([]model.ArchiveEntry, error)
	ArchiveEntry(ctx context.Context, tenant, run int64) (model.ArchiveEntry, error)
}

const archiveSize = 50

func (s *Server) archiveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /p/{workspace}/archive", s.archiveIndex)
	mux.HandleFunc("GET /p/{workspace}/archive.rss", s.archiveRSS)
	mux.HandleFunc("GET /p/{workspace}/archive/{run}", s.archivePage)
	mux.HandleFunc("POST /v1/broadcasts/feed-preview", s.tenant(s.feedPreview))
}

// archiveTenant finds the workspace of a public archive request (false: answered).
func (s *Server) archiveTenant(w http.ResponseWriter, r *http.Request) (model.Tenant, ArchiveStore, bool) {
	as, ok := s.Store.(ArchiveStore)
	if !ok {
		pageNotFound(w)
		return model.Tenant{}, nil, false
	}
	t, err := s.Store.TenantBySlug(r.Context(), r.PathValue("workspace"))
	if err != nil {
		pageNotFound(w)
		return t, nil, false
	}
	if !s.allowRate(r.Context(), "rlpage", t.ID, r, 1, pageViewsPerMin) {
		http.Error(w, "Too many requests from this network; try again in a minute.", http.StatusTooManyRequests)
		return t, nil, false
	}
	return t, as, true
}

// archived is one send rendered for the archive.
type archived struct {
	model.ArchiveEntry
	Subject string
	HTML    string
	Text    string
}

// archiveTTL is how long a rendered send is kept (in Redis), so anonymous views don't render the
// template every time; the pages are cached as long by browsers anyway.
const archiveTTL = 5 * time.Minute

type archivedPage struct{ Subject, HTML, Text string }

func archiveKey(tenant, run int64) string {
	return "archivepage:" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(run, 10)
}

// archiveCSP is the landing pages' CSP without frames (an email has none to show).
func archiveCSP(nonce string) string {
	return strings.Replace(landing.CSP(nonce), "frame-src 'self' https:", "frame-src 'none'", 1)
}

// renderArchived renders a send's template for an empty contact (templates are cached per call),
// sanitized like a landing page, and keeps the result for archiveTTL.
func (s *Server) renderArchived(ctx context.Context, tenant int64, e model.ArchiveEntry, blocks map[string]channel.Block, cache map[int64]model.Template) (archived, error) {
	if s.RDB != nil {
		if raw, err := s.RDB.Get(ctx, archiveKey(tenant, e.RunID)).Bytes(); err == nil {
			var p archivedPage
			if json.Unmarshal(raw, &p) == nil {
				return archived{ArchiveEntry: e, Subject: p.Subject, HTML: p.HTML, Text: p.Text}, nil
			}
		}
	}
	t, ok := cache[e.TemplateID]
	if !ok {
		var err error
		if t, err = s.uiStore().Template(ctx, tenant, e.TemplateID); err != nil {
			return archived{}, err
		}
		cache[e.TemplateID] = t
	}
	if e.FeedHTML != "" {
		b := make(map[string]channel.Block, len(blocks)+1)
		for k, v := range blocks {
			b[k] = v
		}
		b["feed"] = channel.Block{HTML: e.FeedHTML, Text: e.FeedText}
		blocks = b
	}
	out, err := channel.RenderWith(t, model.Contact{Attrs: map[string]any{}}, channel.Vars{
		Campaign: personalize.Campaign{Name: e.Name, Type: "broadcast", ID: e.BroadcastID, Channel: t.Channel}, Blocks: blocks})
	if err != nil {
		return archived{}, err
	}
	subject := strings.TrimSpace(out.Subject)
	if subject == "" {
		subject = e.Name
	}
	a := archived{ArchiveEntry: e, Subject: subject, HTML: landing.Sanitize(out.HTML), Text: out.Content.Text}
	if s.RDB != nil {
		if raw, err := json.Marshal(archivedPage{Subject: a.Subject, HTML: a.HTML, Text: a.Text}); err == nil {
			s.RDB.Set(ctx, archiveKey(tenant, e.RunID), raw, archiveTTL)
		}
	}
	return a, nil
}

func (s *Server) archiveList(ctx context.Context, t model.Tenant, as ArchiveStore) ([]archived, error) {
	list, err := as.ArchiveEntries(ctx, t.ID, archiveSize)
	if err != nil {
		return nil, err
	}
	blocks, cache := s.renderBlocks(ctx, t.ID), map[int64]model.Template{}
	out := make([]archived, 0, len(list))
	for _, e := range list {
		a, err := s.renderArchived(ctx, t.ID, e, blocks, cache)
		if err != nil {
			continue // a deleted or broken template: leave the send out
		}
		out = append(out, a)
	}
	return out, nil
}

func pageNonce() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

func (s *Server) archiveIndex(w http.ResponseWriter, r *http.Request) {
	t, as, ok := s.archiveTenant(w, r)
	if !ok {
		return
	}
	list, err := s.archiveList(r.Context(), t, as)
	if err != nil {
		s.Log.Error("campaign archive", "err", err)
		http.Error(w, "The archive isn't available right now.", http.StatusServiceUnavailable)
		return
	}
	n := pageNonce()
	name := html.EscapeString(t.Name)
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s: past emails</title><link rel="alternate" type="application/rss+xml" title="%s" href="archive.rss">
<style nonce="%s">body{font:16px/1.5 system-ui,sans-serif;max-width:720px;margin:0 auto;padding:32px 16px;color:#1d2029;background:#fff}
h1{font-size:28px;margin:0 0 4px}p.sub{color:#5b6070;margin:0 0 24px}ul{list-style:none;padding:0;margin:0}
li{border-top:1px solid #e6e8ee;padding:14px 0}a{color:#2b4bd1;text-decoration:none;font-weight:600}a:hover{text-decoration:underline}
time{display:block;color:#6b7080;font-size:14px}@media(prefers-color-scheme:dark){body{background:#14161c;color:#e7e9ee}li{border-color:#2a2e38}a{color:#8ea6ff}p.sub,time{color:#a0a6b4}}</style></head>
<body><h1>%s</h1><p class="sub">Past emails · <a href="archive.rss">RSS</a></p>`, name, name, n, name)
	if len(list) == 0 {
		b.WriteString(`<p>Nothing here yet.</p>`)
	} else {
		b.WriteString(`<ul>`)
		for _, a := range list {
			fmt.Fprintf(&b, `<li><a href="archive/%d">%s</a><time datetime="%s">%s</time></li>`, a.RunID, html.EscapeString(a.Subject),
				a.SentAt.UTC().Format(time.RFC3339), a.SentAt.UTC().Format("2 Jan 2006"))
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", archiveCSP(n))
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) archivePage(w http.ResponseWriter, r *http.Request) {
	t, as, ok := s.archiveTenant(w, r)
	if !ok {
		return
	}
	run, err := strconv.ParseInt(r.PathValue("run"), 10, 64)
	if err != nil {
		pageNotFound(w)
		return
	}
	e, err := as.ArchiveEntry(r.Context(), t.ID, run)
	if err != nil {
		if !errors.Is(err, pg.ErrNotFound) {
			s.Log.Error("campaign archive", "err", err)
		}
		pageNotFound(w)
		return
	}
	a, err := s.renderArchived(r.Context(), t.ID, e, s.renderBlocks(r.Context(), t.ID), map[int64]model.Template{})
	if err != nil {
		pageNotFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", archiveCSP(pageNonce()))
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte(a.HTML))
}

type rssDoc struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title       string    `xml:"title"`
		Link        string    `xml:"link"`
		Description string    `xml:"description"`
		Items       []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func (s *Server) archiveRSS(w http.ResponseWriter, r *http.Request) {
	t, as, ok := s.archiveTenant(w, r)
	if !ok {
		return
	}
	list, err := s.archiveList(r.Context(), t, as)
	if err != nil {
		s.Log.Error("campaign archive", "err", err)
		http.Error(w, "The archive isn't available right now.", http.StatusServiceUnavailable)
		return
	}
	base := s.publicBase() + "/p/" + t.Slug + "/archive"
	var d rssDoc
	d.Version = "2.0"
	d.Channel.Title, d.Channel.Link, d.Channel.Description = t.Name, base, "Past emails from "+t.Name
	for _, a := range list {
		link := fmt.Sprintf("%s/%d", base, a.RunID)
		desc := a.Text
		if r := []rune(desc); len(r) > 500 {
			desc = string(r[:500]) + "…"
		}
		d.Channel.Items = append(d.Channel.Items, rssItem{Title: a.Subject, Link: link, GUID: link, PubDate: a.SentAt.UTC().Format(time.RFC1123Z), Description: desc})
	}
	out, _ := xml.MarshalIndent(d, "", "  ")
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

// feedPreview reads a feed and shows what an RSS-to-email campaign's next run would send.
func (s *Server) feedPreview(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		URL    string `json:"url"`
		Repeat string `json:"repeat"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := broadcast.ValidateFeed(model.Broadcast{FeedURL: req.URL, Channel: channel.Email}); err != nil || req.URL == "" {
		if err == nil {
			err = errors.New("give the feed's address")
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allowRate(r.Context(), "rlfeed", tenant, r, 1, 30) {
		writeErr(w, http.StatusTooManyRequests, "too many feed checks; try again in a minute")
		return
	}
	f, err := feed.Fetch(r.Context(), safehttp.Client(15*time.Second, s.AllowPrivateWebhooks), req.URL)
	if err != nil {
		writeErr(w, feedFetchStatus(err), err.Error())
		return
	}
	since := time.Time{}
	switch req.Repeat {
	case "daily":
		since = s.now().Add(-24 * time.Hour)
	case "weekly":
		since = s.now().Add(-7 * 24 * time.Hour)
	case "monthly":
		since = s.now().Add(-31 * 24 * time.Hour)
	}
	items := feed.Since(f, since, s.now(), true)
	h, txt := feed.Render(items)
	writeJSON(w, http.StatusOK, map[string]any{"title": f.Title, "total": len(f.Items), "items": items, "html": h, "text": txt})
}

// feedFetchStatus is the answer to a feed that couldn't be read: 400 for an address that isn't
// allowed (a private network), else 502 (the feed's server failed or sent something else).
func feedFetchStatus(err error) int {
	if errors.Is(err, safehttp.ErrBlocked) {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}
