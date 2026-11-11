package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Whether or not the app was built, the handler answers: the app's index page, or how to build it.
func TestHandlerServesAppOrBuildHint(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/segments/4", nil))
	if Built() {
		if rec.Code != http.StatusOK {
			t.Fatalf("built app: status %d", rec.Code)
		}
		return
	}
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Fatalf("unbuilt app: %d %s", rec.Code, rec.Body.String())
	}
}
