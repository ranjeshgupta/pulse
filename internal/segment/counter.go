package segment

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Per-contact event counts live in one Redis hash per (tenant, contact, event), with one field per
// UTC day ("20261110" -> 3). Fields expire on their own (HEXPIRE, Redis 7.4+), so active contacts
// never accumulate more than a year of buckets.
const bucketTTL = (maxDays + 1) * 24 * time.Hour

const dayLayout = "20060102"

func CounterKey(tenant int64, contact uint32, ev string) string {
	return fmt.Sprintf("cnt:%d:%d:%s", tenant, contact, ev)
}

// Incr adds one occurrence of ev at ts to the contact's day bucket.
func Incr(ctx context.Context, rdb redis.Cmdable, tenant int64, contact uint32, ev string, ts time.Time) error {
	pipe := rdb.Pipeline()
	QueueIncr(ctx, pipe, tenant, contact, ev, ts)
	_, err := pipe.Exec(ctx)
	return err
}

// NamesKey is the set of counter names a contact has, so its counters can be found (and moved
// on an identity merge) without scanning the keyspace.
func NamesKey(tenant int64, contact uint32) string {
	return fmt.Sprintf("cnames:%d:%d", tenant, contact)
}

// QueueIncr adds Incr's commands to a pipeline, so a batch of events costs one round trip.
func QueueIncr(ctx context.Context, pipe redis.Pipeliner, tenant int64, contact uint32, ev string, ts time.Time) {
	QueueAdd(ctx, pipe, tenant, contact, ev, ts.UTC().Format(dayLayout), 1)
}

// QueueAdd adds n to one day bucket ("20261110") of a counter.
func QueueAdd(ctx context.Context, pipe redis.Pipeliner, tenant int64, contact uint32, ev, day string, n int64) {
	key := CounterKey(tenant, contact, ev)
	pipe.HIncrBy(ctx, key, day, n)
	pipe.HExpire(ctx, key, bucketTTL, day)
	names := NamesKey(tenant, contact)
	pipe.SAdd(ctx, names, ev)
	pipe.Expire(ctx, names, bucketTTL)
}

// QueueSet overwrites one day bucket with n (seeding a counter from ClickHouse history).
func QueueSet(ctx context.Context, pipe redis.Pipeliner, tenant int64, contact uint32, ev, day string, n int64) {
	key := CounterKey(tenant, contact, ev)
	pipe.HSet(ctx, key, day, n)
	pipe.HExpire(ctx, key, bucketTTL, day)
	names := NamesKey(tenant, contact)
	pipe.SAdd(ctx, names, ev)
	pipe.Expire(ctx, names, bucketTTL)
}

// MoveCounters adds every counter of contact from into contact to and deletes from's, in one
// MULTI so a retry never adds twice: once from's keys are gone there is nothing left to move.
func MoveCounters(ctx context.Context, rdb redis.Cmdable, tenant int64, from, to uint32) error {
	names, err := rdb.SMembers(ctx, NamesKey(tenant, from)).Result()
	if err != nil || len(names) == 0 {
		return err
	}
	read := rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(names))
	for i, n := range names {
		cmds[i] = read.HGetAll(ctx, CounterKey(tenant, from, n))
	}
	if _, err := read.Exec(ctx); err != nil {
		return err
	}
	tx := rdb.TxPipeline()
	for i, n := range names {
		for day, v := range cmds[i].Val() {
			if c, err := strconv.ParseInt(v, 10, 64); err == nil && c != 0 {
				QueueAdd(ctx, tx, tenant, to, n, day, c)
			}
		}
		tx.Del(ctx, CounterKey(tenant, from, n))
	}
	tx.Del(ctx, NamesKey(tenant, from))
	_, err = tx.Exec(ctx)
	return err
}

// SumWindow adds the buckets for asOf's UTC day and the days-1 days before it.
// Buckets after asOf are ignored, matching the BETWEEN bounds in the SQL compiler.
func SumWindow(buckets map[string]string, asOf time.Time, days int) int {
	day := asOf.UTC()
	total := 0
	for i := 0; i < days; i++ {
		if n, err := strconv.Atoi(buckets[day.Format(dayLayout)]); err == nil {
			total += n
		}
		day = day.AddDate(0, 0, -1)
	}
	return total
}

// SumBetween adds the buckets from start to end, both UTC days included.
func SumBetween(buckets map[string]string, start, end time.Time) int {
	total := 0
	for day := end.UTC(); !day.Before(start); day = day.AddDate(0, 0, -1) {
		if n, err := strconv.Atoi(buckets[day.Format(dayLayout)]); err == nil {
			total += n
		}
	}
	return total
}

// Sum counts a window over buckets on asOf's day. It returns ErrNeedsSweep when the window
// starts before the counters' history does.
func (w Window) Sum(buckets map[string]string, asOf time.Time) (int, error) {
	if !w.InCounters(asOf) {
		return 0, ErrNeedsSweep
	}
	start, end := w.Bounds(asOf)
	return SumBetween(buckets, start, end), nil
}

// BucketCounter is a Counter over buckets that were already fetched (event name -> day -> count),
// so a batch can read every counter it needs in one pipeline.
type BucketCounter struct {
	Buckets map[string]map[string]string
	AsOf    time.Time
}

func (b BucketCounter) Count(_ context.Context, ev string, w Window) (int, error) {
	return w.Sum(b.Buckets[ev], b.AsOf)
}

// RedisCounter is the Counter for one contact. It reads each event's hash at most once per
// evaluation, so a segment with several rules on the same event costs one round trip.
type RedisCounter struct {
	rdb     redis.Cmdable
	tenant  int64
	contact uint32
	asOf    time.Time
	cache   map[string]map[string]string
	known   map[int64]bool
}

func NewRedisCounter(rdb redis.Cmdable, tenant int64, contact uint32, asOf time.Time) *RedisCounter {
	return &RedisCounter{rdb: rdb, tenant: tenant, contact: contact, asOf: asOf, cache: map[string]map[string]string{}}
}

// InSegment reads the contact's bit in another segment, so rules can reference segments.
// Set overrides it (a value this evaluation just computed).
func (c *RedisCounter) InSegment(ctx context.Context, seg int64) (bool, error) {
	if v, ok := c.known[seg]; ok {
		return v, nil
	}
	bit, err := c.rdb.GetBit(ctx, MemberKey(c.tenant, seg), int64(c.contact)).Result()
	if err != nil {
		return false, err
	}
	c.Set(seg, bit == 1)
	return bit == 1, nil
}

func (c *RedisCounter) Set(seg int64, in bool) {
	if c.known == nil {
		c.known = map[int64]bool{}
	}
	c.known[seg] = in
}

func (c *RedisCounter) Count(ctx context.Context, ev string, w Window) (int, error) {
	if !w.InCounters(c.asOf) {
		return 0, ErrNeedsSweep
	}
	buckets, ok := c.cache[ev]
	if !ok {
		var err error
		buckets, err = c.rdb.HGetAll(ctx, CounterKey(c.tenant, c.contact, ev)).Result()
		if err != nil {
			return 0, err
		}
		c.cache[ev] = buckets
	}
	return w.Sum(buckets, c.asOf)
}
