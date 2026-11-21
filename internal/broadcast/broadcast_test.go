package broadcast

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/model"
	"pulse/internal/schedule"
	"pulse/internal/segment"
)

type memStore struct {
	due      []model.Broadcast
	runs     map[time.Time]int
	advanced []*time.Time
	testing  []bool
	tested   int
	due2     *time.Time // winner due
	winners  []WinnerRun
	finished map[int64]string // run -> winner
	winnerN  int
}

func (m *memStore) ClaimDueBroadcast(context.Context) (model.Broadcast, bool, error) {
	if len(m.due) == 0 {
		return model.Broadcast{}, false, nil
	}
	b := m.due[0]
	m.due = m.due[1:]
	return b, true, nil
}
func (m *memStore) StartRun(_ context.Context, _ int64, at time.Time) (int64, error) {
	return int64(len(m.runs) + 1), nil
}
func (m *memStore) FinishRun(_ context.Context, run int64, n, tested int, winnerDue *time.Time) error {
	m.runs[time.Unix(run, 0)] = n
	m.tested, m.due2 = tested, winnerDue
	return nil
}
func (m *memStore) AdvanceBroadcast(_ context.Context, _ int64, next *time.Time, testing bool) error {
	m.advanced = append(m.advanced, next)
	m.testing = append(m.testing, testing)
	return nil
}
func (m *memStore) ClaimDueWinner(context.Context) (WinnerRun, bool, error) {
	if len(m.winners) == 0 {
		return WinnerRun{}, false, nil
	}
	w := m.winners[0]
	m.winners = m.winners[1:]
	return w, true, nil
}
func (m *memStore) FinishWinner(_ context.Context, run, _ int64, winner, _ string, n int) error {
	if m.finished == nil {
		m.finished = map[int64]string{}
	}
	m.finished[run], m.winnerN = winner, n
	return nil
}

type memStats map[string]model.MessageCounts

func (s memStats) VariantCounts(context.Context, int64, int64, int64) (map[string]model.MessageCounts, error) {
	return s, nil
}

type memMembers []uint32

func (m memMembers) Load(context.Context, int64, int64) ([]byte, error) { return segment.Build(m), nil }

type memPub struct{ msgs []model.Message }

func (p *memPub) Publish(_ context.Context, recs ...*kgo.Record) error {
	for _, r := range recs {
		var m model.Message
		_ = json.Unmarshal(r.Value, &m)
		p.msgs = append(p.msgs, m)
	}
	return nil
}

func TestRunSendsToMembersAndSchedulesNext(t *testing.T) {
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 2)
	daily := model.Broadcast{ID: 5, TenantID: 1, Channel: "sms", TemplateID: 3, SegmentID: 9,
		StartAt: start, EndAt: &end, Repeat: schedule.Daily, NextRunAt: &start}
	st, pub := &memStore{due: []model.Broadcast{daily}, runs: map[time.Time]int{}}, &memPub{}
	r := &Runner{Store: st, Members: memMembers{2, 7, 40}, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pub.msgs) != 3 || pub.msgs[1].ContactID != 7 || pub.msgs[1].Channel != "sms" || pub.msgs[1].ID != "bc5:1794474000:7" {
		t.Fatalf("messages = %+v", pub.msgs)
	}
	if len(st.advanced) != 1 || st.advanced[0] == nil || !st.advanced[0].Equal(start.AddDate(0, 0, 1)) {
		t.Errorf("next run = %v, want the next day", st.advanced)
	}

	// The last run inside the window completes the broadcast.
	last := start.AddDate(0, 0, 1)
	daily.NextRunAt = &last
	st.due = []model.Broadcast{daily}
	_ = r.RunDue(context.Background())
	if st.advanced[1] != nil {
		t.Errorf("after the last run, next = %v, want none (completed)", st.advanced[1])
	}
}

