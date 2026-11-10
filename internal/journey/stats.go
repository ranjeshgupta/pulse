package journey

import (
	"context"
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

func (s RedisStepStats) Count(ctx context.Context, tenant, journey int64, step, output string) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = s.RDB.HIncrBy(ctx, stepStatsKey(tenant, journey), step+"|"+output, 1).Err()
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
