package safehttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSameOriginRefusesOtherSites(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = r.Header.Get("X-Api-Key") }))
	defer other.Close()
	var hits int
	own := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL+"/x", http.StatusTemporaryRedirect)
		case "/here":
			http.Redirect(w, r, "/done", http.StatusTemporaryRedirect)
		}
	}))
	defer own.Close()
	c := SameOrigin(&http.Client{})
	req, _ := http.NewRequest("POST", own.URL+"/away", strings.NewReader("password=x"))
	req.Header.Set("X-Api-Key", "secret")
	if _, err := c.Do(req); err == nil || leaked != "" {
		t.Errorf("cross-site redirect followed: err=%v leaked=%q", err, leaked)
	}
	resp, err := c.Get(own.URL + "/here")
	if err != nil || resp.StatusCode != 200 || hits != 3 {
		t.Errorf("same-site redirect: %v %v hits=%d", err, resp, hits)
	}
}
