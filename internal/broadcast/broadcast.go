// Package broadcast sends scheduled broadcasts: when a run is due, every current member of the
// segment gets a send request on the channel's topic, and the next run is scheduled.
//
// A/B tests: a run sends each variant to its share of a test group. With a winner step, the rest of
// the audience waits until the winner is known (best open or click rate after a wait, or picked by
// hand) and then gets the winning variant. A contact's group and variant come from a hash of
// (broadcast, run, contact), so retries and the winner step agree on who was in the test.
//
// Exclusions: members of any excluded segment are left out of every send. Local time: a one-time
// broadcast can go at its start's clock time in each contact's own time zone. The run then starts
// when that time comes in the earliest zone (UTC+14), and each message carries the clock time; the
// dispatcher holds it until that time in the contact's zone (see model.Message.LocalAt).
package broadcast

import (
	"net/url"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"pulse/internal/feed"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/reach"
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
	// Snapshot, when set, counts the run's audience as it starts (reach counts for the broadcast's
	// channel), stored with the run when the Store can keep it (AudienceStore).
	Snapshot func(ctx context.Context, b model.Broadcast) ([]byte, error)
	// Feed fetches an RSS-to-email campaign's feed (feed.Fetch with a safe client).
	Feed func(ctx context.Context, url string) (feed.Feed, error)
}

// FeedStore keeps RSS-to-email runs' content (pg.Store).
type FeedStore interface {
	FeedSeen(ctx context.Context, broadcast int64) (time.Time, error)
	SetRunFeed(ctx context.Context, broadcast, run int64, html, text string, seen time.Time) error
	RunFeed(ctx context.Context, broadcast int64, scheduled time.Time) (html, text string, err error)
}

