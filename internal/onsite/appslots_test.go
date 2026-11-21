package onsite

import (
	"testing"
	"time"

	"pulse/internal/model"
)

func TestDecideApp(t *testing.T) {
	now := time.Now()
	vip := model.OnsiteAudience{SegmentIDs: []int64{7}}
	slots := []model.DWCSlot{
		{Slot: "home_banner", Platform: PlatformApp, Status: "active", StartAt: now.Add(-time.Hour),
			DefaultCard: &model.AppCard{Title: "Welcome"},
			Variants:    []model.DWCVariant{{ID: "vip", Audience: vip, Card: &model.AppCard{Title: "Hi VIP", DeepLink: "myapp://vip"}}}},
		{Slot: "web_hero", Platform: PlatformWeb, Status: "active", StartAt: now.Add(-time.Hour), DefaultHTML: "<b>web</b>"},
		{Slot: "empty", Platform: PlatformApp, Status: "active", StartAt: now.Add(-time.Hour)},
		{Slot: "draft", Platform: PlatformApp, Status: "draft", StartAt: now.Add(-time.Hour), DefaultHTML: "x"},
	}
	wanted := []string{"home_banner", "web_hero", "empty", "draft"}
	anon := DecideApp(slots, wanted, now, Visitor{})
	if len(anon) != 1 || anon["home_banner"].Card.Title != "Welcome" || anon["home_banner"].Variant != "" {
		t.Errorf("anonymous: %+v", anon)
	}
	in := DecideApp(slots, wanted, now, Visitor{IsMember: func(id int64) bool { return id == 7 }})
	if in["home_banner"].Card.Title != "Hi VIP" || in["home_banner"].Variant != "vip" {
		t.Errorf("member: %+v", in)
	}
	// Websites never get app slots.
	if d := DecideFor(nil, slots, wanted, now, Visitor{}); len(d.Content) != 1 || d.Content["web_hero"] == "" {
		t.Errorf("web decide: %+v", d.Content)
	}
}

func TestValidateAppSlot(t *testing.T) {
	now := time.Now()
	ok := model.DWCSlot{Slot: "offers", Name: "Offers", Platform: PlatformApp, Status: "active", StartAt: now,
		DefaultCard: &model.AppCard{Title: "Sale", ImageURL: "https://x.test/a.png", DeepLink: "myapp://sale", Data: map[string]string{"color": "red"}}}
	if err := ValidateDWC(ok); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*model.AppCard{
		"javascript link": {DeepLink: "javascript://alert(1)"},
		"http image":      {ImageURL: "http://x.test/a.png"},
		"bad data key":    {Data: map[string]string{"a b": "x"}},
	} {
		bad := ok
		bad.DefaultCard = c
		if err := ValidateDWC(bad); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	web := ok
	web.Platform = PlatformWeb
	if err := ValidateDWC(web); err == nil {
		t.Error("a website slot with a card passed")
	}
	url := ok
	url.Variants = []model.DWCVariant{{Audience: model.OnsiteAudience{URLContains: []string{"/x"}}, HTML: "x"}}
	if err := ValidateDWC(url); err == nil {
		t.Error("an app slot with a page address rule passed")
	}
}
