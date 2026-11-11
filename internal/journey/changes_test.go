package journey

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Announce returns once the engine has loaded the change, and gives up after the wait otherwise.
func TestAnnounceWaitsForEngines(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cl := &changeLog{rdb: rdb, instance: "e1"}
	cl.loaded(ctx, cl.version(ctx), log) // the engine is running, at version 0

	// Nobody reloads: Announce times out (the change still lands at the next periodic refresh).
	if ok, err := Announce(ctx, rdb, 60*time.Millisecond); err != nil || ok {
		t.Fatalf("no reload: ok=%v err=%v", ok, err)
	}
	// The engine reloads while Announce waits.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cl.loaded(ctx, cl.version(ctx), log)
	}()
	start := time.Now()
	if ok, err := Announce(ctx, rdb, 2*time.Second); err != nil || !ok {
		t.Fatalf("reload: ok=%v err=%v", ok, err)
	}
	if time.Since(start) > time.Second {
		t.Error("Announce waited too long")
	}
}
