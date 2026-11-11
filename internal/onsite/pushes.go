package onsite

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Pushes keeps per-contact on-site content set by journeys: focus items to show on the contact's
// next page view (shown once), and Dynamic Web Content per slot (until it expires or is replaced).
type Pushes struct{ RDB redis.Cmdable }

const focusTTL = 30 * 24 * time.Hour

func focusKey(tenant int64, contact uint32) string {
	return fmt.Sprintf("ofocus:%d:%d", tenant, contact)
}
func dwcKey(tenant int64, contact uint32) string { return fmt.Sprintf("odwc:%d:%d", tenant, contact) }

// PushFocus queues a focus item for the contact's next page view.
func (p Pushes) PushFocus(ctx context.Context, tenant int64, contact uint32, focusID int64) error {
	k := focusKey(tenant, contact)
	pipe := p.RDB.TxPipeline()
	pipe.SAdd(ctx, k, focusID)
	pipe.Expire(ctx, k, focusTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// PushDWC sets the contact's content for a slot for ttl.
func (p Pushes) PushDWC(ctx context.Context, tenant int64, contact uint32, slot, html string, ttl time.Duration) error {
	k := dwcKey(tenant, contact)
	pipe := p.RDB.TxPipeline()
	pipe.HSet(ctx, k, slot, html)
	pipe.Expire(ctx, k, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// personalMark starts stored content that a journey personalised with the contact's data: the
// mark, then JSON {"p": personal HTML, "a": anonymous HTML}.
const personalMark = "\x00pulse-personal\x00"

// PersonalDWC encodes content a journey step personalised: personal is filled with the contact's
// data, anon with the variables' defaults (for visitors whose identity isn't verified).
func PersonalDWC(personal, anon string) string {
	b, _ := json.Marshal(map[string]string{"p": personal, "a": anon})
	return personalMark + string(b)
}

// ResolveDWC returns the stored content to show: personalised content only when the visitor's
// identity is verified, else its anonymous version. Plain content is returned as it is.
func ResolveDWC(stored string, verified bool) string {
	raw, ok := strings.CutPrefix(stored, personalMark)
	if !ok {
		return stored
	}
	var v map[string]string
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}
	if verified {
		return v["p"]
	}
	return v["a"]
}

// TakeFocus returns and clears the focus items queued for the contact.
func (p Pushes) TakeFocus(ctx context.Context, tenant int64, contact uint32) ([]int64, error) {
	k := focusKey(tenant, contact)
	pipe := p.RDB.TxPipeline()
	members := pipe.SMembers(ctx, k)
	pipe.Del(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	var out []int64
	for _, m := range members.Val() {
		if id, err := strconv.ParseInt(m, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out, nil
}

// DWC returns the contact's pushed content for these slots.
func (p Pushes) DWC(ctx context.Context, tenant int64, contact uint32, slots []string) (map[string]string, error) {
	out := map[string]string{}
	if len(slots) == 0 {
		return out, nil
	}
	vals, err := p.RDB.HMGet(ctx, dwcKey(tenant, contact), slots...).Result()
	if err != nil {
		return nil, err
	}
	for i, v := range vals {
		if s, ok := v.(string); ok {
			out[slots[i]] = s
		}
	}
	return out, nil
}