func TestFirstRun(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	future := model.Broadcast{StartAt: now.Add(time.Hour), Repeat: schedule.None}
	if at, ok := FirstRun(future, now); !ok || !at.Equal(future.StartAt) {
		t.Errorf("future start: %v %v", at, ok)
	}
	past := model.Broadcast{StartAt: now.Add(-time.Hour), Repeat: schedule.None}
	if at, ok := FirstRun(past, now); !ok || !at.Equal(now) {
		t.Errorf("one-time with a past start sends now: %v %v", at, ok)
	}
	weekly := model.Broadcast{StartAt: now.Add(-24 * time.Hour), Repeat: schedule.Weekly}
	if at, _ := FirstRun(weekly, now); !at.Equal(weekly.StartAt.AddDate(0, 0, 7)) {
		t.Errorf("weekly with a past start waits for its next slot: %v", at)
	}
	ended := now.Add(-time.Minute)
	if _, ok := FirstRun(model.Broadcast{StartAt: now.Add(-time.Hour), EndAt: &ended, Repeat: schedule.None}, now); ok {
		t.Error("a broadcast whose window has ended cannot be scheduled")
	}
}

func TestABTestThenWinner(t *testing.T) {
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	members := make(memMembers, 2000)
	for i := range members {
		members[i] = uint32(i + 1)
	}
	ab := &model.ABTest{Variants: []model.ABVariant{{Label: "A", TemplateID: 10}, {Label: "B", TemplateID: 11}},
		TestPercent: 20, Metric: MetricOpens, WaitHours: 4}
	b := model.Broadcast{ID: 8, TenantID: 1, Channel: "email", TemplateID: 10, SegmentID: 9, StartAt: at, NextRunAt: &at,
		Repeat: schedule.None, AB: ab}
	st, pub := &memStore{due: []model.Broadcast{b}, runs: map[time.Time]int{}}, &memPub{}
	now := at.Add(time.Minute)
	r := &Runner{Store: st, Members: members, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now },
		Stats: memStats{"A": {Sent: 200, Opened: 40}, "B": {Sent: 200, Opened: 70}}}
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	// About 20% are tested, split between A and B; nobody else gets anything yet.
	if n := len(pub.msgs); n < 320 || n > 480 || st.tested != n {
		t.Fatalf("test group = %d messages (tested %d), want ~400", n, st.tested)
	}
	perVariant := map[string]int{}
	for _, m := range pub.msgs {
		perVariant[m.Variant]++
		if (m.Variant == "A") != (m.TemplateID == 10) || m.BroadcastID != 8 || m.Run != at.Unix() {
			t.Fatalf("message %+v has the wrong template or attribution", m)
		}
	}
	if perVariant["A"] < 150 || perVariant["B"] < 150 {
		t.Errorf("variants = %v, want an even split", perVariant)
	}
	if st.due2 == nil || !st.due2.Equal(now.Add(4*time.Hour)) {
		t.Errorf("winner due = %v, want 4 hours after the send", st.due2)
	}
	if len(st.testing) != 1 || !st.testing[0] || st.advanced[0] != nil {
		t.Errorf("a one-time A/B broadcast waits in testing: advanced %v testing %v", st.advanced, st.testing)
	}

	// The winner (B opened more) goes to everyone outside the test group, never twice to anyone.
	tested := map[uint32]bool{}
	for _, m := range pub.msgs {
		tested[m.ContactID] = true
	}
	pub.msgs = nil
	st.winners = []WinnerRun{{RunID: 1, ScheduledAt: at, Broadcast: b}}
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.finished[1] != "B" || len(pub.msgs) != 2000-len(tested) || st.winnerN != len(pub.msgs) {
		t.Fatalf("winner %q sent to %d, want B to %d", st.finished[1], len(pub.msgs), 2000-len(tested))
	}
	for _, m := range pub.msgs {
		if tested[m.ContactID] || m.TemplateID != 11 || m.Variant != WinnerVariant {
			t.Fatalf("winner message %+v", m)
		}
	}
}

