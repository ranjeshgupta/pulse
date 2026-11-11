package reach

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/segment"
)

// Query is an audience to count: members of any Include segment, minus members of any Exclude
// segment, then (with a Channel) minus those opted out of it and those it can't reach.
type Query struct {
	Tenant  int64
	Include []int64
	Exclude []int64
	Channel string
}

// Counts are the audience after each step. Without a channel, the last two equal AfterExclude.
type Counts struct {
	Audience     int64 `json:"audience"`      // in any included segment
	AfterExclude int64 `json:"after_exclude"` // ...and in no excluded one
	AfterConsent int64 `json:"after_consent"` // ...and not opted out of the channel (or of everything)
	Reachable    int64 `json:"reachable"`     // ...and with a working address on the channel
}

// tmpTTL bounds temporary keys if a count dies halfway.
const tmpTTL = 60 * time.Second

// Count computes the counts in one Redis round trip with BITOP. A AND NOT B is computed as
// A XOR (A AND B), which pads shorter bitmaps with zeros correctly (NOT would turn the padding into
// ones). With keep, the reachable bitmap is left under the returned key (expiring in a minute) for
// the caller to read; the caller deletes it.
func Count(ctx context.Context, rdb redis.Cmdable, q Query, keep bool) (Counts, string, error) {
	if len(q.Include) == 0 {
		return Counts{}, "", errors.New("choose at least one segment")
	}
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	tmp := func(s string) string {
		return "rchtmp:" + strconv.FormatInt(q.Tenant, 10) + ":" + hex.EncodeToString(nonce[:]) + ":" + s
	}
	segKeys := func(ids []int64) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = segment.MemberKey(q.Tenant, id)
		}
		return out
	}
	var temps []string
	p := rdb.Pipeline()
	expire := func(k string) {
		temps = append(temps, k)
		p.Expire(ctx, k, tmpTTL)
	}
	// andNot writes a AND NOT b into dst.
	andNot := func(dst, a, b string) {
		both := tmp("both" + dst[len(dst)-1:])
		p.BitOpAnd(ctx, both, a, b)
		expire(both)
		p.BitOpXor(ctx, dst, a, both)
		expire(dst)
	}

	inc := tmp("i")
	p.BitOpOr(ctx, inc, segKeys(q.Include)...)
	expire(inc)
	nAud := p.BitCount(ctx, inc, nil)

	aud := inc
	if len(q.Exclude) > 0 {
		exc := tmp("e")
		p.BitOpOr(ctx, exc, segKeys(q.Exclude)...)
		expire(exc)
		aud = tmp("a")
		andNot(aud, inc, exc)
	}
	nExc := p.BitCount(ctx, aud, nil)

	last := aud
	var nCon, nReach *redis.IntCmd
	if q.Channel != "" {
		con := tmp("c")
		andNot(con, aud, Key(q.Tenant, OptOut(q.Channel)))
		nCon = p.BitCount(ctx, con, nil)
		r := tmp("r")
		p.BitOpAnd(ctx, r, con, Key(q.Tenant, AddressOf(q.Channel)))
		expire(r)
		nReach = p.BitCount(ctx, r, nil)
		last = r
	}
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		cleanup(rdb, temps)
		return Counts{}, "", err
	}
	c := Counts{Audience: nAud.Val(), AfterExclude: nExc.Val(), AfterConsent: nExc.Val(), Reachable: nExc.Val()}
	if nCon != nil {
		c.AfterConsent, c.Reachable = nCon.Val(), nReach.Val()
	}
	kept := ""
	if keep {
		kept = last
		var rest []string
		for _, k := range temps {
			if k != last {
				rest = append(rest, k)
			}
		}
		temps = rest
	}
	cleanup(rdb, temps)
	return c, kept, nil
}

func cleanup(rdb redis.Cmdable, keys []string) {
	if len(keys) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = rdb.Unlink(ctx, keys...).Err()
}

// ChannelCounts are one channel's numbers for a segment.
type ChannelCounts struct {
	Channel      string `json:"channel"`
	AfterConsent int64  `json:"after_consent"`
	Reachable    int64  `json:"reachable"`
}

// ByChannel counts a segment's members who can be reached on each channel.
func ByChannel(ctx context.Context, rdb redis.Cmdable, tenant, seg int64, channels []string) (int64, []ChannelCounts, error) {
	var size int64
	out := make([]ChannelCounts, 0, len(channels))
	for _, ch := range channels {
		c, _, err := Count(ctx, rdb, Query{Tenant: tenant, Include: []int64{seg}, Channel: ch}, false)
		if err != nil {
			return 0, nil, err
		}
		size = c.Audience
		out = append(out, ChannelCounts{Channel: ch, AfterConsent: c.AfterConsent, Reachable: c.Reachable})
	}
	return size, out, nil
}

// CapCheck counts how many contacts of a bitmap a frequency cap would hold back now: those with at
// least Max messages in their cap set within the window. Exact for up to ExactLimit members;
// above, it checks an evenly spread sample of Sample members and scales the result (Approximate).
type CapCheck struct {
	Key        func(contact uint32) string // the dispatcher's per-contact cap set
	Since      time.Time
	Max        int
	ExactLimit int
	Sample     int
}

// CapResult is how many would be capped.
type CapResult struct {
	Capped      int64 `json:"capped"`
	Approximate bool  `json:"approximate"`
	Checked     int   `json:"checked"`
}

// Capped runs a CapCheck over the bitmap stored at key.
func Capped(ctx context.Context, rdb redis.Cmdable, key string, cc CapCheck) (CapResult, error) {
	bm, err := rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return CapResult{}, nil
	} else if err != nil {
		return CapResult{}, err
	}
	ids := segment.Members(bm)
	ids, approx := SampleIDs(ids, cc.ExactLimit, cc.Sample)
	since := strconv.FormatInt(cc.Since.UnixMilli(), 10)
	var capped int64
	for start := 0; start < len(ids); start += 1000 {
		p := rdb.Pipeline()
		cmds := make([]*redis.IntCmd, 0, 1000)
		for _, id := range ids[start:min(start+1000, len(ids))] {
			cmds = append(cmds, p.ZCount(ctx, cc.Key(id), "("+since, "+inf"))
		}
		if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return CapResult{}, err
		}
		for _, c := range cmds {
			if c.Val() >= int64(cc.Max) {
				capped++
			}
		}
	}
	res := CapResult{Capped: capped, Approximate: approx > 1, Checked: len(ids)}
	if approx > 1 {
		res.Capped = int64(float64(capped) * approx)
	}
	return res, nil
}

// SampleIDs returns ids unchanged when there are at most exactLimit, else every k-th id (n of
// them) and the factor to scale counts by.
func SampleIDs(ids []uint32, exactLimit, n int) ([]uint32, float64) {
	if len(ids) <= exactLimit || n <= 0 || n >= len(ids) {
		return ids, 1
	}
	step := float64(len(ids)) / float64(n)
	out := make([]uint32, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ids[int(float64(i)*step)])
	}
	return out, step
}
