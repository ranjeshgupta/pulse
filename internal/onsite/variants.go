package onsite

// Ordered variants: which ones can never be shown (an earlier variant takes everyone they would
// get), and, from the segment bitmaps, how many contacts match several variants and which one each
// of them would see.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/model"
	"pulse/internal/reach"
	"pulse/internal/segment"
)

// Shadow says variant Index can never be shown: everyone it targets matches variant By first.
type Shadow struct {
	Index int    `json:"index"`
	By    int    `json:"by"`
	Note  string `json:"note"`
}

// Everyone reports whether an audience has no rules at all.
func Everyone(a model.OnsiteAudience) bool {
	return len(a.SegmentIDs) == 0 && len(a.ExcludeSegmentIDs) == 0 && len(a.URLContains) == 0 &&
		len(a.URLNotContains) == 0 && len(a.Devices) == 0 && len(a.Conditions) == 0
}

func subset[T comparable](a, b []T) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// covers reports whether every visitor matching b also matches a (judged from the rules alone).
func covers(a, b model.OnsiteAudience) bool {
	if len(a.SegmentIDs) > 0 && (len(b.SegmentIDs) == 0 || !subset(b.SegmentIDs, a.SegmentIDs)) {
		return false
	}
	if !subset(a.ExcludeSegmentIDs, b.ExcludeSegmentIDs) {
		return false
	}
	if len(a.URLContains) > 0 {
		if len(b.URLContains) == 0 {
			return false
		}
		for _, pb := range b.URLContains { // a page with pb contains some pa
			if !slices.ContainsFunc(a.URLContains, func(pa string) bool { return strings.Contains(pb, pa) }) {
				return false
			}
		}
	}
	for _, pa := range a.URLNotContains { // a page b allows never contains pa
		if !slices.ContainsFunc(b.URLNotContains, func(pb string) bool { return strings.Contains(pa, pb) }) {
			return false
		}
	}
	if len(a.Devices) > 0 && (len(b.Devices) == 0 || !subset(b.Devices, a.Devices)) {
		return false
	}
	key := func(c model.OnsiteCondition) string { j, _ := json.Marshal(c); return string(j) }
	for _, ca := range a.Conditions {
		if !slices.ContainsFunc(b.Conditions, func(cb model.OnsiteCondition) bool { return key(ca) == key(cb) }) {
			return false
		}
	}
	return true
}

// Shadowed lists the variants (in order) that can never be shown, and whether the default can't.
func Shadowed(auds []model.OnsiteAudience) (out []Shadow, defaultNeverShown bool) {
	out = []Shadow{}
	for j := range auds {
		for i := 0; i < j; i++ {
			if covers(auds[i], auds[j]) {
				out = append(out, Shadow{Index: j, By: i, Note: "Never shown: everyone it targets sees variant " + strconv.Itoa(i+1) + " first. Move it up or narrow variant " + strconv.Itoa(i+1) + "."})
				break
			}
		}
	}
	for _, a := range auds {
		if Everyone(a) {
			return out, true
		}
	}
	return out, false
}

// SlotAudiences returns a slot's variant audiences in order.
func SlotAudiences(vs []model.DWCVariant) []model.OnsiteAudience {
	out := make([]model.OnsiteAudience, len(vs))
	for i, v := range vs {
		out[i] = v.Rules()
	}
	return out
}

// Overlap is how a set of ordered audiences splits the workspace's known contacts.
type Overlap struct {
	Variants []VariantCount `json:"variants"`
	Pairs    []PairCount    `json:"pairs"`    // contacts in both of two variants' audiences
	Multi    int64          `json:"multi"`    // contacts matching more than one variant
	Default  int64          `json:"default"`  // contacts matching none (they see the default)
	Contacts int64          `json:"contacts"` // all known contacts
	// Approximate: some variant has page, device or contact-field rules, which aren't counted
	// (its counts are of its segment rules only).
	Approximate bool      `json:"approximate"`
	At          time.Time `json:"at"`
}

// VariantCount: Matching contacts are in the variant's audience; Sees of them see it (no earlier
// variant matched them).
type VariantCount struct {
	Index       int   `json:"index"`
	Matching    int64 `json:"matching"`
	Sees        int64 `json:"sees"`
	Approximate bool  `json:"approximate,omitempty"`
}

type PairCount struct {
	A    int   `json:"a"`
	B    int   `json:"b"`
	Both int64 `json:"both"`
}

// MaxOverlapVariants bounds the BITOP work of one overlap count (pairs grow with the square).
const MaxOverlapVariants = MaxVariants

