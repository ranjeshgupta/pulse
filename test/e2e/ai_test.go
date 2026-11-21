//go:build e2e

package e2e

import "testing"

// TestAIHelpOff (O6): without PULSE_AI_API_KEY the server says AI help is off and sends nothing
// anywhere. (With a key, the requests and their checks are covered by unit tests with a fake
// model, so the suite never calls a paid API.)
func TestAIHelpOff(t *testing.T) {
	c, _ := workspace(t, "AI")
	if st := jsonCall(t, c, "GET", "/v1/ai/status", nil, 200); st["enabled"] != false {
		t.Skip("this server has an AI key: skipping the off-mode check")
	}
	jsonCall(t, c, "POST", "/v1/ai/rewrite", map[string]any{"text": "Hi", "style": "shorter"}, 503)
	jsonCall(t, c, "POST", "/v1/ai/segment", map[string]any{"description": "buyers"}, 503)
}
