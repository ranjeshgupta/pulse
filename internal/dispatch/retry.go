package dispatch

// Retries of temporary send failures.
//
// A send that fails temporarily (network error, timeout, provider 429 or 5xx) isn't retried in
// place: that would hold up the partition, and with it other workspaces' messages, for as long as
// the provider is down. The message is parked in a Redis sorted set scored by when to try again,
// and RunDeferred re-publishes it to its channel topic then, with the number of tries so far in the
// record header HeaderTries. The waits grow from 30 seconds to an hour (about 1 h 50 min in all),
// and are at least the provider's Retry-After. After the last try the message is recorded as
// <channel>_failed with the reason. A retried message keeps its id and isn't marked sent until it
// goes out, so the "already sent" check lets it through exactly once.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/providers"
)

const (
	// retryKey is a sorted set of parked messages (JSON of parked), scored by when to try again.
	retryKey = "retry:messages"
	// HeaderTries is the record header with how many times a re-published message was tried.
	HeaderTries = "pulse-tries"
	// maxRetryAfter caps how long a provider's Retry-After can hold a message.
	maxRetryAfter = time.Hour
	retryBatch    = 500
)

// RetryBackoff is the wait before each retry of a temporarily failed send; when they are used up
// (8 tries in all), the message fails.
var RetryBackoff = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute,
	10 * time.Minute, 30 * time.Minute, time.Hour}

type parked struct {
	Tries int           `json:"tries"`
	Msg   model.Message `json:"m"`
}

// retryDelay is how long to wait after a message failed temporarily for the tries-th time:
// RetryBackoff, or the provider's Retry-After when that is longer (up to an hour). ok is false
// when the retries are used up.
func retryDelay(tries int, err error) (wait time.Duration, ok bool) {
	if tries < 1 || tries > len(RetryBackoff) {
		return 0, false
	}
	wait = RetryBackoff[tries-1]
	if after, ok := providers.RetryAfter(err); ok {
		wait = max(wait, min(after, maxRetryAfter))
	}
	return wait, true
}

// retryLater parks a message that has now failed temporarily tries times, or calls giveUp with
// the reason when the retries are used up.
func (d *Dispatcher) retryLater(ctx context.Context, m model.Message, tries int, err error, giveUp func(reason string) error) error {
	wait, ok := retryDelay(tries, err)
	if !ok {
		return giveUp(fmt.Sprintf("gave up after %d tries: %v", tries, err))
	}
	b, jerr := json.Marshal(parked{Tries: tries, Msg: m})
	if jerr != nil {
		return jerr
	}
	return d.rdb.ZAdd(ctx, retryKey, redis.Z{Score: float64(d.now().Add(wait).UnixMilli()), Member: string(b)}).Err()
}

// triesOf reads HeaderTries from a record (0 for a first try).
func triesOf(r *kgo.Record) int {
	for _, h := range r.Headers {
		if h.Key == HeaderTries {
			n, _ := strconv.Atoi(string(h.Value))
			return max(n, 0)
		}
	}
	return 0
}

// releaseRetries re-publishes every parked message that is due, in batches. ZREM decides which
// worker owns each.
func (d *Dispatcher) releaseRetries(ctx context.Context) {
	for ctx.Err() == nil {
		now := d.now()
		due, err := d.rdb.ZRangeByScore(ctx, retryKey, &redis.ZRangeBy{Min: "-inf",
			Max: strconv.FormatInt(now.UnixMilli(), 10), Count: retryBatch}).Result()
		if err != nil || len(due) == 0 {
			return
		}
		pipe := d.rdb.Pipeline()
		rems := make([]*redis.IntCmd, len(due))
		for i, raw := range due {
			rems[i] = pipe.ZRem(ctx, retryKey, raw)
		}
		_, _ = pipe.Exec(ctx) // each command's own result says whether this worker took the message
		var recs []*kgo.Record
		var taken []redis.Z
		for i, raw := range due {
			if rems[i].Val() != 1 {
				continue // another worker took it
			}
			var p parked
			if json.Unmarshal([]byte(raw), &p) != nil {
				continue
			}
			rec, err := kafka.JSON(channel.Topic(p.Msg.Channel), event.ContactKey(p.Msg.TenantID, p.Msg.ContactID), p.Msg)
			if err != nil {
				continue
			}
			rec.Headers = []kgo.RecordHeader{{Key: HeaderTries, Value: []byte(strconv.Itoa(p.Tries))}}
			recs = append(recs, rec)
			taken = append(taken, redis.Z{Score: float64(now.UnixMilli()), Member: raw})
		}
		if err := d.pub.Publish(ctx, recs...); err != nil {
			// Put them back for the next round (even when ctx ended: they must not be lost).
			if len(taken) > 0 {
				d.rdb.ZAdd(context.WithoutCancel(ctx), retryKey, taken...)
			}
			return
		}
		if len(due) < retryBatch {
			return
		}
	}
}
