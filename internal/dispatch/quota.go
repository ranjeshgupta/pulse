package dispatch

// Per-provider limits (model.ProviderLimits): a send rate shared by every dispatcher (a token
// bucket per provider in Redis, on top of the workspace's per-channel rate) and a daily quota (a
// Redis counter per provider and day, the day running in the provider's time zone). A message over
// the quota is held until the next day starts there, like a quiet-hours message; if its deadline
// comes first it is dropped as late instead.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/model"
	"pulse/internal/providers"
)

// sendVia is a workspace's default provider for a channel with its limits (ID 0: the server's own
// SMTP, which has none).
type sendVia struct {
	p      providers.Provider
	id     int64
	name   string
	limits model.ProviderLimits
}

// QuotaKey counts a provider's messages on one day (yyyymmdd in its time zone).
func QuotaKey(provider int64, day string) string { return fmt.Sprintf("quota:%d:%s", provider, day) }

// QuotaDay is the current quota day in zone tz (UTC when empty or unknown) and when the next one starts.
func QuotaDay(tz string, now time.Time) (day string, next time.Time) {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	l := now.In(loc)
	start := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	return l.Format("20060102"), start.AddDate(0, 0, 1)
}

// quotaScript takes one message from a provider's quota for the day: it returns the new count, or
// -1 when the quota is used up (nothing is taken then). Every dispatcher shares the counter.
var quotaScript = redis.NewScript(`
local limit = tonumber(ARGV[1])
local n = tonumber(redis.call('GET', KEYS[1]) or '0')
if n >= limit then return -1 end
n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], 259200) end
return n
`)

// TakeQuota takes one message from a provider's quota for today. ok is false when it is used up;
// next is when the next day starts.
func TakeQuota(ctx context.Context, rdb redis.Cmdable, provider int64, l model.ProviderLimits, now time.Time) (ok bool, next time.Time, err error) {
	day, next := QuotaDay(l.Timezone, now)
	if l.DailyQuota <= 0 {
		return true, next, nil
	}
	n, err := quotaScript.Run(ctx, rdb, []string{QuotaKey(provider, day)}, l.DailyQuota).Int64()
	if err != nil {
		return false, next, err
	}
	return n >= 0, next, nil
}

// refundQuota gives a message back when nothing went out.
func refundQuota(ctx context.Context, rdb redis.Cmdable, provider int64, l model.ProviderLimits, now time.Time) {
	if l.DailyQuota <= 0 {
		return
	}
	day, _ := QuotaDay(l.Timezone, now)
	_ = rdb.Decr(context.WithoutCancel(ctx), QuotaKey(provider, day)).Err()
}

// UsageToday reads how many messages a provider has sent today.
func UsageToday(ctx context.Context, rdb redis.Cmdable, provider int64, l model.ProviderLimits, now time.Time) (int64, error) {
	day, _ := QuotaDay(l.Timezone, now)
	v, err := rdb.Get(ctx, QuotaKey(provider, day)).Result()
	if err == redis.Nil {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// providerRateKey is a provider's token bucket.
func providerRateKey(provider int64) string { return "rlp:" + strconv.FormatInt(provider, 10) }

// waitProviderRate blocks until the provider's rate allows one more message.
func (d *Dispatcher) waitProviderRate(ctx context.Context, v sendVia) error {
	if v.id == 0 || v.limits.RatePerSecond <= 0 {
		return nil
	}
	l := &Limiter{RDB: d.rdb, Rate: v.limits.RatePerSecond, Burst: max(1, v.limits.RatePerSecond), Now: time.Now}
	return l.Wait(ctx, providerRateKey(v.id))
}

// ValidateLimits checks provider limits entered in Settings → Channels.
func ValidateLimits(l model.ProviderLimits) error {
	switch {
	case l.RatePerSecond < 0 || l.RatePerSecond > 10000:
		return fmt.Errorf("messages per second must be 0 (no limit) to 10000")
	case l.DailyQuota < 0 || l.DailyQuota > 1_000_000_000:
		return fmt.Errorf("the daily quota must be 0 (none) to 1,000,000,000")
	}
	if l.Timezone != "" {
		if _, err := time.LoadLocation(l.Timezone); err != nil {
			return fmt.Errorf("unknown time zone %q", l.Timezone)
		}
	}
	return nil
}

// ---- deadlines ----

// deadline is when a message must have gone by, if it has a deadline: Deadline, or for a
// local-time message DeadlineLocal in the contact's zone.
func (d *Dispatcher) deadline(m model.Message, c model.Contact) (time.Time, bool) {
	if m.Deadline != nil {
		return *m.Deadline, true
	}
	if m.DeadlineLocal != "" {
		loc, err := time.LoadLocation(localZone(c, m.LocalZone))
		if err != nil {
			loc = time.UTC
		}
		if t, err := time.ParseInLocation("2006-01-02T15:04", m.DeadlineLocal, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// late drops a message that can't go out before its deadline: <channel>_failed, reason "deadline".
func (d *Dispatcher) late(ctx context.Context, m model.Message, c model.Contact) error {
	return d.finish(ctx, sentKeyOf(m), m, c, "failed", model.DeadlineReason)
}

func sentKeyOf(m model.Message) string { return fmt.Sprintf("sent:%d:%s", m.TenantID, m.ID) }
