package journey

// Journey changes reach the journey workers at once. The API bumps a version and announces it on
// a Redis channel; each engine reloads its index and records the version it has loaded. The API
// waits (briefly) until every live engine has it, so when "activate" answers, new matches enter.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	versionKey = "journeys:version" // bumped by every change
	loadedKey  = "journeys:loaded"  // hash: engine instance -> "<version>:<unix ms>" of its last reload
	// An engine that hasn't reloaded for this long is taken as gone (they reload every refreshEvery).
	engineAlive = 15 * time.Second
)

// Announce records a journey change and waits up to wait for every live engine to load it. It
// returns false when an engine didn't confirm in time (it still picks the change up within
// refreshEvery).
func Announce(ctx context.Context, rdb redis.Cmdable, wait time.Duration) (bool, error) {
	v, err := rdb.Incr(ctx, versionKey).Result()
	if err != nil {
		return false, err
	}
	if err := rdb.Publish(ctx, ChangesChannel, v).Err(); err != nil {
		return false, err
	}
	deadline := time.Now().Add(wait)
	for {
		loaded, err := rdb.HGetAll(ctx, loadedKey).Result()
		if err != nil {
			return false, err
		}
		if allLoaded(loaded, v, time.Now()) {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// allLoaded reports whether every engine that reloaded recently has loaded version v.
func allLoaded(loaded map[string]string, v int64, now time.Time) bool {
	for _, val := range loaded {
		ver, at, ok := parseLoaded(val)
		if !ok || now.Sub(at) > engineAlive {
			continue // gone, or not ours to wait for
		}
		if ver < v {
			return false
		}
	}
	return true
}

func parseLoaded(s string) (int64, time.Time, bool) {
	a, b, ok := strings.Cut(s, ":")
	ver, err1 := strconv.ParseInt(a, 10, 64)
	ms, err2 := strconv.ParseInt(b, 10, 64)
	if !ok || err1 != nil || err2 != nil {
		return 0, time.Time{}, false
	}
	return ver, time.UnixMilli(ms), true
}

// WatchChanges makes the engine reload as soon as a change is announced, and record each reload's
// version so Announce can wait for it. It runs until ctx is done.
func (e *Engine) WatchChanges(ctx context.Context, rdb *redis.Client) error {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	e.changes.Store(&changeLog{rdb: rdb, instance: hex.EncodeToString(b)})
	sub := rdb.Subscribe(ctx, ChangesChannel)
	defer sub.Close()
	defer func() { // leave the hash, so the API doesn't wait for a stopped engine
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rdb.HDel(ctx, loadedKey, e.changes.Load().instance)
	}()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ch:
			if !ok {
				e.log.Warn("journey change notifications stopped; reloading every " + refreshEvery.String())
				return nil
			}
			e.Notify()
		}
	}
}

type changeLog struct {
	rdb      *redis.Client
	instance string
}

// version is the change version before a reload reads the journeys (0 without Redis).
func (c *changeLog) version(ctx context.Context) int64 {
	if c == nil {
		return 0
	}
	v, err := c.rdb.Get(ctx, versionKey).Int64()
	if err != nil && err != redis.Nil {
		return -1
	}
	return v
}

// loaded records that this engine's index includes every change up to v.
func (c *changeLog) loaded(ctx context.Context, v int64, log *slog.Logger) {
	if c == nil || v < 0 {
		return
	}
	if err := c.rdb.HSet(ctx, loadedKey, c.instance, fmt.Sprintf("%d:%d", v, time.Now().UnixMilli())).Err(); err != nil {
		log.Warn("record loaded journeys", "err", err)
	}
}
