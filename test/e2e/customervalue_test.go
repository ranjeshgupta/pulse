//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

// TestCustomerValue (O6): purchases score buyers into RFM groups with a churn risk; each buyer gets
// rfm_changed (setting rfm_group, rfm_score, churn_risk), the report groups them, and the Contacts
// search filters by value: and churn:.
func TestCustomerValue(t *testing.T) {
	c, _ := workspace(t, "Customer value")
	run := fmt.Sprint(time.Now().UnixNano())
	now := time.Now().UTC()
	var evs []any
	buy := func(ext string, daysAgo, amount int) {
		evs = append(evs, map[string]any{"external_id": ext, "name": "purchase", "ts": now.AddDate(0, 0, -daysAgo).Format(time.RFC3339),
			"props": map[string]any{"amount": amount}})
	}
	// A frequent recent big spender, a regular who went quiet, and a one-off long ago.
	for i := range 12 {
		buy("vip-"+run, 2+i*7, 5000)
		buy("quiet-"+run, 200+i*10, 800)
	}
	buy("once-"+run, 300, 100)
	for i := range 10 { // a few more buyers so the fifths spread
		buy(fmt.Sprintf("mid%d-%s", i, run), 20+i*15, 500+i*100)
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	waitFor(t, 30*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/once-"+run, nil, -1)["contact"] != nil })
	// Scoring reads the contact snapshot in ClickHouse, as the filtered Contacts list does.
	waitFor(t, 30*time.Second, func() bool {
		return len(asList(jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape("is:identified"), nil, 200)["contacts"])) == 13
	})
	if got := jsonCall(t, c, "POST", "/v1/revenue/customer-value/refresh", nil, 200); got["buyers"] != float64(13) || got["changed"] != float64(13) {
		t.Fatalf("refresh: %v", got)
	}
	jsonCall(t, c, "POST", "/v1/revenue/customer-value/refresh", nil, 429)
	attrs := func(ext string) map[string]any {
		ct, _ := jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"].(map[string]any)
		a, _ := ct["attrs"].(map[string]any)
		return a
	}
	waitFor(t, 30*time.Second, func() bool {
		return attrs("vip-" + run)["rfm_group"] != nil && attrs("quiet-" + run)["rfm_group"] != nil
	})
	if a := attrs("vip-" + run); a["rfm_group"] != "Champions" || a["churn_risk"] != "low" || a["rfm_score"] != "555" {
		t.Errorf("vip: %v", a)
	}
	if a := attrs("quiet-" + run); a["rfm_group"] != "Can't lose them" || a["churn_risk"] != "high" {
		t.Errorf("quiet: %v", a)
	}
	if a := attrs("once-" + run); a["churn_risk"] != "high" {
		t.Errorf("once: %v", a)
	}
	// Buyers can't be given a group from outside.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "x-" + run, "name": "rfm_changed", "props": map[string]any{"group": "Champions"}}, 400)

	var rep map[string]any
	waitFor(t, 20*time.Second, func() bool {
		rep = jsonCall(t, c, "GET", "/v1/revenue/customer-value", nil, 200)
		n := 0.0
		for _, r := range asList(rep["rows"]) {
			n += r.(map[string]any)["contacts"].(float64)
		}
		return n == 13
	})
	// Being scored isn't activity: last_seen stays the last purchase's day.
	if ls := attrs("quiet-" + run)["last_seen"]; ls != now.AddDate(0, 0, -200).Format(time.DateOnly) {
		t.Errorf("quiet last_seen = %v", ls)
	}
	list := jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape(`value:"Can't lose them" churn:high`), nil, 200)
	if n := len(asList(list["contacts"])); n != 1 {
		t.Errorf("value/churn filter found %d: %v", n, list)
	}
	jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape("value:Nope"), nil, 400)
}
