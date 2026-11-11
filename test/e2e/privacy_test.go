//go:build e2e

package e2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// auditActions lists the audit log's actions, newest first.
func auditActions(t *testing.T, c *http.Client) []string {
	var out []string
	for _, e := range asList(jsonCall(t, c, "GET", "/v1/audit", nil, 200)["entries"]) {
		m := e.(map[string]any)
		out = append(out, m["action"].(string)+" "+m["object_id"].(string))
	}
	return out
}

func waitAudit(t *testing.T, c *http.Client, want string) {
	t.Helper()
	if !eventually(10*time.Second, func() bool { return contains(auditActions(t, c), want) }) {
		t.Errorf("audit log lacks %q: %v", want, auditActions(t, c))
	}
}

// TestContactDataExportAndDelete: a privacy request end to end. The contact's data downloads as
// JSON (profile, consent, segments, events), then the contact is deleted with its events; both
// are in the audit log.
func TestContactDataExportAndDelete(t *testing.T) {
	c, _ := workspace(t, "Privacy")
	seg := newSegment(t, c, "Delhi", map[string]any{"attr": "city", "op": "eq", "value": "Delhi"})
	ext := "priv/" + strings.ReplaceAll(t.Name(), "/", "")
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []map[string]any{
		{"external_id": ext, "name": "identify", "email": "priv@example.com", "attrs": map[string]any{"city": "Delhi"}},
		{"external_id": ext, "name": "purchase", "props": map[string]any{"amount": 499}},
		{"external_id": ext, "name": "unsubscribed", "props": map[string]any{"channel": "sms", "source": "api"}},
	}}, 202)
	path := "/v1/contacts/" + url.PathEscape(ext)
	if !eventually(20*time.Second, func() bool {
		return len(contactEvents(t, c, ext)) >= 3 && inSegment(segmentsOf(t, c, ext), seg)
	}) {
		t.Fatalf("contact not ready: events %v, segments %v", contactEvents(t, c, ext), segmentsOf(t, c, ext))
	}

	data := jsonCall(t, c, "GET", path+"/data", nil, 200)
	contact, _ := data["contact"].(map[string]any)
	if contact["email"] != "priv@example.com" || len(asList(data["segments"])) != 1 || len(asList(data["events"])) < 3 {
		t.Errorf("export: %v", data)
	}
	sms := false
	for _, ch := range asList(data["consent"].(map[string]any)["channels"]) {
		m := ch.(map[string]any)
		if m["channel"] == "sms" {
			sms = m["subscribed"] == false
		}
	}
	if !sms {
		t.Errorf("export consent: %v", data["consent"])
	}
	waitAudit(t, c, "GET /v1/contacts/{external_id}/data "+ext)

	jsonCall(t, c, "DELETE", path, nil, 200)
	jsonCall(t, c, "GET", path, nil, 404)
	jsonCall(t, c, "DELETE", path, nil, 404)
	waitAudit(t, c, "DELETE /v1/contacts/{external_id} "+ext)
}

func inSegment(ids []int64, seg int64) bool {
	for _, id := range ids {
		if id == seg {
			return true
		}
	}
	return false
}

// TestRemoveSSO: saved single sign-on settings can be removed; sign-in then offers no SSO.
func TestRemoveSSO(t *testing.T) {
	c, _ := workspace(t, "No SSO")
	me := jsonCall(t, c, "GET", "/v1/auth/me", nil, 200)
	slug := me["workspace"].(map[string]any)["slug"].(string)
	jsonCall(t, c, "PUT", "/v1/settings/sso", map[string]any{"enabled": true, "issuer": "https://idp.example.com/realms/x",
		"client_id": "pulse", "client_secret": "s3cret", "auto_create_users": false, "default_role": "viewer"}, 200)
	if info := jsonCall(t, c, "GET", "/v1/auth/sso?workspace="+slug, nil, 200); info["enabled"] != true {
		t.Fatalf("sso info = %v", info)
	}
	jsonCall(t, c, "DELETE", "/v1/settings/sso", nil, 200)
	got := jsonCall(t, c, "GET", "/v1/settings/sso", nil, 200)
	if got["issuer"] != "" || got["client_id"] != "" || got["enabled"] != false || got["has_client_secret"] != false {
		t.Errorf("after removal: %v", got)
	}
	if info := jsonCall(t, c, "GET", "/v1/auth/sso?workspace="+slug, nil, 200); info["enabled"] == true {
		t.Errorf("sso still on: %v", info)
	}
	waitAudit(t, c, "DELETE /v1/settings/sso ")
}
