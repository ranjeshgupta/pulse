// Package kafka wraps franz-go with the delivery semantics every Pulse worker relies on:
//
//   - at-least-once: offsets are committed only after a fetch has been fully handled
//   - per-key ordering: records of one partition are handled one at a time, partitions in parallel
//   - poison records go to "<topic>.dlq" after a few retries instead of blocking the partition
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Publisher produces records and waits for broker acknowledgement.
type Publisher interface {
	Publish(ctx context.Context, recs ...*kgo.Record) error
}

type Producer struct{ cl *kgo.Client }

func NewProducer(brokers []string) (*Producer, error) {
	// No linger: callers publish whole batches with ProduceSync, so waiting for more records
	// would only add latency to every synchronous publish.
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ProducerLinger(0),
		kgo.ProducerBatchCompression(kgo.ZstdCompression()),
	)
	if err != nil {
		return nil, err
	}
	return &Producer{cl: cl}, nil
}

func (p *Producer) Publish(ctx context.Context, recs ...*kgo.Record) error {
	if len(recs) == 0 {
		return nil
	}
	return p.cl.ProduceSync(ctx, recs...).FirstErr()
}

func (p *Producer) Close() { p.cl.Close() }

// JSON builds a record with a JSON value.
func JSON(topic string, key []byte, v any) (*kgo.Record, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &kgo.Record{Topic: topic, Key: key, Value: b}, nil
}

// EnsureTopics creates topics (and their DLQs) if missing.
func EnsureTopics(ctx context.Context, brokers []string, partitions int32, topics ...string) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer cl.Close()
	all := make([]string, 0, len(topics)*2)
	for _, t := range topics {
		all = append(all, t, t+".dlq")
	}
	resp, err := kadm.NewClient(cl).CreateTopics(ctx, partitions, 1, nil, all...)
	if err != nil {
		return err
	}
	for _, r := range resp {
		if r.Err != nil && !errors.Is(r.Err, kerr.TopicAlreadyExists) {
			return fmt.Errorf("create topic %s: %w", r.Topic, r.Err)
		}
	}
	return nil
}

// RecordHandler handles one record. Records of the same partition are never handled concurrently.
type RecordHandler func(ctx context.Context, r *kgo.Record) error

// BatchHandler handles every record of one poll at once (used by sinks that bulk insert).
// Returning an error retries the whole batch until it succeeds or ctx ends.
type BatchHandler func(ctx context.Context, recs []*kgo.Record) error

type Consumer struct {
	cl      *kgo.Client
	dlq     Publisher
	log     *slog.Logger
	retries int
}

func NewConsumer(brokers []string, group string, dlq Publisher, log *slog.Logger, topics ...string) (*Consumer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// Small per-partition slices keep every poll balanced across partitions. Without this, a
		// backlog makes one fetch fill up from a few partitions while the other workers idle.
		kgo.FetchMaxPartitionBytes(256<<10),
	)
	if err != nil {
		return nil, err
	}
	return &Consumer{cl: cl, dlq: dlq, log: log.With("group", group), retries: 3}, nil
}

// Close leaves the group so partitions are reassigned at once instead of after the session timeout.
// With BlockRebalanceOnPoll a poll that was never released makes Close hang (and the process gets
// SIGKILLed on deploy), so release it first.
func (c *Consumer) Close() {
	c.cl.AllowRebalance()
	c.cl.Close()
}

// Run handles records one by one, partitions in parallel, until ctx is done.
func (c *Consumer) Run(ctx context.Context, h RecordHandler) error {
	return c.loop(ctx, func(ctx context.Context, recs []*kgo.Record) error {
		var wg sync.WaitGroup
		for _, part := range byPartition(recs) {
			wg.Add(1)
			go func(part []*kgo.Record) {
				defer wg.Done()
				for _, r := range part {
					c.handleWithRetry(ctx, h, r)
				}
			}(part)
		}
		wg.Wait()
		return ctx.Err()
	})
}

// RunPartitionBatches hands each partition's slice of a poll to h as one batch, partitions in
// parallel, so a stage pays its round trips once per batch instead of once per record. A batch
// that keeps failing is split into single records, so only a poison record goes to the DLQ.
func (c *Consumer) RunPartitionBatches(ctx context.Context, h BatchHandler) error {
	single := func(ctx context.Context, r *kgo.Record) error { return h(ctx, []*kgo.Record{r}) }
	return c.loop(ctx, func(ctx context.Context, recs []*kgo.Record) error {
		var wg sync.WaitGroup
		for _, part := range byPartition(recs) {
			wg.Add(1)
			go func(part []*kgo.Record) {
				defer wg.Done()
				var err error
				for attempt := 0; attempt <= c.retries; attempt++ {
					if err = h(ctx, part); err == nil || ctx.Err() != nil {
						return
					}
					time.Sleep(time.Duration(100<<attempt) * time.Millisecond)
				}
				c.log.Warn("batch failed, retrying records one by one", "records", len(part), "err", err)
				for _, r := range part {
					c.handleWithRetry(ctx, single, r)
				}
			}(part)
		}
		wg.Wait()
		return ctx.Err()
	})
}

func byPartition(recs []*kgo.Record) map[string][]*kgo.Record {
	out := map[string][]*kgo.Record{}
	for _, r := range recs {
		k := fmt.Sprintf("%s/%d", r.Topic, r.Partition)
		out[k] = append(out[k], r)
	}
	return out
}

// RunBatch hands each poll's records to h as one batch.
func (c *Consumer) RunBatch(ctx context.Context, h BatchHandler) error {
	return c.loop(ctx, func(ctx context.Context, recs []*kgo.Record) error {
		for backoff := 200 * time.Millisecond; ; backoff = min(backoff*2, 10*time.Second) {
			err := h(ctx, recs)
			if err == nil {
				return nil
			}
			c.log.Error("batch failed, retrying", "records", len(recs), "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	})
}

func (c *Consumer) loop(ctx context.Context, process func(context.Context, []*kgo.Record) error) error {
	defer c.cl.AllowRebalance()
	for {
		fetches := c.cl.PollRecords(ctx, 20000)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return ctx.Err()
		}
		fetches.EachError(func(t string, p int32, err error) {
			c.log.Error("fetch error", "topic", t, "partition", p, "err", err)
		})
		recs := fetches.Records()
		if len(recs) > 0 {
			if err := process(ctx, recs); err != nil {
				return err // ctx cancelled: leave offsets uncommitted so the records are redelivered
			}
			if err := c.cl.CommitRecords(ctx, recs...); err != nil {
				c.log.Error("commit failed", "err", err)
			}
		}
		c.cl.AllowRebalance()
	}
}

func (c *Consumer) handleWithRetry(ctx context.Context, h RecordHandler, r *kgo.Record) {
	var err error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if err = h(ctx, r); err == nil || ctx.Err() != nil {
			return
		}
		time.Sleep(time.Duration(100<<attempt) * time.Millisecond)
	}
	c.log.Error("record failed, sending to DLQ", "topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "err", err)
	dead := &kgo.Record{
		Topic:   r.Topic + ".dlq",
		Key:     r.Key,
		Value:   r.Value,
		Headers: append(r.Headers, kgo.RecordHeader{Key: "error", Value: []byte(err.Error())}),
	}
	if perr := c.dlq.Publish(ctx, dead); perr != nil {
		c.log.Error("DLQ publish failed", "err", perr)
	}
}
