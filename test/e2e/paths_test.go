//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPathsAndUninstalls (O6): path analysis counts what people did after (and before) an event,
// leaving Pulse's own events out and collapsing repeats; an app token the push service rejects
// records app_uninstalled on its contact.
func TestPathsAndUninstalls(t *testing.T) {
	c, siteKey := workspace(t, "Paths")
	run := fmt.Sprint(time.Now().UnixNano())
	now := time.Now().UTC()
	var evs []any
	seq := func(ext string, names ...string) {
		for i, n := range names {
			evs = append(evs, map[string]any{"external_id": ext, "name": n, "ts": now.Add(time.Duration(i-len(names)) * time.Minute).Format(time.RFC3339)})
		}
	}
	for i := range 3 {
		seq(fmt.Sprintf("a%d-%s", i, run), "signup", "browse", "browse", "purchase")
	}
	seq("b-"+run, "signup", "browse", "logout")
	seq("c-"+run, "signup", "tag_added", "support")
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)

	var got map[string]any
	waitFor(t, 20*time.Second, func() bool {
		got = jsonCall(t, c, "POST", "/v1/analytics/paths", map[string]any{"start": "signup", "steps": 3, "days": 7}, 200)
		return got["people"] == float64(5)
	})
	paths := map[string]float64{}
	for _, p := range asList(got["paths"]) {
		pm := p.(map[string]any)
		var steps []string
		for _, s := range asList(pm["steps"]) {
			steps = append(steps, s.(string))
		}
		paths[strings.Join(steps, ">")] = pm["people"].(float64)
	}
	if paths["signup>browse>purchase"] != 3 || paths["signup>browse>logout"] != 1 || paths["signup>support"] != 1 {
		t.Errorf("paths after signup: %v", paths)
	}
	before := jsonCall(t, c, "POST", "/v1/analytics/paths", map[string]any{"end": "purchase", "steps": 2, "days": 7}, 200)
	if p := asList(before["paths"]); len(p) != 1 || fmt.Sprint(p[0].(map[string]any)["steps"]) != "[signup browse purchase]" {
		t.Errorf("paths before purchase: %v", p)
	}
	jsonCall(t, c, "POST", "/v1/analytics/paths", map[string]any{"start": "a", "end": "b"}, 400)
	// The anchor is never left out, even when it's on the leave-out list.
	if got := jsonCall(t, c, "POST", "/v1/analytics/paths", map[string]any{"start": "browse", "steps": 2, "days": 7, "exclude": []string{"browse"}}, 200); got["people"] != float64(4) || len(asList(got["paths"])) == 0 {
		t.Errorf("anchor on the leave-out list: %v", got)
	}

	// Uninstall: the app's token is rejected as unregistered.
	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "push", "provider": "sandbox_push", "is_default": true,
		"config": map[string]any{"unreachable_suffix": "-gone"}}, 200)
	ext := "app-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify"}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	b, _ := json.Marshal(map[string]any{"external_id": ext, "token": "tok-" + run + "-gone", "platform": "android"})
	req, _ := http.NewRequest("POST", apiURL+"/v1/web/devices", strings.NewReader(string(b)))
	req.Header.Set("X-Pulse-Site-Key", siteKey)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
		t.Fatalf("register device: %v", err)
	}
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "p", "channel": "push", "content": map[string]any{"title": "Hi", "body": "Back?"}}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	waitFor(t, 15*time.Second, func() bool { return slices.Contains(contactEvents(t, c, ext), "app_uninstalled") })
}
