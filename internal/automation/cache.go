package automation

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Source reads a workspace's settings (pg.Store).
type Source interface {
	AutomationSettings(ctx context.Context, tenant int64) (Settings, error)
}

// Cache keeps each workspace's settings for a while, for hot paths that read them per contact.
// A failed read logs and uses the defaults (and is retried after the TTL).
type Cache struct {
	src Source
	ttl time.Duration
	log *slog.Logger
	now func() time.Time
	m   sync.Map // tenant -> cacheEntry
}

type cacheEntry struct {
	v   Settings
	exp time.Time
}

func NewCache(src Source, ttl time.Duration, log *slog.Logger) *Cache {
	return &Cache{src: src, ttl: ttl, log: log, now: time.Now}
}

// Get returns the workspace's settings.
func (c *Cache) Get(ctx context.Context, tenant int64) Settings {
	if e, ok := c.m.Load(tenant); ok && c.now().Before(e.(cacheEntry).exp) {
		return e.(cacheEntry).v
	}
	v, err := c.src.AutomationSettings(ctx, tenant)
	if err != nil {
		if c.log != nil {
			c.log.Error("automation settings", "tenant", tenant, "err", err)
		}
		v = Defaults()
	}
	c.m.Store(tenant, cacheEntry{v: v, exp: c.now().Add(c.ttl)})
	return v
}
