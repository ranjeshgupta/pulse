package dispatch

// Content blocks ({{block.key}}, O6) for rendering, cached per workspace like the other settings.

import (
	"context"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/model"
	"pulse/internal/spamcheck"
)

// BlockStore reads content blocks (pg.Store).
type BlockStore interface {
	ListBlocks(ctx context.Context, tenant int64) ([]model.ContentBlock, error)
}

func (d *Dispatcher) blocks(ctx context.Context, tenant int64) map[string]channel.Block {
	bs, ok := d.store.(BlockStore)
	if !ok {
		return nil
	}
	if c, ok := d.blockCache.Load(tenant); ok {
		if c := c.(cached[map[string]channel.Block]); d.now().Before(c.exp) {
			return c.v
		}
	}
	list, err := bs.ListBlocks(ctx, tenant)
	if err != nil {
		if d.cfg.Log != nil {
			d.cfg.Log.Error("content blocks", "tenant", tenant, "err", err)
		}
		return nil
	}
	out := make(map[string]channel.Block, len(list))
	for _, b := range list {
		text := b.Text
		if strings.TrimSpace(text) == "" {
			text = spamcheck.Text(b.HTML)
		}
		out[b.Key] = channel.Block{HTML: b.HTML, Text: text}
	}
	d.blockCache.Store(tenant, cached[map[string]channel.Block]{v: out, exp: d.now().Add(cacheTTL)})
	return out
}

func (d *Dispatcher) varsWithBlocks(ctx context.Context, m model.Message) channel.Vars {
	v := vars(m, channel.Links{})
	v.Blocks = d.blocks(ctx, m.TenantID)
	return v
}
