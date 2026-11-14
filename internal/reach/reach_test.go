package reach

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/automation"
	"pulse/internal/channel"
	"pulse/internal/segment"
)

func TestBits(t *testing.T) {
	for name, tc := range map[string]struct {
		attrs map[string]any
		on    []string
	}{
		"email only":         {map[string]any{"email": "a@x.com"}, []string{Known, Email}},
		"bounced email":      {map[string]any{"email": "a@x.com", "email_bounced": true}, []string{Known}},
		"phone":              {map[string]any{"phone": "+919800000001"}, []string{Known, Phone, SMS, WhatsApp, RCS}},
		"sms bounced":        {map[string]any{"phone": "+919800000001", "sms_bounced": "true"}, []string{Known, Phone, WhatsApp, RCS}},
		"whatsapp stopped":   {map[string]any{"phone": "+919800000001", "whatsapp_bounced": true}, []string{Known, Phone, SMS, RCS}},
		"national number":    {map[string]any{"phone": "98000 00001"}, []string{Known}},
		"opted out of sms":   {map[string]any{"phone": "+919800000001", "optout_sms": true}, []string{Known, Phone, SMS, WhatsApp, RCS, OptOut(channel.SMS)}},
		"do not contact":     {map[string]any{"email": "a@x.com", "optout_all": "true"}, append([]string{Known, Email}, allOut()...)},
		"merged away":        {map[string]any{"email": "a@x.com", "phone": "+919800000001", "merged_into": "u2"}, nil},
		"resubscribed email": {map[string]any{"email": "a@x.com", "optout_email": false}, []string{Known, Email}},
	} {
		got := Bits(tc.attrs)
		want := map[string]bool{}
		for _, n := range tc.on {
			want[n] = true
		}
		for _, n := range ProfileNames() {
			if got[n] != want[n] {
				t.Errorf("%s: %s = %v, want %v", name, n, got[n], want[n])
			}
		}
	}
	if !Relevant([]string{"last_seen", "optout_email"}) || Relevant([]string{"last_seen", "city"}) || !Relevant([]string{"phone"}) {
		t.Error("Relevant")
	}
	if AddressOf(channel.WhatsApp) != WhatsApp || AddressOf(channel.RCS) != RCS || AddressOf(channel.Inbox) != Known || AddressOf(channel.SMS) != SMS {
		t.Error("AddressOf")
	}
}

func allOut() []string {
	var out []string
	for _, ch := range channel.All {
		out = append(out, OptOut(ch))
	}
	return out
}

