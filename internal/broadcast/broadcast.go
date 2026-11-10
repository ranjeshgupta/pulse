// Package broadcast sends scheduled broadcasts: when a run is due, every current member of the
// segment gets a send request on the channel's topic, and the next run is scheduled.
//
// A/B tests: a run sends each variant to its share of a test group. With a winner step, the rest of
// the audience waits until the winner is known (best open or click rate after a wait, or picked by
// hand) and then gets the winning variant. A contact's group and variant come from a hash of
// (broadcast, run, contact), so retries and the winner step agree on who was in the test.
package broadcast

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/segment"
)

const chunk = 5000

// A/B metrics.
const (
	MetricOpens  = "opens"
	MetricClicks = "clicks"
	MetricManual = "manual"
)

type Store interface {
	ClaimDueBroadcast(ctx context.Context) (model.Broadcast, bool, error)
	StartRun(ctx context.Context, broadcast int64, scheduledAt time.Time) (int64, error)
	// FinishRun records the first send of a run; winnerDue is set when an automatic winner step follows.
	FinishRun(ctx context.Context, run int64, recipients, testRecipients int, winnerDue *time.Time) error
	// AdvanceBroadcast releases the claim: scheduled for next, else testing (a winner step is still
	// to come) or completed.
	AdvanceBroadcast(ctx context.Context, id int64, next *time.Time, testing bool) error
	ClaimDueWinner(ctx context.Context) (WinnerRun, bool, error)
	FinishWinner(ctx context.Context, run, broadcast int64, winner, reason string, recipients int) error
}

// WinnerRun is an A/B run whose winner is due to be sent (Winner is set when picked by hand).
type WinnerRun struct {
	RunID       int64
	ScheduledAt time.Time
	Winner      string
	Broadcast   model.Broadcast
}

type Members interface {
	Load(ctx context.Context, tenant, segment int64) ([]byte, error)
}

// Stats reports outcomes per variant of one run (from the event log).
type Stats interface {
	VariantCounts(ctx context.Context, tenant, broadcast, run int64) (map[string]model.MessageCounts, error)
}

type Runner struct {
	Store   Store
	Members Members
	Stats   Stats // needed for automatic A/B winners
	Pub     kafka.Publisher
	Log     *slog.Logger
	Now     func() time.Time
}

// RunDue sends every broadcast run, and every A/B winner, that is due now.
func (r *Runner) RunDue(ctx context.Context) error {
	for ctx.Err() == nil {
		b, ok, err := r.Store.ClaimDueBroadcast(ctx)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if err := r.run(ctx, b); err != nil {
			// The claim expires and another worker retries; message ids keep it from double-sending.
			r.Log.Error("broadcast run failed", "broadcast", b.ID, "err", err)
		}
	}
	for ctx.Err() == nil {
		w, ok, err := r.Store.ClaimDueWinner(ctx)
		if err != nil || !ok {
			return err
		}
		if err := r.sendWinner(ctx, w); err != nil {
			r.Log.Error("broadcast winner failed", "broadcast", w.Broadcast.ID, "run", w.RunID, "err", err)
		}
	}
	return ctx.Err()
}

// Bucket places a contact in 0..9999 for one run: below TestPercent*100 is the test group, and
// within it the bucket picks the variant.
func Bucket(broadcast, run int64, contact uint32) int {
	h := fnv.New32a()
	fmt.Fprintf(h, "%d:%d:%d", broadcast, run, contact)
	return int(h.Sum32() % 10000)
}

// WinnerVariant labels the winner step's messages, so reports keep the test results apart from
// the send to everyone else (the run records which variant won).
const WinnerVariant = "winner"

// HasWinnerStep reports whether an A/B test sends a winner to the rest of the audience.
func HasWinnerStep(ab *model.ABTest) bool { return ab != nil && ab.TestPercent < 100 }

