// Package rfm is customer value (O6): every day each buyer's recency, frequency and monetary value
// over the last two years of the workspace's revenue event (Settings → Revenue: purchase / amount)
// is scored 1-5 against the workspace's own quintiles, named with the usual RFM groups (Champions,
// At risk, …), and given a churn risk from their own purchase rhythm. A contact whose group, score
// or risk changes gets an rfm_changed event, which sets rfm_group, rfm_score and churn_risk (for
// segments and personalisation) and can start a journey ("moved to At risk").
package rfm

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/event"
)

// Window is how far back purchases count.
const Window = 730 * 24 * time.Hour

// Buyer is one contact's purchases in the window.
type Buyer struct {
	Contact    uint32
	ExternalID string
	First      time.Time
	Last       time.Time
	Orders     uint64
	Revenue    float64
}

// Cuts are the workspace's quintile cut points (20/40/60/80 %): days since the last purchase,
// orders and revenue.
type Cuts struct {
	Recency, Frequency, Monetary [4]float64
	Buyers                       uint64
}

// Source reads purchases (ch.Store).
type Source interface {
	RFMCuts(ctx context.Context, tenant int64, event, amountProp string, since, now time.Time) (Cuts, error)
	RFMBuyers(ctx context.Context, tenant int64, event, amountProp string, since time.Time, fn func(Buyer) error) error
}

// Groups, best first.
const (
	Champions     = "Champions"
	Loyal         = "Loyal"
	Potential     = "Potential loyalists"
	New           = "New customers"
	Promising     = "Promising"
	NeedAttention = "Need attention"
	AboutToSleep  = "About to sleep"
	AtRisk        = "At risk"
	CantLose      = "Can't lose them"
	Hibernating   = "Hibernating"
	Lost          = "Lost"
)

// Groups lists every group, best first.
var Groups = []string{Champions, Loyal, Potential, New, Promising, NeedAttention, AboutToSleep, AtRisk, CantLose, Hibernating, Lost}

// grid names a buyer by recency (rows, 5 = most recent) and frequency (columns, 1..5).
var grid = [5][5]string{
	{Lost, Lost, AtRisk, AtRisk, CantLose},                    // R1
	{Hibernating, Hibernating, AtRisk, AtRisk, CantLose},      // R2
	{AboutToSleep, AboutToSleep, NeedAttention, Loyal, Loyal}, // R3
	{Promising, Potential, Potential, Loyal, Champions},       // R4
	{New, Potential, Potential, Loyal, Champions},             // R5
}

// Group names a buyer from their recency and frequency scores.
func Group(r, f int) string {
	return grid[clamp(r)-1][clamp(f)-1]
}

func clamp(s int) int { return max(1, min(5, s)) }

// score places v among the cut points: 1 (lowest fifth) to 5 (highest).
func score(v float64, cuts [4]float64) int {
	s := 1
	for _, c := range cuts {
		if v > c {
			s++
		}
	}
	return s
}

// Churn risks.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
	RiskLost   = "lost"
)

// Churn is a buyer's churn risk: against their own average gap between orders when they have
// bought more than once (overdue 1.5x: medium, 3x: high), else by days since the purchase (90 /
// 180); a year without buying is lost.
func Churn(b Buyer, now time.Time) string {
	since := now.Sub(b.Last).Hours() / 24
	if since > 365 {
		return RiskLost
	}
	if b.Orders >= 2 {
		gap := math.Max(b.Last.Sub(b.First).Hours()/24/float64(b.Orders-1), 1)
		switch r := since / gap; {
		case r >= 3:
			return RiskHigh
		case r >= 1.5:
			return RiskMedium
		}
		return RiskLow
	}
	switch {
	case since > 180:
		return RiskHigh
	case since > 90:
		return RiskMedium
	}
	return RiskLow
}

// Result is one buyer's scores.
type Result struct {
	R, F, M int
	Group   string
	Churn   string
}

// Score is the "RFM" string, e.g. "545".
func (r Result) Score() string { return fmt.Sprintf("%d%d%d", r.R, r.F, r.M) }

