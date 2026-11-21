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
	"slices"
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
	RFMBuyers(ctx context.Context, tenant int64, event, amountProp string, since, now time.Time, fn func(Buyer) error) error
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

// Churn is a buyer's churn risk: against their own average gap between orders (at least a week)
// when they have bought on more than one day (overdue 1.5x: medium, 3x: high), else by days since
// the purchase (90 / 180); a year without buying is lost.
func Churn(b Buyer, now time.Time) string {
	since := now.Sub(b.Last).Hours() / 24
	if since > 365 {
		return RiskLost
	}
	if b.Orders >= 2 && b.Last.Sub(b.First) >= 24*time.Hour {
		gap := math.Max(b.Last.Sub(b.First).Hours()/24/float64(b.Orders-1), 7)
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

// state is what Redis keeps per buyer: "group|score|risk|external id" (older entries have no
// external id). Changes are about the first three fields only.
func state(r Result, extID string) string {
	return r.Group + "|" + r.Score() + "|" + r.Churn + "|" + extID
}

// parseState splits a stored state into its scores ("group|score|risk") and external id.
func parseState(s string) (scores, extID string) {
	parts := strings.SplitN(s, "|", 4)
	if len(parts) < 3 {
		return s, ""
	}
	if len(parts) == 4 {
		extID = parts[3]
	}
	return strings.Join(parts[:3], "|"), extID
}

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

// Due reports whether a workspace's pass may run now: each workspace has its UTC hour (id % 24),
// so passes spread over the day; one whose last good pass (last, zero if none) is more than the
// interval and 2 hours old is caught up at any hour. Intervals under a day run every time.
func Due(tenant int64, now, last time.Time, interval time.Duration) bool {
	if interval < 24*time.Hour || int64(now.UTC().Hour()) == tenant%24 {
		return true
	}
	return !last.IsZero() && now.Sub(last) > interval+2*time.Hour
}

// Spacing is how long a pass started at now holds off the next (the "done" marker's TTL): the
// interval less 15 minutes, but for daily or longer intervals no later than the workspace's hour
// once the whole days of the interval have passed, so a pass that ran late gets back on its hour.
func Spacing(tenant int64, now time.Time, interval time.Duration) time.Duration {
	d := max(interval-15*time.Minute, 5*time.Minute)
	if interval < 24*time.Hour {
		return d
	}
	now = now.UTC()
	base := now.Add(interval.Truncate(24*time.Hour) - 24*time.Hour)
	slot := time.Date(base.Year(), base.Month(), base.Day(), int(tenant%24), 0, 0, 0, time.UTC)
	if !slot.After(base) {
		slot = slot.AddDate(0, 0, 1)
	}
	return max(min(d, slot.Sub(now)), 5*time.Minute)
}

// LockTTL is how long a pass's lock lives without its heartbeat (a crashed pass frees it by then).
const LockTTL = 10 * time.Minute

var unlock = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`)
var extend = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) end return 0`)

// Hold takes key as a lock for ttl and keeps it alive (every ttl/3) until release is called; ok
// is false when someone else holds it. The heartbeat outlives ctx's cancellation (a request that
// started a background pass); only release stops it.
func Hold(ctx context.Context, rdb redis.Cmdable, key string, ttl time.Duration) (release func(), ok bool) {
	token := strconv.FormatInt(time.Now().UnixNano(), 36)
	if ok, err := rdb.SetNX(ctx, key, token, ttl).Result(); err != nil || !ok {
		return func() {}, false
	}
	hb, stop := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(ttl / 3)
		defer t.Stop()
		for {
			select {
			case <-hb.Done():
				return
			case <-t.C:
				extend.Run(hb, rdb, []string{key}, token, ttl.Milliseconds())
			}
		}
	}()
	return func() {
		stop()
		<-done
		unlock.Run(context.WithoutCancel(ctx), rdb, []string{key}, token)
	}, true
}

func lastKey(tenant int64) string { return "rfmlast:" + strconv.FormatInt(tenant, 10) }

