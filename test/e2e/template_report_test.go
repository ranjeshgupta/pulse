//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// TestTemplateReport (batch O4): a template's report counts its sends across campaigns, with the
// provider they went through, a daily series and the campaign split; bad periods are refused.
func TestTemplateReport(t *testing.T) {
	c, _ := workspace(t, "Template report")
	run := fmt.Sprint(time.Now().UnixNano())
	prov := jsonCall(t, c, "POST", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "is_default": true}, 200)["id"]
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "report sms", "channel": "sms", "content": map[string]any{"text": "Sale today"}}, 201)["id"]
	for i := range 2 {
		ext := fmt.Sprintf("tr-%d-%s", i, run)
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": fmt.Sprintf("+9198111%05d", i+11)}}, 202)
		waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
		jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	}
	path := fmt.Sprintf("/v1/templates/%v/report", tpl)
	var rep map[string]any
	waitFor(t, 30*time.Second, func() bool {
		rep = jsonCall(t, c, "GET", path, nil, 200)["report"].(map[string]any)
		return rep["totals"].(map[string]any)["sent"] == 2.0
	})
	byProv, _ := rep["by_provider"].(map[string]any)
	if p, _ := byProv[fmt.Sprint(prov)].(map[string]any); p == nil || p["sent"] != 2.0 {
		t.Errorf("by provider %v (provider %v)", byProv, prov)
	}
	if s, _ := rep["series"].([]any); len(s) == 0 {
		t.Errorf("no series: %v", rep)
	}
	if bc, _ := rep["by_campaign"].([]any); len(bc) != 1 || bc[0].(map[string]any)["source"] != "other" {
		t.Errorf("by campaign %v", rep["by_campaign"])
	}
	hourly := jsonCall(t, c, "GET", path+"?bucket=hour&from="+time.Now().AddDate(0, 0, -1).Format("2006-01-02"), nil, 200)
	if s, _ := hourly["report"].(map[string]any)["series"].([]any); len(s) == 0 || len(s[0].(map[string]any)["t"].(string)) != 16 {
		t.Errorf("hourly series %v", hourly["report"])
	}
	jsonCall(t, c, "GET", path+"?bucket=hour&from=2026-01-01", nil, 400)
	jsonCall(t, c, "GET", path+"?tz=Mars/Base", nil, 400)
}
