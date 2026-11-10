package api

import (
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
