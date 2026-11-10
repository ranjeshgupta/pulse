package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7")
	if ip := clientIP(r, false); ip != "203.0.113.9" {
		t.Errorf("untrusted: %s, want the connection's address (the header is client-controlled)", ip)
	}
	if ip := clientIP(r, true); ip != "198.51.100.7" {
		t.Errorf("trusted proxy: %s, want the right-most entry", ip)
	}
}

func TestSplitPages(t *testing.T) {
	s := New(Deps{PublicURL: "https://app.example.com", PagesURL: "https://pages.example.com"})
	h := s.splitPages(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	for _, tc := range []struct {
		host, path string
		code       int
		loc        string
	}{
		{"pages.example.com", "/p/acme/sale", 200, ""},
		{"pages.example.com", "/a/k1/download", 200, ""},
		{"pages.example.com", "/sdk/pulse.js", 200, ""},
		{"pages.example.com", "/v1/web/forms/f1", 200, ""},
		{"pages.example.com", "/login", 404, ""},
		{"pages.example.com", "/v1/auth/login", 404, ""},
		{"app.example.com", "/p/acme/sale?utm_source=x", 308, "https://pages.example.com/p/acme/sale?utm_source=x"},
		{"app.example.com", "/f/fkey", 308, "https://pages.example.com/f/fkey"},
		{"app.example.com", "/v1/segments", 200, ""},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code || w.Header().Get("Location") != tc.loc {
			t.Errorf("%s%s: %d %q, want %d %q", tc.host, tc.path, w.Code, w.Header().Get("Location"), tc.code, tc.loc)
		}
	}
}
