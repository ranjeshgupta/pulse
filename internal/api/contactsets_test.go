package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/reach"
	"pulse/internal/segment"
)

func specialTestLookup(allowed func(string) bool) viewLookup {
	lk := testLookup()
	graph, _ := json.Marshal(map[string]any{"entry": "n1", "nodes": map[string]any{"n1": map[string]any{"type": "send_email", "next": "x"}, "x": map[string]any{"type": "exit"}}})
	lk.Special = specialLookup{
		Broadcast: func(id int64) (string, error) {
			if id == 4 {
				return "Spring catalogue", nil
			}
			return "", errors.New("no")
		},
		Journey: func(id int64) (model.Journey, error) {
			if id == 7 {
				return model.Journey{ID: 7, Name: "Welcome", Graph: graph}, nil
			}
			return model.Journey{}, errors.New("no")
		},
		Journeys: func() ([]model.Journey, error) { return []model.Journey{{ID: 7, Name: "Welcome", Graph: graph}}, nil },
		Template: func(id int64) (string, error) { return "Survey", nil },
		Form:     func(id int64) (string, error) { return "Newsletter", nil },
		Allowed:  allowed,
	}
	return lk
}

func TestParseSpecialFilters(t *testing.T) {
	all := func(string) bool { return true }
	cases := []struct {
		q      string
		labels []string
	}{
		{q: `in:"VIP buyers" not_in:9 reachable:email`, labels: []string{"In segment “VIP buyers”", "Not in segment “Webinar guests”", "Reachable by email"}},
		{q: `in:4,9`, labels: []string{"In any of “VIP buyers”, “Webinar guests”"}},
		{q: `campaign:broadcast/4 outcome:opened`, labels: []string{"Campaign “Spring catalogue”", "Opened"}},
		{q: `campaign:journey/7 step:n1 outcome:sent`, labels: []string{"Campaign “Welcome”", "At “Send email”", "Sent"}},
		{q: `journey:7 at:n1`, labels: []string{"Entered “Welcome”", "Now at “Send email”"}},
		{q: `journey:Welcome journey_status:in_progress`, labels: []string{"Entered “Welcome”", "Still in the journey"}},
		{q: `event:purchase product:SKU1 period:2026-10-01..2026-10-31`, labels: []string{"Did purchase", "Product SKU1", "Between 1 Oct and 31 Oct 2026"}},
		{q: `event:add_to_cart no_event:purchase product:SKU1`, labels: []string{"Did add_to_cart", "Didn't do purchase", "Product SKU1"}},
		{q: `event:purchase no_event:add_to_cart product:* period:2026-10-01..2026-10-31`, labels: []string{"Did purchase", "Didn't do add_to_cart", "Any product", "Between 1 Oct and 31 Oct 2026"}},
		{q: `active:web utm:google period:2026-11-01`, labels: []string{"Active on the web", "Came from google", "On 1 Nov 2026"}},
		{q: `amp_poll:12/p1 answer:Yes`, labels: []string{"Answered the poll in “Survey”", "Answer “Yes”"}},
		{q: `outcome:failed reason:invalid_email period:2026-10-01..2026-10-30`, labels: []string{"Failed", "Reason: " + "Invalid email address"}},
		{q: `form:3`, labels: []string{"Submitted “Newsletter”"}},
	}
	for _, c := range cases {
		v, err := parseContactView(c.q, specialTestLookup(all))
		if err != nil {
			t.Errorf("%q: %v", c.q, err)
			continue
		}
		if v.Special == nil || !v.Special.any() || v.Rule != nil {
			t.Errorf("%q: special %+v rule %v", c.q, v.Special, v.Rule)
		}
		var got []string
		for _, f := range v.Filters {
			got = append(got, f.Label)
		}
		for i, l := range c.labels {
			if i >= len(got) || (got[i] != l && !strings.HasPrefix(l, "Reason: ")) {
				t.Errorf("%q: labels %q, want %q", c.q, got, c.labels)
				break
			}
		}
		where, args := v.Special.chConds(1)
		if strings.Count(where, "?") != len(args) {
			t.Errorf("%q: %d placeholders, %d args: %s", c.q, strings.Count(where, "?"), len(args), where)
		}
		if where != "" && !strings.Contains(where, "tenant_id = ?") {
			t.Errorf("%q: a subquery without the tenant: %s", c.q, where)
		}
	}
	// product:* (the Products dashboard's totals) means any product, not a product coded "*".
	v, _ := parseContactView("event:purchase product:*", specialTestLookup(all))
	if where, args := v.Special.chConds(1); !strings.Contains(where, "product_code != ''") || strings.Contains(where, "product_code = ?") || len(args) != 2 {
		t.Errorf("product:*: %s %v", where, args)
	}
	// Defaults and combinations.
	v, _ = parseContactView("campaign:broadcast/4", specialTestLookup(all))
	if v.Special.Outcome != "targeted" {
		t.Errorf("a campaign alone lists its targeted contacts, got %q", v.Special.Outcome)
	}
	v, _ = parseContactView("in:4 tag:vip", specialTestLookup(all))
	if v.Rule == nil || !v.Special.bitmap() {
		t.Error("rule filters and set filters combine")
	}
}

