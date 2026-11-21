package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/model"
)

// FuzzCatalog decodes arbitrary products and event properties: Validate, Codes, Price, Vars and
// Render must not panic; Codes returns distinct, trimmed, bounded codes; Render never links a
// non-http(s) address.
func FuzzCatalog(f *testing.F) {
	f.Add(`{"code":"KIT-1","name":"Kitchen kit","url":"https://shop.example.com/k","image_url":"https://cdn.example.com/k.png","price":999.5,"sale_price":799,"currency":"INR","category":"home","in_stock":true}`,
		`{"product_code":"KIT-1","items":[{"product_code":"BED-2"},{"product_code":3},{"product_code":" KIT-1 "}]}`)
	f.Add(`{"code":" ","name":"","url":"javascript:alert(1)","price":-1}`, `{"product_code":null,"items":"x"}`)
	f.Fuzz(func(t *testing.T, productJSON, propsJSON string) {
		var p model.Product
		if json.Unmarshal([]byte(productJSON), &p) == nil {
			_ = Validate(p)
			Price(p)
			h, _ := Render([]model.Product{p, p, p})
			if strings.Contains(strings.ToLower(h), `href="javascript`) {
				t.Fatalf("rendered a javascript link: %s", h)
			}
			Vars([]model.Product{p}, &p)
		}
		var props map[string]any
		if json.Unmarshal([]byte(propsJSON), &props) != nil {
			return
		}
		codes := Codes(props)
		seen := map[string]bool{}
		for _, c := range codes {
			if c == "" || c != strings.TrimSpace(c) || len(c) > MaxCode || seen[c] {
				t.Fatalf("Codes(%s) = %q", propsJSON, codes)
			}
			seen[c] = true
		}
		if len(codes) > 51 {
			t.Fatalf("%d codes", len(codes))
		}
	})
}
