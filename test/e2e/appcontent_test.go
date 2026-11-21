//go:build e2e

package e2e

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// TestAppContentSlots (batch O3): an app content slot answers the app SDKs' /v1/web/content with
// the default card for everyone and a segment's variant for its members; websites never get it.
func TestAppContentSlots(t *testing.T) {
	c, siteKey := workspace(t, "AppSlots")
	run := fmt.Sprint(time.Now().UnixNano())
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Pune", "rule": map[string]any{"attr": "city", "op": "eq", "value": "Pune"}}, 201)["id"])
	slot := map[string]any{
		"slot": "home_banner", "name": "Home banner", "platform": "app", "status": "active", "start_at": time.Now().Add(-time.Minute).Format(time.RFC3339),
		"default_card": map[string]any{"title": "Welcome", "deep_link": "myapp://home"},
		"variants": []any{map[string]any{"id": "pune", "audience": map[string]any{"segment_ids": []any{seg}},
			"card": map[string]any{"title": "Hello Pune", "button_text": "See offers", "deep_link": "myapp://pune", "data": map[string]any{"theme": "dark"}}}},
	}
	bad := map[string]any{}
	for k, v := range slot {
		bad[k] = v
	}
	bad["default_card"] = map[string]any{"deep_link": "javascript://alert(1)"}
	jsonCall(t, c, "POST", "/v1/dwc", bad, 400)
	jsonCall(t, c, "POST", "/v1/dwc", slot, 200)

	ext := "anon-app-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"city": "Pune"}}, 202)
	time.Sleep(3 * time.Second) // segment index refresh, the onsite cache

	ask := func(id string) map[string]any {
		code, got := webCall(t, "POST", "/v1/web/content", siteKey, map[string]any{"id": id, "platform": "android", "slots": []any{"home_banner", "missing"}})
		if code != 200 {
			t.Fatalf("content: %d %v", code, got)
		}
		cs, _ := got["content"].(map[string]any)
		return cs
	}
	if cs := ask("anon-nobody-" + run); len(cs) != 1 || cs["home_banner"].(map[string]any)["card"].(map[string]any)["title"] != "Welcome" {
		t.Errorf("anonymous content = %v", cs)
	}
	waitFor(t, 15*time.Second, func() bool {
		hb, _ := ask(ext)["home_banner"].(map[string]any)
		return hb != nil && hb["variant"] == "pune"
	})
	hb := ask(ext)["home_banner"].(map[string]any)
	if card := hb["card"].(map[string]any); card["title"] != "Hello Pune" || card["data"].(map[string]any)["theme"] != "dark" {
		t.Errorf("member content = %v", hb)
	}
	if code, _ := webCall(t, "POST", "/v1/web/content", siteKey, map[string]any{"id": ext, "platform": "web", "slots": []any{"home_banner"}}); code != 400 {
		t.Errorf("platform web: %d", code)
	}
	// The website's decide API never serves an app slot.
	_, d := webCall(t, "POST", "/v1/web/decide", siteKey, map[string]any{"url": "https://shop.test/", "slots": []any{"home_banner"}})
	if content, _ := d["content"].(map[string]any); len(content) != 0 {
		t.Errorf("web decide served the app slot: %v", d)
	}
	list := jsonCall(t, c, "GET", "/v1/dwc", nil, 200)["slots"].([]any)
	if !slices.ContainsFunc(list, func(x any) bool { return x.(map[string]any)["platform"] == "app" }) {
		t.Errorf("slot list lost the platform: %v", list)
	}
}
