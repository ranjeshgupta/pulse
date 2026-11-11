package providers

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"pulse/internal/httpauth"
)

func dialTo(f *fakeSMTP) func(ctx context.Context, network, _ string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, f.addr())
	}
}

func TestGmailXOAUTH2(t *testing.T) {
	var refreshes atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "rt-1" ||
			r.PostForm.Get("client_id") != "cid" || r.PostForm.Get("client_secret") != "cs" || r.PostForm.Get("scope") != gmailScope {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		n := refreshes.Add(1)
		fmt.Fprintf(w, `{"access_token":"ya29.token-%d","expires_in":3599}`, n)
	}))
	defer tokenSrv.Close()
	oldURL, oldHost := googleTokenURL, gmailSMTPHost
	googleTokenURL, gmailSMTPHost = tokenSrv.URL, "mail.test"
	defer func() { googleTokenURL, gmailSMTPHost = oldURL, oldHost }()

	cert, roots := testCert(t)
	f := newFakeSMTP(t)
	f.tls = &tls.Config{Certificates: []tls.Certificate{cert}}
	var seen []string
	f.xoauth = func(ir string) bool {
		seen = append(seen, ir)
		return ir == string(XOAuth2Response("news@acme.test", "ya29.token-1"))
	}
	f.start(t)

	cache := &httpauth.MemoryCache{}
	p, err := New("gworkspace_smtp", Deps{AllowPrivate: true, Tokens: cache, Scope: "1:5"},
		map[string]string{"username": "news@acme.test", "from": "Acme <news@acme.test>", "auth_method": "oauth2", "client_id": "cid"},
		map[string]string{httpauth.SecretClientSecret: "cs", httpauth.SecretRefreshToken: "rt-1"})
	if err != nil {
		t.Fatal(err)
	}
	s := p.(*SMTP)
	if s.Addr != "mail.test:587" {
		t.Errorf("addr %s", s.Addr)
	}
	s.tlsConfig, s.dial = &tls.Config{RootCAs: roots}, dialTo(f)
	for i := range 2 {
		if _, err := s.Send(context.Background(), outbound(i)); err != nil {
			t.Fatalf("send %d: %v (auth %q)", i, err, seen)
		}
	}
	if refreshes.Load() != 1 || len(cache.Keys()) == 0 {
		t.Errorf("token refreshes %d, cache %v", refreshes.Load(), cache.Keys())
	}
	res, err := s.TestAuth(context.Background())
	if err != nil || res.Token == "" || strings.Contains(res.Token, "token") || res.Expires.IsZero() {
		t.Errorf("test auth %+v %v", res, err)
	}

	// A refused refresh token is a permanent failure of the provider's credentials.
	p2, _ := New("gmail_smtp", Deps{AllowPrivate: true}, map[string]string{"username": "news@acme.test", "from": "news@acme.test",
		"auth_method": "oauth2", "client_id": "cid"}, map[string]string{httpauth.SecretClientSecret: "cs", httpauth.SecretRefreshToken: "revoked"})
	if _, err := p2.(*SMTP).TestAuth(context.Background()); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("revoked refresh token: %v", err)
	}
}

func TestGoogleSMTPPresets(t *testing.T) {
	cfg := map[string]string{"username": "me@gmail.com", "from": "me@gmail.com"}
	p, err := New("gmail_smtp", Deps{}, cfg, map[string]string{"password": "abcd efgh ijkl mnop"})
	if err != nil {
		t.Fatal(err)
	}
	if s := p.(*SMTP); s.Addr != "smtp.gmail.com:587" || s.username != "me@gmail.com" {
		t.Errorf("gmail %+v", s)
	}
	cfg["port"] = "465"
	p, _ = New("gmail_smtp", Deps{}, cfg, map[string]string{"password": "x"})
	if p.(*SMTP).Addr != "smtp.gmail.com:465" {
		t.Error("port 465")
	}
	cfg["port"] = "25"
	if _, err := New("gmail_smtp", Deps{}, cfg, map[string]string{"password": "x"}); err == nil {
		t.Error("port 25 accepted for smtp.gmail.com")
	}
	if _, err := New("gmail_smtp", Deps{}, map[string]string{"username": "me@gmail.com", "from": "me@gmail.com"}, nil); err == nil {
		t.Error("no app password accepted")
	}
	for kind, quota := range map[string]int64{"gmail_smtp": 500, "gworkspace_smtp": 2000, "gworkspace_relay": 10000} {
		k, _ := Lookup(kind)
		if k.DefaultLimits == nil || k.DefaultLimits.DailyQuota != quota || !strings.Contains(k.Notes, "relay") {
			t.Errorf("%s: limits %+v notes %q", kind, k.DefaultLimits, k.Notes)
		}
		if ReportsDelivery(kind, nil) {
			t.Errorf("%s reports delivery", kind)
		}
	}
}

func TestGoogleRelayEHLOAndIPAllowlist(t *testing.T) {
	oldHost := gmailRelayHost
	gmailRelayHost = "127.0.0.1"
	defer func() { gmailRelayHost = oldHost }()
	f := newFakeSMTP(t)
	f.start(t)
	p, err := New("gworkspace_relay", Deps{}, map[string]string{"from": "news@acme.test", "helo_domain": "acme.test", "port": "25"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := p.(*SMTP)
	s.dial = dialTo(f)
	if _, err := s.Send(context.Background(), outbound(1)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.helos) == 0 || f.helos[0] != "acme.test" || len(f.msgs) != 1 {
		t.Errorf("helo %v, %d messages", f.helos, len(f.msgs))
	}
	if _, err := New("gworkspace_relay", Deps{}, map[string]string{"from": "a@acme.test", "helo_domain": "acme.test"}, map[string]string{"username": "u"}); err == nil {
		t.Error("relay auth without password accepted")
	}
}
