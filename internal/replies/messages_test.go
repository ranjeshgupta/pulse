package replies

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/tracking"
)

func TestLastMessage(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	if _, ok, err := Last(ctx, rdb, 1, "rcs", 5); ok || err != nil {
		t.Fatalf("nothing sent yet: ok=%v err=%v", ok, err)
	}
	for _, id := range []string{"7:m", "8:m"} {
		if err := SaveLast(ctx, rdb, 5, tracking.Token{TenantID: 1, Channel: "rcs", MessageID: id, JourneyID: 3}); err != nil {
			t.Fatal(err)
		}
	}
	tok, ok, err := Last(ctx, rdb, 1, "rcs", 5)
	if err != nil || !ok || tok.MessageID != "8:m" || tok.JourneyID != 3 {
		t.Fatalf("latest = %+v %v %v", tok, ok, err)
	}
	if _, ok, _ := Last(ctx, rdb, 2, "rcs", 5); ok {
		t.Error("another workspace's contact 5 must not see it")
	}
	mr.FastForward(ReplyWindow + 1)
	if _, ok, _ := Last(ctx, rdb, 1, "rcs", 5); ok {
		t.Error("a reply after the window is not matched")
	}
}
