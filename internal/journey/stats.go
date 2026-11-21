package journey

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStepStats counts contacts per journey step and output in one Redis hash per journey
// (field "step|output"). Counts are best effort: a retried step can count twice.
type RedisStepStats struct{ RDB redis.Cmdable }

func stepStatsKey(tenant, journey int64) string {
	return "jsteps:" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(journey, 10)
}

// StepHourKey holds a journey's step counts of one UTC hour (yyyymmddhh), for reports over time.
func StepHourKey(tenant, journey int64, hour time.Time) string {
	return stepStatsKey(tenant, journey) + ":h:" + hour.UTC().Format("2006010215")
}

// StepHourTTL is how long hourly step counts are kept.
const StepHourTTL = 400 * 24 * time.Hour

func (s RedisStepStats) Count(ctx context.Context, tenant, journey int64, step, output string) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	field := step + "|" + output
	hk := StepHourKey(tenant, journey, time.Now())
	pipe := s.RDB.Pipeline()
	pipe.HIncrBy(ctx, stepStatsKey(tenant, journey), field, 1)
	pipe.HIncrBy(ctx, hk, field, 1)
	pipe.Expire(ctx, hk, StepHourTTL)
	_, _ = pipe.Exec(ctx)
}

// HourCount is one step output's count in one UTC hour.
type HourCount struct {
	Hour   time.Time
	Step   string
	Output string
	N      int64
}

// ReadStepHours returns a journey's hourly step counts in [from, to).
func ReadStepHours(ctx context.Context, rdb redis.Cmdable, tenant, journey int64, from, to time.Time) ([]HourCount, error) {
	var hours []time.Time
	for h := from.UTC().Truncate(time.Hour); h.Before(to); h = h.Add(time.Hour) {
		hours = append(hours, h)
	}
	pipe := rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(hours))
	for i, h := range hours {
		cmds[i] = pipe.HGetAll(ctx, StepHourKey(tenant, journey, h))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var out []HourCount
	for i, c := range cmds {
		for k, v := range c.Val() {
			step, output, ok := strings.Cut(k, "|")
			n, err := strconv.ParseInt(v, 10, 64)
			if ok && err == nil && n > 0 {
				out = append(out, HourCount{Hour: hours[i], Step: step, Output: output, N: n})
			}
		}
	}
	return out, nil
}

// ReadStepStats returns a journey's counts: step -> output (entered, yes, no, v0...) -> contacts.
func ReadStepStats(ctx context.Context, rdb redis.Cmdable, tenant, journey int64) (map[string]map[string]int64, error) {
	m, err := rdb.HGetAll(ctx, stepStatsKey(tenant, journey)).Result()
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]int64{}
	for k, v := range m {
		step, output, ok := strings.Cut(k, "|")
		n, err := strconv.ParseInt(v, 10, 64)
		if !ok || err != nil {
			continue
		}
		if out[step] == nil {
			out[step] = map[string]int64{}
		}
		out[step][output] = n
	}
	return out, nil
}
