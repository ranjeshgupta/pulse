package eventschema

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// VersionKey is bumped by the API whenever a schema changes, so the roles that check events
	// reload at once instead of after cacheTTL (the same pattern as scoring:v).
	VersionKey = "eventschemas:v"
	cacheTTL   = 5 * time.Minute // full reload even if a version bump was missed
	checkEvery = time.Second     // how often the version key is read
)

// Store loads every workspace's schemas.
type Store interface {
	AllEventSchemas(ctx context.Context) (map[int64][]Schema, error)
}

// Cache holds every workspace's schemas in memory for the roles that check events (profile, api).
type Cache struct {
	store Store
	rdb   redis.Cmdable
	log   *slog.Logger
	now   func() time.Time

	mu        sync.Mutex
	schemas   map[int64]map[string]*Schema
	loadedAt  time.Time
	checkedAt time.Time
	version   string
}

func NewCache(store Store, rdb redis.Cmdable, log *slog.Logger) *Cache {
	return &Cache{store: store, rdb: rdb, log: log, now: time.Now}
}

// Lookup returns the schema of an event, or nil.
func (c *Cache) Lookup(ctx context.Context, tenant int64, name string) *Schema {
	if c == nil {
		return nil
	}
	return c.Tenant(ctx, tenant)[name]
}

// Tenant returns a workspace's schemas by event name (nil when it has none). It reloads when the
// version key changed (read at most once a second) or every cacheTTL; a failed reload keeps the
// previous schemas.
func (c *Cache) Tenant(ctx context.Context, tenant int64) map[string]*Schema {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	stale := c.schemas == nil || now.Sub(c.loadedAt) > cacheTTL
	if !stale && now.Sub(c.checkedAt) >= checkEvery {
		c.checkedAt = now
		if v, err := c.rdb.Get(ctx, VersionKey).Result(); err == nil || errors.Is(err, redis.Nil) {
			stale = v != c.version
		}
	}
	if stale {
		v, _ := c.rdb.Get(ctx, VersionKey).Result() // read first: a bump during the load reloads again
		all, err := c.store.AllEventSchemas(ctx)
		if err == nil {
			m := make(map[int64]map[string]*Schema, len(all))
			for t, list := range all {
				m[t] = make(map[string]*Schema, len(list))
				for i := range list {
					s := list[i]
					if err := s.Normalize(); err != nil {
						if c.log != nil {
							c.log.Error("skipping invalid event schema", "tenant", t, "event", s.Name, "err", err)
						}
						continue
					}
					m[t][s.Name] = &s
				}
			}
			c.schemas, c.version = m, v
		} else {
			if c.log != nil {
				c.log.Error("load event schemas", "err", err)
			}
			if c.schemas == nil {
				c.schemas = map[int64]map[string]*Schema{} // try again at the next bump or cacheTTL
			}
		}
		c.loadedAt, c.checkedAt = now, now
	}
	return c.schemas[tenant]
}

// Invalidate makes the next lookup reload (the process that changed a schema doesn't wait for
// its next version check).
func (c *Cache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.schemas = nil
	c.mu.Unlock()
}

// Bump tells every role to reload schemas.
func Bump(ctx context.Context, rdb redis.Cmdable) error {
	return rdb.Incr(ctx, VersionKey).Err()
}
