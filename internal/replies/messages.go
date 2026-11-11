package replies

// Replies to RCS messages. An RCS reply (typed text or a tapped suggestion) doesn't name the agent
// message it answers, so Pulse attributes it to the latest RCS message the contact got: the
// dispatcher remembers that message's tracking token per contact for ReplyWindow, and the inbound
// webhook records rcs_replied with the message's ids, which a journey's "replied" decision waits for.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/tracking"
)

// ReplyWindow is how long after a message an inbound message still counts as a reply to it.
const ReplyWindow = 7 * 24 * time.Hour

func lastKey(tenant int64, ch string, contact uint32) string {
	return fmt.Sprintf("lastmsg:%d:%s:%d", tenant, ch, contact)
}

// SaveLast remembers tok as the latest message sent to the contact on its channel.
func SaveLast(ctx context.Context, rdb redis.Cmdable, contact uint32, tok tracking.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, lastKey(tok.TenantID, tok.Channel, contact), b, ReplyWindow).Err()
}

// Last returns the latest message sent to the contact on ch within the reply window.
func Last(ctx context.Context, rdb redis.Cmdable, tenant int64, ch string, contact uint32) (tracking.Token, bool, error) {
	raw, err := rdb.Get(ctx, lastKey(tenant, ch, contact)).Bytes()
	if errors.Is(err, redis.Nil) {
		return tracking.Token{}, false, nil
	} else if err != nil {
		return tracking.Token{}, false, err
	}
	var tok tracking.Token
	if err := json.Unmarshal(raw, &tok); err != nil {
		return tracking.Token{}, false, nil
	}
	return tok, true, nil
}
