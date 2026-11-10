package api

import (
	"net/http"
	"strings"
	"testing"

	"pulse/internal/auth"
)

// Every signed-in route resolves to an area, and the built-in roles get what they should.
func TestRoutePermissions(t *testing.T) {
	s := New(Deps{})
	h := s.Handler()
	_ = h
	viewer, _ := auth.BuiltinPerms(auth.RoleViewer)
	editor, _ := auth.BuiltinPerms(auth.RoleEditor)
	for _, tc := range []struct {
		pattern string
		perms   auth.Perms
		want    bool
	}{
		{"GET /v1/segments", viewer, true},
		{"POST /v1/segments", viewer, false},
		{"POST /v1/segments/preview", viewer, true},
		{"POST /v1/journeys/{id}/{action}", editor, true},
		{"POST /v1/journeys/{id}/{action}", viewer, false},
		{"GET /v1/settings/identity", editor, false},
		{"GET /v1/settings/delivery", editor, true},
		{"GET /v1/settings/delivery", viewer, false},
		{"POST /v1/channels/providers", editor, false},
		{"GET /v1/contacts/export", viewer, false},
		{"POST /v1/something-new", editor, false}, // unlisted: settings edit
	} {
		method, path, _ := strings.Cut(tc.pattern, " ")
		r, _ := http.NewRequest(method, path, nil)
		r.Pattern = tc.pattern
		n := required(r)
		if got := tc.perms.Allows(n.area, n.level); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v (needs %s %v)", tc.pattern, got, tc.want, n.area, n.level)
		}
	}
}

func TestRedact(t *testing.T) {
	out := string(redactJSON([]byte(`{"name":"x","password":"p","config":{"client_secret":"s","url":"u"},"secrets":{"auth_token":"t"}}`)))
	for _, leak := range []string{`"p"`, `"s"`, `"t"`} {
		if strings.Contains(out, leak) {
			t.Errorf("secret %s leaked: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"url":"u"`) {
		t.Errorf("non-secret dropped: %s", out)
	}
}
