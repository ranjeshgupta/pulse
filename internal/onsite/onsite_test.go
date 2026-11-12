package onsite

import (
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func item(id int64, t model.FocusTargeting) model.FocusItem {
	return model.FocusItem{ID: id, Name: "f", Kind: "modal", Status: "active", Content: model.FocusContent{Title: "Hi"}, Targeting: t, StartAt: now.Add(-time.Hour)}
}

func TestDecide(t *testing.T) {
	items := []model.FocusItem{
		item(1, model.FocusTargeting{}),                                  // everyone, every page
		item(2, model.FocusTargeting{SegmentIDs: []int64{10}}),           // members of 10
		item(3, model.FocusTargeting{ExcludeSegmentIDs: []int64{10}}),    // everyone except 10
		item(4, model.FocusTargeting{URLContains: []string{"/pricing"}}), // pricing page
		{ID: 5, Status: "draft", Targeting: model.FocusTargeting{}},      // not live
	}
	slots := []model.DWCSlot{
		{Slot: "hero", Status: "active", StartAt: now.Add(-time.Hour), DefaultHTML: "default", Variants: []model.DWCVariant{{SegmentID: 11, HTML: "eleven"}, {SegmentID: 10, HTML: "ten"}}},
		{Slot: "footer", Status: "draft", DefaultHTML: "hidden"},
	}
	// Scheduled windows: not started yet, and already ended.
	later, ended := item(6, model.FocusTargeting{}), item(7, model.FocusTargeting{})
	later.StartAt = now.Add(time.Hour)
	ended.EndAt = &now
	items = append(items, later, ended)
	in := func(ids ...int64) func(int64) bool {
		return func(id int64) bool {
			for _, x := range ids {
				if x == id {
					return true
				}
			}
			return false
		}
	}
	ids := func(d Decision) (out []int64) {
		for _, f := range d.Focus {
			out = append(out, f.ID)
		}
		return out
	}

	anon := Decide(items, slots, []string{"hero", "footer", "nope"}, "https://shop.in/home", now, in())
	if got := ids(anon); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("anonymous on home: %v, want [1 3]", got)
	}
	if anon.Content["hero"] != "default" || len(anon.Content) != 1 {
		t.Errorf("anonymous content = %v", anon.Content)
	}

	member := Decide(items, slots, []string{"hero"}, "https://shop.in/pricing", now, in(10))
	if got := ids(member); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 4 {
		t.Errorf("member of 10 on pricing: %v, want [1 2 4]", got)
	}
	if member.Content["hero"] != "ten" {
		t.Errorf("hero for 10 = %q", member.Content["hero"])
	}
	if Decide(nil, slots, []string{"hero"}, "", now, in(10, 11)).Content["hero"] != "eleven" {
		t.Error("first matching variant must win")
	}
	// Page rules look at the path, not the host.
	shop := []model.FocusItem{item(9, model.FocusTargeting{URLContains: []string{"/shop"}})}
	if got := ids(Decide(shop, nil, nil, "https://shop.in/about", now, in())); len(got) != 0 {
		t.Errorf("/shop matched the host of https://shop.in/about")
	}
	if got := ids(Decide(shop, nil, nil, "https://x.in/shop/42?ref=1", now, in())); len(got) != 1 {
		t.Errorf("/shop did not match the path /shop/42")
	}
	if got := Segments(items, slots); len(got) != 2 {
		t.Errorf("segments = %v, want [10 11]", got)
	}
}

func TestValidateFocus(t *testing.T) {
	good := model.FocusItem{Name: "Exit offer", Kind: "modal", Status: "active", StartAt: now,
		Content:   model.FocusContent{Title: "Wait!", CTAURL: "https://shop.in/offer"},
		Targeting: model.FocusTargeting{Trigger: "exit_intent", Frequency: "once"}}
	if err := ValidateFocus(good); err != nil {
		t.Fatal(err)
	}
	for want, mut := range map[string]func(*model.FocusItem){
		"kind":       func(f *model.FocusItem) { f.Kind = "popover" },
		"http(s)":    func(f *model.FocusItem) { f.Content.CTAURL = "javascript:alert(1)" },
		"trigger":    func(f *model.FocusItem) { f.Targeting.Trigger = "hover" },
		"percentage": func(f *model.FocusItem) { f.Targeting.Trigger, f.Targeting.TriggerValue = "scroll", 0 },
		"frequency":  func(f *model.FocusItem) { f.Targeting.Frequency = "hourly" },
		"title or":   func(f *model.FocusItem) { f.Content.Title = "" },
	} {
		f := good
		mut(&f)
		if err := ValidateFocus(f); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
	if err := ValidateFocus(model.FocusItem{Name: "x", Kind: "bar", Status: "draft", Content: model.FocusContent{Title: "x"},
		Targeting: model.FocusTargeting{Trigger: "load", Frequency: "once"}}); err == nil || !strings.Contains(err.Error(), "start") {
		t.Errorf("focus item without a start: %v", err)
	}
	if err := ValidateDWC(model.DWCSlot{Slot: "Home Hero", Name: "x", Status: "draft"}); err == nil {
		t.Error("slot key with spaces accepted")
	}
	// M6: a variant may have no segment (everyone, on some pages or devices); bad rules are refused.
	for _, a := range []model.OnsiteAudience{{Devices: []string{"watch"}}, {SegmentIDs: []int64{1}, ExcludeSegmentIDs: []int64{1}},
		{URLContains: []string{" "}}, {Conditions: []model.OnsiteCondition{{Prop: "city", Op: "nope"}}}, {SegmentIDs: []int64{-1}}} {
		if err := ValidateDWC(model.DWCSlot{Slot: "home-hero", Name: "x", Status: "active", StartAt: now, Variants: []model.DWCVariant{{HTML: "x", Audience: a}}}); err == nil {
			t.Errorf("audience %+v accepted", a)
		}
	}
}