func TestParseSpecialFilterErrors(t *testing.T) {
	all := func(string) bool { return true }
	for q, want := range map[string]string{
		"at:n1":                                 "need journey:",
		"journey:7 at:nope":                     "has no step",
		"step:n1":                               "needs campaign:journey",
		"campaign:broadcast/4 run:x":            "Unix seconds",
		"campaign:broadcast/99":                 "no campaign",
		"campaign:email/4":                      "campaign:broadcast/<id>",
		"outcome:liked":                         "isn't an outcome",
		"reason:bad_code":                       "isn't a reason code",
		"answer:Yes":                            "needs amp_poll",
		"product:SKU1":                          "need event:",
		"active:any":                            "needs period:",
		"period:2026-01-01..2027-06-01 event:x": "at most 366 days",
		"period:2026-10-01":                     "applies to",
		"reachable:fax":                         "use a channel",
		"in:Nobody":                             "no segment",
		"campaign:broadcast/4 campaign:broadcast/4": "once",
	} {
		_, err := parseContactView(q, specialTestLookup(all))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", q, err, want)
		}
	}
}

// A role without view access to where a number came from can't list the people behind it.
func TestSpecialFilterPermissions(t *testing.T) {
	contactsOnly := auth.Perms{auth.AreaContacts: auth.View}
	reportsToo := auth.Perms{auth.AreaContacts: auth.View, auth.AreaReports: auth.View}
	journeysToo := auth.Perms{auth.AreaContacts: auth.View, auth.AreaJourneys: auth.View}
	for _, tc := range []struct {
		q     string
		perms auth.Perms
		ok    bool
	}{
		{"campaign:broadcast/4 outcome:opened", contactsOnly, false},
		{"campaign:broadcast/4 outcome:opened", reportsToo, true},
		{"campaign:broadcast/4 outcome:opened", auth.Perms{auth.AreaContacts: auth.View, auth.AreaBroadcasts: auth.View}, true},
		{"campaign:journey/7", journeysToo, true},
		{"campaign:journey/7", auth.Perms{auth.AreaContacts: auth.View, auth.AreaBroadcasts: auth.View}, false},
		{"journey:7", contactsOnly, false},
		{"journey:7", journeysToo, true},
		{"outcome:bounced", journeysToo, false}, // every campaign: reports only
		{"outcome:bounced", reportsToo, true},
		{"amp_poll:12/p1", contactsOnly, false},
		{"form:3", reportsToo, true},
		{"in:4 reachable:sms", contactsOnly, true},
		{"event:purchase product:x", contactsOnly, true},
	} {
		perms := tc.perms
		_, err := parseContactView(tc.q, specialTestLookup(func(f string) bool { return viewFilterAllowed(perms, f) }))
		if (err == nil) != tc.ok {
			t.Errorf("%q with %v: err %v, want ok=%v", tc.q, tc.perms, err, tc.ok)
		}
		if err != nil && strings.Contains(err.Error(), "Spring catalogue") {
			t.Errorf("%q: the error names a campaign the role can't see: %v", tc.q, err)
		}
	}
	for f := range viewFilterNeeds {
		if viewFilterAllowed(auth.Perms{auth.AreaContacts: auth.View}, f) {
			t.Errorf("%s: contacts view alone shouldn't be enough", f)
		}
		admin, _ := auth.BuiltinPerms(auth.RoleAdmin)
		if !viewFilterAllowed(admin, f) {
			t.Errorf("%s: admins may use it", f)
		}
	}
}

