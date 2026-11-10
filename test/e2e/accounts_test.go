//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

func stageOf(t *testing.T, c *http.Client, ext string) string {
	s, _ := attrsOf(t, c, ext)["stage"].(string)
	return s
}

// TestStages: stages move contacts by hand, by a journey step and by points thresholds (forward
// only), segments on the stage follow at once, and renaming a stage renames it on its contacts.
func TestStages(t *testing.T) {
	c, _ := workspace(t, "Stages")
	run := fmt.Sprint(time.Now().UnixNano())
	for _, st := range []map[string]any{{"name": "Lead"}, {"name": "MQL", "min_points": 50}, {"name": "SQL", "min_points": 100}, {"name": "Customer"}} {
		jsonCall(t, c, "POST", "/v1/stages", st, 201)
	}
	jsonCall(t, c, "POST", "/v1/stages", map[string]any{"name": "mql"}, 409)
	jsonCall(t, c, "PUT", "/v1/scoring/rules", map[string]any{"event": "demo_booked", "points": 40}, 200)
	mql := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "MQLs", "rule": map[string]any{"attr": "stage", "op": "eq", "value": "MQL"}}, 201)["id"])
	moved := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Moved by points", "rule": map[string]any{
		"event": "stage_changed", "op": "gte", "count": 1, "days": 7, "where": []any{map[string]any{"prop": "source", "op": "eq", "value": "points"}}}}, 201)["id"])
	time.Sleep(3 * time.Second) // segment index refresh

	ext := "st-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"city": "Pune"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, ext)["city"] == "Pune" })
	jsonCall(t, c, "PUT", "/v1/contacts/"+ext+"/stage", map[string]any{"stage": "Nope"}, 400)
	jsonCall(t, c, "PUT", "/v1/contacts/"+ext+"/stage", map[string]any{"stage": "lead"}, 202)
	waitFor(t, 10*time.Second, func() bool { return stageOf(t, c, ext) == "Lead" })

	// 80 points cross MQL's 50: the contact moves on its own and enters the segment.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext, "name": "demo_booked"}, map[string]any{"external_id": ext, "name": "demo_booked"}}}, 202)
	took := waitFor(t, 10*time.Second, func() bool {
		s := segmentsOf(t, c, ext)
		return slices.Contains(s, mql) && slices.Contains(s, moved)
	})
	t.Logf("points threshold moved the contact to MQL and into its segment in %v", took)
	if a := attrsOf(t, c, ext); a["points"] != 80.0 || a["stage_changed_at"] == nil {
		t.Errorf("attrs = %v", a)
	}
	// 120 points: SQL, out of the MQL segment.
	jsonCall(t, c, "POST", "/v1/contacts/"+ext+"/points", map[string]any{"delta": 40}, 202)
	waitFor(t, 10*time.Second, func() bool { return stageOf(t, c, ext) == "SQL" && !slices.Contains(segmentsOf(t, c, ext), mql) })
	// Never backwards: a Customer gaining points stays a Customer.
	jsonCall(t, c, "PUT", "/v1/contacts/"+ext+"/stage", map[string]any{"stage": "Customer"}, 202)
	waitFor(t, 10*time.Second, func() bool { return stageOf(t, c, ext) == "Customer" })
	jsonCall(t, c, "POST", "/v1/contacts/"+ext+"/points", map[string]any{"delta": 100}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, ext)["points"] == 220.0 })
	time.Sleep(500 * time.Millisecond)
	if st := stageOf(t, c, ext); st != "Customer" {
		t.Errorf("points moved a Customer back to %q", st)
	}

	// A journey step moves contacts too; a journey can't name a stage that doesn't exist.
	step := func(stage string) map[string]any {
		return map[string]any{"name": "won", "graph": map[string]any{"trigger": map[string]any{"type": "event", "event": "contract_signed"}, "entry": "s",
			"nodes": map[string]any{"s": map[string]any{"type": "change_stage", "stage": stage, "next": "x"}, "x": map[string]any{"type": "exit"}}}}
	}
	jsonCall(t, c, "POST", "/v1/journeys", step("Champion"), 400)
	j := jsonCall(t, c, "POST", "/v1/journeys", step("MQL"), 201)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j["id"]), nil, 200)
	time.Sleep(3 * time.Second)
	other := "st2-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": other, "name": "contract_signed"}, 202)
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, other), mql) })

	// Renaming a stage renames it on its contacts (and keeps when they entered it).
	since := attrsOf(t, c, other)["stage_changed_at"]
	var mqlID int64
	for _, s := range asList(jsonCall(t, c, "GET", "/v1/stages", nil, 200)["stages"]) {
		if st := s.(map[string]any); st["name"] == "MQL" {
			mqlID = idOf(st["id"])
			if st["contacts"] != 1.0 {
				t.Errorf("MQL has %v contacts, want 1", st["contacts"])
			}
		}
	}
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/stages/%d", mqlID), map[string]any{"name": "Marketing qualified", "min_points": 50}, 200)
	waitFor(t, 10*time.Second, func() bool { return stageOf(t, c, other) == "Marketing qualified" })
	if got := attrsOf(t, c, other)["stage_changed_at"]; got != since {
		t.Errorf("rename changed stage_changed_at from %v to %v", since, got)
	}
}

