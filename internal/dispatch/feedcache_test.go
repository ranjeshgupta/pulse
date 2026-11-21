package dispatch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pulse/internal/model"
	"pulse/internal/store/pg"
)

type feedRunStore struct {
	Store
	kept map[int64]string // run (unix seconds) -> feed html
}

func (f *feedRunStore) RunFeed(_ context.Context, _ int64, at time.Time) (string, string, error) {
	h, ok := f.kept[at.Unix()]
	if !ok {
		return "", "", pg.ErrNotFound
	}
	return h, "", nil
}

func TestFeedBlockNotFoundIsShortLived(t *testing.T) {
	st := &feedRunStore{kept: map[int64]string{}}
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	d := &Dispatcher{store: st, now: func() time.Time { return now }}
	run := now.Unix()
	m := model.Message{TenantID: 1, BroadcastID: 4, Run: run}
	if b := d.messageBlocks(context.Background(), m); b[FeedBlock].HTML != "" {
		t.Fatalf("feed before the run is kept: %v", b)
	}
	st.kept[run] = "<p>news</p>"
	now = now.Add(2 * time.Minute)
	if b := d.messageBlocks(context.Background(), m); b[FeedBlock].HTML != "<p>news</p>" {
		t.Errorf("a missing run was cached too long: %v", b)
	}
}

func TestStoreBounded(t *testing.T) {
	var m sync.Map
	var n atomic.Int64
	for i := range 25 {
		storeBounded(&m, &n, 10, fmt.Sprint(i), i)
	}
	size := 0
	m.Range(func(_, _ any) bool { size++; return true })
	if size > 10 || size == 0 {
		t.Errorf("cache holds %d entries, limit 10", size)
	}
	if _, ok := m.Load("24"); !ok {
		t.Error("latest entry dropped")
	}
}
