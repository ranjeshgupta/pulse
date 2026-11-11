package reach

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Row is what a rebuild reads of one contact: its id and the attributes Bits looks at (with
// "email" set from the contact's email).
type Row struct {
	ID    uint32
	Attrs map[string]any
}

// Store reads contacts for rebuilds (pg.Store).
type Store interface {
	TenantIDs(ctx context.Context) ([]int64, error)
	// ReachRows pages through a workspace's contacts by id; with since set, only contacts changed
	// at or after it.
	ReachRows(ctx context.Context, tenant int64, afterID uint32, limit int, since *time.Time) ([]Row, error)
	// PushContacts lists contacts with at least one push device.
	PushContacts(ctx context.Context, tenant int64) ([]uint32, error)
}

// Rebuilder rebuilds the bitmaps of workspaces that have none (new, or a day old).
type Rebuilder struct {
	Store Store
	RDB   redis.Cmdable
	Log   *slog.Logger
	Now   func() time.Time
	// Every is how long a rebuild stays fresh (a day by default).
	Every time.Duration
}

const (
	pageSize = 10000
	lockTTL  = time.Hour
)

func lockKey(tenant int64) string { return Key(tenant, "lock") }

func (r *Rebuilder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// RunDue rebuilds every workspace whose bitmaps aren't fresh, one at a time. A lock per workspace
// keeps two segment workers from rebuilding the same one.
func (r *Rebuilder) RunDue(ctx context.Context) error {
	ids, err := r.Store.TenantIDs(ctx)
	if err != nil {
		return err
	}
	p := r.RDB.Pipeline()
	cmds := make([]*redis.IntCmd, len(ids))
	for i, id := range ids {
		cmds[i] = p.Exists(ctx, BuiltKey(id))
	}
	if len(ids) > 0 {
		if _, err := p.Exec(ctx); err != nil {
			return err
		}
	}
	for i, id := range ids {
		if ctx.Err() != nil || cmds[i].Val() == 1 {
			continue
		}
		ok, err := r.RDB.SetNX(ctx, lockKey(id), 1, lockTTL).Result()
		if err != nil || !ok {
			continue
		}
		start := time.Now()
		n, err := r.Rebuild(ctx, id)
		r.RDB.Del(context.WithoutCancel(ctx), lockKey(id))
		if err != nil {
			r.Log.Error("rebuild reachability", "tenant", id, "err", err)
			continue
		}
		r.Log.Info("reachability rebuilt", "tenant", id, "contacts", n, "took", time.Since(start))
	}
	return nil
}

// Rebuild recomputes a workspace's bitmaps from Postgres and swaps them in. Events processed while
// it runs could be overwritten by the swap, so contacts changed since it started are applied again
// afterwards.
func (r *Rebuilder) Rebuild(ctx context.Context, tenant int64) (int, error) {
	started := r.now().Add(-5 * time.Second)
	maps := map[string][]byte{}
	set := func(name string, id uint32) {
		b := maps[name]
		if need := int(id/8) + 1; need > len(b) {
			nb := make([]byte, max(need, len(b)*2))
			copy(nb, b)
			b = nb
		}
		b[id/8] |= 0x80 >> (id % 8)
		maps[name] = b
	}
	n := 0
	var after uint32
	for {
		rows, err := r.Store.ReachRows(ctx, tenant, after, pageSize, nil)
		if err != nil {
			return n, err
		}
		for _, row := range rows {
			for name, on := range Bits(row.Attrs) {
				if on {
					set(name, row.ID)
				}
			}
			after = row.ID
		}
		n += len(rows)
		if len(rows) < pageSize {
			break
		}
	}
	push, err := r.Store.PushContacts(ctx, tenant)
	if err != nil {
		return n, err
	}
	for _, id := range push {
		set(Push, id)
	}
	for _, name := range AllNames() {
		b := trim(maps[name])
		if err := replace(ctx, r.RDB, Key(tenant, name), b); err != nil {
			return n, err
		}
	}
	// Re-apply contacts changed while the rebuild ran.
	after = 0
	for {
		rows, err := r.Store.ReachRows(ctx, tenant, after, pageSize, &started)
		if err != nil {
			return n, err
		}
		p := r.RDB.Pipeline()
		for _, row := range rows {
			Queue(ctx, p, tenant, row.ID, Bits(row.Attrs))
			after = row.ID
		}
		if len(rows) > 0 {
			if _, err := p.Exec(ctx); err != nil {
				return n, err
			}
		}
		if len(rows) < pageSize {
			break
		}
	}
	every := r.Every
	if every <= 0 {
		every = 24 * time.Hour
	}
	return n, r.RDB.Set(ctx, BuiltKey(tenant), r.now().UTC().Format(time.RFC3339), every).Err()
}

// trim drops trailing zero bytes (the doubling growth above over-allocates).
func trim(b []byte) []byte {
	i := len(b)
	for i > 0 && b[i-1] == 0 {
		i--
	}
	return b[:i]
}

// replace swaps a whole bitmap in atomically (an empty one deletes the key).
func replace(ctx context.Context, rdb redis.Cmdable, key string, bitmap []byte) error {
	if len(bitmap) == 0 {
		return rdb.Del(ctx, key).Err()
	}
	tmp := key + ":tmp"
	_, err := rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, tmp, bitmap, 0)
		p.Rename(ctx, tmp, key)
		return nil
	})
	return err
}

// Ready reports whether a workspace's bitmaps have been built.
func Ready(ctx context.Context, rdb redis.Cmdable, tenant int64) (bool, error) {
	n, err := rdb.Exists(ctx, BuiltKey(tenant)).Result()
	return n == 1, err
}

// Run checks for workspaces to rebuild every 10 seconds until ctx is done.
func (r *Rebuilder) Run(ctx context.Context) error {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		if err := r.RunDue(ctx); err != nil && ctx.Err() == nil {
			r.Log.Error("reachability rebuilds", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
