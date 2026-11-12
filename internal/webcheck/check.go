package webcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"pulse/internal/safehttp"
)

// Fetch limits of the install check.
const (
	DefaultMaxBytes = 1 << 20
	DefaultTimeout  = 10 * time.Second
)

// ErrTooLarge: the page or service worker is bigger than the check reads.
var ErrTooLarge = errors.New("larger than 1 MB")

// Fetcher gets pages of a customer's site for the install check. The site address is typed by a
// workspace user, so: https only, no private or internal addresses (checked on every connection,
// redirects included, by safehttp), redirects only within the same site (safehttp.SameOrigin), a
// size cap and a time limit. AllowPrivate (PULSE_ALLOW_PRIVATE_WEBHOOKS, local development) lets
// it reach private addresses and plain http.
type Fetcher struct {
	Client       *http.Client
	AllowPrivate bool
	MaxBytes     int64
}

// NewFetcher builds the production fetcher.
func NewFetcher(allowPrivate bool) *Fetcher {
	return &Fetcher{Client: safehttp.SameOrigin(safehttp.Client(DefaultTimeout, allowPrivate)), AllowPrivate: allowPrivate, MaxBytes: DefaultMaxBytes}
}

// Page is one fetched resource.
type Page struct {
	Status      int
	ContentType string
	Body        []byte
	URL         string // after redirects
}

// Get fetches one URL within the limits.
func (f *Fetcher) Get(ctx context.Context, raw string) (Page, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return Page{}, errors.New("not a valid web address")
	}
	if u.Scheme != "https" && !(f.AllowPrivate && u.Scheme == "http") {
		return Page{}, errors.New("only https addresses are checked")
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", "PulseInstallCheck/1.0")
	req.Header.Set("Accept", "text/html,application/javascript,*/*;q=0.5")
	resp, err := f.Client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if errors.Is(err, safehttp.ErrBlocked) || strings.Contains(err.Error(), safehttp.ErrBlocked.Error()) {
			return Page{}, errors.New("the address is private or internal, which Pulse doesn't connect to")
		}
		return Page{}, err
	}
	defer resp.Body.Close()
	max := f.MaxBytes
	if max <= 0 {
		max = DefaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return Page{}, err
	}
	if int64(len(body)) > max {
		return Page{}, ErrTooLarge
	}
	return Page{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: body, URL: resp.Request.URL.String()}, nil
}

// Item is one line of the report.
type Item struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"` // ok | warn | fail | skip
	Detail string `json:"detail,omitempty"`
}

// Report is the install check's answer.
type Report struct {
	SiteURL          string    `json:"site_url"`
	ServiceWorkerURL string    `json:"service_worker_url"`
	Items            []Item    `json:"items"`
	OK               bool      `json:"ok"`
	CheckedAt        time.Time `json:"checked_at"`
}

// Input is what the check compares the site with.
type Input struct {
	SiteURL       string
	ServiceWorker string // path
	SiteKey       string // the workspace's
	PulseURL      string // Pulse's public URL (where the SDK should load from)
	VAPID         bool   // the workspace has web push keys
}

var (
	snippetRe = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=\s*["']([^"']*/sdk/pulse\.js)(?:\?[^"']*)?["']`)
	siteKeyRe = regexp.MustCompile(`siteKey\s*:\s*["']([A-Za-z0-9_\-]{4,128})["']`)
)

