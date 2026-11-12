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
		// M3 campaigns
		{"POST /v1/segments/estimate", viewer, true},
		{"POST /v1/segments/estimate", auth.Perms{auth.AreaBroadcasts: auth.Publish}, false},
		{"GET /v1/segments/{id}/reach", viewer, true},
		{"POST /v1/segments/{id}/duplicate", viewer, false},
		{"POST /v1/segments/{id}/duplicate", auth.Perms{auth.AreaSegments: auth.Edit}, true},
		{"POST /v1/broadcasts/{id}/duplicate", auth.Perms{auth.AreaBroadcasts: auth.Edit}, true},
		{"POST /v1/broadcasts/{id}/duplicate", auth.Perms{auth.AreaBroadcasts: auth.View}, false},
		{"PUT /v1/broadcasts/{id}/tags", auth.Perms{auth.AreaBroadcasts: auth.Edit}, true},
		{"PUT /v1/broadcasts/{id}/tags", viewer, false},
		{"POST /v1/journeys/{id}/duplicate", auth.Perms{auth.AreaJourneys: auth.Edit}, true},
		{"POST /v1/journeys/{id}/duplicate", auth.Perms{auth.AreaJourneys: auth.View}, false},
		{"PUT /v1/journeys/{id}/tags", auth.Perms{auth.AreaJourneys: auth.Edit}, true},
		{"POST /v1/journey-steps/check", auth.Perms{auth.AreaJourneys: auth.Edit}, true},
		{"POST /v1/journey-steps/check", viewer, false},
		{"POST /v1/templates/{id}/duplicate", auth.Perms{auth.AreaContent: auth.Edit}, true},
		{"POST /v1/templates/{id}/duplicate", viewer, false},
		{"PUT /v1/channels/providers/{id}/limits", auth.Perms{auth.AreaChannels: auth.Edit}, true},
		{"PUT /v1/channels/providers/{id}/limits", auth.Perms{auth.AreaSettings: auth.Edit, auth.AreaChannels: auth.View}, false},
		// M6
		{"POST /v1/templates/amp/check", viewer, true},
		{"POST /v1/templates/amp/check", auth.Perms{auth.AreaChannels: auth.Edit}, false},
		{"GET /v1/templates/{id}/amp/results", viewer, true},
		{"PUT /v1/templates/{id}", viewer, false},
		{"PUT /v1/templates/{id}", auth.Perms{auth.AreaContent: auth.Edit}, true},
		{"POST /v1/dwc/overlap", viewer, true},
		{"POST /v1/dwc/overlap", auth.Perms{auth.AreaSegments: auth.Edit}, false},
		{"POST /v1/focus-items/overlap", auth.Perms{auth.AreaContent: auth.View}, true},
		{"PUT /v1/focus-items/order", auth.Perms{auth.AreaContent: auth.View}, false},
		{"PUT /v1/focus-items/order", auth.Perms{auth.AreaContent: auth.Edit}, true},
		{"PUT /v1/dwc/{id}", auth.Perms{auth.AreaContent: auth.Edit}, true},
		{"GET /v1/settings/web-push", auth.Perms{auth.AreaChannels: auth.View}, true},
		{"GET /v1/settings/web-push", auth.Perms{auth.AreaSettings: auth.Edit}, false},
		{"PUT /v1/settings/web-push", auth.Perms{auth.AreaChannels: auth.View}, false},
		{"PUT /v1/settings/web-push", auth.Perms{auth.AreaChannels: auth.Edit}, true},
		{"POST /v1/settings/web-push/check", auth.Perms{auth.AreaChannels: auth.View}, false},
		{"POST /v1/settings/web-push/check", auth.Perms{auth.AreaChannels: auth.Edit}, true},
		{"POST /v1/settings/web-push/check-token", auth.Perms{auth.AreaChannels: auth.Edit}, true},
		{"GET /v1/settings/web-push/diagnosis", auth.Perms{auth.AreaChannels: auth.View}, true},
		{"GET /v1/test-contacts", viewer, true},
		{"POST /v1/test-contacts", viewer, false},
		{"POST /v1/test-contacts", auth.Perms{auth.AreaContent: auth.Edit}, true},
		{"DELETE /v1/test-contacts/{id}", auth.Perms{auth.AreaContent: auth.Edit}, true},
		{"GET /v1/campaigns", auth.Perms{}, true}, // the handler shows only what the role may view
		// M4 delivery and dashboards
		{"GET /v1/analytics/users", viewer, true},
		{"GET /v1/analytics/users", auth.Perms{auth.AreaReports: auth.View}, true},
		{"GET /v1/analytics/campaigns", auth.Perms{auth.AreaBroadcasts: auth.Publish, auth.AreaJourneys: auth.Publish}, false},
		{"GET /v1/analytics/campaigns", auth.Perms{auth.AreaReports: auth.View}, true},
		{"GET /v1/analytics/products", auth.Perms{auth.AreaContacts: auth.Edit}, false},
		{"GET /v1/analytics/products", auth.Perms{auth.AreaReports: auth.View}, true},
		{"GET /v1/live/events", auth.Perms{auth.AreaReports: auth.Edit}, false}, // people's events: contacts access
		{"GET /v1/live/events", auth.Perms{auth.AreaContacts: auth.View}, true},
		{"GET /v1/contacts/{external_id}/profile", auth.Perms{auth.AreaContacts: auth.View}, true},
		{"GET /v1/contacts/{external_id}/profile", auth.Perms{auth.AreaReports: auth.Edit}, false},
		{"GET /v1/contacts/{external_id}/messages", auth.Perms{auth.AreaContacts: auth.View}, true},
		{"GET /v1/contacts/{external_id}/messages", auth.Perms{auth.AreaBroadcasts: auth.Edit}, false},
		{"PUT /v1/settings/bounces/signing", editor, false},
		{"PUT /v1/settings/bounces/signing", auth.Perms{auth.AreaSettings: auth.Edit}, true},
		// M5 automation & limits
		{"GET /v1/settings/automation", editor, true}, // settings view
		{"GET /v1/settings/automation", viewer, false},
		{"PUT /v1/settings/automation", editor, false},
		{"PUT /v1/settings/automation", auth.Perms{auth.AreaSettings: auth.Edit}, true},
		{"POST /v1/settings/automation/preview", editor, false},
		{"POST /v1/settings/automation/preview", auth.Perms{auth.AreaSettings: auth.Edit}, true},
		{"POST /v1/settings/automation/run-retention", editor, false},
		{"POST /v1/settings/automation/run-retention", auth.Perms{auth.AreaContacts: auth.Edit, auth.AreaSettings: auth.View}, false},
		{"POST /v1/settings/automation/run-retention", auth.Perms{auth.AreaSettings: auth.Edit}, true},
		// M5 provider tools (handlers also need settings edit: s.admin)
		{"POST /v1/channels/providers/check-auth", viewer, false},
		{"POST /v1/channels/providers/check-auth", auth.Perms{auth.AreaChannels: auth.Edit}, true},
		{"POST /v1/channels/providers/preview", auth.Perms{auth.AreaChannels: auth.View, auth.AreaSettings: auth.Edit}, false},
		{"POST /v1/channels/providers/preview", auth.Perms{auth.AreaChannels: auth.Edit}, true},
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
