package rfm

import (
	"context"
	"testing"
	"time"

	"strings"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/event"
)

type src struct {
	cuts   Cuts
	buyers []Buyer
}

func (s *src) RFMCuts(context.Context, int64, string, string, time.Time, time.Time) (Cuts, error) {
	return s.cuts, nil
}
func (s *src) RFMBuyers(_ context.Context, _ int64, _, _ string, _, _ time.Time, fn func(Buyer) error) error {
	for _, b := range s.buyers {
		if err := fn(b); err != nil {
			return err
		}
	}
	return nil
}

func TestScoresAndGroups(t *testing.T) {
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	cuts := Cuts{Recency: [4]float64{10, 30, 90, 180}, Frequency: [4]float64{1, 2, 4, 8}, Monetary: [4]float64{500, 1000, 5000, 20000}, Buyers: 100}
	day := 24 * time.Hour
	champ := Of(Buyer{First: now.Add(-300 * day), Last: now.Add(-2 * day), Orders: 20, Revenue: 50000}, cuts, now)
	if champ.Group != Champions || champ.Score() != "555" || champ.Churn != RiskLow {
		t.Errorf("champion: %+v", champ)
	}
	gone := Of(Buyer{First: now.Add(-400 * day), Last: now.Add(-200 * day), Orders: 12, Revenue: 30000}, cuts, now)
	if gone.Group != CantLose || gone.Churn != RiskHigh {
		t.Errorf("can't lose: %+v", gone)
	}
	newbie := Of(Buyer{First: now.Add(-3 * day), Last: now.Add(-3 * day), Orders: 1, Revenue: 300}, cuts, now)
	if newbie.Group != New || newbie.Churn != RiskLow {
		t.Errorf("new: %+v", newbie)
	}
	if Churn(Buyer{First: now.Add(-700 * day), Last: now.Add(-400 * day), Orders: 3}, now) != RiskLost {
		t.Error("a year without buying is lost")
	}
	// Overdue against their own rhythm: every 10 days, last one 20 days ago.
	if c := Churn(Buyer{First: now.Add(-120 * day), Last: now.Add(-20 * day), Orders: 11}, now); c != RiskMedium {
		t.Errorf("rhythm: %s", c)
	}
	for r := 1; r <= 5; r++ {
		for f := 1; f <= 5; f++ {
			if Group(r, f) == "" {
				t.Errorf("no group for %d%d", r, f)
			}
		}
	}
}

func TestRefreshRecordsChangesOnly(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	s := &src{cuts: Cuts{Recency: [4]float64{10, 30, 90, 180}, Frequency: [4]float64{1, 2, 4, 8}, Monetary: [4]float64{500, 1000, 5000, 20000}, Buyers: 2},
		buyers: []Buyer{
			{Contact: 1, ExternalID: "a", First: now.AddDate(0, 0, -300), Last: now.AddDate(0, 0, -2), Orders: 20, Revenue: 50000},
			{Contact: 2, ExternalID: "b", First: now.AddDate(0, 0, -5), Last: now.AddDate(0, 0, -5), Orders: 1, Revenue: 100},
			{Contact: 3, ExternalID: ""}, // not yet in the contact snapshot
		}}
	var got []event.Event
	pub := func(_ context.Context, _ int64, evs []event.Event) error { got = append(got, evs...); return nil }
	n, ch, err := Refresh(context.Background(), s, rdb, pub, 7, "purchase", "amount", now)
	if err != nil || n != 2 || ch != 2 || got[0].Name != event.RFMChanged || got[0].Props["group"] != Champions {
		t.Fatalf("first pass: %d %d %v %+v", n, ch, err, got)
	}
	got = nil
	if _, ch, _ := Refresh(context.Background(), s, rdb, pub, 7, "purchase", "amount", now); ch != 0 || len(got) != 0 {
		t.Errorf("unchanged pass recorded %d", ch)
	}
	// Champion goes quiet: they move, and the event says where from.
	s.buyers[0].Last = now.AddDate(0, 0, -200)
	_, ch, _ = Refresh(context.Background(), s, rdb, pub, 7, "purchase", "amount", now)
	if ch != 1 || got[0].Props["previous_group"] != Champions || got[0].Props["group"] != CantLose {
		t.Errorf("move: %d %+v", ch, got)
	}
}

func TestThrottleAndDue(t *testing.T) {
	var batches []int
	pub := Throttled(func(_ context.Context, _ int64, evs []event.Event) error {
		batches = append(batches, len(evs))
		return nil
	}, 1000)
	start := time.Now()
	if err := pub(context.Background(), 1, make([]event.Event, 2500)); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); len(batches) != 3 || batches[2] != 500 || took < 2*time.Second {
		t.Errorf("batches %v in %v", batches, took)
	}
	at := time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)
	if !Due(29, at, time.Time{}, 24*time.Hour) || Due(30, at, time.Time{}, 24*time.Hour) || !Due(30, at, time.Time{}, time.Hour) {
		t.Error("Due")
	}
	// Out of its hour: caught up once the last pass is more than the interval and 2 hours old.
	if Due(30, at, at.Add(-25*time.Hour), 24*time.Hour) || !Due(30, at, at.Add(-27*time.Hour), 24*time.Hour) {
		t.Error("Due catch-up")
	}
}

