package journey

// Automatic A/B winners of split steps. A WinnerPicker runs with the journey workers: for every
// active journey's split step with a winner setting and no decision yet, it reads each path's test
// stats from ClickHouse, and once the test window has passed and every path has enough contacts it
// records the winning path on the journey. The engine's next refresh (every 2 seconds) routes
// every later contact down that path.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"pulse/internal/analytics"
	"pulse/internal/model"
)

// WinnerStore is the Postgres side: active journeys, and recording a decision once.
type WinnerStore interface {
	ActiveJourneys(ctx context.Context) ([]model.Journey, error)
	RecordSplitWinner(ctx context.Context, tenant, journey int64, step string, decision []byte) (bool, error)
}

// SplitStats reads a split's per-path test results (ClickHouse).
type SplitStats interface {
	SplitStats(ctx context.Context, q analytics.SplitQuery) ([]analytics.PathStat, error)
}

type WinnerPicker struct {
	Store WinnerStore
	Stats SplitStats
	Log   *slog.Logger
	Now   func() time.Time
}

// Decisions decodes a journey's recorded winners.
func Decisions(raw json.RawMessage) (map[string]SplitDecision, error) {
	ds := map[string]SplitDecision{}
	if len(raw) == 0 || string(raw) == "null" {
		return ds, nil
	}
	err := json.Unmarshal(raw, &ds)
	return ds, err
}

// SplitTest is what the stats of a split step's test are read with.
func SplitTest(j model.Journey, g Graph, step string, metric string) analytics.SplitQuery {
	q := analytics.SplitQuery{Tenant: j.TenantID, Journey: j.ID, Step: step, Metric: metric, Since: j.StartAt.Add(-24 * time.Hour)}
	if metric == analytics.SplitConverted && g.Goal != nil {
		q.Goal = g.Goal.Event
	} else {
		q.Nodes = g.TrackedSendsAfter(step)
	}
	return q
}

// Run checks every interval until ctx is done.
func (p *WinnerPicker) Run(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := p.Check(ctx); err != nil && ctx.Err() == nil {
			p.Log.Error("journey A/B winners", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Check decides every split test that is due.
func (p *WinnerPicker) Check(ctx context.Context) error {
	js, err := p.Store.ActiveJourneys(ctx)
	if err != nil {
		return err
	}
	now := p.Now()
	for _, j := range js {
		g, err := ParseGraph(j.Graph)
		if err != nil {
			continue
		}
		decided, err := Decisions(j.SplitWinners)
		if err != nil {
			continue
		}
		for id, n := range g.Nodes {
			if n.Type != NodeSplit || n.Winner == nil {
				continue
			}
			if _, done := decided[id]; done {
				continue
			}
			d, ok, err := p.decide(ctx, j, g, id, n, now)
			if err != nil {
				p.Log.Error("journey A/B test", "journey", j.ID, "step", id, "err", err)
				continue
			}
			if !ok {
				continue
			}
			b, _ := json.Marshal(d)
			won, err := p.Store.RecordSplitWinner(ctx, j.TenantID, j.ID, id, b)
			if err != nil {
				return err
			}
			if won {
				p.Log.Info("journey A/B winner", "journey", j.ID, "step", id, "path", d.Path, "label", d.Label)
			}
		}
	}
	return nil
}

func (p *WinnerPicker) decide(ctx context.Context, j model.Journey, g Graph, id string, n Node, now time.Time) (SplitDecision, bool, error) {
	stats, err := p.Stats.SplitStats(ctx, SplitTest(j, g, id, n.Winner.Metric))
	if err != nil || len(stats) == 0 {
		return SplitDecision{}, false, err
	}
	first := stats[0].FirstAt
	for _, s := range stats {
		if s.FirstAt.Before(first) {
			first = s.FirstAt
		}
	}
	if now.Before(first.Add(time.Duration(n.Winner.AfterSeconds) * time.Second)) {
		return SplitDecision{}, false, nil // still testing
	}
	path, ok := analytics.PickWinner(stats, len(n.Variants), n.Winner.MinPerVariant)
	if !ok {
		return SplitDecision{}, false, nil // waiting for enough contacts on every path
	}
	for i := range stats {
		stats[i].Rate = analytics.Rate(stats[i])
	}
	v := n.Variants[path]
	return SplitDecision{Path: path, Next: v.Next, Label: v.Label, Metric: n.Winner.Metric, DecidedAt: now.UTC(), Stats: stats}, true, nil
}