func (r *Runner) run(ctx context.Context, b model.Broadcast) error {
	at := *b.NextRunAt
	runID, err := r.Store.StartRun(ctx, b.ID, at)
	if err != nil {
		return err
	}
	bitmap, err := r.Members.Load(ctx, b.TenantID, b.SegmentID)
	if err != nil {
		return err
	}
	runKey := at.Unix()
	members := segment.Members(bitmap)
	send := make([]model.Message, 0, min(chunk, len(members)))
	sent, tested := 0, 0
	flush := func() error {
		if err := r.publish(ctx, send); err != nil {
			return err
		}
		send = send[:0]
		return nil
	}
	for _, contact := range members {
		m := r.message(b, runKey, contact, b.TemplateID, "")
		if b.AB != nil {
			bk := Bucket(b.ID, runKey, contact)
			if bk >= b.AB.TestPercent*100 {
				continue // waits for the winner
			}
			v := b.AB.Variants[bk%len(b.AB.Variants)]
			m.TemplateID, m.Variant = v.TemplateID, v.Label
			tested++
		}
		send = append(send, m)
		sent++
		if len(send) == chunk {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	var winnerDue *time.Time
	if HasWinnerStep(b.AB) && b.AB.Metric != MetricManual {
		t := r.Now().Add(time.Duration(b.AB.WaitHours) * time.Hour)
		winnerDue = &t
	}
	if err := r.Store.FinishRun(ctx, runID, sent, tested, winnerDue); err != nil {
		return err
	}
	var next *time.Time
	if t, ok := schedule.Next(schedule.Window{Start: b.StartAt, End: b.EndAt}, b.Repeat, at); ok {
		next = &t
	}
	r.Log.Info("broadcast sent", "broadcast", b.ID, "recipients", sent, "ab_test", b.AB != nil, "next", next)
	return r.Store.AdvanceBroadcast(ctx, b.ID, next, HasWinnerStep(b.AB))
}

// sendWinner picks the winner if no one has, and sends it to everyone outside the test group.
func (r *Runner) sendWinner(ctx context.Context, w WinnerRun) error {
	b := w.Broadcast
	if b.AB == nil || b.Status == "cancelled" {
		return r.Store.FinishWinner(ctx, w.RunID, b.ID, w.Winner, "not sent: the broadcast was cancelled or is no longer an A/B test", 0)
	}
	runKey := w.ScheduledAt.Unix()
	winner, reason := w.Winner, "picked by hand"
	if winner == "" {
		if r.Stats == nil {
			return errors.New("no stats source for automatic winners")
		}
		counts, err := r.Stats.VariantCounts(ctx, b.TenantID, b.ID, runKey)
		if err != nil {
			return err
		}
		winner, reason = PickWinner(b.AB, counts)
	}
	var tpl int64
	for _, v := range b.AB.Variants {
		if v.Label == winner {
			tpl = v.TemplateID
		}
	}
	if tpl == 0 {
		return r.Store.FinishWinner(ctx, w.RunID, b.ID, winner, "not sent: variant "+winner+" no longer exists", 0)
	}
	bitmap, err := r.Members.Load(ctx, b.TenantID, b.SegmentID)
	if err != nil {
		return err
	}
	var send []model.Message
	n := 0
	for _, contact := range segment.Members(bitmap) {
		if Bucket(b.ID, runKey, contact) < b.AB.TestPercent*100 {
			continue // was in the test
		}
		send = append(send, r.message(b, runKey, contact, tpl, WinnerVariant))
		n++
		if len(send) == chunk {
			if err := r.publish(ctx, send); err != nil {
				return err
			}
			send = send[:0]
		}
	}
	if err := r.publish(ctx, send); err != nil {
		return err
	}
	r.Log.Info("broadcast winner sent", "broadcast", b.ID, "run", w.RunID, "winner", winner, "recipients", n)
	return r.Store.FinishWinner(ctx, w.RunID, b.ID, winner, reason, n)
}

// PickWinner returns the variant with the best open (or click) rate among its sent messages, and
// why. Ties and a test without data go to the first variant.
func PickWinner(ab *model.ABTest, counts map[string]model.MessageCounts) (string, string) {
	best, bestRate := ab.Variants[0].Label, -1.0
	var parts []string
	verb := "opened"
	if ab.Metric == MetricClicks {
		verb = "clicked"
	}
	for _, v := range ab.Variants {
		c := counts[v.Label]
		hits := c.Opened
		if ab.Metric == MetricClicks {
			hits = c.Clicked
		}
		rate := 0.0
		if c.Sent > 0 {
			rate = float64(hits) / float64(c.Sent)
		}
		parts = append(parts, fmt.Sprintf("%s %.1f%% of %d", v.Label, rate*100, c.Sent))
		if rate > bestRate {
			best, bestRate = v.Label, rate
		}
	}
	return best, fmt.Sprintf("best %s rate: %s", verb, strings.Join(parts, ", "))
}

// message is one send request. One id per (broadcast, run, contact): a retried run is deduplicated by
// the dispatcher, and the winner step never re-sends to the test group.
func (r *Runner) message(b model.Broadcast, run int64, contact uint32, template int64, variant string) model.Message {
	return model.Message{ID: fmt.Sprintf("bc%d:%d:%d", b.ID, run, contact), TenantID: b.TenantID, ContactID: contact,
		Channel: b.Channel, TemplateID: template, FallbackTemplateID: b.FallbackTemplateID,
		BroadcastID: b.ID, Run: run, Variant: variant}
}

func (r *Runner) publish(ctx context.Context, ms []model.Message) error {
	if len(ms) == 0 {
		return nil
	}
	recs := make([]*kgo.Record, len(ms))
	for i, m := range ms {
		rec, err := kafka.JSON(channel.Topic(m.Channel), event.ContactKey(m.TenantID, m.ContactID), m)
		if err != nil {
			return err
		}
		recs[i] = rec
	}
	return r.Pub.Publish(ctx, recs...)
}

// ValidateAB checks an A/B test against the broadcast's channel. Template channels are checked by the caller.
func ValidateAB(ab *model.ABTest, ch string) error {
	if ab == nil {
		return nil
	}
	var errs []error
	if len(ab.Variants) < 2 || len(ab.Variants) > 5 {
		errs = append(errs, errors.New("an A/B test needs 2 to 5 variants"))
	}
	seen := map[string]bool{}
	for _, v := range ab.Variants {
		if strings.TrimSpace(v.Label) == "" || len(v.Label) > 20 || seen[v.Label] || v.Label == WinnerVariant {
			errs = append(errs, errors.New("give each variant a different short label"))
			break
		}
		seen[v.Label] = true
	}
	if ab.TestPercent < 2 || ab.TestPercent > 100 {
		errs = append(errs, errors.New("the test group must be 2-100% of the audience"))
	}
	if HasWinnerStep(ab) {
		switch ab.Metric {
		case MetricOpens, MetricClicks:
			if ch != channel.Email {
				errs = append(errs, errors.New("only email tracks opens and clicks; pick the winner by hand for other channels"))
			}
			if ab.WaitHours < 1 || ab.WaitHours > 72 {
				errs = append(errs, errors.New("wait 1-72 hours before picking the winner"))
			}
		case MetricManual:
		default:
			errs = append(errs, errors.New("pick the winner by opens, clicks or by hand"))
		}
	}
	return errors.Join(errs...)
}

// FirstRun is when a broadcast scheduled now should first send: its start, or right away if the start
// has passed (a one-time broadcast), or the next repeat after now.
func FirstRun(b model.Broadcast, now time.Time) (time.Time, bool) {
	w := schedule.Window{Start: b.StartAt, End: b.EndAt}
	if now.Before(b.StartAt) {
		return b.StartAt, true
	}
	if b.Repeat == schedule.None {
		return now, w.End == nil || now.Before(*w.End)
	}
	return schedule.Next(w, b.Repeat, now)
}