// Of scores a buyer against the workspace's cut points.
func Of(b Buyer, c Cuts, now time.Time) Result {
	days := now.Sub(b.Last).Hours() / 24
	r := 6 - score(days, c.Recency) // fewer days is better
	f := score(float64(b.Orders), c.Frequency)
	m := score(b.Revenue, c.Monetary)
	return Result{R: r, F: f, M: m, Group: Group(r, f), Churn: Churn(b, now)}
}

func key(tenant int64) string { return "rfm:" + strconv.FormatInt(tenant, 10) }

func state(r Result) string { return r.Group + "|" + r.Score() + "|" + r.Churn }

// Publisher sends rfm_changed events (to events.raw).
type Publisher func(ctx context.Context, tenant int64, evs []event.Event) error

// DefaultPerSec is how many rfm_changed events a pass sends a second at most (first passes over a
// big workspace change every buyer, and must not crowd out live events).
const DefaultPerSec = 500

// Throttled sends through pub at most perSec events a second.
func Throttled(pub Publisher, perSec int) Publisher {
	if perSec <= 0 {
		perSec = DefaultPerSec
	}
	return func(ctx context.Context, tenant int64, evs []event.Event) error {
		for len(evs) > 0 {
			n := min(len(evs), perSec)
			start := time.Now()
			if err := pub(ctx, tenant, evs[:n]); err != nil {
				return err
			}
			evs = evs[n:]
			wait := time.Duration(float64(n)/float64(perSec)*float64(time.Second)) - time.Since(start)
			if wait > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
			}
		}
		return nil
	}
}

// Due reports whether a workspace's daily pass runs in this UTC hour: each workspace has its
// hour (id % 24), so passes spread over the day. Intervals under a day run every time.
func Due(tenant int64, now time.Time, interval time.Duration) bool {
	return interval < 24*time.Hour || int64(now.UTC().Hour()) == tenant%24
}

// Refresh scores one workspace's buyers and records rfm_changed for those whose group, score or
// churn risk changed since the last pass (kept in Redis). It returns the buyers scored and changed.
func Refresh(ctx context.Context, src Source, rdb redis.Cmdable, pub Publisher, tenant int64, evName, amountProp string, now time.Time) (int, int, error) {
	since := now.Add(-Window)
	cuts, err := src.RFMCuts(ctx, tenant, evName, amountProp, since, now)
	if err != nil || cuts.Buyers == 0 {
		return 0, 0, err
	}
	k := key(tenant)
	type pending struct {
		contact string
		state   string
		ev      event.Event
	}
	var batch []pending
	n, changed := 0, 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		fields := make([]string, len(batch))
		for i, p := range batch {
			fields[i] = p.contact
		}
		prev, err := rdb.HMGet(ctx, k, fields...).Result()
		if err != nil {
			return err
		}
		var evs []event.Event
		pipe := rdb.Pipeline()
		for i, p := range batch {
			old, _ := prev[i].(string)
			if old == p.state {
				continue
			}
			if og, _, ok := strings.Cut(old, "|"); ok {
				p.ev.Props["previous_group"] = og
			}
			evs = append(evs, p.ev)
			pipe.HSet(ctx, k, p.contact, p.state)
		}
		batch = batch[:0]
		if len(evs) == 0 {
			return nil
		}
		// Events first: if publishing fails, the state isn't stored and the next pass retries.
		if err := pub(ctx, tenant, evs); err != nil {
			return err
		}
		changed += len(evs)
		_, err = pipe.Exec(ctx)
		return err
	}
	err = src.RFMBuyers(ctx, tenant, evName, amountProp, since, func(b Buyer) error {
		if b.ExternalID == "" {
			return nil
		}
		n++
		r := Of(b, cuts, now)
		batch = append(batch, pending{contact: strconv.FormatUint(uint64(b.Contact), 10), state: state(r), ev: event.Event{
			ExternalID: b.ExternalID, Name: event.RFMChanged, Ts: now,
			Props: map[string]any{"group": r.Group, "score": r.Score(), "r": r.R, "f": r.F, "m": r.M, "churn_risk": r.Churn,
				"orders": b.Orders, "revenue": math.Round(b.Revenue*100) / 100, "days_since_last": int(now.Sub(b.Last).Hours() / 24)},
		}})
		if len(batch) == 1000 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return n, changed, err
	}
	return n, changed, flush()
}
