package tracking

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestShortenText(t *testing.T) {
	base := "https://pulse.example.com"
	text := "Sale! See https://shop.in/sale?x=1. Or https://shop.in/sale?x=1, and (https://b.co/a) " + base + "/t/c/abc stays"
	out, urls := ShortenText(text, base, "AbCdEfGhIj", nil)
	want := "Sale! See " + base + "/t/s/AbCdEfGhIj0. Or " + base + "/t/s/AbCdEfGhIj0, and (" + base + "/t/s/AbCdEfGhIj1) " + base + "/t/c/abc stays"
	if out != want {
		t.Errorf("out = %q\nwant %q", out, want)
	}
	if len(urls) != 2 || urls[0] != "https://shop.in/sale?x=1" || urls[1] != "https://b.co/a" {
		t.Errorf("urls = %v", urls)
	}
	if out, urls := ShortenText("no links", base, "x", nil); out != "no links" || len(urls) != 0 {
		t.Error("text without links changed")
	}
}

func TestShortLinksRoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	code := NewShortCode()
	if len(code) != 10 {
		t.Fatalf("code %q", code)
	}
	tok := Token{TenantID: 3, ExternalID: "u1", MessageID: "m1", Channel: "sms"}
	if err := SaveShort(ctx, rdb, code, ShortLink{Token: tok, URLs: []string{"https://a.in", "https://b.in"}}); err != nil {
		t.Fatal(err)
	}
	got, dest, err := ResolveShort(ctx, rdb, code+"1")
	if err != nil || got != tok || dest != "https://b.in" {
		t.Fatalf("resolve = %+v %q %v", got, dest, err)
	}
	for _, bad := range []string{code + "2", code, code + "1x", "!!!!!!!!!!0", "zzzzzzzzzz0"} {
		if _, _, err := ResolveShort(ctx, rdb, bad); err != ErrNoShortLink {
			t.Errorf("%q: err %v", bad, err)
		}
	}
}
