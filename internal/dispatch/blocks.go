package dispatch

// Content blocks ({{block.key}}, O6) for rendering, cached per workspace like the other settings.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"pulse/internal/channel"
	"pulse/internal/model"
	"pulse/internal/spamcheck"
	"pulse/internal/store/pg"
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
	v.Blocks = d.messageBlocks(ctx, m)
	return v
}

// FeedRuns reads an RSS-to-email run's content (pg.Store).
type FeedRuns interface {
	RunFeed(ctx context.Context, broadcast int64, scheduled time.Time) (html, text string, err error)
}

// FeedBlock is the block key of an RSS-to-email run's items.
const FeedBlock = "feed"

// messageBlocks is the workspace's blocks plus, for a campaign run with feed content, {{block.feed}}
// (a run's content never changes, so it is cached).
func (d *Dispatcher) messageBlocks(ctx context.Context, m model.Message) map[string]channel.Block {
	bl := d.blocks(ctx, m.TenantID)
	fr, ok := d.store.(FeedRuns)
	if !ok || m.BroadcastID == 0 || m.Run <= 0 {
		return bl
	}
	key := fmt.Sprintf("%d:%d", m.BroadcastID, m.Run)
	var fb channel.Block
	if c, ok := d.feedCache.Load(key); ok && d.now().Before(c.(cached[channel.Block]).exp) {
		fb = c.(cached[channel.Block]).v
	} else {
		h, t, err := fr.RunFeed(ctx, m.BroadcastID, time.Unix(m.Run, 0))
		if err != nil && !errors.Is(err, pg.ErrNotFound) && d.cfg.Log != nil {
			d.cfg.Log.Error("campaign feed", "broadcast", m.BroadcastID, "err", err)
		}
		fb = channel.Block{HTML: h, Text: t}
		if err == nil || errors.Is(err, pg.ErrNotFound) {
			d.feedCache.Store(key, cached[channel.Block]{v: fb, exp: d.now().Add(time.Hour)})
		}
	}
	if fb.HTML == "" {
		return bl
	}
	out := make(map[string]channel.Block, len(bl)+1)
	for k, b := range bl {
		out[k] = b
	}
	out[FeedBlock] = fb
	return out
}