// Check runs every check and reports each.
func (f *Fetcher) Check(ctx context.Context, in Input) Report {
	r := Report{SiteURL: in.SiteURL, CheckedAt: time.Now().UTC()}
	add := func(key, label, status, detail string) {
		r.Items = append(r.Items, Item{Key: key, Label: label, Status: status, Detail: detail})
	}
	u, err := url.Parse(in.SiteURL)
	if err != nil || u.Host == "" {
		add("page", "Your site opens", "fail", "Enter your site's address, like https://www.example.com/")
		return r
	}
	if u.Scheme == "https" {
		add("https", "Your site uses HTTPS", "ok", "")
	} else {
		add("https", "Your site uses HTTPS", "fail", "Browsers only allow notifications on https sites.")
	}
	sw := in.ServiceWorker
	if sw == "" {
		sw = DefaultServiceWorker
	}
	swURL := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: sw}).String()
	r.ServiceWorkerURL = swURL

	page, err := f.Get(ctx, in.SiteURL)
	switch {
	case err != nil:
		add("page", "Your site opens", "fail", "Couldn't open it: "+err.Error())
	case page.Status != http.StatusOK:
		add("page", "Your site opens", "fail", fmt.Sprintf("It answered HTTP %d.", page.Status))
	default:
		add("page", "Your site opens", "ok", "")
	}
	if err == nil && page.Status == http.StatusOK {
		html := string(page.Body)
		m := snippetRe.FindStringSubmatch(html)
		switch {
		case m == nil:
			add("snippet", "The Pulse snippet is on the page", "fail", "No <script src=\".../sdk/pulse.js\"> found. Copy the snippet from Settings → Sign-in & keys.")
		default:
			src, _ := url.Parse(m[1])
			pu, _ := url.Parse(in.PulseURL)
			if src != nil && pu != nil && src.Host != "" && pu.Host != "" && !strings.EqualFold(src.Host, pu.Host) {
				add("snippet", "The Pulse snippet is on the page", "warn", "It loads from "+src.Host+", not "+pu.Host+".")
			} else {
				add("snippet", "The Pulse snippet is on the page", "ok", "")
			}
		}
		keys := siteKeyRe.FindAllStringSubmatch(html, -1)
		found, other := false, ""
		for _, k := range keys {
			if k[1] == in.SiteKey {
				found = true
			} else {
				other = k[1]
			}
		}
		switch {
		case found:
			add("site_key", "The site key is this workspace's", "ok", "")
		case other != "":
			add("site_key", "The site key is this workspace's", "fail", "The page uses another site key ("+mask(other)+").")
		default:
			add("site_key", "The site key is this workspace's", "fail", "No siteKey found in Pulse.init({ siteKey: … }).")
		}
	} else {
		add("snippet", "The Pulse snippet is on the page", "skip", "")
		add("site_key", "The site key is this workspace's", "skip", "")
	}

	w, err := f.Get(ctx, swURL)
	switch {
	case err != nil:
		add("service_worker", "The service worker file is there", "fail", "Couldn't open "+sw+": "+err.Error())
		add("sw_imports", "It loads Pulse's worker", "skip", "")
	case w.Status != http.StatusOK:
		add("service_worker", "The service worker file is there", "fail", fmt.Sprintf("%s answered HTTP %d. Put a file there with one line: importScripts(\"%s/sdk/pulse-sw.js\");", sw, w.Status, strings.TrimRight(in.PulseURL, "/")))
		add("sw_imports", "It loads Pulse's worker", "skip", "")
	default:
		ct := strings.ToLower(w.ContentType)
		if strings.Contains(ct, "javascript") || strings.Contains(ct, "ecmascript") {
			add("service_worker", "The service worker file is there", "ok", "")
		} else {
			add("service_worker", "The service worker file is there", "warn", "It's served as "+or(ct, "unknown")+"; browsers need a JavaScript type.")
		}
		body := string(w.Body)
		if strings.Contains(body, "importScripts") && strings.Contains(body, "/sdk/pulse-sw.js") {
			add("sw_imports", "It loads Pulse's worker", "ok", "")
		} else {
			add("sw_imports", "It loads Pulse's worker", "fail", "Add: importScripts(\""+strings.TrimRight(in.PulseURL, "/")+"/sdk/pulse-sw.js\");")
		}
	}
	if in.VAPID {
		add("vapid", "Web push keys are set", "ok", "")
	} else {
		add("vapid", "Web push keys are set", "fail", "The server has no secret key (PULSE_SECRET_KEY), so it can't create them.")
	}
	r.OK = true
	for _, it := range r.Items {
		if it.Status == "fail" {
			r.OK = false
		}
	}
	return r
}

func mask(k string) string {
	if len(k) <= 8 {
		return k
	}
	return k[:6] + "…"
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