func setBits(t *testing.T, rdb *redis.Client, key string, ids ...uint32) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		if err := rdb.SetBit(ctx, key, int64(id), 1).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBitmapIDsPagesNewestFirst(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	var ids []uint32
	for i := uint32(1); i <= 300; i++ {
		ids = append(ids, i*997) // spread over many bytes and chunks
	}
	setBits(t, rdb, "k", ids...)
	var got []uint32
	var before uint32
	for {
		page, err := bitmapIDs(ctx, rdb, "k", before, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page...)
		if len(page) < 50 {
			break
		}
		before = page[len(page)-1]
	}
	want := slices.Clone(ids)
	slices.Reverse(want)
	if !slices.Equal(got, want) {
		t.Fatalf("paged %d ids, want %d in descending order", len(got), len(want))
	}
	odd, err := bitmapIDs(ctx, rdb, "k", 0, 1000, func(c []uint32) ([]uint32, error) {
		var out []uint32
		for _, id := range c {
			if id%2 == 1 {
				out = append(out, id)
			}
		}
		return out, nil
	})
	if err != nil || len(odd) != 150 {
		t.Fatalf("kept %d, err %v; want the 150 odd ids", len(odd), err)
	}
}

// The list behind every estimate step has exactly the step's count.
func TestViewBitmapMatchesEstimate(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	const tenant = 5
	setBits(t, rdb, segment.MemberKey(tenant, 4), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 100, 2000)
	setBits(t, rdb, segment.MemberKey(tenant, 9), 3, 4, 50)
	setBits(t, rdb, segment.MemberKey(tenant, 11), 2, 2000)
	setBits(t, rdb, reach.Key(tenant, reach.Known), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 50, 100, 2000)
	setBits(t, rdb, reach.Key(tenant, reach.Email), 1, 2, 5, 6, 7, 100, 2000)
	setBits(t, rdb, reach.Key(tenant, reach.OptOut("email")), 6, 50)
	s := New(Deps{RDB: rdb})
	est, _, err := reach.Count(ctx, rdb, reach.Query{Tenant: tenant, Include: []int64{4, 9}, Exclude: []int64{11}, Channel: "email"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sp   viewSpecial
		want int64
	}{
		{viewSpecial{In: [][]int64{{4, 9}}}, est.Audience},
		{viewSpecial{In: [][]int64{{4, 9}}, NotIn: []int64{11}}, est.AfterExclude},
		{viewSpecial{In: [][]int64{{4, 9}}, NotIn: []int64{11}, Subscribed: "email"}, est.AfterConsent},
		{viewSpecial{In: [][]int64{{4, 9}}, NotIn: []int64{11}, Reachable: "email"}, est.Reachable},
		{viewSpecial{NotIn: []int64{4}}, 1}, // known contacts not in the segment: 50
		{viewSpecial{In: [][]int64{{9}}, Reachable: "push"}, 0},
		{viewSpecial{In: [][]int64{{4}, {9}}}, 2}, // in both (overlaps): 3 and 4
	} {
		sp := tc.sp
		key, err := s.viewBitmap(ctx, tenant, &sp)
		if err != nil {
			t.Fatal(err)
		}
		n, approx, err := s.countSetView(ctx, setView{key: key})
		if err != nil || approx || int64(n) != tc.want {
			t.Errorf("%+v: count %d approx %v err %v, want %d", tc.sp, n, approx, err, tc.want)
		}
		ids, _ := bitmapIDs(ctx, rdb, key, 0, 100, nil)
		if int64(len(ids)) != tc.want {
			t.Errorf("%+v: listed %d, want %d", tc.sp, len(ids), tc.want)
		}
	}
	// Other workspaces' bitmaps never leak in: the same segment id in tenant 6 is a different key.
	setBits(t, rdb, segment.MemberKey(6, 4), 999)
	sp := viewSpecial{In: [][]int64{{4}}}
	key, _ := s.viewBitmap(ctx, tenant, &sp)
	if ids, _ := bitmapIDs(ctx, rdb, key, 0, 100, nil); slices.Contains(ids, 999) {
		t.Error("tenant 6's member listed in tenant 5")
	}
	if !strings.HasPrefix(key, "cvset:5:") {
		t.Errorf("view key %q isn't per workspace", key)
	}
}

// Counting with a filter is exact up to the limit and sampled above it.
func TestCountSetViewSampling(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	var ids []uint32
	for i := uint32(0); i < 20000; i++ {
		ids = append(ids, i)
	}
	setBits(t, rdb, "k", ids...)
	s := New(Deps{RDB: rdb})
	third := func(c []uint32) ([]uint32, error) {
		var out []uint32
		for _, id := range c {
			if id%3 == 0 {
				out = append(out, id)
			}
		}
		return out, nil
	}
	n, approx, err := s.countSetView(ctx, setView{key: "k", keep: third, exact: 50000})
	if err != nil || approx || n != 6667 {
		t.Fatalf("exact: %d %v %v", n, approx, err)
	}
	n, approx, err = s.countSetView(ctx, setView{key: "k", keep: third, exact: 1000})
	if err != nil || !approx || n < 6000 || n > 7300 {
		t.Fatalf("sampled: %d %v %v", n, approx, err)
	}
}

func TestPeriod(t *testing.T) {
	from, to, err := parsePeriod("2026-10-01..2026-10-31")
	if err != nil || !from.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v %v %v", from, to, err)
	}
	if _, _, err := parsePeriod("2026-10-31..2026-10-01"); err == nil {
		t.Error("a reversed period")
	}
}