// AudienceStore keeps a run's audience snapshot.
type AudienceStore interface {
	SetRunAudience(ctx context.Context, run int64, audience []byte) error
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
	if as, ok := r.Store.(AudienceStore); ok && r.Snapshot != nil {
		// Best effort: a report without the snapshot still has every other step of its funnel.
		if snap, err := r.Snapshot(ctx, b); err != nil {
			r.Log.Warn("broadcast audience snapshot", "broadcast", b.ID, "err", err)
		} else if err := as.SetRunAudience(ctx, runID, snap); err != nil {
			r.Log.Warn("broadcast audience snapshot", "broadcast", b.ID, "err", err)
		}
	}
	if b.FeedURL != "" {
		ok, err := r.feedRun(ctx, b, runID, at)
		if err != nil {
			return err
		}
		if !ok { // nothing new in the feed: this run sends nothing
			return r.skipRun(ctx, b, runID, at)
		}
	}
	members, err := r.audience(ctx, b)
	if err != nil {
		return err
	}
	runKey := at.Unix()
	localAt := ""
	if b.LocalTime {
		localAt = LocalClock(b)
	}
	send := make([]model.Message, 0, min(chunk, len(members)))
	sent, tested := 0, 0
	flush := func() error {
		if err := r.publish(ctx, send); err != nil {
			return err
		}
		send = send[:0]
		return nil
	}
	deadline := b.Deadline.After(at)
	deadlineLocal := ""
	if localAt != "" && b.Deadline != nil {
		deadline, deadlineLocal = nil, b.Deadline.LocalClock(localAt)
	}
	for i, contact := range members {
		m := r.message(b, runKey, contact, b.TemplateID, "")
		m.LocalAt, m.LocalZone = localAt, b.Timezone
		m.Deadline, m.DeadlineLocal = deadline, deadlineLocal
		m.NotBefore = PacedAt(b, at, i)
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

// feedRun gets an RSS-to-email run's items: the feed's items published since the last run (on the
// first run, within one repeat interval) are kept with the run for its messages' {{block.feed}}.
// A retried run reuses what it kept. ok is false when there is nothing new, or the feed can't be
// read (the run is skipped, not retried: the next run picks up whatever it missed).
func (r *Runner) feedRun(ctx context.Context, b model.Broadcast, runID int64, at time.Time) (bool, error) {
	fs, ok := r.Store.(FeedStore)
	if !ok || r.Feed == nil {
		return false, errors.New("RSS-to-email isn't available on this server")
	}
	if h, _, err := fs.RunFeed(ctx, b.ID, at); err == nil && h != "" {
		return true, nil
	}
	f, err := r.Feed(ctx, b.FeedURL)
	if err != nil {
		r.Log.Warn("broadcast feed", "broadcast", b.ID, "url", b.FeedURL, "err", err)
		return false, nil
	}
	seen, err := fs.FeedSeen(ctx, b.ID)
	if err != nil {
		return false, err
	}
	if seen.IsZero() {
		if d := repeatSpan(b.Repeat); d > 0 {
			seen = at.Add(-d)
		}
	}
	items := feed.Since(f, seen)
	if len(items) == 0 {
		return false, nil
	}
	html, text := feed.Render(items)
	mark := feed.Newest(items)
	if mark.IsZero() {
		mark = at
	}
	return true, fs.SetRunFeed(ctx, b.ID, runID, html, text, mark)
}

// repeatSpan is how far back a feed campaign's first run looks.
func repeatSpan(repeat string) time.Duration {
	switch repeat {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 31 * 24 * time.Hour
	}
	return 0
}

// skipRun finishes a run that sends nothing and moves the campaign to its next run.
func (r *Runner) skipRun(ctx context.Context, b model.Broadcast, runID int64, at time.Time) error {
	if err := r.Store.FinishRun(ctx, runID, 0, 0, nil); err != nil {
		return err
	}
	var next *time.Time
	if t, ok := schedule.Next(schedule.Window{Start: b.StartAt, End: b.EndAt}, b.Repeat, at); ok {
		next = &t
	}
	r.Log.Info("broadcast run skipped: nothing new in its feed", "broadcast", b.ID, "next", next)
	return r.Store.AdvanceBroadcast(ctx, b.ID, next, false)
}

// ValidateFeed checks an RSS-to-email campaign.
func ValidateFeed(b model.Broadcast) error {
	if b.FeedURL == "" {
		return nil
	}
	u, err := url.Parse(b.FeedURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(b.FeedURL) > 2000 {
		return errors.New("feed: give the feed's full address, starting with https://")
	}
	if b.Channel != "email" {
		return errors.New("feed: RSS-to-email is for email campaigns")
	}
	if b.AB != nil {
		return errors.New("feed: an RSS-to-email campaign can't be an A/B test")
	}
	return nil
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
	members, err := r.audience(ctx, b)
	if err != nil {
		return err
	}
	var send []model.Message
	n := 0
	for _, contact := range members {
		if Bucket(b.ID, runKey, contact) < b.AB.TestPercent*100 {
			continue // was in the test
		}
		m := r.message(b, runKey, contact, tpl, WinnerVariant)
		m.Deadline = b.Deadline.After(r.Now()) // the winner's send starts now
		send = append(send, m)
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

// audience lists the broadcast's recipients now: members of its segments who are in none of the
// excluded segments.
func (r *Runner) audience(ctx context.Context, b model.Broadcast) ([]uint32, error) {
	include, err := r.Members.Load(ctx, b.TenantID, b.SegmentID)
	if err != nil {
		return nil, err
	}
	for _, id := range b.IncludeSegmentIDs {
		more, err := r.Members.Load(ctx, b.TenantID, id)
		if err != nil {
			return nil, err
		}
		include = Union(include, more)
	}
	var excludes [][]byte
	for _, id := range b.ExcludeSegmentIDs {
		ex, err := r.Members.Load(ctx, b.TenantID, id)
		if err != nil {
			return nil, err
		}
		excludes = append(excludes, ex)
	}
	return segment.Members(Exclude(include, excludes...)), nil
}

// Union returns a bitmap with the bits of both.
func Union(a, b []byte) []byte {
	if len(b) > len(a) {
		a, b = b, a
	}
	out := append([]byte(nil), a...)
	for i := range b {
		out[i] |= b[i]
	}
	return out
}

// Exclude returns the include bitmap without the bits set in any of excludes.
func Exclude(include []byte, excludes ...[]byte) []byte {
	if len(excludes) == 0 {
		return include
	}
	out := append([]byte(nil), include...)
	for _, ex := range excludes {
		for i := 0; i < len(out) && i < len(ex); i++ {
			out[i] &^= ex[i]
		}
	}
	return out
}

// earliestOffset is the zone furthest ahead of UTC (UTC+14, Line Islands): a local-time broadcast
// starts sending when its clock time comes there.
const earliestOffset = 14 * time.Hour

// LocalClockLayout is the clock time a local-time message carries.
const LocalClockLayout = "2006-01-02T15:04"

// LocalClock is the broadcast's start as clock time in the zone it was chosen in.
func LocalClock(b model.Broadcast) string {
	loc, err := time.LoadLocation(b.Timezone)
	if err != nil || b.Timezone == "" {
		loc = time.UTC
	}
	return b.StartAt.In(loc).Format(LocalClockLayout)
}

// LocalStart is when a local-time broadcast's run starts: its clock time in the earliest zone.
func LocalStart(b model.Broadcast) time.Time {
	clock, _ := time.ParseInLocation(LocalClockLayout, LocalClock(b), time.UTC)
	return clock.Add(-earliestOffset)
}

// LocalDue is when a message held for clock (LocalClockLayout) may go in zone tz. ok is false when
// clock doesn't parse (the message then goes right away).
func LocalDue(clock, tz string) (time.Time, bool) {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	t, err := time.ParseInLocation(LocalClockLayout, clock, loc)
	return t, err == nil
}

// ValidateLocalTime checks the local-time option.
func ValidateLocalTime(b model.Broadcast) error {
	if !b.LocalTime {
		return nil
	}
	if b.Repeat != schedule.None {
		return errors.New("sending in each contact's time zone works for one-time broadcasts only")
	}
	if b.Timezone != "" {
		if _, err := time.LoadLocation(b.Timezone); err != nil {
			return fmt.Errorf("unknown time zone %q", b.Timezone)
		}
	}
	return nil
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
		Channel: b.Channel, TemplateID: template, FallbackTemplateID: b.FallbackTemplateID, ProviderID: b.ProviderID, Topic: b.Topic,
		BroadcastID: b.ID, Run: run, Variant: variant, Campaign: b.Name, Holdout: InHoldout(b, contact), BestTime: b.BestTime && b.Repeat == "none"}
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
	if b.LocalTime && b.Repeat == schedule.None {
		// Starts when the clock time comes in the earliest zone; contacts whose time has already
		// passed get it right away.
		start := LocalStart(b)
		if now.Before(start) {
			return start, true
		}
		return now, w.End == nil || now.Before(*w.End)
	}
	if now.Before(b.StartAt) {
		return b.StartAt, true
	}
	if b.Repeat == schedule.None {
		return now, w.End == nil || now.Before(*w.End)
	}
	return schedule.Next(w, b.Repeat, now)
}

// AudienceSnapshot is a run's audience when it started.
type AudienceSnapshot struct {
	reach.Counts
	Channel string    `json:"channel"`
	Ready   bool      `json:"ready"` // reachability was built (else the last two steps read 0)
	At      time.Time `json:"at"`
}

// ReachSnapshot counts a broadcast's audience with the reachability bitmaps (package reach): its
// segments, after exclusions, after consent for its channel, reachable on it.
func ReachSnapshot(rdb redis.Cmdable, now func() time.Time) func(ctx context.Context, b model.Broadcast) ([]byte, error) {
	return func(ctx context.Context, b model.Broadcast) ([]byte, error) {
		include := append([]int64{b.SegmentID}, b.IncludeSegmentIDs...)
		counts, _, err := reach.Count(ctx, rdb, reach.Query{Tenant: b.TenantID, Include: include, Exclude: b.ExcludeSegmentIDs, Channel: b.Channel}, false)
		if err != nil {
			return nil, err
		}
		ready, err := reach.Ready(ctx, rdb, b.TenantID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(AudienceSnapshot{Counts: counts, Channel: b.Channel, Ready: ready, At: now().UTC()})
	}
}

// PacedAt is when the i-th message of a paced run may go (nil: not paced, or right away).
func PacedAt(b model.Broadcast, start time.Time, i int) *time.Time {
	if b.SendPerHour <= 0 || i == 0 {
		return nil
	}
	t := start.Add(time.Duration(float64(i) / float64(b.SendPerHour) * float64(time.Hour)))
	return &t
}

// Sending speed limits (messages an hour).
const (
	MinSendPerHour = 60
	MaxSendPerHour = 10_000_000
)

// ValidateSpeed checks a sending speed.
func ValidateSpeed(b model.Broadcast) error {
	if b.BestTime && (b.LocalTime || b.SendPerHour > 0) {
		return errors.New("send at each contact's best time, or at a clock time in their zone, or at a sending speed: one of them")
	}
	if b.BestTime && b.Repeat != "none" {
		return errors.New("best time to send is for campaigns sent once")
	}
	if b.HoldoutPercent < 0 || b.HoldoutPercent > MaxHoldout {
		return fmt.Errorf("control group: 0 to %d %% of the audience", MaxHoldout)
	}
	if b.SendPerHour != 0 && (b.SendPerHour < MinSendPerHour || b.SendPerHour > MaxSendPerHour) {
		return fmt.Errorf("sending speed: %d to %d messages an hour, or 0 for as fast as possible", MinSendPerHour, MaxSendPerHour)
	}
	return nil
}

// InHoldout reports whether a contact is in a campaign's control group: a hash of (campaign,
// contact), so the same people are kept back on every run and in the winner step.
func InHoldout(b model.Broadcast, contact uint32) bool {
	if b.HoldoutPercent <= 0 {
		return false
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "holdout:%d:%d", b.ID, contact)
	return int(h.Sum32()%10000) < b.HoldoutPercent*100
}

// InControlGroup reports whether a contact is in the workspace's control group (percent of all).
func InControlGroup(tenant int64, contact uint32, percent int) bool {
	if percent <= 0 {
		return false
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "control:%d:%d", tenant, contact)
	return int(h.Sum32()%10000) < percent*100
}

// MaxHoldout caps a campaign's control group.
const MaxHoldout = 50