func TestPickWinnerAndValidate(t *testing.T) {
	ab := &model.ABTest{Variants: []model.ABVariant{{Label: "A"}, {Label: "B"}, {Label: "C"}}, Metric: MetricClicks}
	w, why := PickWinner(ab, map[string]model.MessageCounts{"A": {Sent: 100, Clicked: 5}, "B": {Sent: 50, Clicked: 5}, "C": {}})
	if w != "B" || why == "" {
		t.Errorf("winner = %q (%s), want B (10%% beats 5%%)", w, why)
	}
	if w, _ := PickWinner(ab, nil); w != "A" {
		t.Errorf("no data: winner = %q, want the first variant", w)
	}
	ok := &model.ABTest{Variants: []model.ABVariant{{Label: "A"}, {Label: "B"}}, TestPercent: 20, Metric: MetricOpens, WaitHours: 4}
	if err := ValidateAB(ok, "email"); err != nil {
		t.Error(err)
	}
	if ValidateAB(ok, "sms") == nil {
		t.Error("opens can't pick an SMS winner")
	}
	manual := *ok
	manual.Metric = MetricManual
	if err := ValidateAB(&manual, "sms"); err != nil {
		t.Error(err)
	}
	bad := *ok
	bad.Variants = []model.ABVariant{{Label: "A"}, {Label: "A"}}
	if ValidateAB(&bad, "email") == nil {
		t.Error("duplicate labels must be refused")
	}
	split := *ok
	split.TestPercent, split.Metric = 100, ""
	if err := ValidateAB(&split, "push"); err != nil {
		t.Errorf("a plain 100%% split needs no metric: %v", err)
	}
}

type segMembers map[int64][]uint32

func (m segMembers) Load(_ context.Context, _, seg int64) ([]byte, error) {
	return segment.Build(m[seg]), nil
}

