package dispatch

// Product recommendations at send time (package catalog): {{block.recommendations}} and reco.*,
// filled only for templates that use them.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"pulse/internal/analytics"
	"pulse/internal/catalog"
	"pulse/internal/channel"
	"pulse/internal/model"
	"pulse/internal/personalize"
)

// CatalogStore reads the catalog and the revenue event (pg.Store).
type CatalogStore interface {
	ProductsByCode(ctx context.Context, tenant int64, codes []string) (map[string]model.Product, error)
	RevenueSettings(ctx context.Context, tenant int64) (analytics.RevenueSettings, error)
}

// maxProductCache bounds the per-code catalog cache (entries across workspaces); maxRecoNeeds
// bounds the per-template-version cache (a new entry each time a template is saved).
const (
	maxProductCache = 200_000
	maxRecoNeeds    = 20_000
)

// RecoBlock is the block key of the recommended products.
const RecoBlock = "recommendations"

var recoIndexRe = regexp.MustCompile(`reco\.(\d{1,2})\.`)

// recoNeed is how many products a template shows (0: it uses none).
func (d *Dispatcher) recoNeed(t model.Template) int {
	key := fmt.Sprintf("%d:%d", t.ID, t.UpdatedAt.UnixNano())
	if c, ok := d.recoNeeds.Load(key); ok {
		return c.(int)
	}
	raw, _ := json.Marshal(t)
	src := string(raw)
	n := 0
	if personalize.UsesReco(src) {
		if strings.Contains(src, "block."+RecoBlock) {
			n = catalog.DefaultN
		}
		for _, m := range recoIndexRe.FindAllStringSubmatch(src, -1) {
			if k, _ := strconv.Atoi(m[1]); k > n && k <= catalog.MaxPicks {
				n = k
			}
		}
		n = max(n, 1) // reco.last_viewed or reco.count alone
	}
	storeBounded(&d.recoNeeds, &d.recoCached, maxRecoNeeds, key, n)
	return n
}

// buyEvent is the workspace's revenue event (cached).
func (d *Dispatcher) buyEvent(ctx context.Context, cs CatalogStore, tenant int64) string {
	if c, ok := d.buyEvents.Load(tenant); ok && d.now().Before(c.(cached[string]).exp) {
		return c.(cached[string]).v
	}
	ev := analytics.DefaultRevenue().Event
	if rs, err := cs.RevenueSettings(ctx, tenant); err == nil && rs.Event != "" {
		ev = rs.Event
	}
	d.buyEvents.Store(tenant, cached[string]{v: ev, exp: d.now().Add(cacheTTL)})
	return ev
}

// products looks codes up in the catalog, through a short per-code cache (missing codes too).
func (d *Dispatcher) products(ctx context.Context, cs CatalogStore, tenant int64, codes []string) (map[string]model.Product, error) {
	out := make(map[string]model.Product, len(codes))
	var miss []string
	for _, c := range codes {
		if v, ok := d.productCache.Load(fmt.Sprintf("%d:%s", tenant, c)); ok && d.now().Before(v.(cached[*model.Product]).exp) {
			if p := v.(cached[*model.Product]).v; p != nil {
				out[c] = *p
			}
			continue
		}
		miss = append(miss, c)
	}
	if len(miss) == 0 {
		return out, nil
	}
	found, err := cs.ProductsByCode(ctx, tenant, miss)
	if err != nil {
		return nil, err
	}
	exp := d.now().Add(cacheTTL)
	// Codes come from events anyone may send, so the cache is bounded: past the limit it starts over.
	if d.productCached.Add(int64(len(miss))) > maxProductCache {
		d.productCache.Range(func(k, _ any) bool { d.productCache.Delete(k); return true })
		d.productCached.Store(int64(len(miss)))
	}
	for _, c := range miss {
		var p *model.Product
		if f, ok := found[c]; ok {
			p = &f
			out[c] = f
		}
		d.productCache.Store(fmt.Sprintf("%d:%s", tenant, c), cached[*model.Product]{v: p, exp: exp})
	}
	return out, nil
}

// withReco fills the recommendations of one message into v when its template uses them. Errors
// leave them empty (the message still goes; the template's defaults apply).
func (d *Dispatcher) withReco(ctx context.Context, t model.Template, c model.Contact, v *channel.Vars) {
	n := d.recoNeed(t)
	cs, ok := d.store.(CatalogStore)
	if n == 0 || !ok || c.ID == 0 {
		return
	}
	logErr := func(err error) {
		if d.cfg.Log != nil {
			d.cfg.Log.Warn("recommendations", "tenant", c.TenantID, "err", err)
		}
	}
	lookup := func(ctx context.Context, tenant int64, codes []string) (map[string]model.Product, error) {
		return d.products(ctx, cs, tenant, codes)
	}
	picks, last, err := catalog.Recommend(ctx, d.rdb, lookup, c.TenantID, c.ID, d.buyEvent(ctx, cs, c.TenantID), n)
	if err != nil {
		logErr(err)
		return
	}
	v.Reco = catalog.Vars(picks, last)
	if h, txt := catalog.Render(picks); h != "" {
		bl := make(map[string]channel.Block, len(v.Blocks)+1)
		for k, b := range v.Blocks {
			bl[k] = b
		}
		bl[RecoBlock] = channel.Block{HTML: h, Text: txt}
		v.Blocks = bl
	}
}
