package segment

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func MemberKey(tenant, segment int64) string { return fmt.Sprintf("seg:%d:%d", tenant, segment) }

// Membership reads and writes segment bitmaps in Redis.
type Membership struct{ RDB redis.Cmdable }

func (m Membership) IsMember(ctx context.Context, tenant, segment int64, contact uint32) (bool, error) {
	v, err := m.RDB.GetBit(ctx, MemberKey(tenant, segment), int64(contact)).Result()
	return v == 1, err
}

// SetMember flips one bit and reports whether it changed.
func (m Membership) SetMember(ctx context.Context, tenant, segment int64, contact uint32, in bool) (bool, error) {
	val := 0
	if in {
		val = 1
	}
	old, err := m.RDB.SetBit(ctx, MemberKey(tenant, segment), int64(contact), val).Result()
	return old != int64(val), err
}

func (m Membership) Size(ctx context.Context, tenant, segment int64) (int64, error) {
	return m.RDB.BitCount(ctx, MemberKey(tenant, segment), nil).Result()
}

func (m Membership) Load(ctx context.Context, tenant, segment int64) ([]byte, error) {
	b, err := m.RDB.Get(ctx, MemberKey(tenant, segment)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return b, err
}

// Delete drops a segment's bitmap (the segment was deleted).
func (m Membership) Delete(ctx context.Context, tenant, segment int64) error {
	return m.RDB.Del(ctx, MemberKey(tenant, segment)).Err()
}

// Replace swaps in a whole bitmap atomically (SET to a temp key, then RENAME in one MULTI).
func (m Membership) Replace(ctx context.Context, tenant, segment int64, bitmap []byte) error {
	key := MemberKey(tenant, segment)
	if len(bitmap) == 0 {
		return m.RDB.Del(ctx, key).Err()
	}
	tmp := key + ":tmp"
	_, err := m.RDB.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, tmp, bitmap, 0)
		p.Rename(ctx, tmp, key)
		return nil
	})
	return err
}
