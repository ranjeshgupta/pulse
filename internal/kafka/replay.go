package kafka

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// ReplayGroup is the consumer group that replays dead-letter topics. Its committed offsets
// remember what was replayed, so running the replay again doesn't repeat records.
const ReplayGroup = "pulse-replay-dlq"

// ReplayOptions control Replay.
type ReplayOptions struct {
	Limit int // stop after this many records (0: all)
	// DryRun lists the records to Out without re-publishing them or committing anything.
	DryRun bool
	Out    io.Writer
	// Idle ends the replay when no record arrives for this long (default 10 s).
	Idle time.Duration
}

// Replay moves the records of "<topic>.dlq" back to topic so their consumers handle them again,
// committing each batch once it is re-published. It stops when the dead-letter topic has nothing
// more (no record for opts.Idle) or after opts.Limit records, and returns how many it replayed.
func Replay(ctx context.Context, brokers []string, topic string, pub Publisher, log *slog.Logger, opts ReplayOptions) (int, error) {
	if strings.HasSuffix(topic, ".dlq") {
		return 0, fmt.Errorf("%s is a dead-letter topic: name the topic to replay into", topic)
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(ReplayGroup),
		kgo.ConsumeTopics(topic+".dlq"),
		kgo.DisableAutoCommit(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return 0, err
	}
	defer cl.Close()
	return replay(ctx, cl, pub, topic, log, opts)
}

// dlqReader is the part of kgo.Client that replay uses.
type dlqReader interface {
	PollRecords(ctx context.Context, maxRecords int) kgo.Fetches
	CommitRecords(ctx context.Context, recs ...*kgo.Record) error
}

func replay(ctx context.Context, src dlqReader, pub Publisher, topic string, log *slog.Logger, opts ReplayOptions) (int, error) {
	if opts.Idle <= 0 {
		opts.Idle = 10 * time.Second
	}
	done := 0
	for opts.Limit <= 0 || done < opts.Limit {
		want := 1000
		if opts.Limit > 0 {
			want = min(want, opts.Limit-done)
		}
		pctx, cancel := context.WithTimeout(ctx, opts.Idle)
		fetches := src.PollRecords(pctx, want)
		cancel()
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		fetches.EachError(func(t string, p int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				log.Error("fetch error", "topic", t, "partition", p, "err", err)
			}
		})
		recs := fetches.Records()
		if len(recs) == 0 {
			return done, nil // nothing more within the idle time
		}
		if len(recs) > want {
			recs = recs[:want]
		}
		if opts.DryRun {
			for _, r := range recs {
				describeDead(opts.Out, r)
			}
			done += len(recs)
			continue
		}
		out := make([]*kgo.Record, len(recs))
		for i, r := range recs {
			out[i] = replayed(r, topic)
		}
		if err := pub.Publish(ctx, out...); err != nil {
			return done, fmt.Errorf("re-publish to %s: %w", topic, err)
		}
		if err := src.CommitRecords(ctx, recs...); err != nil {
			return done, fmt.Errorf("commit %s.dlq: %w", topic, err)
		}
		done += len(recs)
		log.Info("replayed", "topic", topic, "records", len(recs), "total", done)
	}
	return done, nil
}

// replayed is a dead record ready to go back to topic: same key, value and headers, without the
// "error" header the DLQ added (a record that fails again gets a fresh one).
func replayed(r *kgo.Record, topic string) *kgo.Record {
	var hs []kgo.RecordHeader
	for _, h := range r.Headers {
		if h.Key != "error" {
			hs = append(hs, h)
		}
	}
	return &kgo.Record{Topic: topic, Key: r.Key, Value: r.Value, Headers: hs}
}

// describeDead writes one line about a dead record for a dry run.
func describeDead(w io.Writer, r *kgo.Record) {
	if w == nil {
		return
	}
	reason := ""
	for _, h := range r.Headers {
		if h.Key == "error" {
			reason = string(h.Value)
		}
	}
	fmt.Fprintf(w, "%s/%d@%d key=%q error=%q value=%.200s\n", r.Topic, r.Partition, r.Offset, r.Key, reason, r.Value)
}