// TestCompanies: contacts link to a company by hand or by an identify naming it; segments on the
// company's attributes follow, changes to the company fan out to its contacts, and deleting it
// unlinks them.
func TestCompanies(t *testing.T) {
	c, siteKey := workspace(t, "Companies")
	run := fmt.Sprint(time.Now().UnixNano())
	acme := idOf(jsonCall(t, c, "POST", "/v1/companies", map[string]any{"name": "Acme " + run, "domain": "acme.test", "attrs": map[string]any{"industry": "Retail", "size": 120}}, 201)["id"])
	jsonCall(t, c, "POST", "/v1/companies", map[string]any{"name": "ACME " + run}, 409)
	retail := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Retail accounts", "rule": map[string]any{"all": []any{
		map[string]any{"attr": "company_industry", "op": "eq", "value": "Retail"}, map[string]any{"attr": "company_size", "op": "gte", "value": 100}}}}, 201)["id"])
	worksAt := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Works at Acme", "rule": map[string]any{"attr": "company_id", "op": "eq", "value": acme}}, 201)["id"])
	time.Sleep(3 * time.Second)

	a, b, g := "ca-"+run, "cb-"+run, "cg-"+run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": a, "name": "identify", "attrs": map[string]any{"city": "Pune"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, a)["city"] == "Pune" })
	jsonCall(t, c, "PUT", "/v1/contacts/"+a+"/company", map[string]any{"company_id": acme}, 202)
	// An identify naming the company (any letter case) links b; a new name creates a company.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": b, "name": "identify", "attrs": map[string]any{"company": "acme " + run}},
		map[string]any{"external_id": g, "name": "identify", "attrs": map[string]any{"company": "Globex " + run}}}}, 202)
	took := waitFor(t, 10*time.Second, func() bool {
		sa, sb := segmentsOf(t, c, a), segmentsOf(t, c, b)
		return slices.Contains(sa, retail) && slices.Contains(sb, retail) && slices.Contains(sa, worksAt) && slices.Contains(sb, worksAt)
	})
	t.Logf("linked contacts entered the company-attribute segment in %v", took)
	if at := attrsOf(t, c, b); at["company_name"] != "Acme "+run || at["company_industry"] != "Retail" {
		t.Errorf("b = %v", at)
	}
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, g)["company_id"] != nil })
	list := jsonCall(t, c, "GET", "/v1/companies?q=Acme", nil, 200)
	if cs := asList(list["companies"]); len(cs) != 1 || cs[0].(map[string]any)["members"] != 2.0 {
		t.Errorf("companies = %v", list)
	}
	// A browser can't link companies or set the copies.
	if code := webEvents(t, siteKey, map[string]any{"external_id": "anon-c" + run, "name": "identify",
		"attrs": map[string]any{"company": "Initech " + run, "company_industry": "Retail", "company_id": acme}}); code != 202 {
		t.Fatalf("web identify: %d", code)
	}
	time.Sleep(time.Second)
	if at := attrsOf(t, c, "wc-"+run); at["company_id"] != nil || at["company_industry"] != nil {
		t.Errorf("a browser set company attributes: %v", at)
	}

	// Changing the company's attributes reaches every member: both leave the Retail segment.
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/companies/%d", acme), map[string]any{"name": "Acme " + run, "attrs": map[string]any{"industry": "Wholesale", "size": 120}}, 200)
	took = waitFor(t, 10*time.Second, func() bool {
		return !slices.Contains(segmentsOf(t, c, a), retail) && !slices.Contains(segmentsOf(t, c, b), retail) && attrsOf(t, c, b)["company_industry"] == "Wholesale"
	})
	t.Logf("company change fanned out to its contacts in %v", took)
	detail := jsonCall(t, c, "GET", fmt.Sprintf("/v1/companies/%d", acme), nil, 200)
	if len(asList(detail["members"])) != 2 {
		t.Errorf("members = %v", detail["members"])
	}

	// Unlinking by hand, and deleting the company, take contacts out.
	jsonCall(t, c, "PUT", "/v1/contacts/"+a+"/company", map[string]any{"company_id": nil}, 202)
	waitFor(t, 10*time.Second, func() bool { return !slices.Contains(segmentsOf(t, c, a), worksAt) })
	jsonCall(t, c, "DELETE", fmt.Sprintf("/v1/companies/%d", acme), nil, 204)
	waitFor(t, 10*time.Second, func() bool {
		at := attrsOf(t, c, b)
		return at["company_id"] == nil && at["company_industry"] == nil && !slices.Contains(segmentsOf(t, c, b), worksAt)
	})
}