func TestOutcomeConds(t *testing.T) {
	for o := range outcomeWords {
		c, args := outcomeCond(o, false)
		if strings.Count(c, "?") != len(args) {
			t.Errorf("%s: %s with %d args", o, c, len(args))
		}
	}
	c, _ := outcomeCond("failed", false)
	if !strings.Contains(c, "NOT IN") {
		t.Errorf("failed must leave out skips: %s", c)
	}
}

// The old Journeys and Broadcasts lists redirect to Campaigns; a campaign's own page doesn't.
func TestOldCampaignListsRedirect(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app")) })
	h := New(Deps{}).WithUI(ui).Handler()
	for path, want := range map[string]string{"/journeys": "/campaigns", "/broadcasts": "/campaigns?tab=one_time", "/journeys/7": "", "/broadcasts/new": ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if want == "" {
			if rec.Code != http.StatusOK || rec.Body.String() != "app" {
				t.Errorf("%s: %d %q, want the app", path, rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != want {
			t.Errorf("%s: %d to %q, want %q", path, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

// "Create segment from this list" runs at most viewSegmentRuns fills per workspace at once.
func TestViewSegmentLimits(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	s := New(Deps{RDB: rdb})
	var held []func()
	for i := 0; i < viewSegmentRuns; i++ {
		rel, ok := s.acquireViewSegmentRun(ctx, 7)
		if !ok {
			t.Fatalf("slot %d refused", i)
		}
		held = append(held, rel)
	}
	if _, ok := s.acquireViewSegmentRun(ctx, 7); ok {
		t.Fatal("a run past the limit was allowed")
	}
	if _, ok := s.acquireViewSegmentRun(ctx, 8); !ok {
		t.Fatal("another workspace was limited")
	}
	held[0]()
	held[0]() // releasing twice frees one slot only
	if _, ok := s.acquireViewSegmentRun(ctx, 7); !ok {
		t.Fatal("a released slot wasn't reusable")
	}
	if _, ok := s.acquireViewSegmentRun(ctx, 7); ok {
		t.Fatal("a double release freed two slots")
	}

}
