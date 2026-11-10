package dispatch

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucket refills `rate` tokens per second up to `burst`, shared by every dispatcher replica.
// It returns 0 when a token was taken, or the milliseconds to wait for the next one.
// The caller passes the clock so the script stays deterministic (and testable).
var tokenBucket = redis.NewScript(`
local rate  = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now   = tonumber(ARGV[3])
local s = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(s[1]) or burst
local ts = tonumber(s[2]) or now
tokens = math.min(burst, tokens + math.max(0, now - ts) * rate / 1000)
local wait = 0
if tokens >= 1 then
  tokens = tokens - 1
else
  wait = math.ceil((1 - tokens) * 1000 / rate)
end
redis.call('HSET', KEYS[1], 'tokens', tostring(tokens), 'ts', tostring(now))
redis.call('PEXPIRE', KEYS[1], 60000)
return wait
`)

type Limiter struct {
	RDB   redis.Cmdable
	Rate  float64 // tokens per second
	Burst float64
	Now   func() time.Time
}

// Reserve tries to take a token and returns how long to wait if none was available.
func (l *Limiter) Reserve(ctx context.Context, key string) (time.Duration, error) {
	ms, err := tokenBucket.Run(ctx, l.RDB, []string{key}, l.Rate, l.Burst, l.Now().UnixMilli()).Int64()
	return time.Duration(ms) * time.Millisecond, err
}

// Wait blocks until a token is taken.
func (l *Limiter) Wait(ctx context.Context, key string) error {
	for {
		d, err := l.Reserve(ctx, key)
		if err != nil || d == 0 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}
