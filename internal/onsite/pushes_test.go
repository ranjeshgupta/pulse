package onsite

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestPushes(t *testing.T) {
	mr := miniredis.RunT(t)
	p := Pushes{RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	ctx := context.Background()
	if err := p.PushFocus(ctx, 1, 42, 7); err != nil {
		t.Fatal(err)
	}
	_ = p.PushFocus(ctx, 1, 42, 7)
	ids, err := p.TakeFocus(ctx, 1, 42)
	if err != nil || len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("focus %v %v", ids, err)
	}
	if ids, _ := p.TakeFocus(ctx, 1, 42); len(ids) != 0 {
		t.Errorf("shown once: %v", ids)
	}
	_ = p.PushDWC(ctx, 1, 42, "hero", "<b>Hi</b>", time.Hour)
	got, err := p.DWC(ctx, 1, 42, []string{"hero", "footer"})
	if err != nil || got["hero"] != "<b>Hi</b>" || len(got) != 1 {
		t.Errorf("dwc %v %v", got, err)
	}
	if got, _ := p.DWC(ctx, 1, 43, []string{"hero"}); len(got) != 0 {
		t.Errorf("other contact: %v", got)
	}
}

func TestPersonalDWC(t *testing.T) {
	stored := PersonalDWC("<p>Hi Asha</p>", "<p>Hi there</p>")
	if ResolveDWC(stored, true) != "<p>Hi Asha</p>" || ResolveDWC(stored, false) != "<p>Hi there</p>" {
		t.Error("personal content must go only to a verified identity")
	}
	if ResolveDWC("<b>plain</b>", false) != "<b>plain</b>" {
		t.Error("plain content is shown as it is")
	}
}
