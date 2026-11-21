package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/model"
)

func f(v float64) *float64 { return &v }

func TestCodesValidateRender(t *testing.T) {
	got := Codes(map[string]any{"product_code": "SKU1", "items": []any{map[string]any{"product_code": "SKU2"}, map[string]any{"product_code": 3.0}, map[string]any{"product_code": "SKU1"}}})
	if strings.Join(got, ",") != "SKU1,SKU2,3" {
		t.Errorf("codes: %v", got)
	}
	if Validate(model.Product{Code: "a", Name: "A", URL: "javascript:x"}) == nil || Validate(model.Product{Code: "a", Name: "A", ImageURL: "http://x"}) == nil ||
		Validate(model.Product{Code: "", Name: "A"}) == nil || Validate(model.Product{Code: "a", Name: "A", Price: f(-1)}) == nil {
		t.Error("bad products accepted")
	}
	if err := Validate(model.Product{Code: "a", Name: "A", URL: "https://shop.test/a", ImageURL: "https://cdn.test/a.jpg", Price: f(10)}); err != nil {
		t.Error(err)
	}
	h, txt := Render([]model.Product{{Code: "a", Name: "Kurta <b>", URL: "https://shop.test/a", ImageURL: "https://cdn.test/a.jpg", Price: f(999), SalePrice: f(799), Currency: "INR"},
		{Code: "b", Name: "Bad", URL: "javascript:alert(1)"}})
	if !strings.Contains(h, "Kurta &lt;b&gt;") || strings.Contains(h, "javascript:") || !strings.Contains(h, "INR 799") || !strings.Contains(txt, "- Kurta <b> (INR 799) https://shop.test/a") {
		t.Errorf("render:\n%s\n%s", h, txt)
	}
}

func TestPick(t *testing.T) {
	l := Lists{Popular: []string{"p1", "p2", "bought"}, Trending: []string{"t1", "oos"}, Also: map[string][]string{"bought": {"a1", "a2"}, "v1": {"a3"}}}
	r := Recent{Bought: []string{"bought"}, Viewed: []string{"v1"}}
	ok := func(c string) bool { return c != "oos" && c != "a2" }
	got := Pick(r, l, 6, ok)
	if strings.Join(got, ",") != "a1,a3,v1,t1,p1,p2" {
		t.Errorf("pick: %v", got)
	}
	if got := Pick(Recent{}, l, 2, ok); strings.Join(got, ",") != "t1,p1" {
		t.Errorf("no history: %v", got)
	}
}

func TestRecentAndLists(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	for _, e := range []struct{ name, code string }{{"product_viewed", "x"}, {"purchase", "x"}, {"product_viewed", "y"}, {"add_to_cart", "z"}} {
		pipe := rdb.Pipeline()
		QueueRecent(ctx, pipe, 1, 9, e.name, []string{e.code})
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	r, err := ReadRecent(ctx, rdb, 1, 9, "purchase")
	if err != nil || strings.Join(r.Bought, ",") != "x" || strings.Join(r.Viewed, ",") != "z,y" {
		t.Errorf("recent: %+v %v", r, err)
	}
	if err := SaveLists(ctx, rdb, 1, Lists{Popular: []string{"p"}, Trending: []string{"t"}, Also: map[string][]string{"x": {"q", "w"}}}); err != nil {
		t.Fatal(err)
	}
	l, err := LoadLists(ctx, rdb, 1, []string{"x", "nope"})
	if err != nil || l.Popular[0] != "p" || strings.Join(l.Also["x"], ",") != "q,w" || l.Also["nope"] != nil {
		t.Errorf("lists: %+v %v", l, err)
	}
}
