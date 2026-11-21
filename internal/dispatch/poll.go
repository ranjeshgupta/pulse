package dispatch

// Status requests (custom gateways with a "We ask them" delivery-report setup): after a send, the
// message is queued per provider in a Redis sorted set by when to ask next; RunPolls asks the
// provider, publishes what it answers to delivery.receipts like a webhook would, and asks again
// until the status is final or the follow-up window ends. ZREM decides which worker asks.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/providers"
)

const (
	pollIndex   = "rpoll:index" // members tenant:provider:channel
	pollBatch   = 50            // messages asked about per provider per round
	pollRound   = 15 * time.Second
	pollTimeout = 20 * time.Second
)

func pollKey(tenant, provider int64) string { return fmt.Sprintf("rpoll:%d:%d", tenant, provider) }

// pollItem is one message waiting for a final status.
type pollItem struct {
	Key        string `json:"k"` // what its receipts are matched by (provider id, or Pulse's id)
	MessageID  string `json:"m"`
	ProviderID string `json:"p,omitempty"`
	Ref        string `json:"r,omitempty"`
	To         string `json:"t"`
	Channel    string `json:"c"`
	Sent       int64  `json:"s"`
}

// queuePoll follows a sent message when its provider is asked for statuses.
func (d *Dispatcher) queuePoll(ctx context.Context, via sendVia, m model.Message, it pollItem) {
	pl, ok := via.p.(providers.Poller)
	if !ok || via.id == 0 {
		return
	}
	every, _, on := pl.Polls()
	if !on {
		return
	}
	b, _ := json.Marshal(it)
	pipe := d.rdb.Pipeline()
	pipe.ZAdd(ctx, pollKey(m.TenantID, via.id), redis.Z{Score: float64(d.now().Add(every).Unix()), Member: string(b)})
	pipe.SAdd(ctx, pollIndex, fmt.Sprintf("%d:%d:%s", m.TenantID, via.id, m.Channel))
	if _, err := pipe.Exec(ctx); err != nil && d.cfg.Log != nil {
		d.cfg.Log.Error("queue status request", "message", m.ID, "err", err)
	}
}

// RunPolls asks providers for the statuses that are due, every 15 seconds.
func (d *Dispatcher) RunPolls(ctx context.Context) error {
	tick := time.NewTicker(pollRound)
	defer tick.Stop()
	for {
		d.pollRound(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (d *Dispatcher) pollRound(ctx context.Context) {
	idx, err := d.rdb.SMembers(ctx, pollIndex).Result()
	if err != nil {
		return
	}
	for _, entry := range idx {
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) != 3 {
			d.rdb.SRem(ctx, pollIndex, entry)
			continue
		}
		tenant, _ := strconv.ParseInt(parts[0], 10, 64)
		provider, _ := strconv.ParseInt(parts[1], 10, 64)
		d.pollProvider(ctx, entry, tenant, provider, parts[2])
		if ctx.Err() != nil {
			return
		}
	}
}

func (d *Dispatcher) pollProvider(ctx context.Context, entry string, tenant, provider int64, ch string) {
	key := pollKey(tenant, provider)
	now := d.now()
	due, err := d.rdb.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(now.Unix(), 10), Count: pollBatch}).Result()
	if err != nil || len(due) == 0 {
		if err == nil && d.rdb.ZCard(ctx, key).Val() == 0 {
			d.rdb.SRem(ctx, pollIndex, entry)
		}
		return
	}
	via, err := d.senderVia(ctx, tenant, ch, provider)
	pl, ok := via.p.(providers.Poller)
	if err == nil && ok && via.id != provider {
		ok = false // the provider is gone and the channel's default answered instead
	}
	var every, follow time.Duration
	if ok {
		every, follow, ok = pl.Polls()
	}
	if err != nil || !ok {
		// Deleted, or no longer asked: stop following its messages.
		d.rdb.Del(ctx, key)
		d.rdb.SRem(ctx, pollIndex, entry)
		return
	}
	var recs []*kgo.Record
	for _, member := range due {
		if d.rdb.ZRem(ctx, key, member).Val() == 0 {
			continue // another worker took it
		}
		var it pollItem
		if json.Unmarshal([]byte(member), &it) != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, pollTimeout)
		rs, err := pl.Poll(cctx, providers.Outbound{TenantID: tenant, MessageID: it.MessageID, Channel: it.Channel, To: it.To,
			Ref: it.Ref, ProviderMessageID: it.ProviderID}, it.Key)
		cancel()
		final := false
		if err != nil && d.cfg.Log != nil {
			d.cfg.Log.Warn("status request", "provider", provider, "message", it.MessageID, "err", err)
		}
		for _, rc := range rs {
			switch rc.Status {
			case "delivered", "failed", "read", "bounced", "complained":
				final = true
			default:
				continue // pending: ask again
			}
			rec, err := kafka.JSON(TopicReceipts, []byte(strconv.FormatInt(tenant, 10)+":"+rc.ProviderMessageID), ReceiptRecord{TenantID: tenant, Receipt: rc})
			if err == nil {
				recs = append(recs, rec)
			}
		}
		if !final && now.Before(time.Unix(it.Sent, 0).Add(follow)) {
			d.rdb.ZAdd(ctx, key, redis.Z{Score: float64(now.Add(every).Unix()), Member: member})
		}
	}
	if len(recs) > 0 {
		if err := d.pub.Publish(ctx, recs...); err != nil && d.cfg.Log != nil {
			d.cfg.Log.Error("publish polled receipts", "provider", provider, "err", err)
		}
	}
}
