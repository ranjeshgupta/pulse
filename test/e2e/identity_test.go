//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestIdentityKeys (batch O1): an external id that is an email fills the email; with email and phone
// as merging keys, a new id carrying a known email joins that contact, and a known contact that
// gets another contact's phone takes it in; a key added later is filled in for existing contacts
// and lists its duplicates.
func TestIdentityKeys(t *testing.T) {
	c, _ := workspace(t, "Identity")
	run := fmt.Sprint(time.Now().UnixNano())
	send := func(ev map[string]any) { jsonCall(t, c, "POST", "/v1/events", ev, 202) }

	// Before any key is set: an email-shaped id fills the email.
	mail := "asha" + run + "@example.com"
	send(map[string]any{"external_id": mail, "name": "signup"})
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, mail)["email"] == mail })

	// A user name exists on two contacts before it becomes a key.
	send(map[string]any{"external_id": "u1-" + run, "name": "identify", "attrs": map[string]any{"username": "Dup" + run}})
	send(map[string]any{"external_id": "u2-" + run, "name": "identify", "attrs": map[string]any{"username": "dup" + run}})
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, "u2-"+run)["username"] != nil })

	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"identity": map[string]any{"keys": []any{
		map[string]any{"field": "email", "mode": "merge"}}}}, 400) // the external id must stay
	jsonCall(t, c, "PUT", "/v1/settings/automation", map[string]any{"identity": map[string]any{"keys": []any{
		map[string]any{"field": "external_id"}, map[string]any{"field": "email", "mode": "merge"},
		map[string]any{"field": "phone", "mode": "merge"}, map[string]any{"field": "username", "mode": "suggest"}}}}, 200)

	// The existing email is filled in by the backfill (it starts ~40 s after the change).
	got := jsonCall(t, c, "GET", "/v1/settings/automation", nil, 200)
	if bs, _ := got["identity_backfills"].([]any); len(bs) != 3 {
		t.Fatalf("backfills = %v", got["identity_backfills"])
	}
	took := waitFor(t, 90*time.Second, func() bool {
		bs, _ := jsonCall(t, c, "GET", "/v1/settings/automation", nil, 200)["identity_backfills"].([]any)
		for _, b := range bs {
			if b.(map[string]any)["done_at"] == nil {
				return false
			}
		}
		return true
	})
	t.Logf("backfill finished in %v", took)

	// The user name only suggests: both contacts show under Duplicates, neither merged.
	d := jsonCall(t, c, "GET", "/v1/contacts/duplicates?by=username", nil, 200)
	if gs, _ := d["groups"].([]any); len(gs) != 1 || gs[0].(map[string]any)["value"] != "dup"+run {
		t.Errorf("username duplicates = %v", d)
	}
	jsonCall(t, c, "GET", "/v1/contacts/duplicates?by=city", nil, 400)

	// A new id with the known email (written differently) joins the email contact.
	app := "app-" + run
	send(map[string]any{"external_id": app, "name": "login", "email": "ASHA" + run + "@Example.com", "attrs": map[string]any{"city": "Pune"}})
	waitFor(t, 20*time.Second, func() bool { return attrsOf(t, c, mail)["city"] == "Pune" })
	if a := attrsOf(t, c, app); a["merged_into"] != mail {
		t.Errorf("%s = %v, want merged into %s", app, a, mail)
	}
	// Its later events follow.
	send(map[string]any{"external_id": app, "name": "identify", "attrs": map[string]any{"plan": "pro"}})
	waitFor(t, 20*time.Second, func() bool { return attrsOf(t, c, mail)["plan"] == "pro" })

	// A known contact that gets another contact's phone takes that contact in.
	other := "crm-" + run
	send(map[string]any{"external_id": other, "name": "identify", "attrs": map[string]any{"phone": "+91 98765 " + run[len(run)-5:], "tier": "gold"}})
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, other)["tier"] == "gold" })
	send(map[string]any{"external_id": mail, "name": "identify", "attrs": map[string]any{"phone": "+9198765" + run[len(run)-5:]}})
	waitFor(t, 20*time.Second, func() bool { return attrsOf(t, c, other)["merged_into"] == mail })
	if a := attrsOf(t, c, mail); a["tier"] != "gold" {
		t.Errorf("winner = %v, want the loser's tier", a)
	}
}
