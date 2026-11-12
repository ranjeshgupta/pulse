package onsite

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/model"
	"pulse/internal/reach"
	"pulse/internal/segment"
)

func slot(vs ...model.DWCVariant) model.DWCSlot {
	return model.DWCSlot{Slot: "hero", Status: "active", StartAt: now.Add(-time.Hour), DefaultHTML: "default", Variants: vs}
}

func members(ids ...int64) func(int64) bool {
	return func(id int64) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
}

// Variants are tried in order with all their rules; the first match wins, else the default.
func TestVariantOrderAndRules(t *testing.T) {
	s := slot(
		model.DWCVariant{ID: "vip-mobile", HTML: "vip mobile", Audience: model.OnsiteAudience{SegmentIDs: []int64{1}, Devices: []string{"mobile"}}},
		model.DWCVariant{ID: "pune", HTML: "pune", Audience: model.OnsiteAudience{Conditions: []model.OnsiteCondition{{Prop: "city", Op: "eq", Value: "Pune"}}}},
		model.DWCVariant{ID: "vip", HTML: "vip", Audience: model.OnsiteAudience{SegmentIDs: []int64{1}, URLNotContains: []string{"/checkout"}}},
		model.DWCVariant{ID: "sale", HTML: "sale", Audience: model.OnsiteAudience{URLContains: []string{"/sale"}, ExcludeSegmentIDs: []int64{2}}},
		model.DWCVariant{ID: "old", SegmentID: 3, HTML: "legacy"}, // a pre-M6 variant
	)
	for name, c := range map[string]struct {
		v    Visitor
		want string
	}{
		"vip on mobile":           {Visitor{PageURL: "https://s.in/", Device: "mobile", IsMember: members(1)}, "vip-mobile"},
		"vip on desktop":          {Visitor{PageURL: "https://s.in/", Device: "desktop", IsMember: members(1)}, "vip"},
		"verified Pune vip":       {Visitor{PageURL: "https://s.in/", Device: "desktop", IsMember: members(1), Attrs: map[string]any{"city": "Pune"}}, "pune"},
		"unverified Pune":         {Visitor{PageURL: "https://s.in/", IsMember: members()}, ""},
		"vip at checkout":         {Visitor{PageURL: "https://s.in/checkout", IsMember: members(1)}, ""},
		"sale page":               {Visitor{PageURL: "https://s.in/sale/shoes", IsMember: members()}, "sale"},
		"sale page, excluded":     {Visitor{PageURL: "https://s.in/sale", IsMember: members(2)}, ""},
		"legacy segment":          {Visitor{PageURL: "https://s.in/", IsMember: members(3)}, "old"},
		"nobody":                  {Visitor{PageURL: "https://s.in/", IsMember: members()}, ""},
		"host isn't a page match": {Visitor{PageURL: "https://sale.s.in/about", IsMember: members()}, ""},
	} {
		d := DecideFor(nil, []model.DWCSlot{s}, []string{"hero"}, now, c.v)
		if d.Variants["hero"] != c.want {
			t.Errorf("%s: variant %q, want %q (%v)", name, d.Variants["hero"], c.want, d.Content)
		}
		if c.want == "" && d.Content["hero"] != "default" {
			t.Errorf("%s: content %q", name, d.Content["hero"])
		}
	}
	// Reordering changes who wins.
	s.Variants[0], s.Variants[2] = s.Variants[2], s.Variants[0]
	if d := DecideFor(nil, []model.DWCSlot{s}, []string{"hero"}, now, Visitor{PageURL: "https://s.in/", Device: "mobile", IsMember: members(1)}); d.Variants["hero"] != "vip" {
		t.Errorf("after reorder: %v", d.Variants)
	}
}

// Focus items come back in priority order (the SDK shows the first allowed one of each kind).
func TestFocusPriorityAndRules(t *testing.T) {
	a := item(1, model.FocusTargeting{Priority: 2})
	b := item(2, model.FocusTargeting{Priority: 1, Devices: []string{"desktop"}})
	c := item(3, model.FocusTargeting{Priority: 1})
	d := item(4, model.FocusTargeting{URLNotContains: []string{"/cart"}})
	got := DecideFor([]model.FocusItem{a, b, c, d}, nil, nil, now, Visitor{PageURL: "https://s.in/cart", Device: "desktop", IsMember: members()})
	var ids []int64
	for _, f := range got.Focus {
		ids = append(ids, f.ID)
	}
	if len(ids) != 3 || ids[0] != 2 || ids[1] != 3 || ids[2] != 1 {
		t.Errorf("order %v, want [2 3 1]", ids)
	}
}

