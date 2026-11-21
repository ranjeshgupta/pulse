// Package besttime is "send at each contact's best time" (O6): every few hours, each contact's hour
// of the day (UTC) with the most opens, clicks and reads over the last 90 days is kept in Redis, with
// the workspace's busiest hour for contacts without history. A campaign set to it holds each
// message until that hour, within a day of its start.
package besttime

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Window is how far back engagement counts.
const Window = 90 * 24 * time.Hour

// TTL keeps a computed hour until a newer pass replaces it.
const TTL = 3 * 24 * time.Hour

func key(tenant int64) string { return "besthour:" + strconv.FormatInt(tenant, 10) }

// DefaultField holds the workspace's busiest hour in the hash.
const DefaultField = "default"

// Source reads engagement hours (ch.Store).
type Source interface {
	// BestHours calls fn with each engaged contact's busiest UTC hour; DefaultHour is the busiest
	// hour over every contact (ok false without engagement).
	BestHours(ctx context.Context, tenant int64, since time.Time, fn func(contact uint32, hour uint8) error) error
	DefaultHour(ctx context.Context, tenant int64, since time.Time) (uint8, bool, error)
}

// Refresh recomputes one workspace's hours.
func Refresh(ctx context.Context, src Source, rdb redis.Cmdable, tenant int64, now time.Time) (int, error) {
	since := now.Add(-Window)
	k := key(tenant)
	pipe := rdb.Pipeline()
	n := 0
	flush := func() error {
		if pipe.Len() == 0 {
			return nil
		}
		pipe.Expire(ctx, k, TTL)
		_, err := pipe.Exec(ctx)
		pipe = rdb.Pipeline()
		return err
	}
	err := src.BestHours(ctx, tenant, since, func(contact uint32, hour uint8) error {
		pipe.HSet(ctx, k, strconv.FormatUint(uint64(contact), 10), hour)
		n++
		if n%5000 == 0 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return n, err
	}
	if h, ok, err := src.DefaultHour(ctx, tenant, since); err != nil {
		return n, err
	} else if ok {
		pipe.HSet(ctx, k, DefaultField, h)
	}
	return n, flush()
}

// Hour is a contact's best UTC hour: theirs, else the workspace's; ok false when neither is known.
func Hour(ctx context.Context, rdb redis.Cmdable, tenant int64, contact uint32) (int, bool) {
	vals, err := rdb.HMGet(ctx, key(tenant), strconv.FormatUint(uint64(contact), 10), DefaultField).Result()
	if err != nil {
		return 0, false
	}
	for _, v := range vals {
		if s, ok := v.(string); ok {
			if h, err := strconv.Atoi(s); err == nil && h >= 0 && h < 24 {
				return h, true
			}
		}
	}
	return 0, false
}

// NextAt is the first time at hour (UTC) at or after from.
func NextAt(from time.Time, hour int) time.Time {
	from = from.UTC()
	t := time.Date(from.Year(), from.Month(), from.Day(), hour, 0, 0, 0, time.UTC)
	if t.Before(from) {
		t = t.Add(24 * time.Hour)
	}
	return t
}