// TestDuplicateMerge: two contacts share an email; merging moves the loser's attributes, tags,
// points, event history and list memberships to the winner, so the winner's segments change and
// the loser leaves everything.
func TestDuplicateMerge(t *testing.T) {
	c, _ := workspace(t, "Merge")
	run := fmt.Sprint(time.Now().UnixNano())
	win, lose := "mw-"+run, "ml-"+run
	email := "dup-" + run + "@example.com"
	list := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Webinar"}, 201)["id"])
	buyers := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Two purchases", "rule": map[string]any{
		"event": "purchase", "op": "gte", "count": 2, "days": 30}}, 201)["id"])
	vipPune := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Pune VIPs", "rule": map[string]any{"all": []any{
		map[string]any{"tag": "vip", "op": "has"}, map[string]any{"attr": "city", "op": "eq", "value": "Pune"}}}}, 201)["id"])
	time.Sleep(3 * time.Second)

	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": win, "name": "identify", "email": email, "attrs": map[string]any{"city": "Pune"}},
		map[string]any{"external_id": lose, "name": "identify", "email": email, "attrs": map[string]any{"city": "Goa", "phone": "+919800000001"}},
		map[string]any{"external_id": lose, "name": "purchase"}, map[string]any{"external_id": lose, "name": "purchase"},
	}}, 202)
	jsonCall(t, c, "POST", "/v1/contacts/"+lose+"/tags", map[string]any{"add": []string{"vip"}}, 202)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/lists/%d/members", list), map[string]any{"external_ids": []string{lose}, "action": "add"}, 202)
	waitFor(t, 10*time.Second, func() bool {
		s := segmentsOf(t, c, lose)
		return slices.Contains(s, list) && slices.Contains(s, buyers)
	})

	dups := jsonCall(t, c, "GET", "/v1/contacts/duplicates?by=email", nil, 200)
	groups := asList(dups["groups"])
	if len(groups) != 1 || groups[0].(map[string]any)["value"] != email || len(asList(groups[0].(map[string]any)["contacts"])) != 2 {
		t.Fatalf("duplicates = %v", dups)
	}

	jsonCall(t, c, "POST", "/v1/contacts/merge", map[string]any{"winner": win, "loser": win}, 400)
	jsonCall(t, c, "POST", "/v1/contacts/merge", map[string]any{"winner": win, "loser": lose}, 202)
	took := waitFor(t, 15*time.Second, func() bool {
		s := segmentsOf(t, c, win)
		return slices.Contains(s, list) && slices.Contains(s, buyers) && slices.Contains(s, vipPune) && len(segmentsOf(t, c, lose)) == 0
	})
	t.Logf("merge moved list, event-count and tag segments to the winner in %v", took)
	a := attrsOf(t, c, win)
	if a["city"] != "Pune" || a["phone"] != "+919800000001" || fmt.Sprint(a["tags"]) != "[vip]" {
		t.Errorf("winner = %v: its own city wins, the loser's phone and tags carry over", a)
	}
	if attrsOf(t, c, lose)["merged_into"] != win {
		t.Error("the loser should point at the winner")
	}
	waitFor(t, 10*time.Second, func() bool {
		evs := contactEvents(t, c, win)
		n := 0
		for _, e := range evs {
			if e == "purchase" {
				n++
			}
		}
		return n == 2 && slices.Contains(evs, "contact_merged")
	})
	// A merged contact can't be merged again, nor receive a merge; its group is gone.
	time.Sleep(time.Second)
	jsonCall(t, c, "POST", "/v1/contacts/merge", map[string]any{"winner": win, "loser": lose}, 409)
	jsonCall(t, c, "POST", "/v1/contacts/merge", map[string]any{"winner": lose, "loser": win}, 409)
	if g := asList(jsonCall(t, c, "GET", "/v1/contacts/duplicates?by=email", nil, 200)["groups"]); len(g) != 0 {
		t.Errorf("duplicates after merging = %v", g)
	}
	// The loser's later events go to the winner.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": lose, "name": "purchase"}, 202)
	waitFor(t, 10*time.Second, func() bool {
		n := 0
		for _, e := range contactEvents(t, c, win) {
			if e == "purchase" {
				n++
			}
		}
		return n == 3
	})
}
