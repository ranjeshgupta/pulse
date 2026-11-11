package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
	"pulse/internal/segment"
)

func testLookup() viewLookup {
	return viewLookup{
		Segments: func() ([]model.Segment, error) {
			return []model.Segment{{ID: 4, Name: "VIP buyers", Kind: "rule"}, {ID: 9, Name: "Webinar guests", Kind: "static"}}, nil
		},
		Users: func() ([]model.User, error) {
			return []model.User{{ID: 7, Name: "Ravi", Email: "ravi@example.com"}, {ID: 8, Email: "meera@example.com"}}, nil
		},
		Me: 3,
	}
}

func TestParseContactView(t *testing.T) {
	cases := []struct {
		q, rule, text string
		labels        []string
	}{
		{q: "", rule: "null"},
		{q: "asha", rule: "null", text: "asha"},
		{q: "is:anonymous", rule: `{"all":[{"all":[{"attr":"date_identified","op":"not_exists"},{"attr":"email","op":"eq","value":""},{"attr":"phone","op":"eq","value":""}]}]}`, labels: []string{"Anonymous visitors"}},
		{q: `segment:"vip buyers" priya k`, rule: `{"all":[{"segment":4,"op":"in"}]}`, text: "priya k", labels: []string{"Segment: VIP buyers"}},
		{q: "list:9 tag:vip", rule: `{"all":[{"segment":9,"op":"in"},{"tag":"vip","op":"has"}]}`, labels: []string{"Static segment: Webinar guests", "Tag: vip"}},
		{q: "points:>50", rule: `{"all":[{"attr":"points","op":"gt","value":50}]}`, labels: []string{"Points > 50"}},
		// Contacts without points have 0, so they pass "fewer than 10".
		{q: "points:<10", rule: `{"all":[{"any":[{"attr":"points","op":"lt","value":10},{"attr":"points","op":"not_exists"}]}]}`},
		{q: "points:=5", rule: `{"all":[{"attr":"points","op":"eq","value":"5"}]}`},
		{q: "owner:me owner:none", rule: `{"all":[{"attr":"owner_id","op":"eq","value":"3"},{"attr":"owner_id","op":"not_exists"}]}`, labels: []string{"Owner: me", "No owner"}},
		{q: "owner:meera@example.com", rule: `{"all":[{"attr":"owner_id","op":"eq","value":"8"}]}`, labels: []string{"Owner: meera@example.com"}},
		{q: "stage:Customer company:Acme company:12", rule: `{"all":[{"attr":"stage","op":"eq","value":"Customer"},{"attr":"company_name","op":"eq","value":"Acme"},{"attr":"company_id","op":"eq","value":"12"}]}`},
		{q: "email:Gmail", rule: `{"all":[{"any":[{"attr":"email","op":"contains","value":"Gmail"},{"attr":"email","op":"contains","value":"gmail"}]}]}`},
		{q: "is:bounced", rule: `{"all":[{"any":[{"attr":"email_bounced","op":"eq","value":"true"},{"attr":"sms_bounced","op":"eq","value":"true"}]}]}`},
		// Unknown keys are text: IDs may contain colons.
		{q: "shopify:123", rule: "null", text: "shopify:123"},
	}
	for _, c := range cases {
		v, err := parseContactView(c.q, testLookup())
		if err != nil {
			t.Errorf("%q: %v", c.q, err)
			continue
		}
		b, _ := json.Marshal(v.Rule)
		if string(b) != c.rule || v.Text != c.text {
			t.Errorf("%q: rule %s text %q, want %s %q", c.q, b, v.Text, c.rule, c.text)
		}
		for i, l := range c.labels {
			if i >= len(v.Filters) || v.Filters[i].Label != l {
				t.Errorf("%q: filters %+v, want label %q at %d", c.q, v.Filters, l, i)
			}
		}
	}
}

func TestParseContactViewErrors(t *testing.T) {
	for q, want := range map[string]string{
		"is:sleeping":                      "isn't a filter",
		"segment:Nobody":                   "no segment",
		"tag:bad!tag":                      "tags are",
		"points:lots":                      "use a number",
		"owner:stranger":                   "no teammate",
		"tag:":                             "needs a value",
		"name:" + strings.Repeat("x", 300): "", // long filter values are fine; only free text is capped
	} {
		_, err := parseContactView(q, testLookup())
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v", q[:20], err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", q, err, want)
		}
	}
	noUser := testLookup()
	noUser.Me = 0
	if _, err := parseContactView("owner:me", noUser); err == nil {
		t.Error("owner:me with an API key should fail")
	}
}

// Every view compiles to a valid rule, and the free-text search becomes no part of it.
func TestOwnerFilterWithoutTeamAccess(t *testing.T) {
	lk := testLookup()
	lk.Users = nil // no view access to settings
	for _, q := range []string{"owner:me", "owner:none"} {
		if _, err := parseContactView(q, lk); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	for _, q := range []string{"owner:ravi@example.com", "owner:Ravi", "owner:7"} {
		_, err := parseContactView(q, lk)
		if err == nil || !strings.Contains(err.Error(), "view access to settings") || strings.Contains(err.Error(), "ravi") {
			t.Errorf("%s looked up a teammate without team access: %v", q, err)
		}
	}
}

func TestContactViewRuleCompiles(t *testing.T) {
	v, err := parseContactView(`is:dnc segment:4 tag:vip stage:Lead company:Acme owner:7 points:>=10 email:x name:"asha k" free`, testLookup())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := segment.WhereSQL(*v.Rule, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	ok, err := segment.Eval(context.Background(), *v.Rule, map[string]any{"optout_sms": true, "tags": []any{"vip"}, "stage": "Lead",
		"company_name": "Acme", "owner_id": 7.0, "points": 12.0, "email": "x@y.z", "first_name": "Asha K"}, inSegments{4: true})
	if err != nil || !ok {
		t.Fatalf("eval = %v %v, want a match", ok, err)
	}
	if v.Text != "free" || len(v.Filters) != 9 || v.Filters[8].Token != `name:"asha k"` {
		t.Fatalf("text %q filters %+v", v.Text, v.Filters)
	}
}

type inSegments map[int64]bool

func (inSegments) Count(context.Context, string, int) (int, error) { return 0, nil }
func (m inSegments) InSegment(_ context.Context, id int64) (bool, error) {
	return m[id], nil
}
