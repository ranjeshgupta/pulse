package besttime

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type src struct{}

func (src) BestHours(_ context.Context, _ int64, _ time.Time, fn func(uint32, uint8) error) error {
	return fn(7, 19)
}
func (src) DefaultHour(context.Context, int64, time.Time) (uint8, bool, error) { return 9, true, nil }

func TestRefreshAndHour(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	if n, err := Refresh(context.Background(), src{}, rdb, 1, time.Now()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if h, ok := Hour(context.Background(), rdb, 1, 7); !ok || h != 19 {
		t.Errorf("contact 7: %d %v", h, ok)
	}
	if h, ok := Hour(context.Background(), rdb, 1, 8); !ok || h != 9 {
		t.Errorf("no history gets the workspace's hour: %d %v", h, ok)
	}
	if _, ok := Hour(context.Background(), rdb, 2, 8); ok {
		t.Error("an unknown workspace has no hour")
	}
	from := time.Date(2026, 11, 21, 20, 30, 0, 0, time.UTC)
	if got := NextAt(from, 19); !got.Equal(time.Date(2026, 11, 22, 19, 0, 0, 0, time.UTC)) {
		t.Errorf("next 19:00 = %v", got)
	}
	if got := NextAt(from, 21); !got.Equal(time.Date(2026, 11, 21, 21, 0, 0, 0, time.UTC)) {
		t.Errorf("next 21:00 = %v", got)
	}
}
