package rfm

import (
	"context"
	"testing"
	"time"

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
func (s *src) RFMBuyers(_ context.Context, _ int64, _, _ string, _ time.Time, fn func(Buyer) error) error {
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
