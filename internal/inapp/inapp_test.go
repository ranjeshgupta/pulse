package inapp

import (
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

func msg() model.InAppMessage {
	return model.InAppMessage{ID: 1, Name: "Welcome", Layout: "modal", Status: "active", StartAt: time.Now().Add(-time.Hour),
		Content:   model.InAppContent{Title: "Hi", Body: "20% off", Buttons: []model.InAppButton{{Text: "Shop", Action: "url", Value: "myapp://offers"}, {Text: "Later", Action: "dismiss"}}},
		Targeting: model.InAppTargeting{TriggerEvent: "app_open", Frequency: "once", Platforms: []string{"android"}, SegmentIDs: []int64{7}, ExcludeSegmentIDs: []int64{9}}}
}

func TestValidate(t *testing.T) {
	if err := Validate(msg()); err != nil {
		t.Fatal(err)
	}
	bad := msg()
	bad.Layout = "popup"
	bad.Content.Buttons[0].Value = "javascript:alert(1)"
	bad.Targeting.Screen = "Home" // only for screen_view
	bad.Targeting.Frequency = "hourly"
	err := Validate(bad)
	for _, want := range []string{"layout", "button 1", "screen", "frequency"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v should mention %s", err, want)
		}
	}
}

func TestFor(t *testing.T) {
	now := time.Now()
	draft := msg()
	draft.ID, draft.Status = 2, "draft"
	everyone := msg()
	everyone.ID, everyone.Priority, everyone.Targeting.SegmentIDs, everyone.Targeting.Platforms = 3, 5, nil, nil
	ms := []model.InAppMessage{msg(), draft, everyone}
	ids := func(out []model.InAppMessage) (r []int64) {
		for _, m := range out {
			r = append(r, m.ID)
		}
		return
	}
	in7 := func(id int64) bool { return id == 7 }
	if got := ids(For(ms, "android", now, in7)); len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Errorf("member on android = %v, want [3 1] (priority first, no drafts)", got)
	}
	if got := ids(For(ms, "ios", now, in7)); len(got) != 1 || got[0] != 3 {
		t.Errorf("ios = %v, want only the all-platform campaign", got)
	}
	if got := ids(For(ms, "android", now, func(id int64) bool { return id == 7 || id == 9 })); len(got) != 0 {
		t.Errorf("excluded segment still sees it: %v", got)
	}
}
