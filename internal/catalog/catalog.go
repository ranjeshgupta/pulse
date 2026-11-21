// Package catalog is the product catalog and recommendations (O6). Products are kept per workspace
// (code, name, link, image, price, category, stock); events carrying product_code (or
// items[].product_code) tell what each contact viewed and bought. Every day the popular, trending
// and bought-together lists are worked out from the event log; a message whose template uses
// {{block.recommendations}} or {{reco.1.name}} gets products picked for its contact at send time:
// bought together with what they bought or looked at, then trending, then popular — in stock,
// in the catalog and not bought by them lately.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/attr"
	"pulse/internal/model"
)

// Limits.
const (
	MaxBatch   = 1000 // products per upsert call
	MaxCode    = 128
	MaxName    = 300
	MaxPicks   = 12 // products one message can show
	DefaultN   = 4
	recentKeep = 30
	recentTTL  = 120 * 24 * time.Hour
)

// Validate checks a product before it is saved.
func Validate(p model.Product) error {
	var errs []string
	if p.Code = strings.TrimSpace(p.Code); p.Code == "" || len(p.Code) > MaxCode {
		errs = append(errs, fmt.Sprintf("code: 1 to %d characters", MaxCode))
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > MaxName {
		errs = append(errs, fmt.Sprintf("name: 1 to %d characters", MaxName))
	}
	if p.URL != "" && !webURL(p.URL) {
		errs = append(errs, "url: a full http(s) address")
	}
	if p.ImageURL != "" && !strings.HasPrefix(p.ImageURL, "https://") {
		errs = append(errs, "image_url: an https address")
	}
	if (p.Price != nil && *p.Price < 0) || (p.SalePrice != nil && *p.SalePrice < 0) {
		errs = append(errs, "price: 0 or more")
	}
	if len(p.Category) > 200 || len(p.Currency) > 3 {
		errs = append(errs, "category: at most 200 characters; currency: an ISO code like INR")
	}
	if len(errs) > 0 {
		return errors.New(p.Code + ": " + strings.Join(errs, "; "))
	}
	return nil
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// Codes are the product codes an event carries: props product_code and items[].product_code.
func Codes(props map[string]any) []string {
	var out []string
	add := func(v any) {
		if s := strings.TrimSpace(attr.String(v)); s != "" && len(s) <= MaxCode {
			for _, o := range out {
				if o == s {
					return
				}
			}
			out = append(out, s)
		}
	}
	add(props["product_code"])
	if items, ok := props["items"].([]any); ok {
		for i, it := range items {
			if i == 50 {
				break
			}
			if m, ok := it.(map[string]any); ok {
				add(m["product_code"])
			}
		}
	}
	return out
}

func recentKey(tenant int64, contact uint32) string {
	return "recent:" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatUint(uint64(contact), 10)
}

// QueueRecent records an event's products on the contact's recent list (newest first), in pipe.
func QueueRecent(ctx context.Context, pipe redis.Pipeliner, tenant int64, contact uint32, name string, codes []string) {
	if len(codes) == 0 || contact == 0 {
		return
	}
	k := recentKey(tenant, contact)
	vals := make([]any, 0, len(codes))
	for _, c := range codes {
		vals = append(vals, name+"\x1f"+c)
	}
	pipe.LPush(ctx, k, vals...)
	pipe.LTrim(ctx, k, 0, recentKeep-1)
	pipe.Expire(ctx, k, recentTTL)
}

// Recent is what a contact did with products lately, newest first.
type Recent struct {
	Bought []string
	Viewed []string // viewed or carted, not bought
}

// ReadRecent reads a contact's recent products; buyEvent names the purchase event.
func ReadRecent(ctx context.Context, rdb redis.Cmdable, tenant int64, contact uint32, buyEvent string) (Recent, error) {
	vals, err := rdb.LRange(ctx, recentKey(tenant, contact), 0, recentKeep-1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Recent{}, err
	}
	var r Recent
	bought := map[string]bool{}
	for _, v := range vals {
		name, code, ok := strings.Cut(v, "\x1f")
		if ok && name == buyEvent && !bought[code] {
			bought[code] = true
			r.Bought = append(r.Bought, code)
		}
	}
	seen := map[string]bool{}
	for _, v := range vals {
		name, code, ok := strings.Cut(v, "\x1f")
		if ok && name != buyEvent && !bought[code] && !seen[code] {
			seen[code] = true
			r.Viewed = append(r.Viewed, code)
		}
	}
	return r, nil
}

// Lists are a workspace's daily lists: popular (most bought, 30 days), trending (most viewed,
// 7 days) and bought together (per product, what its buyers also bought, 90 days).
type Lists struct {
	Popular  []string
	Trending []string
	Also     map[string][]string
}

// Pick chooses up to n products for a contact: bought together with their recent purchases and
// views, then what they viewed, then trending, then popular; never one they bought lately, and
// only those ok accepts (in the catalog, in stock).
func Pick(r Recent, l Lists, n int, ok func(code string) bool) []string {
	n = max(1, min(n, MaxPicks))
	skip := map[string]bool{}
	for _, c := range r.Bought {
		skip[c] = true
	}
	var out []string
	add := func(codes []string) {
		for _, c := range codes {
			if len(out) == n {
				return
			}
			if !skip[c] && ok(c) {
				skip[c] = true
				out = append(out, c)
			}
		}
	}
	for _, c := range append(append([]string{}, r.Bought...), r.Viewed...) {
		add(l.Also[c])
	}
	add(r.Viewed)
	add(l.Trending)
	add(l.Popular)
	return out
}

// Lookup finds codes in the catalog.
type Lookup func(ctx context.Context, tenant int64, codes []string) (map[string]model.Product, error)

// Recommend picks up to n in-stock catalog products for a contact, and the product they last
// viewed without buying (nil when none is in the catalog).
func Recommend(ctx context.Context, rdb redis.Cmdable, lookup Lookup, tenant int64, contact uint32, buyEvent string, n int) ([]model.Product, *model.Product, error) {
	recent, err := ReadRecent(ctx, rdb, tenant, contact, buyEvent)
	if err != nil {
		return nil, nil, err
	}
	seed := append(append([]string{}, recent.Bought[:min(len(recent.Bought), 5)]...), recent.Viewed[:min(len(recent.Viewed), 5)]...)
	lists, err := LoadLists(ctx, rdb, tenant, seed)
	if err != nil {
		return nil, nil, err
	}
	pool := append([]string{}, recent.Viewed...)
	for _, s := range seed {
		pool = append(pool, lists.Also[s]...)
	}
	pool = append(append(pool, lists.Trending...), lists.Popular...)
	cat, err := lookup(ctx, tenant, pool)
	if err != nil {
		return nil, nil, err
	}
	codes := Pick(recent, lists, n, func(code string) bool { p, ok := cat[code]; return ok && p.InStock })
	picks := make([]model.Product, 0, len(codes))
	for _, code := range codes {
		picks = append(picks, cat[code])
	}
	for _, code := range recent.Viewed {
		if p, ok := cat[code]; ok {
			return picks, &p, nil
		}
	}
	return picks, nil, nil
}

// Vars are the reco.* variables: reco.1.name … for each pick, and reco.last_viewed (the latest
// product they looked at without buying, for browse-abandonment messages).
func Vars(picks []model.Product, lastViewed *model.Product) map[string]any {
	out := map[string]any{"count": len(picks)}
	for i, p := range picks {
		out[strconv.Itoa(i+1)] = productMap(p)
	}
	if lastViewed != nil {
		out["last_viewed"] = productMap(*lastViewed)
	}
	return out
}

func productMap(p model.Product) map[string]any {
	m := map[string]any{"code": p.Code, "name": p.Name, "url": p.URL, "image": p.ImageURL, "category": p.Category, "currency": p.Currency}
	if p.Price != nil {
		m["price"] = *p.Price
	}
	if p.SalePrice != nil {
		m["sale_price"] = *p.SalePrice
	}
	return m
}

// Render makes {{block.recommendations}}: a two-column grid of cards for email, a list for text.
func Render(picks []model.Product) (htmlOut, text string) {
	if len(picks) == 0 {
		return "", ""
	}
	var h, t strings.Builder
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse"><tr>`)
	for i, p := range picks {
		if i > 0 && i%2 == 0 {
			h.WriteString(`</tr><tr>`)
		}
		link := ""
		if webURL(p.URL) {
			link = html.EscapeString(p.URL)
		}
		h.WriteString(`<td width="50%" valign="top" style="padding:8px">`)
		if link != "" {
			fmt.Fprintf(&h, `<a href="%s" style="text-decoration:none;color:inherit">`, link)
		}
		if strings.HasPrefix(p.ImageURL, "https://") {
			fmt.Fprintf(&h, `<img src="%s" alt="%s" width="100%%" style="display:block;max-width:100%%;border-radius:6px">`, html.EscapeString(p.ImageURL), html.EscapeString(p.Name))
		}
		fmt.Fprintf(&h, `<p style="margin:6px 0 2px;font-weight:600">%s</p>`, html.EscapeString(p.Name))
		if pr := Price(p); pr != "" {
			fmt.Fprintf(&h, `<p style="margin:0">%s</p>`, html.EscapeString(pr))
		}
		if link != "" {
			h.WriteString(`</a>`)
		}
		h.WriteString(`</td>`)
		fmt.Fprintf(&t, "- %s", p.Name)
		if pr := Price(p); pr != "" {
			fmt.Fprintf(&t, " (%s)", pr)
		}
		if link != "" {
			fmt.Fprintf(&t, " %s", p.URL)
		}
		t.WriteString("\n")
	}
	if len(picks)%2 == 1 {
		h.WriteString(`<td width="50%"></td>`)
	}
	h.WriteString(`</tr></table>`)
	return h.String(), strings.TrimSpace(t.String())
}

// Price is a product's price as shown: the sale price when it has one, with its currency.
func Price(p model.Product) string {
	v := p.Price
	if p.SalePrice != nil {
		v = p.SalePrice
	}
	if v == nil {
		return ""
	}
	s := strconv.FormatFloat(*v, 'f', -1, 64)
	if p.Currency != "" {
		return p.Currency + " " + s
	}
	return s
}

// ---- daily lists in Redis ----

func listsKey(tenant int64) string { return "reco:" + strconv.FormatInt(tenant, 10) }

// ListsTTL keeps the lists until a later pass replaces them.
const ListsTTL = 3 * 24 * time.Hour

// SaveLists stores a workspace's lists (one hash: popular, trending, also:<code>).
func SaveLists(ctx context.Context, rdb redis.Cmdable, tenant int64, l Lists) error {
	k := listsKey(tenant)
	pipe := rdb.TxPipeline()
	pipe.Del(ctx, k)
	fields := map[string]any{"popular": strings.Join(l.Popular, "\x1f"), "trending": strings.Join(l.Trending, "\x1f")}
	for c, also := range l.Also {
		fields["also:"+c] = strings.Join(also, "\x1f")
	}
	pipe.HSet(ctx, k, fields)
	pipe.Expire(ctx, k, ListsTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// LoadLists reads the lists a pick needs: popular, trending and bought-together of codes.
func LoadLists(ctx context.Context, rdb redis.Cmdable, tenant int64, codes []string) (Lists, error) {
	fields := []string{"popular", "trending"}
	for _, c := range codes {
		fields = append(fields, "also:"+c)
	}
	vals, err := rdb.HMGet(ctx, listsKey(tenant), fields...).Result()
	if err != nil {
		return Lists{}, err
	}
	split := func(v any) []string {
		s, _ := v.(string)
		if s == "" {
			return nil
		}
		return strings.Split(s, "\x1f")
	}
	l := Lists{Popular: split(vals[0]), Trending: split(vals[1]), Also: map[string][]string{}}
	for i, c := range codes {
		if a := split(vals[i+2]); a != nil {
			l.Also[c] = a
		}
	}
	return l, nil
}