func TestExcludedSegmentsGetNothing(t *testing.T) {
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	b := model.Broadcast{ID: 5, TenantID: 1, Channel: "email", TemplateID: 3, SegmentID: 1, ExcludeSegmentIDs: []int64{2, 3},
		StartAt: at, Repeat: schedule.None, NextRunAt: &at}
	st, pub := &memStore{due: []model.Broadcast{b}, runs: map[time.Time]int{}}, &memPub{}
	r := &Runner{Store: st, Members: segMembers{1: {1, 2, 3, 4, 900}, 2: {2}, 3: {4, 5000}}, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got []uint32
	for _, m := range pub.msgs {
		got = append(got, m.ContactID)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 900 {
		t.Fatalf("recipients = %v, want 1 3 900", got)
	}
	if st.runs[time.Unix(1, 0)] != 3 { // memStore keys runs by run id
		t.Errorf("run recipients = %v", st.runs)
	}
}

func TestLocalTime(t *testing.T) {
	// 9:00 on 12 Nov, chosen in India (UTC+5:30).
	b := model.Broadcast{ID: 6, TenantID: 1, Channel: "email", TemplateID: 3, SegmentID: 1, Repeat: schedule.None,
		StartAt: time.Date(2026, 11, 12, 3, 30, 0, 0, time.UTC), LocalTime: true, Timezone: "Asia/Kolkata"}
	if c := LocalClock(b); c != "2026-11-12T09:00" {
		t.Fatalf("clock = %s", c)
	}
	// The run starts when 9:00 comes in UTC+14: 19:00 UTC the day before.
	start := time.Date(2026, 11, 11, 19, 0, 0, 0, time.UTC)
	if s := LocalStart(b); !s.Equal(start) {
		t.Errorf("local start = %v", s)
	}
	if at, ok := FirstRun(b, start.Add(-time.Hour)); !ok || !at.Equal(start) {
		t.Errorf("first run = %v %v", at, ok)
	}
	if at, ok := FirstRun(b, start.Add(time.Hour)); !ok || !at.Equal(start.Add(time.Hour)) {
		t.Errorf("late schedule starts now: %v %v", at, ok)
	}
	// Each contact gets it at 9:00 in their zone.
	ny, _ := LocalDue("2026-11-12T09:00", "America/New_York")
	if !ny.Equal(time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("New York: %v", ny)
	}
	utc, _ := LocalDue("2026-11-12T09:00", "")
	if !utc.Equal(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("no zone: %v", utc)
	}
	if _, ok := LocalDue("soon", "UTC"); ok {
		t.Error("a bad clock parsed")
	}
	// Messages carry the clock time and the fallback zone.
	b.NextRunAt = &start
	st, pub := &memStore{due: []model.Broadcast{b}, runs: map[time.Time]int{}}, &memPub{}
	r := &Runner{Store: st, Members: memMembers{7}, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pub.msgs) != 1 || pub.msgs[0].LocalAt != "2026-11-12T09:00" || pub.msgs[0].LocalZone != "Asia/Kolkata" {
		t.Errorf("messages = %+v", pub.msgs)
	}
	if err := ValidateLocalTime(model.Broadcast{LocalTime: true, Repeat: schedule.Daily}); err == nil {
		t.Error("a repeating local-time broadcast was accepted")
	}
	if err := ValidateLocalTime(model.Broadcast{LocalTime: true, Repeat: schedule.None, Timezone: "Mars/Base"}); err == nil {
		t.Error("an unknown zone was accepted")
	}
}

func TestUnionAndExclude(t *testing.T) {
	a := segment.Build([]uint32{1, 9})
	b := segment.Build([]uint32{2, 30})
	u := Union(a, b)
	if got := segment.Members(u); len(got) != 4 || got[3] != 30 {
		t.Fatalf("union %v", got)
	}
	if got := segment.Members(Exclude(u, segment.Build([]uint32{9, 30}))); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("exclude %v", got)
	}
	if len(segment.Members(a)) != 2 {
		t.Fatal("union changed its input")
	}
}

// Every message of a run carries the run's deadline; a local-time run carries it as a clock time.
func TestRunStampsDeadline(t *testing.T) {
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	b := model.Broadcast{ID: 6, TenantID: 1, Channel: "email", TemplateID: 3, SegmentID: 9, StartAt: start,
		Repeat: schedule.None, NextRunAt: &start, Deadline: &model.Deadline{Hours: 4}}
	st, pub := &memStore{due: []model.Broadcast{b}, runs: map[time.Time]int{}}, &memPub{}
	r := &Runner{Store: st, Members: memMembers{2, 7}, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pub.msgs) != 2 || pub.msgs[0].Deadline == nil || !pub.msgs[0].Deadline.Equal(start.Add(4*time.Hour)) {
		t.Fatalf("messages %+v", pub.msgs)
	}
	b.ID, b.LocalTime, b.Timezone, b.Deadline = 7, true, "Asia/Kolkata", &model.Deadline{At: "21:00"}
	st.due, pub.msgs = []model.Broadcast{b}, nil
	if err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := pub.msgs[0]; m.Deadline != nil || m.LocalAt != "2026-11-12T14:30" || m.DeadlineLocal != "2026-11-12T21:00" {
		t.Fatalf("local-time message %+v", m)
	}
}

func TestPacedAt(t *testing.T) {
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	b := model.Broadcast{SendPerHour: 3600}
	if PacedAt(b, start, 0) != nil {
		t.Error("the first message waits")
	}
	if at := PacedAt(b, start, 1800); at == nil || !at.Equal(start.Add(30*time.Minute)) {
		t.Errorf("1800th at 3600/h = %v, want 10:30", at)
	}
	if PacedAt(model.Broadcast{}, start, 5) != nil {
		t.Error("unpaced campaigns don't wait")
	}
	if ValidateSpeed(model.Broadcast{SendPerHour: 10}) == nil || ValidateSpeed(model.Broadcast{SendPerHour: 500}) != nil {
		t.Error("speed limits")
	}
}