// LastPass is when a workspace's last good pass ran (zero if none is recorded).
func LastPass(ctx context.Context, rdb redis.Cmdable, tenant int64) time.Time {
	n, err := rdb.Get(ctx, lastKey(tenant)).Int64()
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// Passed records a good pass (kept 60 days).
func Passed(ctx context.Context, rdb redis.Cmdable, tenant int64, at time.Time) error {
	return rdb.Set(ctx, lastKey(tenant), at.Unix(), 60*24*time.Hour).Err()
}

// Refresh scores one workspace's buyers and records rfm_changed for those whose group, score or
// churn risk changed since the last pass (kept in Redis). Buyers scored before but no longer in
// the window get an rfm_changed with "cleared": true (their scores are removed). It returns the
// buyers scored and changed.
func Refresh(ctx context.Context, src Source, rdb redis.Cmdable, pub Publisher, tenant int64, evName, amountProp string, now time.Time) (int, int, error) {
	since := now.Add(-Window)
	cuts, err := src.RFMCuts(ctx, tenant, evName, amountProp, since, now)
	if err != nil {
		return 0, 0, err
	}
	k := key(tenant)
	if cuts.Buyers == 0 {
		cleared, err := clearUnseen(ctx, rdb, pub, tenant, k, nil, now)
		return 0, cleared, err
	}
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
		writes := 0
		for i, p := range batch {
			old, _ := prev[i].(string)
			if old == p.state {
				continue
			}
			pipe.HSet(ctx, k, p.contact, p.state)
			writes++
			oldScores, _ := parseState(old)
			if newScores, _ := parseState(p.state); oldScores == newScores {
				continue // only the external id is new (an older entry)
			}
			if og, _, ok := strings.Cut(old, "|"); ok {
				p.ev.Props["previous_group"] = og
			}
			evs = append(evs, p.ev)
		}
		batch = batch[:0]
		if writes == 0 {
			return nil
		}
		// Events first: if publishing fails, the state isn't stored and the next pass retries.
		if len(evs) > 0 {
			if err := pub(ctx, tenant, evs); err != nil {
				return err
			}
		}
		changed += len(evs)
		_, err = pipe.Exec(ctx)
		return err
	}
	var seen []uint32 // buyers in this pass (sorted after it)
	err = src.RFMBuyers(ctx, tenant, evName, amountProp, since, now, func(b Buyer) error {
		seen = append(seen, b.Contact)
		if b.ExternalID == "" {
			return nil
		}
		n++
		r := Of(b, cuts, now)
		batch = append(batch, pending{contact: strconv.FormatUint(uint64(b.Contact), 10), state: state(r, b.ExternalID), ev: event.Event{
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
	if err := flush(); err != nil {
		return n, changed, err
	}
	slices.Sort(seen)
	cleared, err := clearUnseen(ctx, rdb, pub, tenant, k, seen, now)
	return n, changed + cleared, err
}

// clearUnseen removes the stored state of buyers not in this pass (seen, sorted): those with an
// external id get an rfm_changed that clears their scores first.
func clearUnseen(ctx context.Context, rdb redis.Cmdable, pub Publisher, tenant int64, k string, seen []uint32, now time.Time) (int, error) {
	cleared := 0
	var cursor uint64
	for {
		kv, next, err := rdb.HScan(ctx, k, cursor, "", 1000).Result()
		if err != nil {
			return cleared, err
		}
		var gone []string
		var evs []event.Event
		for i := 0; i+1 < len(kv); i += 2 {
			id, err := strconv.ParseUint(kv[i], 10, 32)
			if err == nil {
				if _, found := slices.BinarySearch(seen, uint32(id)); found {
					continue
				}
			}
			gone = append(gone, kv[i])
			if _, ext := parseState(kv[i+1]); ext != "" {
				g, _, _ := strings.Cut(kv[i+1], "|")
				evs = append(evs, event.Event{ExternalID: ext, Name: event.RFMChanged, Ts: now,
					Props: map[string]any{"cleared": true, "previous_group": g}})
			}
		}
		if len(evs) > 0 {
			if err := pub(ctx, tenant, evs); err != nil {
				return cleared, err
			}
			cleared += len(evs)
		}
		if len(gone) > 0 {
			if err := rdb.HDel(ctx, k, gone...).Err(); err != nil {
				return cleared, err
			}
		}
		if next == 0 {
			return cleared, nil
		}
		cursor = next
	}
}
