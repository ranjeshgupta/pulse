//go:build e2e

// Package e2e drives the running stack (make up) through its public API and checks the
// end-to-end latencies Pulse exists for: event -> segment membership -> journey -> email.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"
)

var (
	apiURL     = env("PULSE_API", "http://localhost:7080")
	mailpitURL = env("PULSE_MAILPIT", "http://localhost:18025")
	adminToken = env("PULSE_ADMIN_TOKEN", "dev-admin-token")
)

const (
	segmentSLO = 2 * time.Second
	emailSLO   = 5 * time.Second
)

func TestRealTimePipeline(t *testing.T) {
	run := fmt.Sprint(time.Now().UnixNano())
	tenant := call(t, "POST", "/v1/tenants", adminToken, map[string]any{"name": "e2e " + run}, 201)
	key := tenant["api_key"].(string)

	tpl := call(t, "POST", "/v1/templates", key, map[string]any{
		"name": "welcome", "subject": "Welcome {{.Attrs.first_name}}", "html": "<p>Hi {{.Attrs.first_name}}</p>",
	}, 201)
	seg := call(t, "POST", "/v1/segments", key, map[string]any{
		"name": "Delhi buyers",
		"rule": map[string]any{"all": []any{
			map[string]any{"attr": "city", "op": "eq", "value": "Delhi"},
			map[string]any{"event": "purchase", "op": "gte", "count": 1, "days": 7},
		}},
	}, 201)
	segID := seg["id"].(float64)
	j := call(t, "POST", "/v1/journeys", key, map[string]any{
		"name": "welcome buyers",
		"graph": map[string]any{
			"trigger": map[string]any{"type": "segment_entered", "segment_id": segID},
			"entry":   "send",
			"nodes": map[string]any{
				"send": map[string]any{"type": "send_email", "template_id": tpl["id"], "next": "done"},
				"done": map[string]any{"type": "exit"},
			},
		},
	}, 201)
	journeyPath := fmt.Sprintf("/v1/journeys/%v", j["id"])
	call(t, "POST", journeyPath+"/activate", key, nil, 200)
	time.Sleep(3 * time.Second) // let the workers' 2s index refresh pick up the segment and journey

	buyer, other := "buyer-"+run, "other-"+run
	email := buyer + "@example.com"
	start := time.Now()
	call(t, "POST", "/v1/events", key, map[string]any{"events": []any{
		map[string]any{"external_id": buyer, "name": "identify", "email": email, "attrs": map[string]any{"city": "Delhi", "first_name": "Asha"}},
		map[string]any{"external_id": buyer, "name": "purchase", "props": map[string]any{"amount": 499}},
		map[string]any{"external_id": other, "name": "identify", "attrs": map[string]any{"city": "Pune"}},
		map[string]any{"external_id": other, "name": "purchase"},
	}}, 202)

	waitFor(t, 10*time.Second, func() bool {
		c := call(t, "GET", "/v1/contacts/"+buyer, key, nil, -1)
		segs, _ := c["segments"].([]any)
		return slices.Contains(segs, any(segID))
	})
	inSegment := time.Since(start)
	t.Logf("event -> segment membership: %v (SLO %v)", inSegment, segmentSLO)

	waitFor(t, 15*time.Second, func() bool { return mailCount(t, email) > 0 })
	delivered := time.Since(start)
	t.Logf("event -> email in inbox:     %v (SLO %v)", delivered, emailSLO)

	if inSegment > segmentSLO {
		t.Errorf("segment latency %v exceeds %v", inSegment, segmentSLO)
	}
	if delivered > emailSLO {
		t.Errorf("email latency %v exceeds %v", delivered, emailSLO)
	}

	// The Pune buyer must not qualify.
	c := call(t, "GET", "/v1/contacts/"+other, key, nil, 200)
	if segs, _ := c["segments"].([]any); slices.Contains(segs, any(segID)) {
		t.Errorf("Pune contact is in the Delhi segment: %v", segs)
	}

	// Redelivery-safe: the journey completed once and sent exactly one email.
	time.Sleep(time.Second)
	if n := mailCount(t, email); n != 1 {
		t.Errorf("%d emails for %s, want 1", n, email)
	}
	stats := call(t, "GET", journeyPath+"/stats", key, nil, 200)
	if got := stats["status"].(map[string]any)["completed"]; got != 1.0 {
		t.Errorf("journey stats %v, want 1 completed", stats)
	}
}

func TestWaitNodeAndBackfill(t *testing.T) {
	run := fmt.Sprint(time.Now().UnixNano())
	tenant := call(t, "POST", "/v1/tenants", adminToken, map[string]any{"name": "e2e wait " + run}, 201)
	key := tenant["api_key"].(string)
	tpl := call(t, "POST", "/v1/templates", key, map[string]any{"name": "nudge", "subject": "Still there?", "html": "<p>Nudge</p>"}, 201)

	// Event-triggered journey with a 3 second durable wait.
	j := call(t, "POST", "/v1/journeys", key, map[string]any{
		"name": "signup nudge",
		"graph": map[string]any{
			"trigger": map[string]any{"type": "event", "event": "signup"},
			"entry":   "wait",
			"nodes": map[string]any{
				"wait": map[string]any{"type": "wait", "seconds": 3, "next": "send"},
				"send": map[string]any{"type": "send_email", "template_id": tpl["id"], "next": "done"},
				"done": map[string]any{"type": "exit"},
			},
		},
	}, 201)
	call(t, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j["id"]), key, nil, 200)
	time.Sleep(3 * time.Second)

	user := "signup-" + run
	email := user + "@example.com"
	call(t, "POST", "/v1/events", key, map[string]any{"external_id": user, "name": "signup", "email": email, "attrs": map[string]any{"city": "Mumbai"}}, 202)

	took := waitFor(t, 20*time.Second, func() bool { return mailCount(t, email) > 0 })
	t.Logf("signup -> nudge after 3s wait: %v", took)
	if took < 3*time.Second {
		t.Errorf("email arrived after %v, before the 3s wait elapsed", took)
	}

	// A segment created after the data exists is filled by the ClickHouse backfill.
	time.Sleep(2 * time.Second) // let the ClickHouse sink catch up
	seg := call(t, "POST", "/v1/segments", key, map[string]any{
		"name": "Mumbai", "rule": map[string]any{"attr": "city", "op": "eq", "value": "Mumbai"},
	}, 201)
	took = waitFor(t, 20*time.Second, func() bool {
		s := call(t, "GET", fmt.Sprintf("/v1/segments/%v", seg["id"]), key, nil, 200)
		return s["status"] == "ready" && s["size"] == 1.0
	})
	t.Logf("backfill of a new segment: %v", took)
}

// ---- helpers ----

func call(t *testing.T, method, path, key string, body any, wantCode int) map[string]any {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, apiURL+path, rd)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if wantCode > 0 && resp.StatusCode != wantCode {
		t.Fatalf("%s %s: %d %s, want %d", method, path, resp.StatusCode, raw, wantCode)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func mailCount(t *testing.T, to string) int {
	t.Helper()
	resp, err := http.Get(mailpitURL + "/api/v1/search?query=" + url.QueryEscape("to:"+to))
	if err != nil {
		t.Fatalf("mailpit: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		MessagesCount int `json:"messages_count"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.MessagesCount
}

// waitFor polls cond every 50ms and returns how long it took to become true.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < timeout {
		if cond() {
			return time.Since(start)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
	return 0
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
