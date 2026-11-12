package webcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func site(t *testing.T, page, sw string, swType string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(page))
		case "/pulse-sw.js":
			if sw == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", swType)
			w.Write([]byte(sw))
		case "/big":
			w.Write([]byte(strings.Repeat("x", DefaultMaxBytes+10)))
		case "/away":
			http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const goodPage = `<html><body><script src="https://pulse.example/sdk/pulse.js" defer onload="Pulse.init({ siteKey: 'sk_live_1234' })"></script></body></html>`

func status(r Report) map[string]string {
	out := map[string]string{}
	for _, it := range r.Items {
		out[it.Key] = it.Status
	}
	return out
}

func TestCheckReportsEachStep(t *testing.T) {
	srv := site(t, goodPage, `importScripts("https://pulse.example/sdk/pulse-sw.js");`, "text/javascript")
	f := NewFetcher(true) // the test server is on 127.0.0.1
	r := f.Check(context.Background(), Input{SiteURL: srv.URL + "/", SiteKey: "sk_live_1234", PulseURL: "https://pulse.example", VAPID: true})
	got := status(r)
	want := map[string]string{"https": "fail", "page": "ok", "snippet": "ok", "site_key": "ok", "service_worker": "ok", "sw_imports": "ok", "vapid": "ok"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s (%+v)", k, got[k], v, r.Items)
		}
	}
	if r.OK { // plain http fails the https check
		t.Error("ok over http")
	}

	srv = site(t, strings.Replace(goodPage, "sk_live_1234", "sk_other_999", 1), `self.addEventListener("push", () => {})`, "text/html")
	r = f.Check(context.Background(), Input{SiteURL: srv.URL, SiteKey: "sk_live_1234", PulseURL: "https://elsewhere.example"})
	got = status(r)
	for k, v := range map[string]string{"snippet": "warn", "site_key": "fail", "service_worker": "warn", "sw_imports": "fail", "vapid": "fail"} {
		if got[k] != v {
			t.Errorf("wrong site: %s = %s, want %s", k, got[k], v)
		}
	}
	srv = site(t, "<html>nothing</html>", "", "")
	got = status(f.Check(context.Background(), Input{SiteURL: srv.URL, SiteKey: "k"}))
	if got["snippet"] != "fail" || got["service_worker"] != "fail" || got["sw_imports"] != "skip" {
		t.Errorf("empty site: %v", got)
	}
}

// The fetcher never reaches private addresses (without PULSE_ALLOW_PRIVATE_WEBHOOKS), refuses
// plain http, follows redirects only within the site, and caps size.
func TestFetcherSafety(t *testing.T) {
	srv := site(t, goodPage, "", "")
	ctx := context.Background()
	strict := NewFetcher(false)
	if _, err := strict.Get(ctx, srv.URL); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http fetched: %v", err)
	}
	if _, err := strict.Get(ctx, strings.Replace(srv.URL, "http://", "https://", 1)); err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("loopback fetched: %v", err)
	}
	for _, target := range []string{"https://169.254.169.254/latest/meta-data/", "https://10.0.0.5/"} {
		if _, err := strict.Get(ctx, target); err == nil || !strings.Contains(err.Error(), "private") {
			t.Errorf("%s fetched: %v", target, err)
		}
	}
	dev := NewFetcher(true)
	// Redirects to another host (here a private address and a hostname for the same server) are refused.
	for _, to := range []string{"http://10.0.0.5/", strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/"} {
		if _, err := dev.Get(ctx, srv.URL+"/away?to="+to); err == nil || !strings.Contains(err.Error(), "another site") {
			t.Errorf("redirect to %s: %v", to, err)
		}
	}
	// Within the site it follows.
	if p, err := dev.Get(ctx, srv.URL+"/away?to=/"); err != nil || !strings.Contains(string(p.Body), "sk_live") {
		t.Errorf("same-site redirect: %v", err)
	}
	if _, err := dev.Get(ctx, srv.URL+"/big"); err != ErrTooLarge {
		t.Errorf("big page: %v", err)
	}
	if _, err := dev.Get(ctx, "https://user:pw@example.com/"); err == nil {
		t.Error("credentials in the URL accepted")
	}
}

func TestPromptSettingsValidate(t *testing.T) {
	ok := Defaults()
	ok.Prompt.Enabled = true
	ok.SiteURL = "https://www.example.com/"
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*Settings){
		"style":     func(s *Settings) { s.Prompt.Style = "toast" },
		"position":  func(s *Settings) { s.Prompt.Position = "middle" },
		"no text":   func(s *Settings) { s.Prompt.Text = " " },
		"colour":    func(s *Settings) { s.Prompt.Button = "red" },
		"delay":     func(s *Settings) { s.Prompt.DelaySeconds = 601 },
		"scroll":    func(s *Settings) { s.Prompt.ScrollPercent = 101 },
		"pages":     func(s *Settings) { s.Prompt.PageViews = -1 },
		"repeat":    func(s *Settings) { s.Prompt.RepeatDays = 400 },
		"page rule": func(s *Settings) { s.Prompt.HideOn = []string{""} },
		"site":      func(s *Settings) { s.SiteURL = "ftp://x" },
		"site user": func(s *Settings) { s.SiteURL = "https://a:b@x.com" },
		"sw path":   func(s *Settings) { s.ServiceWorker = "/../etc/x.js" },
		"sw abs":    func(s *Settings) { s.ServiceWorker = "https://x.com/sw.js" },
		"long text": func(s *Settings) { s.Prompt.Text = strings.Repeat("x", 301) },
	} {
		s := ok
		mod(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	s, err := Load([]byte(`{"prompt":{"enabled":true,"style":"bar","text":"x","allow":"Yes"}}`))
	if err != nil || s.ServiceWorker != DefaultServiceWorker || s.Prompt.Style != "bar" || s.Prompt.RepeatDays != 7 {
		t.Errorf("load: %+v %v", s, err)
	}
}