func TestShadowed(t *testing.T) {
	auds := []model.OnsiteAudience{
		{SegmentIDs: []int64{1, 2}},                                                                                          // 0
		{SegmentIDs: []int64{1}, Devices: []string{"mobile"}},                                                                // 1: inside 0
		{SegmentIDs: []int64{3}},                                                                                             // 2
		{URLContains: []string{"/shop"}},                                                                                     // 3
		{URLContains: []string{"/shop/shoes"}, Devices: []string{"desktop"}},                                                 // 4: inside 3
		{SegmentIDs: []int64{3}, ExcludeSegmentIDs: []int64{9}},                                                              // 5: inside 2
		{SegmentIDs: []int64{4}, URLNotContains: []string{"/cart"}},                                                          // 6
		{SegmentIDs: []int64{4}, URLNotContains: []string{"/cart/x"}},                                                        // 7: not inside 6 (/cart/y pages)
		{Conditions: []model.OnsiteCondition{{Prop: "city", Op: "eq", Value: "Pune"}}},                                       // 8
		{Conditions: []model.OnsiteCondition{{Prop: "city", Op: "eq", Value: "Pune"}, {Prop: "vip", Op: "eq", Value: true}}}, // 9: inside 8
	}
	sh, def := Shadowed(auds)
	got := map[int]int{}
	for _, s := range sh {
		got[s.Index] = s.By
	}
	want := map[int]int{1: 0, 4: 3, 5: 2, 9: 8}
	if len(got) != len(want) || def {
		t.Fatalf("shadowed %v (default never shown %v), want %v", got, def, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("variant %d shadowed by %d, want %d", k, got[k], v)
		}
	}
	// An "everyone" variant hides everything after it and the default.
	sh, def = Shadowed([]model.OnsiteAudience{{}, {SegmentIDs: []int64{1}}})
	if len(sh) != 1 || !def {
		t.Errorf("everyone: %v %v", sh, def)
	}
}

func TestCountOverlap(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	set := func(key string, ids ...int64) {
		for _, id := range ids {
			rdb.SetBit(ctx, key, id, 1)
		}
	}
	set(reach.Key(1, reach.Known), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	set(segment.MemberKey(1, 1), 1, 2, 3, 4)    // VIP
	set(segment.MemberKey(1, 2), 3, 4, 5, 6)    // Pune
	set(segment.MemberKey(1, 3), 4)             // staff
	set(segment.MemberKey(2, 1), 1, 2, 3, 4, 5) // another workspace
	auds := []model.OnsiteAudience{
		{SegmentIDs: []int64{1}},                                // VIP: 1-4
		{SegmentIDs: []int64{2}, ExcludeSegmentIDs: []int64{3}}, // Pune but not staff: 3,5,6
		{SegmentIDs: []int64{2}, Devices: []string{"mobile"}},   // Pune (on mobile): 3-6
	}
	o, err := CountOverlap(ctx, rdb, 1, auds)
	if err != nil {
		t.Fatal(err)
	}
	m := func(i int) (int64, int64) { return o.Variants[i].Matching, o.Variants[i].Sees }
	if a, s := m(0); a != 4 || s != 4 {
		t.Errorf("variant 1: %d/%d", a, s)
	}
	if a, s := m(1); a != 3 || s != 2 { // 3 is VIP first
		t.Errorf("variant 2: %d/%d", a, s)
	}
	if a, s := m(2); a != 4 || s != 0 || !o.Variants[2].Approximate { // 3,4 VIP; 5,6 variant 2
		t.Errorf("variant 3: %d/%d", a, s)
	}
	pairs := map[[2]int]int64{}
	for _, p := range o.Pairs {
		pairs[[2]int{p.A, p.B}] = p.Both
	}
	if pairs[[2]int{0, 1}] != 1 || pairs[[2]int{0, 2}] != 2 || pairs[[2]int{1, 2}] != 3 {
		t.Errorf("pairs %v", pairs)
	}
	if o.Multi != 4 || o.Default != 4 || o.Contacts != 10 || !o.Approximate { // 3,4,5,6 in two or more; 7-10 none
		t.Errorf("multi %d default %d contacts %d", o.Multi, o.Default, o.Contacts)
	}
	// "Everyone" counts every known contact; no temporary keys are left behind.
	o, _ = CountOverlap(ctx, rdb, 1, []model.OnsiteAudience{{}})
	if o.Variants[0].Matching != 10 || o.Default != 0 {
		t.Errorf("everyone: %+v", o)
	}
	for _, k := range mr.Keys() {
		if len(k) > 7 && k[:7] == "ovltmp:" {
			t.Errorf("left %s", k)
		}
	}
}