func TestChurnSameDayOrders(t *testing.T) {
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	// Two orders on one day, 4 days ago: a one-time buyer, not overdue.
	if c := Churn(Buyer{First: now.Add(-4*day - time.Hour), Last: now.Add(-4 * day), Orders: 2}, now); c != RiskLow {
		t.Errorf("same day: %s", c)
	}
	// Orders two days apart, last one 10 days ago: the gap counts as a week, so not yet high.
	if c := Churn(Buyer{First: now.Add(-12 * day), Last: now.Add(-10 * day), Orders: 2}, now); c != RiskLow {
		t.Errorf("short gap: %s", c)
	}
}

func TestRefreshClearsBuyersOutOfWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	s := &src{cuts: Cuts{Recency: [4]float64{10, 30, 90, 180}, Frequency: [4]float64{1, 2, 4, 8}, Monetary: [4]float64{500, 1000, 5000, 20000}, Buyers: 2},
		buyers: []Buyer{
			{Contact: 1, ExternalID: "a", First: now.AddDate(0, 0, -300), Last: now.AddDate(0, 0, -2), Orders: 20, Revenue: 50000},
			{Contact: 2, ExternalID: "b", First: now.AddDate(0, 0, -5), Last: now.AddDate(0, 0, -5), Orders: 1, Revenue: 100},
		}}
	var got []event.Event
	pub := func(_ context.Context, _ int64, evs []event.Event) error { got = append(got, evs...); return nil }
	if _, _, err := Refresh(context.Background(), s, rdb, pub, 7, "purchase", "amount", now); err != nil {
		t.Fatal(err)
	}
	if v := mr.HGet("rfm:7", "1"); !strings.HasSuffix(v, "|a") {
		t.Errorf("stored state %q has no external id", v)
	}
	mr.HSet("rfm:7", "9", "Lost|111|lost") // an older entry without an external id
	got = nil
	s.buyers = s.buyers[:1] // b left the window
	_, ch, err := Refresh(context.Background(), s, rdb, pub, 7, "purchase", "amount", now)
	if err != nil || ch != 1 || len(got) != 1 || got[0].ExternalID != "b" || got[0].Props["cleared"] != true || got[0].Props["previous_group"] != New {
		t.Fatalf("cleared: %d %v %+v", ch, err, got)
	}
	if mr.HGet("rfm:7", "2") != "" || mr.HGet("rfm:7", "9") != "" || mr.HGet("rfm:7", "1") == "" {
		t.Errorf("left: %v", mr.HGet("rfm:7", "2")+mr.HGet("rfm:7", "9"))
	}
	// An older 3-field entry with the same scores: no event, the external id is added.
	mr.HSet("rfm:7", "1", strings.TrimSuffix(mr.HGet("rfm:7", "1"), "|a"))
	got = nil
	if _, ch, _ := Refresh(context.Background(), s, rdb, pub, 7, "purchase", "amount", now); ch != 0 || !strings.HasSuffix(mr.HGet("rfm:7", "1"), "|a") {
		t.Errorf("old entry: %d %q", ch, mr.HGet("rfm:7", "1"))
	}
}

func TestHold(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	release, ok := Hold(ctx, rdb, "rfmrun:1", time.Second)
	if !ok {
		t.Fatal("not held")
	}
	if _, ok := Hold(ctx, rdb, "rfmrun:1", time.Second); ok {
		t.Error("held twice")
	}
	time.Sleep(1500 * time.Millisecond) // the heartbeat keeps it set
	if ttl := mr.TTL("rfmrun:1"); ttl <= 0 {
		t.Errorf("no TTL: %v", ttl)
	}
	release()
	if mr.Exists("rfmrun:1") {
		t.Error("not released")
	}
	// A crashed holder: the key expires after its short TTL.
	_, _ = rdb.SetNX(ctx, "rfmrun:2", "x", LockTTL).Result()
	mr.FastForward(LockTTL + time.Second)
	if r, ok := Hold(ctx, rdb, "rfmrun:2", LockTTL); !ok {
		t.Error("stale lock still held")
	} else {
		r()
	}
}

func TestSpacing(t *testing.T) {
	day := 24 * time.Hour
	// On its hour: a day less the margin.
	if d := Spacing(5, time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC), day); d != day-15*time.Minute {
		t.Errorf("on time: %v", d)
	}
	// Caught up late: back on its hour the next day.
	if d := Spacing(5, time.Date(2026, 11, 21, 7, 10, 0, 0, time.UTC), day); d != 21*time.Hour+50*time.Minute {
		t.Errorf("late: %v", d)
	}
	if d := Spacing(5, time.Date(2026, 11, 21, 7, 10, 0, 0, time.UTC), time.Hour); d != 45*time.Minute {
		t.Errorf("hourly: %v", d)
	}
}
