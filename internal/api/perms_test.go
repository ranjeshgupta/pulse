package api

import (
	"context"
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
		{"DELETE /v1/contacts/{external_id}", viewer, false},
		{"DELETE /v1/contacts/{external_id}", editor, true},
		{"DELETE /v1/contacts/{external_id}", auth.Perms{auth.AreaContacts: auth.View}, false},
		{"GET /v1/contacts/{external_id}/data", viewer, false}, // personal data: contacts edit, like export
		{"GET /v1/contacts/{external_id}/data", auth.Perms{auth.AreaContacts: auth.View}, false},
		{"GET /v1/contacts/{external_id}/data", auth.Perms{auth.AreaContacts: auth.Edit}, true},
		{"POST /v1/something-new", editor, false}, // unlisted: settings edit
		{"GET /v1/inapp/check", auth.Perms{auth.AreaContent: auth.Publish}, false},
		{"GET /v1/inapp/check", viewer, true},
		{"POST /v1/cohorts/run", viewer, true},
		{"POST /v1/reports/run", viewer, true},
		{"POST /v1/revenue/report", viewer, true},
		{"POST /v1/reports", viewer, false},
		{"POST /v1/reports", editor, true},
		{"GET /v1/revenue/settings", viewer, true},
		{"PUT /v1/revenue/settings", editor, false},
		{"GET /v1/audit/export", editor, false},
		{"GET /v1/audit/export", auth.Perms{auth.AreaSettings: auth.Edit}, true},
		{"GET /v1/events/catalog", viewer, true},
		{"GET /v1/events/{name}/summary", viewer, true},
		{"PUT /v1/events/{name}/schema", viewer, false},
		{"PUT /v1/events/{name}/schema", editor, true}, // enforce also needs settings edit (handler)
		{"POST /v1/events/schemas/suggested", viewer, false},
		{"DELETE /v1/events/{name}/schema", editor, true},
		{"GET /v1/contacts/{external_id}/segments/{id}/explain", viewer, true},
		{"GET /v1/contacts/{external_id}/segments/{id}/explain", auth.Perms{auth.AreaSegments: auth.Edit}, false},
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

func TestValueSuggestionsNeedContactsAccess(t *testing.T) {
	segmentsOnly := auth.Perms{auth.AreaSegments: auth.Edit}
	for _, pattern := range []string{"GET /v1/attributes/{key}/values", "GET /v1/events/{name}/values"} {
		method, path, _ := strings.Cut(pattern, " ")
		r, _ := http.NewRequest(method, path, nil)
		r.Pattern = pattern
		n := required(r)
		if segmentsOnly.Allows(n.area, n.level) {
			t.Errorf("%s: segments access alone must not reveal contact values", pattern)
		}
	}
	s := New(Deps{})
	for _, k := range []string{"email", "phone", "first_name", "owner_name"} {
		if !s.personalData(context.Background(), 1, k) {
			t.Errorf("%s should never be suggested", k)
		}
	}
}