// CountOverlap computes Overlap with BITOP inside Redis in one pipeline: each audience is (any
// included segment, or every known contact) AND NOT (any excluded segment); A AND NOT B is
// written A XOR (A AND B) so shorter bitmaps pad with zeros. Only counts leave Redis.
func CountOverlap(ctx context.Context, rdb redis.Cmdable, tenant int64, auds []model.OnsiteAudience) (Overlap, error) {
	if len(auds) == 0 {
		return Overlap{}, errors.New("add a variant first")
	}
	if len(auds) > MaxOverlapVariants {
		return Overlap{}, errors.New("too many variants to compare")
	}
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	pre := "ovltmp:" + strconv.FormatInt(tenant, 10) + ":" + hex.EncodeToString(nonce[:]) + ":"
	var temps []string
	p := rdb.Pipeline()
	tmp := func(name string) string {
		k := pre + name
		temps = append(temps, k)
		return k
	}
	ttl := func(k string) { p.Expire(ctx, k, time.Minute) }
	known := reach.Key(tenant, reach.Known)
	segKeys := func(ids []int64) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = segment.MemberKey(tenant, id)
		}
		return out
	}
	res := Overlap{At: time.Now().UTC()}
	keys := make([]string, len(auds))
	counts := make([]*redis.IntCmd, len(auds))
	for i, a := range auds {
		inc := tmp("i" + strconv.Itoa(i))
		if len(a.SegmentIDs) > 0 {
			p.BitOpOr(ctx, inc, segKeys(a.SegmentIDs)...)
		} else {
			p.BitOpOr(ctx, inc, known)
		}
		ttl(inc)
		keys[i] = inc
		if len(a.ExcludeSegmentIDs) > 0 {
			exc, both, aud := tmp("e"+strconv.Itoa(i)), tmp("b"+strconv.Itoa(i)), tmp("a"+strconv.Itoa(i))
			p.BitOpOr(ctx, exc, segKeys(a.ExcludeSegmentIDs)...)
			p.BitOpAnd(ctx, both, inc, exc)
			p.BitOpXor(ctx, aud, inc, both)
			ttl(exc)
			ttl(both)
			ttl(aud)
			keys[i] = aud
		}
		counts[i] = p.BitCount(ctx, keys[i], nil)
		approx := len(a.URLContains) > 0 || len(a.URLNotContains) > 0 || len(a.Devices) > 0 || len(a.Conditions) > 0
		res.Approximate = res.Approximate || approx
		res.Variants = append(res.Variants, VariantCount{Index: i, Approximate: approx})
	}
	type pairCmd struct {
		a, b int
		c    *redis.IntCmd
	}
	var pairs []pairCmd
	for i := range auds {
		for j := i + 1; j < len(auds); j++ {
			k := tmp("p" + strconv.Itoa(i) + "_" + strconv.Itoa(j))
			p.BitOpAnd(ctx, k, keys[i], keys[j])
			ttl(k)
			pairs = append(pairs, pairCmd{i, j, p.BitCount(ctx, k, nil)})
		}
	}
	// Walk the order: earlier variants take their contacts first.
	union, multi := tmp("u"), tmp("m")
	taken := make([]*redis.IntCmd, len(auds))
	for i := range auds {
		if i > 0 {
			k := tmp("t" + strconv.Itoa(i))
			p.BitOpAnd(ctx, k, union, keys[i])
			ttl(k)
			taken[i] = p.BitCount(ctx, k, nil)
			p.BitOpOr(ctx, multi, multi, k)
		}
		p.BitOpOr(ctx, union, union, keys[i])
	}
	ttl(union)
	ttl(multi)
	nUnion, nMulti, nKnown := p.BitCount(ctx, union, nil), p.BitCount(ctx, multi, nil), p.BitCount(ctx, known, nil)
	_, err := p.Exec(ctx)
	if len(temps) > 0 {
		rdb.Del(context.WithoutCancel(ctx), temps...)
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return Overlap{}, err
	}
	for i := range auds {
		res.Variants[i].Matching = counts[i].Val()
		res.Variants[i].Sees = counts[i].Val()
		if taken[i] != nil {
			res.Variants[i].Sees -= taken[i].Val()
		}
	}
	for _, pc := range pairs {
		res.Pairs = append(res.Pairs, PairCount{A: pc.a, B: pc.b, Both: pc.c.Val()})
	}
	res.Multi, res.Contacts = nMulti.Val(), nKnown.Val()
	res.Default = max(0, res.Contacts-nUnion.Val())
	return res, nil
}
