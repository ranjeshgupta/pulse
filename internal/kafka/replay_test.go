package kafka

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// fakeDLQ hands out its records in polls of at most maxRecords and records commits.
type fakeDLQ struct {
	recs      []*kgo.Record
	pos       int
	committed []*kgo.Record
}

func (f *fakeDLQ) PollRecords(ctx context.Context, maxRecords int) kgo.Fetches {
	if f.pos >= len(f.recs) {
		<-ctx.Done() // like the broker: wait for records until the idle timeout
		return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "", Partitions: []kgo.FetchPartition{{Partition: -1, Err: ctx.Err()}}}}}}
	}
	end := min(f.pos+maxRecords, len(f.recs))
	part := f.recs[f.pos:end]
	f.pos = end
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "messages.email.dlq", Partitions: []kgo.FetchPartition{{Records: part}}}}}}
}

func (f *fakeDLQ) CommitRecords(_ context.Context, recs ...*kgo.Record) error {
	f.committed = append(f.committed, recs...)
	return nil
}

type fakePub struct {
	recs []*kgo.Record
	err  error
}

func (p *fakePub) Publish(_ context.Context, recs ...*kgo.Record) error {
	if p.err != nil {
		return p.err
	}
	p.recs = append(p.recs, recs...)
	return nil
}

func deadRecords(n int) []*kgo.Record {
	out := make([]*kgo.Record, n)
	for i := range out {
		out[i] = &kgo.Record{Topic: "messages.email.dlq", Key: []byte("1:7"), Value: []byte(`{"id":"m"}`), Offset: int64(i),
			Headers: []kgo.RecordHeader{{Key: "pulse-tries", Value: []byte("2")}, {Key: "error", Value: []byte("redis down")}}}
	}
	return out
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestReplayRepublishesAndCommits(t *testing.T) {
	src, pub := &fakeDLQ{recs: deadRecords(5)}, &fakePub{}
	n, err := replay(context.Background(), src, pub, "messages.email", quiet, ReplayOptions{Idle: 10 * time.Millisecond})
	if err != nil || n != 5 {
		t.Fatalf("replayed %d, err %v", n, err)
	}
	if len(pub.recs) != 5 || len(src.committed) != 5 {
		t.Fatalf("published %d, committed %d", len(pub.recs), len(src.committed))
	}
	r := pub.recs[0]
	if r.Topic != "messages.email" || string(r.Key) != "1:7" || string(r.Value) != `{"id":"m"}` {
		t.Errorf("record = %s %s %s", r.Topic, r.Key, r.Value)
	}
	if len(r.Headers) != 1 || r.Headers[0].Key != "pulse-tries" {
		t.Errorf("headers = %v (the error header goes, others stay)", r.Headers)
	}
}

func TestReplayLimit(t *testing.T) {
	src, pub := &fakeDLQ{recs: deadRecords(5)}, &fakePub{}
	n, err := replay(context.Background(), src, pub, "messages.email", quiet, ReplayOptions{Limit: 3, Idle: 10 * time.Millisecond})
	if err != nil || n != 3 || len(pub.recs) != 3 || len(src.committed) != 3 {
		t.Fatalf("replayed %d, published %d, committed %d, err %v", n, len(pub.recs), len(src.committed), err)
	}
}

func TestReplayDryRunChangesNothing(t *testing.T) {
	src, pub := &fakeDLQ{recs: deadRecords(2)}, &fakePub{}
	var out bytes.Buffer
	n, err := replay(context.Background(), src, pub, "messages.email", quiet, ReplayOptions{DryRun: true, Out: &out, Idle: 10 * time.Millisecond})
	if err != nil || n != 2 || len(pub.recs) != 0 || len(src.committed) != 0 {
		t.Fatalf("dry run: %d, published %d, committed %d, err %v", n, len(pub.recs), len(src.committed), err)
	}
	if lines := strings.Count(out.String(), "\n"); lines != 2 || !strings.Contains(out.String(), `error="redis down"`) {
		t.Errorf("listing = %q", out.String())
	}
}

func TestReplayDoesNotCommitWhatItCouldNotPublish(t *testing.T) {
	src, pub := &fakeDLQ{recs: deadRecords(2)}, &fakePub{err: errors.New("broker down")}
	if _, err := replay(context.Background(), src, pub, "messages.email", quiet, ReplayOptions{Idle: 10 * time.Millisecond}); err == nil {
		t.Fatal("want the publish error")
	}
	if len(src.committed) != 0 {
		t.Errorf("committed %d records that were not re-published", len(src.committed))
	}
}

func TestReplayRefusesADLQAsTarget(t *testing.T) {
	if _, err := Replay(context.Background(), nil, "messages.email.dlq", &fakePub{}, quiet, ReplayOptions{}); err == nil {
		t.Error("replaying into a .dlq topic must be refused")
	}
}
