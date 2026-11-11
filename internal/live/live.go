// Package live keeps each workspace's latest events for the live event feed (Analytics → Live):
// the ClickHouse sink appends every event it stores to a capped Redis stream per workspace
// (live:{tenant}, about Keep events or the workspace's own size), at most MaxPerSecond events per workspace per second (the rest still reach
// reports, just not the feed), and the API reads the stream after a cursor. Keys are per
// workspace, so a reader only ever sees its own workspace's events.
package live

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/event"
)

const (
	// MaxPerSecond is how many events a workspace's feed takes per second.
	MaxPerSecond = 50
	// Keep is about how many events each stream holds unless the workspace set its own size
	// (Settings → Automation & limits; SetSize). MaxKeep bounds that size.
	Keep    = 1000
	MaxKeep = 10000
	// MaxProps bounds the properties kept per event (larger ones are dropped from the feed item).
	MaxProps = 4 << 10
	ttl      = 24 * time.Hour
)

// Item is one event in the feed. Props are as stored (the API removes personal properties).
type Item struct {
	Cursor     string          `json:"cursor"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Ts         time.Time       `json:"ts"`
	ExternalID string          `json:"external_id"`
	ContactID  uint32          `json:"contact_id"`
	Props      json.RawMessage `json:"props,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
}

// Key is a workspace's stream.
func Key(tenant int64) string { return "live:" + strconv.FormatInt(tenant, 10) }

// SizeKey holds a workspace's feed size when it isn't Keep.
func SizeKey(tenant int64) string { return "livek:" + strconv.FormatInt(tenant, 10) }

// SetSize records a workspace's feed size (the API on save, and the daily cleanup); n out of
// range (or Keep) goes back to Keep.
func SetSize(ctx context.Context, rdb redis.Cmdable, tenant int64, n int) error {
	if n <= 0 || n > MaxKeep || n == Keep {
		return rdb.Del(ctx, SizeKey(tenant)).Err()
	}
	return rdb.Set(ctx, SizeKey(tenant), n, 0).Err()
}

// sizeOf reads a size from SizeKey's value ("" or invalid: Keep).
func sizeOf(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 || n > MaxKeep {
		return Keep
	}
	return n
}

// Feed appends events to the streams.
type Feed struct {
	RDB redis.Cmdable
	Now func() time.Time
}

// Push adds a batch of stored events (best effort: the feed is a view, not a record).
func (f Feed) Push(ctx context.Context, evs []event.Event) error {
	if f.RDB == nil || len(evs) == 0 {
		return nil
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	sec := strconv.FormatInt(now().Unix(), 10)
	byTenant := map[int64][]event.Event{}
	for _, e := range evs {
		byTenant[e.TenantID] = append(byTenant[e.TenantID], e)
	}
	// How many each workspace may still add this second.
	counters := map[int64]*redis.IntCmd{}
	sizes := map[int64]*redis.StringCmd{}
	p := f.RDB.Pipeline()
	for t, es := range byTenant {
		k := "livec:" + strconv.FormatInt(t, 10) + ":" + sec
		counters[t] = p.IncrBy(ctx, k, int64(len(es)))
		p.Expire(ctx, k, 5*time.Second)
		sizes[t] = p.Get(ctx, SizeKey(t))
	}
	if _, err := p.Exec(ctx); err != nil && err != redis.Nil {
		return err
	}
	p = f.RDB.Pipeline()
	for t, es := range byTenant {
		after := counters[t].Val()
		room := MaxPerSecond - (after - int64(len(es)))
		if room <= 0 {
			continue
		}
		if int64(len(es)) > room {
			es = es[len(es)-int(room):] // the newest
		}
		for _, e := range es {
			it := Item{ID: e.ID, Name: e.Name, Ts: e.Ts, ExternalID: e.ExternalID, ContactID: e.ContactID}
			if len(e.Props) > 0 {
				if b, err := json.Marshal(e.Props); err == nil {
					if len(b) <= MaxProps {
						it.Props = b
					} else {
						it.Truncated = true
					}
				}
			}
			b, _ := json.Marshal(it)
			p.XAdd(ctx, &redis.XAddArgs{Stream: Key(t), MaxLen: sizeOf(sizes[t].Val()), Approx: true, Values: []any{"e", b}})
		}
		p.Expire(ctx, Key(t), ttl)
	}
	_, err := p.Exec(ctx)
	return err
}

// Read returns up to limit events after the cursor (oldest first), or the latest limit events
// when cursor is empty. The returned cursor continues from the last event (the given one when
// nothing is new).
func Read(ctx context.Context, rdb redis.Cmdable, tenant int64, cursor string, limit int) ([]Item, string, error) {
	var msgs []redis.XMessage
	var err error
	if cursor == "" {
		msgs, err = rdb.XRevRangeN(ctx, Key(tenant), "+", "-", int64(limit)).Result()
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	} else {
		msgs, err = rdb.XRangeN(ctx, Key(tenant), "("+cursor, "+", int64(limit)).Result()
	}
	if err != nil {
		return nil, cursor, err
	}
	out := make([]Item, 0, len(msgs))
	for _, m := range msgs {
		raw, _ := m.Values["e"].(string)
		var it Item
		if json.Unmarshal([]byte(raw), &it) != nil {
			continue
		}
		it.Cursor = m.ID
		cursor = m.ID
		out = append(out, it)
	}
	return out, cursor, nil
}

// ValidCursor reports whether s looks like a stream id ("1731300000000-0").
func ValidCursor(s string) bool {
	if s == "" {
		return true
	}
	ms, seq, ok := cut(s, '-')
	if !ok {
		return false
	}
	_, e1 := strconv.ParseUint(ms, 10, 64)
	_, e2 := strconv.ParseUint(seq, 10, 64)
	return e1 == nil && e2 == nil
}

func cut(s string, c byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
