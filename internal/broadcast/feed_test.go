package broadcast

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"pulse/internal/feed"
	"pulse/internal/model"
	"pulse/internal/schedule"
)

type feedStore struct {
	*memStore
	seen  time.Time
	kept  map[time.Time]string
	setAt []time.Time
}

func (f *feedStore) FeedSeen(context.Context, int64) (time.Time, error) { return f.seen, nil }
func (f *feedStore) SetRunFeed(_ context.Context, _, run int64, html, _ string, seen time.Time) error {
	f.kept[time.Unix(run, 0)] = html
	f.seen = seen
	f.setAt = append(f.setAt, seen)
	return nil
}
func (f *feedStore) RunFeed(context.Context, int64, time.Time) (string, string, error) {
	return "", "", nil
}

func TestFeedRuns(t *testing.T) {
	at := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	b := model.Broadcast{ID: 8, TenantID: 1, Channel: "email", TemplateID: 3, SegmentID: 9, StartAt: at,
		Repeat: schedule.Daily, NextRunAt: &at, FeedURL: "https://acme.test/feed"}
	items := feed.Feed{Items: []feed.Item{
		{Title: "Old", Link: "https://acme.test/1", Published: at.Add(-50 * time.Hour)},
		{Title: "New", Link: "https://acme.test/2", Published: at.Add(-2 * time.Hour)},
	}}
	st := &feedStore{memStore: &memStore{due: []model.Broadcast{b}, runs: map[time.Time]int{}}, kept: map[time.Time]string{}}
	pub := &memPub{}
	r := &Runner{Store: st, Members: memMembers{2, 7}, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Feed: func(context.Context, string) (feed.Feed, error) { return items, nil }}
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	// First run: only what's within one day (the repeat) is sent.
	if len(pub.msgs) != 2 || len(st.setAt) != 1 || !st.setAt[0].Equal(at.Add(-2*time.Hour)) {
		t.Fatalf("first run: msgs %d, seen %v", len(pub.msgs), st.setAt)
	}
	// Next run, nothing newer: skipped, still moves on.
	next := at.AddDate(0, 0, 1)
	b.NextRunAt = &next
	st.due = []model.Broadcast{b}
	_ = r.RunDue(context.Background())
	if len(pub.msgs) != 2 || len(st.advanced) != 2 || st.advanced[1] == nil {
		t.Errorf("empty feed run: msgs %d advanced %v", len(pub.msgs), st.advanced)
	}
	// A broken feed skips the run rather than retrying forever.
	r.Feed = func(context.Context, string) (feed.Feed, error) { return feed.Feed{}, errors.New("HTTP 500") }
	st.due = []model.Broadcast{b}
	if err := r.RunDue(context.Background()); err != nil || len(pub.msgs) != 2 || len(st.advanced) != 3 {
		t.Errorf("broken feed: err %v msgs %d", err, len(pub.msgs))
	}
	if ValidateFeed(model.Broadcast{FeedURL: "ftp://x", Channel: "email"}) == nil ||
		ValidateFeed(model.Broadcast{FeedURL: "https://x.test/f", Channel: "sms"}) == nil ||
		ValidateFeed(model.Broadcast{FeedURL: "https://x.test/f", Channel: "email"}) != nil {
		t.Error("ValidateFeed")
	}
}

// runFeedStore keeps feeds per run and finds them as pg does: by the run's scheduled time in
// whole seconds (how messages carry it).
type runFeedStore struct {
	*memStore
	seen      time.Time
	scheduled map[int64]time.Time
	kept      map[int64]string
	marks     []time.Time
}

func (f *runFeedStore) FeedSeen(context.Context, int64) (time.Time, error) { return f.seen, nil }
func (f *runFeedStore) SetRunFeed(_ context.Context, _, run int64, html, _ string, seen time.Time) error {
	f.kept[run], f.seen = html, seen
	f.marks = append(f.marks, seen)
	return nil
}
func (f *runFeedStore) RunFeed(_ context.Context, _ int64, at time.Time) (string, string, error) {
	for run, s := range f.scheduled {
		if s.Truncate(time.Second).Equal(at) {
			return f.kept[run], "", nil
		}
	}
	return "", "", nil
}

func TestFeedRunSendNowRetry(t *testing.T) {
	at := time.Date(2026, 11, 21, 9, 0, 0, 123456000, time.UTC) // "send now": not a whole second
	b := model.Broadcast{ID: 8, TenantID: 1, Channel: "email", Repeat: schedule.None, FeedURL: "https://acme.test/feed"}
	st := &runFeedStore{memStore: &memStore{runs: map[time.Time]int{}}, scheduled: map[int64]time.Time{1: at}, kept: map[int64]string{}}
	r := &Runner{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Feed: func(context.Context, string) (feed.Feed, error) {
			return feed.Feed{Items: []feed.Item{{Title: "New", Published: at.Add(-time.Hour)}}}, nil
		}}
	if ok, err := r.feedRun(context.Background(), b, 1, at); !ok || err != nil || st.kept[1] == "" {
		t.Fatalf("first try: %v %v", ok, err)
	}
	// The retry reuses what the run kept, even when the feed can't be read now.
	r.Feed = func(context.Context, string) (feed.Feed, error) { return feed.Feed{}, errors.New("HTTP 500") }
	if ok, err := r.feedRun(context.Background(), b, 1, at); !ok || err != nil {
		t.Errorf("retry didn't reuse the kept feed: %v %v", ok, err)
	}
}

func TestFeedRunFutureAndUndatedItems(t *testing.T) {
	at := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	b := model.Broadcast{ID: 8, TenantID: 1, Channel: "email", Repeat: schedule.Daily, FeedURL: "https://acme.test/feed"}
	items := feed.Feed{Items: []feed.Item{
		{Title: "Scheduled", Published: at.Add(48 * time.Hour)},
		{Title: "Recent", Published: at.Add(-time.Hour)},
		{Title: "Undated"},
	}}
	st := &runFeedStore{memStore: &memStore{runs: map[time.Time]int{}}, scheduled: map[int64]time.Time{}, kept: map[int64]string{}}
	r := &Runner{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Feed: func(context.Context, string) (feed.Feed, error) { return items, nil }}
	run := func(id int64, when time.Time) bool {
		st.scheduled[id] = when
		ok, err := r.feedRun(context.Background(), b, id, when)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	// First feed run of a repeating campaign: the recent and the undated item, not the scheduled one.
	if !run(1, at) || !strings.Contains(st.kept[1], "Recent") || !strings.Contains(st.kept[1], "Undated") ||
		strings.Contains(st.kept[1], "Scheduled") || !st.seen.Equal(at.Add(-time.Hour)) {
		t.Fatalf("first run: %q seen %v", st.kept[1], st.seen)
	}
	// Next day: nothing new yet (the undated item counted once).
	if run(2, at.Add(24*time.Hour)) {
		t.Errorf("second run sent %q", st.kept[2])
	}
	// The scheduled item goes once its date has come.
	if !run(3, at.Add(48*time.Hour)) || !strings.Contains(st.kept[3], "Scheduled") || strings.Contains(st.kept[3], "Undated") {
		t.Errorf("third run: %q", st.kept[3])
	}
}
