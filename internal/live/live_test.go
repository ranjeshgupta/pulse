package live

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/event"
)

func TestFeedIsCappedAndTenantScoped(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	f := Feed{RDB: rdb, Now: func() time.Time { return now }}
	var evs []event.Event
	for i := range MaxPerSecond + 10 {
		evs = append(evs, event.Event{ID: fmt.Sprint("a", i), TenantID: 1, Name: "page_view", ExternalID: "u1", Ts: now,
			Props: map[string]any{"url": "/x"}})
	}
	evs = append(evs, event.Event{ID: "b1", TenantID: 2, Name: "purchase", ExternalID: "u9", Ts: now})
	if err := f.Push(ctx, evs); err != nil {
		t.Fatal(err)
	}
	items, cur, err := Read(ctx, rdb, 1, "", 500)
	if err != nil || len(items) != MaxPerSecond {
		t.Fatalf("tenant 1 got %d items (%v), want the cap %d", len(items), err, MaxPerSecond)
	}
	if items[len(items)-1].ID != fmt.Sprint("a", MaxPerSecond+9) || string(items[0].Props) != `{"url":"/x"}` {
		t.Errorf("the newest events are kept: %+v", items[len(items)-1])
	}
	for _, it := range items {
		if it.Name != "page_view" {
			t.Fatalf("another workspace's event leaked: %+v", it)
		}
	}
	// Same second: the cap is used up.
	_ = f.Push(ctx, []event.Event{{ID: "late", TenantID: 1, Name: "x", Ts: now}})
	if more, _, _ := Read(ctx, rdb, 1, cur, 10); len(more) != 0 {
		t.Errorf("over the per-second cap: %v", more)
	}
	now = now.Add(time.Second)
	_ = f.Push(ctx, []event.Event{{ID: "next", TenantID: 1, Name: "signup", Ts: now, Props: map[string]any{"big": string(make([]byte, MaxProps))}}})
	more, cur2, _ := Read(ctx, rdb, 1, cur, 10)
	if len(more) != 1 || more[0].ID != "next" || !more[0].Truncated || cur2 == cur {
		t.Errorf("after the cursor: %+v", more)
	}
	if other, _, _ := Read(ctx, rdb, 2, "", 10); len(other) != 1 || other[0].ID != "b1" {
		t.Errorf("tenant 2: %+v", other)
	}
	if !ValidCursor("1800000000000-3") || ValidCursor("1-x") || ValidCursor("+") {
		t.Error("cursor check")
	}
}

// A workspace's own feed size (Settings → Automation & limits) caps its stream; others keep Keep.
func TestFeedSizePerWorkspace(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	if err := SetSize(ctx, rdb, 1, 100); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	f := Feed{RDB: rdb, Now: func() time.Time { return now }}
	for s := range 4 {
		var evs []event.Event
		for i := range MaxPerSecond {
			for _, tenant := range []int64{1, 2} {
				evs = append(evs, event.Event{ID: fmt.Sprint(s, "-", i), TenantID: tenant, Name: "x", Ts: now})
			}
		}
		if err := f.Push(ctx, evs); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	if n, _ := rdb.XLen(ctx, Key(1)).Result(); n > 100 {
		t.Errorf("workspace 1 keeps %d, want at most its size 100", n)
	}
	if n, _ := rdb.XLen(ctx, Key(2)).Result(); n != 4*MaxPerSecond {
		t.Errorf("workspace 2 keeps %d, want all %d (under Keep)", n, 4*MaxPerSecond)
	}
	// back to the default: the key goes away
	if err := SetSize(ctx, rdb, 1, Keep); err != nil || mr.Exists(SizeKey(1)) {
		t.Errorf("default size must remove the key (%v)", err)
	}
	if sizeOf("junk") != Keep || sizeOf("20000") != Keep || sizeOf("250") != 250 {
		t.Error("sizeOf")
	}
}
