//go:build e2e

package e2e

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestProductRecommendations (O6): a catalog, purchase and view events, the daily lists worked
// out on demand, a contact's picks (bought together with what they looked at, in stock only) and
// an email filled with {{reco.1.name}} and {{block.recommendations}}.
func TestProductRecommendations(t *testing.T) {
	c, _ := workspace(t, "Catalog")
	run := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	p := func(code, name string, stock bool) map[string]any {
		return map[string]any{"code": code + run, "name": name, "url": "https://shop.example.com/" + code, "price": 999, "currency": "INR", "in_stock": stock}
	}
	jsonCall(t, c, "PUT", "/v1/products", map[string]any{"products": []any{map[string]any{"code": "x", "name": "Bad", "url": "javascript:alert(1)"}}}, 400)
	jsonCall(t, c, "PUT", "/v1/products", map[string]any{"products": []any{
		p("K", "Linen kurta", true), p("D", "Cotton dupatta", true), p("S", "Silk saree", true), p("O", "Sold-out stole", false)}}, 200)
	if got := jsonCall(t, c, "GET", "/v1/products?q="+url.QueryEscape("kurta"), nil, 200); got["total"] != float64(1) {
		t.Fatalf("search: %v", got)
	}
	buy := func(ext string, codes ...string) map[string]any {
		var items []any
		for _, code := range codes {
			items = append(items, map[string]any{"product_code": code + run})
		}
		return map[string]any{"external_id": ext, "name": "purchase", "props": map[string]any{"amount": 999, "items": items}}
	}
	view := func(ext, code string) map[string]any {
		return map[string]any{"external_id": ext, "name": "product_viewed", "props": map[string]any{"product_code": code + run}}
	}
	shopper := "shopper-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		buy("a-"+run, "K", "D", "O"), buy("b-"+run, "K", "D"), buy("c-"+run, "K", "S"),
		map[string]any{"external_id": shopper, "name": "identify", "email": shopper + "@example.com"},
		view(shopper, "K"),
	}}, 202)
	waitFor(t, 20*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+shopper, nil, -1)["contact"] != nil })
	time.Sleep(2 * time.Second) // events reach ClickHouse
	jsonCall(t, c, "POST", "/v1/products/refresh-lists", nil, 200)
	jsonCall(t, c, "POST", "/v1/products/refresh-lists", nil, 429)

	rec := jsonCall(t, c, "GET", "/v1/contacts/"+shopper+"/recommendations?n=4", nil, 200)
	var names []string
	for _, x := range asList(rec["products"]) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	// Bought together with the kurta they viewed: the dupatta (twice) before the saree; then the kurta itself.
	if strings.Join(names, ",") != "Cotton dupatta,Silk saree,Linen kurta" {
		t.Errorf("picks: %v (%v)", names, rec)
	}
	if lv, _ := rec["last_viewed"].(map[string]any); lv == nil || lv["name"] != "Linen kurta" {
		t.Errorf("last viewed: %v", rec["last_viewed"])
	}

	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "reco", "channel": "email", "subject": "Picked for you: {{reco.1.name}} " + run,
		"html": `<html><body><p>Still thinking about {{reco.last_viewed.name}}?</p>{{block.recommendations}}</body></html>`}, 201)["id"]
	jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "bad", "channel": "email", "subject": "{{reco.99.name}}", "html": "<p>x</p>"}, 400)
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": shopper}, 202)
	var search struct {
		Messages []struct {
			ID      string
			Subject string
		} `json:"messages"`
	}
	waitFor(t, 20*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+shopper+"@example.com"), &search)
		return len(search.Messages) == 1
	})
	if s := search.Messages[0].Subject; s != "Picked for you: Cotton dupatta "+run {
		t.Errorf("subject %q", s)
	}
	var msg struct{ HTML string }
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID, &msg)
	if !strings.Contains(msg.HTML, "Still thinking about Linen kurta?") || !strings.Contains(msg.HTML, "Silk saree") || strings.Contains(msg.HTML, "Sold-out") {
		t.Errorf("email:\n%s", msg.HTML)
	}
	jsonCall(t, c, "DELETE", "/v1/products/O"+run, nil, 204)
	jsonCall(t, c, "DELETE", "/v1/products/O"+run, nil, 404)
}
