package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/reach"
	"pulse/internal/segment"
)

// Estimates count only the caller's workspace, refuse other workspaces' segments, and are cached.
func TestEstimateIsolationAndCache(t *testing.T) {
	s, st, _ := newServer()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s.RDB = rdb
	st.segments = []model.Segment{{ID: 1, TenantID: 1, Name: "Mine"}, {ID: 2, TenantID: 2, Name: "Theirs"}, {ID: 3, TenantID: 1, Name: "VIP"}}
	ctx := context.Background()
	for _, id := range []int64{1, 2, 3, 4, 5} {
		rdb.SetBit(ctx, segment.MemberKey(1, 1), id, 1)
		rdb.SetBit(ctx, reach.Key(1, reach.Email), id, 1)
	}
	rdb.SetBit(ctx, segment.MemberKey(1, 3), 5, 1)             // excluded
	rdb.SetBit(ctx, reach.Key(1, reach.OptOut("email")), 4, 1) // opted out
	rdb.SetBit(ctx, reach.Key(1, reach.Email), 3, 0)           // no address
	rdb.SetBit(ctx, segment.MemberKey(2, 1), 9, 1)             // another workspace's segment 1
	rdb.SetBit(ctx, reach.Key(2, reach.Email), 9, 1)
	h := s.Handler()

	w := do(t, h, "POST", "/v1/segments/estimate", "key-1", `{"include":[1],"exclude":[3],"channel":"email"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("estimate: %d %s", w.Code, w.Body)
	}
	var est Estimate
	_ = json.Unmarshal(w.Body.Bytes(), &est)
	if est.Audience != 5 || est.AfterExclude != 4 || est.AfterConsent != 3 || est.Reachable != 2 || est.WillSend != 2 {
		t.Fatalf("estimate %+v", est)
	}
	if w := do(t, h, "POST", "/v1/segments/estimate", "key-1", `{"include":[2],"channel":"email"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("another workspace's segment: %d %s", w.Code, w.Body)
	}
	// Cached for 30 seconds: a change shows only after that.
	rdb.SetBit(ctx, segment.MemberKey(1, 1), 6, 1)
	rdb.SetBit(ctx, reach.Key(1, reach.Email), 6, 1)
	w = do(t, h, "POST", "/v1/segments/estimate", "key-1", `{"include":[1],"exclude":[3],"channel":"email"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &est)
	if est.Reachable != 2 {
		t.Fatalf("not cached: %+v", est)
	}
	mr.FastForward(31e9)
	w = do(t, h, "POST", "/v1/segments/estimate", "key-1", `{"include":[1],"exclude":[3],"channel":"email"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &est)
	if est.Reachable != 3 {
		t.Fatalf("after the cache: %+v", est)
	}
	// Per-segment reach by channel.
	w = do(t, h, "GET", "/v1/segments/1/reach", "key-1", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"channel":"email","after_consent":5,"reachable":4`) {
		t.Fatalf("reach: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "GET", "/v1/segments/2/reach", "key-1", ""); w.Code != http.StatusNotFound {
		t.Fatalf("another workspace's reach: %d", w.Code)
	}
}

func TestEstimateRateLimit(t *testing.T) {
	s, _, _ := newServer()
	s.RDB = redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	ctx := context.Background()
	for i := range estimatesPerMin {
		if !s.allowTenant(ctx, "rlest", 1, estimatesPerMin) {
			t.Fatalf("refused call %d", i+1)
		}
	}
	if s.allowTenant(ctx, "rlest", 1, estimatesPerMin) {
		t.Fatal("the limit isn't enforced")
	}
	if !s.allowTenant(ctx, "rlest", 2, estimatesPerMin) {
		t.Fatal("another workspace shares the limit")
	}
}

func TestCampaignFilter(t *testing.T) {
	rows := []CampaignRow{
		{Kind: "broadcast", Name: "Diwali offer", Category: CategoryOneTime, Channels: []string{"whatsapp", "sms"}, Status: "scheduled", Tags: []string{"Festive"}},
		{Kind: "journey", Name: "Cart recovery", Category: CategoryTrigger, Channels: []string{"email"}, Status: "active", Tags: []string{"Retention"}},
		{Kind: "journey", Name: "Win back", Category: CategoryAutomation, Channels: []string{"email", "push"}, Status: "draft"},
	}
	count := func(f CampaignFilter) int {
		n := 0
		for _, r := range rows {
			if f.Match(r) {
				n++
			}
		}
		return n
	}
	for _, tc := range []struct {
		f    CampaignFilter
		want int
	}{
		{CampaignFilter{}, 3}, {CampaignFilter{Category: CategoryTrigger}, 1}, {CampaignFilter{Tag: "festive"}, 1},
		{CampaignFilter{Channel: "email"}, 2}, {CampaignFilter{Channel: "sms"}, 1}, {CampaignFilter{Status: "draft"}, 1},
		{CampaignFilter{Q: "CART"}, 1}, {CampaignFilter{Q: "retention"}, 1}, {CampaignFilter{Tag: "Festive", Channel: "email"}, 0},
	} {
		if got := count(tc.f); got != tc.want {
			t.Errorf("%+v: %d rows, want %d", tc.f, got, tc.want)
		}
	}
	g, err := journey.ParseGraph([]byte(`{"trigger":{"type":"event","event":"cart_abandoned"},"entry":"m","nodes":{
		"m":{"type":"send_message","channels":[{"channel":"whatsapp","template_id":1},{"channel":"sms","template_id":2}],"next":"e"},
		"e":{"type":"send_email","template_id":3,"next":"x"},"x":{"type":"exit"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(JourneyChannels(g), ","); got != "email,sms,whatsapp" {
		t.Errorf("channels %s", got)
	}
	if JourneyCategory(g.Trigger) != CategoryTrigger || JourneyCategory(journey.Trigger{Type: journey.TriggerSegmentEntered}) != CategoryAutomation {
		t.Error("categories")
	}
}

func TestValidTestContact(t *testing.T) {
	for _, bad := range []model.TestContact{{}, {Name: "x"}, {Name: "x", Email: "nope"}, {Name: "x", Phone: "98000"}} {
		if ValidTestContact(bad) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	for _, ok := range []model.TestContact{{Name: "QA", Email: "qa@acme.test"}, {Name: "QA", Phone: "+919800000001"}, {Name: "Asha", ExternalID: "u1"}} {
		if err := ValidTestContact(ok); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
}

// The hub shows only what a role may view.
func TestCampaignHubNeedsViewAccess(t *testing.T) {
	s, _, _ := newServer()
	r, _ := http.NewRequest("GET", "/v1/campaigns", nil)
	r.Pattern = "GET /v1/campaigns"
	if n := required(r); n.area != "" {
		t.Fatalf("hub route needs %v; the handler checks per kind", n)
	}
	w := httptest.NewRecorder()
	s.listCampaigns(w, r, model.Principal{TenantID: 1, Perms: auth.Perms{auth.AreaSegments: auth.Edit}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("no broadcasts or journeys access: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.campaignTags(w, r, model.Principal{TenantID: 1, Perms: auth.Perms{auth.AreaContent: auth.Edit}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("tags without access: %d", w.Code)
	}
}
