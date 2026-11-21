//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGatewayStatusRequests (batch O2): a custom SMS gateway without webhooks gets its delivery
// reports by status requests: Pulse sends with {{message.id}} as the client reference, asks the
// status address by it, and records sms_delivered; a 200 answer with "status": "error" counts as
// refused. The setup's dry runs read pasted answers and reports.
func TestGatewayStatusRequests(t *testing.T) {
	var mu sync.Mutex
	asked := 0
	refs := map[string]bool{}
	base := providerMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/send":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if strings.HasSuffix(in["to"], "0000") {
				fmt.Fprint(w, `{"status":"error","reason":"blocked number"}`)
				return
			}
			mu.Lock()
			refs[in["clientref"]] = true
			mu.Unlock()
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/status":
			mu.Lock()
			asked++
			known := refs[r.URL.Query().Get("ref")]
			mu.Unlock()
			state := "SUBMITTED" // an unknown reference stays pending
			if known {
				state = "DELIVRD"
			}
			fmt.Fprintf(w, `{"data":{"state":%q}}`, state)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c, _ := workspace(t, "Gateway polls")
	run := fmt.Sprint(time.Now().UnixNano())
	ext := "gwp-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": "+919811155501"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })

	request, _ := json.Marshal(map[string]any{"method": "POST", "url": base + "/send",
		"body":     `{"to":"{{message.to}}","text":"{{message.text}}","clientref":"{{message.id}}"}`,
		"failure":  map[string]any{"json_path": "status", "values": []string{"error"}},
		"match_by": "pulse",
		"receipt":  map[string]any{"id_path": "data.ref", "status_path": "data.state", "statuses": map[string]string{"DELIVRD": "delivered", "SUBMITTED": "pending"}, "verify": ""},
		"poll":     map[string]any{"method": "GET", "url": base + "/status?ref={{message.id}}", "every_minutes": 1, "for_hours": 1}})
	cfg := map[string]any{"mode": "custom", "request": string(request)}
	draft := map[string]any{"provider": "http_sms", "config": cfg, "secrets": map[string]any{}}

	// Dry runs: a refused answer, and a pasted report.
	a := jsonCall(t, c, "POST", "/v1/channels/providers/check-answer", merge(draft, map[string]any{"status": 200, "body": `{"status":"ERROR"}`}), 200)
	if a["accepted"] != false {
		t.Errorf("check-answer: %v", a)
	}
	rp := jsonCall(t, c, "POST", "/v1/channels/providers/check-report", merge(draft, map[string]any{"body": `{"data":{"ref":"m1","state":"DELIVRD"}}`}), 200)
	if rs, _ := rp["receipts"].([]any); len(rs) != 1 || rs[0].(map[string]any)["status"] != "delivered" {
		t.Errorf("check-report: %v", rp)
	}

	jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "http_sms", "name": "Polled gateway",
		"is_default": true, "config": cfg, "secrets": map[string]any{}}, 200)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "poll sms", "channel": "sms", "content": map[string]any{"text": "Sale today"}}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	took := waitFor(t, 120*time.Second, func() bool { return eventsNamed(t, c, ext, "sms_delivered") == 1 })
	t.Logf("delivered by status requests after %v", took)
	mu.Lock()
	if asked < 1 {
		t.Errorf("status never asked")
	}
	mu.Unlock()
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
