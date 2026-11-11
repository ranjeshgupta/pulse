//go:build e2e

package e2e

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// viewIDs returns the external IDs a Contacts view lists, and its whole first-page response.
func viewIDs(t *testing.T, c *http.Client, q string) ([]string, map[string]any) {
	t.Helper()
	out := jsonCall(t, c, "GET", "/v1/contacts?limit=200&q="+url.QueryEscape(q), nil, 200)
	var ids []string
	for _, v := range asList(out["contacts"]) {
		ids = append(ids, v.(map[string]any)["external_id"].(string))
	}
	slices.Sort(ids)
	return ids, out
}

// TestContactViews: the Contacts list's filters (is:, segment:/list:, tag:, points:, owner:, name:,
// free text) are compiled into segment rules and run in ClickHouse; "Save as segment" stores the
// same rule; the CSV export follows the view and its columns.
func TestContactViews(t *testing.T) {
	c, _ := workspace(t, "Views")
	run := fmt.Sprint(time.Now().UnixNano())
	vip, plain, anon := "vip-"+run, "plain-"+run, "anon-"+run
	me := idOf(jsonCall(t, c, "GET", "/v1/auth/me", nil, 200)["user"].(map[string]any)["id"])
	guests := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Gala guests"}, 201)["id"])

	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": vip, "name": "identify", "email": vip + "@example.com", "attrs": map[string]any{"name": "Asha Rao", "city": "=Pune"}},
		map[string]any{"external_id": plain, "name": "identify", "email": plain + "@example.com", "attrs": map[string]any{"name": "Ravi"}},
		map[string]any{"external_id": anon, "name": "page_view", "props": map[string]any{"url": "https://example.com/"}},
	}}, 202)
	// The profile service creates the contacts a moment after the events are accepted.
	waitFor(t, 15*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+vip, nil, -1)["contact"] != nil })
	jsonCall(t, c, "POST", "/v1/contacts/"+vip+"/tags", map[string]any{"add": []string{"vip"}}, 202)
	jsonCall(t, c, "POST", "/v1/contacts/"+vip+"/points", map[string]any{"delta": 60}, 202)
	jsonCall(t, c, "PUT", "/v1/contacts/"+vip+"/owner", map[string]any{"user_id": me}, 202)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/lists/%d/members", guests), map[string]any{"external_ids": []string{vip}, "action": "add"}, 202)

	// ClickHouse gets profile changes a moment after Postgres; wait for the last of them.
	waitFor(t, 15*time.Second, func() bool {
		ids, _ := viewIDs(t, c, fmt.Sprintf("tag:vip points:>50 owner:me segment:%d", guests))
		return slices.Equal(ids, []string{vip})
	})

	for q, want := range map[string][]string{
		"is:anonymous":           {anon},
		"is:identified":          {plain, vip},
		"tag:vip":                {vip},
		"points:>50":             {vip},
		"points:<10":             {anon, plain}, // no points counts as 0
		`list:"gala guests"`:     {vip},
		"owner:none":             {anon, plain},
		"name:asha":              {vip}, // tried as typed, lower case and capitalised
		"ASHA":                   {vip}, // free text ignores case
		"is:identified " + plain: {plain},
		"tag:vip is:anonymous":   nil,
	} {
		ids, out := viewIDs(t, c, q)
		if !slices.Equal(ids, want) {
			t.Errorf("%q lists %v, want %v", q, ids, want)
		}
		if n := out["count"]; n != float64(len(want)) {
			t.Errorf("%q counts %v, want %d", q, n, len(want))
		}
	}
	_, out := viewIDs(t, c, fmt.Sprintf("segment:%d", guests))
	if f := asList(out["filters"]); len(f) != 1 || f[0].(map[string]any)["label"] != "Static segment: Gala guests" {
		t.Errorf("filters = %v", out["filters"])
	}
	jsonCall(t, c, "GET", "/v1/contacts?q=is:sleeping", nil, 400)
	jsonCall(t, c, "GET", "/v1/contacts?q=segment:Nobody", nil, 400)

	// Save as segment: the view's rule becomes a dynamic segment with the same members.
	_, out = viewIDs(t, c, "tag:vip points:>50")
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Saved VIPs", "rule": out["rule"]}, 201)["id"])
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, vip), seg) })
	if slices.Contains(segmentsOf(t, c, plain), seg) {
		t.Error("the saved segment took in a contact the view didn't list")
	}
	ids, _ := viewIDs(t, c, fmt.Sprintf("segment:%d", seg))
	if !slices.Equal(ids, []string{vip}) {
		t.Errorf("segment:%d lists %v", seg, ids)
	}

	// The export follows the view and the chosen columns, with formula-like cells defused.
	req, _ := http.NewRequest("GET", apiURL+"/v1/contacts/export?q=tag:vip&columns=city,points", nil)
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("export: %v %v", resp, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(rows) != 2 || strings.Join(rows[0], ",") != "external_id,email,created_at,updated_at,city,points" {
		t.Fatalf("export = %q %v", raw, err)
	}
	if rows[1][0] != vip || rows[1][4] != "'=Pune" || rows[1][5] != "60" {
		t.Errorf("export row = %v", rows[1])
	}
}
