package segment

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestBuildMembersRoundTrip(t *testing.T) {
	ids := []uint32{0, 1, 7, 8, 9, 63, 64, 1000}
	b := Build(ids)
	if got := Members(b); !reflect.DeepEqual(got, ids) {
		t.Fatalf("Members = %v, want %v", got, ids)
	}
	for _, id := range ids {
		if !Has(b, id) {
			t.Errorf("Has(%d) = false", id)
		}
	}
	if Has(b, 2) || Has(b, 5000) {
		t.Error("Has reports a bit that is not set")
	}
	if Build(nil) != nil {
		t.Error("Build(nil) should be nil")
	}
}

func TestDiff(t *testing.T) {
	old := Build([]uint32{1, 2, 3, 100})
	cur := Build([]uint32{2, 3, 4, 5000})
	entered, exited := Diff(old, cur)
	if want := []uint32{4, 5000}; !reflect.DeepEqual(entered, want) {
		t.Errorf("entered = %v, want %v", entered, want)
	}
	if want := []uint32{1, 100}; !reflect.DeepEqual(exited, want) {
		t.Errorf("exited = %v, want %v", exited, want)
	}
	if e, x := Diff(cur, cur); e != nil || x != nil {
		t.Errorf("Diff of equal bitmaps = %v, %v", e, x)
	}
}

// A bitmap built in Go and stored with SET must read back correctly with GETBIT/BITCOUNT.
func TestBitOrderMatchesRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	ids := []uint32{0, 3, 9, 17, 1023}
	if err := rdb.Set(ctx, "seg", Build(ids), 0).Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if v, _ := rdb.GetBit(ctx, "seg", int64(id)).Result(); v != 1 {
			t.Errorf("GETBIT %d = %d, want 1", id, v)
		}
	}
	if v, _ := rdb.GetBit(ctx, "seg", 4).Result(); v != 0 {
		t.Error("GETBIT 4 = 1, want 0")
	}
	if n, _ := rdb.BitCount(ctx, "seg", nil).Result(); n != int64(len(ids)) {
		t.Errorf("BITCOUNT = %d, want %d", n, len(ids))
	}

	// And the other way: SETBIT in Redis, decode in Go.
	rdb.SetBit(ctx, "seg2", 42, 1)
	raw, _ := rdb.Get(ctx, "seg2").Bytes()
	if got := Members(raw); !reflect.DeepEqual(got, []uint32{42}) {
		t.Errorf("Members(SETBIT 42) = %v", got)
	}
}

func TestSumWindow(t *testing.T) {
	now := time.Date(2026, 11, 10, 23, 59, 0, 0, time.UTC)
	buckets := map[string]string{
		"20261110": "2", // today
		"20261104": "1", // 6 days ago: inside a 7-day window
		"20261103": "5", // 7 days ago: outside
		"20261111": "9", // tomorrow: ignored
		"junk":     "x",
	}
	if got := SumWindow(buckets, now, 7); got != 3 {
		t.Errorf("7-day sum = %d, want 3", got)
	}
	if got := SumWindow(buckets, now, 1); got != 2 {
		t.Errorf("1-day sum = %d, want 2", got)
	}
	if got := SumWindow(buckets, now, 8); got != 8 {
		t.Errorf("8-day sum = %d, want 8", got)
	}
}