func setBits(t *testing.T, rdb *redis.Client, key string, ids map[uint32]bool) {
	t.Helper()
	for id := range ids {
		if err := rdb.SetBit(context.Background(), key, int64(id), 1).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func randomSet(r *rand.Rand, n, maxID int) map[uint32]bool {
	out := map[uint32]bool{}
	for range n {
		out[uint32(r.IntN(maxID))] = true
	}
	return out
}

// The bitmap maths agree with plain set arithmetic, for bitmaps of different lengths.
func TestCountMatchesSets(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	r := rand.New(rand.NewPCG(1, 2))
	const tenant = 5
	for round := range 20 {
		mr.FlushAll()
		inc1, inc2 := randomSet(r, 300, 2000), randomSet(r, 200, 900)
		exc := randomSet(r, 150, 400+r.IntN(3000)) // shorter or longer than the audience
		out, addr := randomSet(r, 300, 2500), randomSet(r, 900, 1500)
		setBits(t, rdb, segment.MemberKey(tenant, 1), inc1)
		setBits(t, rdb, segment.MemberKey(tenant, 2), inc2)
		setBits(t, rdb, segment.MemberKey(tenant, 3), exc)
		setBits(t, rdb, Key(tenant, OptOut(channel.Email)), out)
		setBits(t, rdb, Key(tenant, Email), addr)
		// Another workspace's bitmaps must not count.
		setBits(t, rdb, segment.MemberKey(tenant+1, 1), randomSet(r, 500, 2000))
		setBits(t, rdb, Key(tenant+1, Email), randomSet(r, 500, 2000))

		var want Counts
		for id := range uint32(4000) {
			if !inc1[id] && !inc2[id] {
				continue
			}
			want.Audience++
			if exc[id] {
				continue
			}
			want.AfterExclude++
			if out[id] {
				continue
			}
			want.AfterConsent++
			if addr[id] {
				want.Reachable++
			}
		}
		got, kept, err := Count(ctx, rdb, Query{Tenant: tenant, Include: []int64{1, 2}, Exclude: []int64{3}, Channel: channel.Email}, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("round %d: got %+v, want %+v", round, got, want)
		}
		if n, _ := rdb.BitCount(ctx, kept, nil).Result(); n != want.Reachable {
			t.Fatalf("round %d: kept bitmap has %d, want %d", round, n, want.Reachable)
		}
		rdb.Del(ctx, kept)
		if keys, _ := rdb.Keys(ctx, "rchtmp:*").Result(); len(keys) != 0 {
			t.Fatalf("temporary keys left: %v", keys)
		}
	}
	// Without a channel the last steps equal the audience after exclusions.
	got, _, err := Count(ctx, rdb, Query{Tenant: tenant, Include: []int64{1}}, false)
	if err != nil || got.AfterConsent != got.AfterExclude || got.Reachable != got.AfterExclude || got.Audience != got.AfterExclude {
		t.Fatalf("no channel: %+v %v", got, err)
	}
	if _, _, err := Count(ctx, rdb, Query{Tenant: tenant}, false); err == nil {
		t.Fatal("no segments should fail")
	}
}

func TestCapped(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	now := time.Now()
	key := func(c uint32) string { return fmt.Sprintf("cap:1:%d:email", c) }
	members := map[uint32]bool{}
	for id := uint32(1); id <= 400; id++ {
		members[id] = true
		sends := 0
		if id%4 == 0 {
			sends = 2 // at the cap
		} else if id%4 == 1 {
			sends = 1
		}
		for i := range sends {
			rdb.ZAdd(ctx, key(id), redis.Z{Score: float64(now.Add(-time.Duration(i+1) * time.Hour).UnixMilli()), Member: fmt.Sprint(i)})
		}
		if id%4 == 2 { // two old sends, outside the window
			rdb.ZAdd(ctx, key(id), redis.Z{Score: float64(now.Add(-48 * time.Hour).UnixMilli()), Member: "a"},
				redis.Z{Score: float64(now.Add(-49 * time.Hour).UnixMilli()), Member: "b"})
		}
	}
	setBits(t, rdb, "bm", members)
	cc := CapCheck{Key: key, Since: now.Add(-24 * time.Hour), Max: 2, ExactLimit: 1000, Sample: 130}
	res, err := Capped(ctx, rdb, "bm", cc)
	if err != nil || res.Capped != 100 || res.Approximate || res.Checked != 400 {
		t.Fatalf("exact: %+v %v", res, err)
	}
	cc.ExactLimit = 50
	res, err = Capped(ctx, rdb, "bm", cc)
	if err != nil || !res.Approximate || res.Checked != 130 || res.Capped < 80 || res.Capped > 120 {
		t.Fatalf("sampled: %+v %v", res, err)
	}
	if res, _ := Capped(ctx, rdb, "missing", cc); res.Capped != 0 {
		t.Fatal("missing bitmap")
	}
}

type fakeStore struct {
	rows    []Row
	changed []Row
	push    []uint32
}

func (f *fakeStore) TenantIDs(context.Context) ([]int64, error)            { return []int64{1, 2}, nil }
func (f *fakeStore) PushContacts(context.Context, int64) ([]uint32, error) { return f.push, nil }
func (f *fakeStore) ReachRows(_ context.Context, _ int64, after uint32, limit int, since *time.Time) ([]Row, error) {
	src := f.rows
	if since != nil {
		src = f.changed
	}
	var out []Row
	for _, r := range src {
		if r.ID > after && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestRebuild(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	st := &fakeStore{
		rows: []Row{
			{ID: 1, Attrs: map[string]any{"email": "a@x.com"}},
			{ID: 2, Attrs: map[string]any{"phone": "+919800000002", "optout_whatsapp": true}},
			{ID: 70000, Attrs: map[string]any{"email": "z@x.com", "phone": "+919800000003"}},
		},
		// Contact 1 lost its email while the rebuild ran: re-applied after the swap.
		changed: []Row{{ID: 1, Attrs: map[string]any{}}},
		push:    []uint32{2},
	}
	// A stale bit from before is cleared by the swap.
	rdb.SetBit(ctx, Key(1, SMS), 9, 1)
	rb := &Rebuilder{Store: st, RDB: rdb, Log: slog.New(slog.DiscardHandler)}
	if err := rb.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	bit := func(name string, id int64) int64 { v, _ := rdb.GetBit(ctx, Key(1, name), id).Result(); return v }
	for _, c := range []struct {
		name string
		id   int64
		want int64
	}{{Known, 1, 1}, {Email, 1, 0}, {Email, 70000, 1}, {SMS, 2, 1}, {OptOut(channel.WhatsApp), 2, 1}, {Push, 2, 1},
		{SMS, 9, 0}, {Phone, 70000, 1}, {Push, 1, 0}} {
		if got := bit(c.name, c.id); got != c.want {
			t.Errorf("%s bit %d = %d, want %d", c.name, c.id, got, c.want)
		}
	}
	for _, tenant := range []int64{1, 2} {
		if ok, _ := Ready(ctx, rdb, tenant); !ok {
			t.Errorf("tenant %d not marked built", tenant)
		}
	}
	if ttl := mr.TTL(BuiltKey(1)); ttl != 24*time.Hour {
		t.Errorf("built TTL %v", ttl)
	}
	// Fresh workspaces aren't rebuilt again; locks are released.
	st.rows = nil
	if err := rb.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if bit(Email, 70000) != 1 {
		t.Error("a fresh workspace was rebuilt")
	}
	if mr.Exists(Key(1, "lock")) {
		t.Error("lock left behind")
	}
}

func TestSampleIDs(t *testing.T) {
	ids := make([]uint32, 1000)
	for i := range ids {
		ids[i] = uint32(i)
	}
	if out, f := SampleIDs(ids, 1000, 10); len(out) != 1000 || f != 1 {
		t.Error("under the limit")
	}
	if out, f := SampleIDs(ids, 100, 10); len(out) != 10 || f != 100 || out[1] != 100 {
		t.Errorf("sample %v %v", out, f)
	}
}

type hourSettings map[int64]int

func (h hourSettings) AutomationSettings(_ context.Context, tenant int64) (automation.Settings, error) {
	s := automation.Defaults()
	if hr, ok := h[tenant]; ok {
		s.Schedules.ReachRebuildHour = hr
	}
	return s, nil
}

// A workspace with a rebuild hour stays fresh until that hour; others for a day, as before.
func TestRebuildHour(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC)
	rb := &Rebuilder{Store: &fakeStore{}, RDB: rdb, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now },
		Settings: hourSettings{1: 2}}
	if err := rb.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if ttl := mr.TTL(BuiltKey(1)); ttl != 21*time.Hour {
		t.Errorf("rebuild at 02:00 UTC: TTL %v, want 21h", ttl)
	}
	if ttl := mr.TTL(BuiltKey(2)); ttl != 24*time.Hour {
		t.Errorf("default: TTL %v, want 24h", ttl)
	}
}
