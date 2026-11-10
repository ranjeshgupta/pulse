//go:build e2e

package e2e

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// clickFirstLink opens the tracked click link of an email in Mailpit (without following it).
func clickFirstLink(t *testing.T, mailID string) {
	t.Helper()
	var msg struct{ HTML string }
	mailpitGet(t, "/api/v1/message/"+mailID, &msg)
	m := regexp.MustCompile(`href="(` + regexp.QuoteMeta(apiURL) + `/t/c/[^"]+)"`).FindStringSubmatch(msg.HTML)
	if m == nil {
		t.Fatalf("no tracked link in:\n%s", msg.HTML)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(html.UnescapeString(m[1]))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("click: %v %v", err, resp)
	}
	resp.Body.Close()
}

type mailSearch struct {
	Messages []struct {
		ID, Subject string
		To          []struct{ Address string }
	} `json:"messages"`
}

func searchMail(t *testing.T, query string) mailSearch {
	var s mailSearch
	mailpitGet(t, "/api/v1/search?query="+url.QueryEscape(query), &s)
	return s
}

// TestCohorts: contacts grouped by the day they signed up, and who came back on each later day.
func TestCohorts(t *testing.T) {
	c, _ := workspace(t, "Cohorts")
	run := fmt.Sprint(time.Now().UnixNano())
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(k int) string { return today.AddDate(0, 0, -k).Add(12 * time.Hour).Format(time.RFC3339) }
	ev := func(who, name string, k int) map[string]any {
		return map[string]any{"external_id": "co-" + run + "-" + who, "name": name, "ts": day(k)}
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		ev("a", "signup", 3), ev("a", "open_app", 2), ev("a", "open_app", 1),
		ev("b", "signup", 3), ev("b", "open_app", 1),
		ev("c", "signup", 2),
		ev("d", "signup", 2), ev("d", "open_app", 2),
	}}, 202)

	spec := map[string]any{"by": "event", "event": "signup", "period": "day", "periods": 3, "return": map[string]any{"event": "open_app"}}
	body := map[string]any{"spec": spec, "from": today.AddDate(0, 0, -3).Format(time.RFC3339), "to": time.Now().UTC().Format(time.RFC3339)}
	cells := func(res map[string]any) string {
		var out []string
		for _, row := range asList(res["cohorts"]) {
			r := row.(map[string]any)
			out = append(out, fmt.Sprint(r["size"], asList(r["returned"])))
		}
		return strings.Join(out, " ")
	}
	// Signed up 3 days ago: a and b; a back 2 days ago (day 1), both back yesterday (day 2).
	// Signed up 2 days ago: c and d; d opened the app the same day (day 0); day 3 hasn't come.
	// Yesterday's and today's cohorts are empty, with the periods that have started shown as 0.
	want := "2 [0 1 2 0] 2 [1 0 0 <nil>] 0 [0 0 <nil> <nil>] 0 [0 <nil> <nil> <nil>]"
	var got string
	if !eventually(25*time.Second, func() bool {
		got = cells(jsonCall(t, c, "POST", "/v1/cohorts/run", body, 200))
		return got == want
	}) {
		t.Fatalf("cohorts = %s, want %s", got, want)
	}

	// By first seen with any activity: everyone is active on their first day.
	spec2 := map[string]any{"by": "first_seen", "period": "day", "periods": 1, "return": map[string]any{}}
	waitFor(t, 15*time.Second, func() bool {
		res := jsonCall(t, c, "POST", "/v1/cohorts/run", map[string]any{"spec": spec2, "from": body["from"], "to": body["to"]}, 200)
		rows := asList(res["cohorts"])
		return len(rows) >= 2 && fmt.Sprint(rows[0].(map[string]any)["returned"]) == "[2 1]"
	})

	id := idOf(jsonCall(t, c, "POST", "/v1/cohorts", map[string]any{"name": "Signup retention", "spec": spec}, 200)["id"])
	if got := jsonCall(t, c, "GET", fmt.Sprint("/v1/cohorts/", id), nil, 200); got["name"] != "Signup retention" {
		t.Errorf("saved cohort = %v", got)
	}
	if l := asList(jsonCall(t, c, "GET", "/v1/cohorts", nil, 200)["cohorts"]); len(l) != 1 {
		t.Errorf("cohorts = %v", l)
	}
	spec["periods"] = 99
	jsonCall(t, c, "POST", "/v1/cohorts/run", body, 400)
	jsonCall(t, c, "POST", "/v1/cohorts", map[string]any{"name": "bad", "spec": map[string]any{"by": "event", "event": "x'; --", "period": "day", "periods": 2}}, 400)
}

// TestRevenueAttribution: a contact clicks a broadcast email and then buys; another arrives from a
// UTM campaign and buys. The revenue report credits each, and the broadcast report shows revenue.
func TestRevenueAttribution(t *testing.T) {
	c, _ := workspace(t, "Revenue")
	run := fmt.Sprint(time.Now().UnixNano())
	buyer, email := "rv-"+run, "rv-"+run+"@example.com"
	visitor := "rv-utm-" + run

	if rs := jsonCall(t, c, "GET", "/v1/revenue/settings", nil, 200)["settings"].(map[string]any); rs["event"] != "purchase" || rs["amount_prop"] != "amount" {
		t.Errorf("default revenue settings = %v", rs)
	}
	jsonCall(t, c, "PUT", "/v1/revenue/settings", map[string]any{"event": "purchase", "amount_prop": "amount", "currency": "inr"}, 200)
	jsonCall(t, c, "PUT", "/v1/revenue/settings", map[string]any{"event": "purchase", "amount_prop": "amount') --", "currency": "INR"}, 400)

	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "sale", "channel": "email", "subject": "Sale " + run,
		"html": `<p>Sale</p><a href="https://shop.test/sale">Shop</a>`}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "buyers", "rule": map[string]any{"attr": "cohort", "op": "eq", "value": run}}, 201)["id"]
	time.Sleep(3 * time.Second)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": buyer, "name": "identify", "email": email, "attrs": map[string]any{"cohort": run}}, 202)
	waitFor(t, 15*time.Second, func() bool {
		return jsonCall(t, c, "GET", fmt.Sprint("/v1/segments/", seg), nil, 200)["size"] == 1.0
	})
	b := idOf(jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Diwali sale", "channel": "email", "template_id": tpl, "segment_id": seg,
		"start_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "repeat": "none"}, 200)["id"])
	jsonCall(t, c, "POST", fmt.Sprint("/v1/broadcasts/", b, "/schedule"), nil, 200)
	var mails mailSearch
	waitFor(t, 20*time.Second, func() bool {
		mails = searchMail(t, "to:"+email)
		return len(mails.Messages) == 1
	})
	clickFirstLink(t, mails.Messages[0].ID)
	waitFor(t, 10*time.Second, func() bool { return contains(contactEvents(t, c, buyer), "email_clicked") })

	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": buyer, "name": "purchase", "props": map[string]any{"amount": 499}},
		map[string]any{"external_id": visitor, "name": "page_view", "ts": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			"props": map[string]any{"url": "https://shop.test/?utm_campaign=diwali", "utm_source": "google", "utm_medium": "cpc", "utm_campaign": "diwali"}},
		map[string]any{"external_id": visitor, "name": "purchase", "props": map[string]any{"amount": "100.5"}},
	}}, 202)

	var rep map[string]any
	waitFor(t, 20*time.Second, func() bool {
		rep = jsonCall(t, c, "POST", "/v1/revenue/report", map[string]any{"model": "last_touch", "window_days": 7}, 200)
		tot := rep["totals"].(map[string]any)
		msgs := rep["messages"].(map[string]any)
		return tot["revenue"] == 599.5 && tot["orders"] == 2.0 && msgs["revenue"] == 499.0
	})
	credit := asList(rep["messages"].(map[string]any)["credits"])[0].(map[string]any)
	if credit["kind"] != "broadcast" || idOf(credit["id"]) != b || credit["name"] != "Diwali sale" || credit["conversions"] != 1.0 {
		t.Errorf("message credit = %v", credit)
	}
	camp := asList(rep["campaigns"].(map[string]any)["credits"])
	if len(camp) != 1 || camp[0].(map[string]any)["campaign"] != "diwali" || camp[0].(map[string]any)["source"] != "google" || camp[0].(map[string]any)["revenue"] != 100.5 {
		t.Errorf("campaign credits = %v", camp)
	}
	if rep["settings"].(map[string]any)["currency"] != "INR" {
		t.Errorf("settings = %v", rep["settings"])
	}
	for _, model := range []string{"first_touch", "linear"} {
		r := jsonCall(t, c, "POST", "/v1/revenue/report", map[string]any{"model": model}, 200)
		if r["messages"].(map[string]any)["revenue"] != 499.0 {
			t.Errorf("%s: %v", model, r["messages"])
		}
	}
	jsonCall(t, c, "POST", "/v1/revenue/report", map[string]any{"model": "position_based"}, 400)

	waitFor(t, 10*time.Second, func() bool {
		rv, _ := jsonCall(t, c, "GET", fmt.Sprint("/v1/broadcasts/", b, "/report"), nil, 200)["revenue"].(map[string]any)
		return rv != nil && rv["revenue"] == 499.0 && rv["orders"] == 1.0
	})
}

// TestFunnelBreakdown splits a funnel by a profile attribute.
func TestFunnelBreakdown(t *testing.T) {
	c, _ := workspace(t, "Breakdown")
	run := fmt.Sprint(time.Now().UnixNano())
	var evs []any
	add := func(who, plan string, buys bool) {
		ext := "bd-" + run + "-" + who
		evs = append(evs, map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"plan": plan}},
			map[string]any{"external_id": ext, "name": "signup", "ts": time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)})
		if buys {
			evs = append(evs, map[string]any{"external_id": ext, "name": "purchase", "ts": time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)})
		}
	}
	add("a", "pro", true)
	add("b", "pro", true)
	add("c", "pro", false)
	add("d", "free", true)
	add("e", "free", false)
	add("f", "", false)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	steps := []any{map[string]any{"event": "signup"}, map[string]any{"event": "purchase"}}
	var got string
	if !eventually(20*time.Second, func() bool {
		res := jsonCall(t, c, "POST", "/v1/funnels/run", map[string]any{"steps": steps, "window_days": 1, "breakdown": map[string]any{"attr": "plan"}}, 200)
		bd, _ := res["breakdown"].(map[string]any)
		var rows []string
		for _, r := range asList(bd["rows"]) {
			rows = append(rows, fmt.Sprintf("%v=%v", r.(map[string]any)["value"], asList(r.(map[string]any)["reached"])))
		}
		got = strings.Join(rows, " ")
		return got == "pro=[3 2] free=[2 1] =[1 0]" // f has no plan
	}) {
		t.Fatalf("breakdown = %s", got)
	}
	jsonCall(t, c, "POST", "/v1/funnels/run", map[string]any{"steps": steps, "window_days": 1, "breakdown": map[string]any{"attr": "plan'] OR 1=1 --"}}, 400)
}

// TestCustomReport: a saved report of revenue by product, and contacts by plan.
func TestCustomReport(t *testing.T) {
	c, _ := workspace(t, "Reports")
	run := fmt.Sprint(time.Now().UnixNano())
	buy := func(who, sku string, amount float64) map[string]any {
		return map[string]any{"external_id": "cr-" + run + "-" + who, "name": "purchase", "props": map[string]any{"sku": sku, "amount": amount}}
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": "cr-" + run + "-a", "name": "identify", "attrs": map[string]any{"plan": "pro"}},
		map[string]any{"external_id": "cr-" + run + "-b", "name": "identify", "attrs": map[string]any{"plan": "free"}},
		buy("a", "shoes", 300), buy("a", "socks", 20), buy("b", "shoes", 200), buy("b", "hat", 5),
	}}, 202)
	spec := map[string]any{"source": "events", "metric": map[string]any{"type": "sum", "key": "amount"},
		"group_by": map[string]any{"type": "prop", "key": "sku"}, "events": []string{"purchase"},
		"where": []any{map[string]any{"prop": "amount", "op": "gte", "value": 10}}, "chart": "bars", "range_days": 7}
	rows := func(spec map[string]any) string {
		var out []string
		for _, r := range asList(jsonCall(t, c, "POST", "/v1/reports/run", map[string]any{"spec": spec}, 200)["rows"]) {
			out = append(out, fmt.Sprint(r.(map[string]any)["group"], "=", r.(map[string]any)["value"]))
		}
		return strings.Join(out, " ")
	}
	waitFor(t, 20*time.Second, func() bool { return rows(spec) == "shoes=500 socks=20" })

	id := idOf(jsonCall(t, c, "POST", "/v1/reports", map[string]any{"name": "Revenue by product", "spec": spec}, 200)["id"])
	saved := jsonCall(t, c, "GET", fmt.Sprint("/v1/reports/", id), nil, 200)
	if rows(saved["spec"].(map[string]any)) != "shoes=500 socks=20" {
		t.Errorf("saved report gives %s", rows(saved["spec"].(map[string]any)))
	}
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/reports/", id), map[string]any{"name": "Revenue by product (all)", "spec": spec}, 200)
	if l := asList(jsonCall(t, c, "GET", "/v1/reports", nil, 200)["reports"]); len(l) != 1 || l[0].(map[string]any)["name"] != "Revenue by product (all)" {
		t.Errorf("reports = %v", l)
	}

	byPlan := map[string]any{"source": "events", "metric": map[string]any{"type": "unique_contacts"}, "group_by": map[string]any{"type": "attr", "key": "plan"},
		"events": []string{"purchase"}, "chart": "table"}
	waitFor(t, 10*time.Second, func() bool { return rows(byPlan) == "free=1 pro=1" })
	daily := map[string]any{"source": "events", "metric": map[string]any{"type": "count"}, "group_by": map[string]any{"type": "day"}, "events": []string{"purchase"}, "chart": "line", "range_days": 3}
	if got := asList(jsonCall(t, c, "POST", "/v1/reports/run", map[string]any{"spec": daily}, 200)["rows"]); len(got) < 3 {
		t.Errorf("daily rows = %v, want every day filled", got)
	}

	bad := map[string]any{"source": "events", "metric": map[string]any{"type": "sum", "key": "amount) FROM system.tables --"}, "group_by": map[string]any{"type": "none"}, "chart": "table"}
	jsonCall(t, c, "POST", "/v1/reports/run", map[string]any{"spec": bad}, 400)
	jsonCall(t, c, "POST", "/v1/reports", map[string]any{"name": "bad", "spec": bad}, 400)
	jsonCall(t, c, "POST", "/v1/reports/run", map[string]any{"spec": spec, "from": "2024-01-01T00:00:00Z", "to": "2026-01-01T00:00:00Z"}, 400)
	jsonCall(t, c, "DELETE", fmt.Sprint("/v1/reports/", id), nil, 204)
}

// TestJourneyABWinner: a split with an automatic winner by clicks. Once its short test window has
// passed and both paths have contacts, the path whose emails were clicked wins, and everyone after
// takes it.
func TestJourneyABWinner(t *testing.T) {
	c, _ := workspace(t, "ABWinner")
	run := fmt.Sprint(time.Now().UnixNano())
	tpl := func(v string) any {
		return jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": v, "channel": "email", "subject": "AB-" + v + " " + run,
			"html": `<p>` + v + `</p><a href="https://shop.test/` + v + `">Go</a>`}, 201)["id"]
	}
	ta, tb := tpl("A"), tpl("B")
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "ab winner", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "ab_start"},
		"entry":   "ab",
		"nodes": map[string]any{
			"ab": map[string]any{"type": "split", "variants": []any{
				map[string]any{"weight": 1, "next": "ma", "label": "A"}, map[string]any{"weight": 1, "next": "mb", "label": "B"}},
				"winner": map[string]any{"metric": "clicked", "after_seconds": 3, "min_per_variant": 2}},
			"ma": map[string]any{"type": "send_email", "template_id": ta, "next": "x"},
			"mb": map[string]any{"type": "send_email", "template_id": tb, "next": "x"},
			"x":  map[string]any{"type": "exit"},
		},
	}}, 201)
	jpath := fmt.Sprintf("/v1/journeys/%v", j["id"])
	jsonCall(t, c, "POST", jpath+"/activate", nil, 200)
	time.Sleep(3 * time.Second)

	enter := func(from, n int) {
		var evs []any
		for i := from; i < from+n; i++ {
			ext := fmt.Sprintf("abw-%s-%d", run, i)
			evs = append(evs, map[string]any{"external_id": ext, "name": "identify", "email": ext + "@example.com"},
				map[string]any{"external_id": ext, "name": "ab_start"})
		}
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	}
	const first = 12
	enter(0, first)
	var mails mailSearch
	waitFor(t, 25*time.Second, func() bool {
		mails = searchMail(t, "subject:\"AB-\" "+run)
		return len(mails.Messages) == first
	})
	nB := 0
	for _, m := range mails.Messages {
		if strings.HasPrefix(m.Subject, "AB-B") {
			clickFirstLink(t, m.ID)
			nB++
		}
	}
	if nB < 2 || nB > first-2 {
		t.Fatalf("%d of %d took path B; the test needs both paths", nB, first)
	}

	var rep map[string]any
	waitFor(t, 40*time.Second, func() bool {
		rep = jsonCall(t, c, "GET", jpath+"/report", nil, 200)
		sp, _ := rep["splits"].(map[string]any)
		ab, _ := sp["ab"].(map[string]any)
		d, _ := ab["decision"].(map[string]any)
		return d != nil
	})
	ab := rep["splits"].(map[string]any)["ab"].(map[string]any)
	d := ab["decision"].(map[string]any)
	if d["path"] != 1.0 || d["label"] != "B" || d["metric"] != "clicked" {
		t.Fatalf("decision = %v", d)
	}
	if paths := asList(ab["paths"]); len(paths) != 2 || paths[1].(map[string]any)["hits"] != float64(nB) {
		t.Errorf("paths = %v", paths)
	}
	if got := jsonCall(t, c, "GET", jpath, nil, 200)["split_winners"]; got == nil {
		t.Error("the journey should carry its recorded winner")
	}

	time.Sleep(3 * time.Second) // the engine's next refresh applies the winner
	enter(first, 6)
	waitFor(t, 25*time.Second, func() bool {
		return len(searchMail(t, "subject:\"AB-\" "+run).Messages) == first+6
	})
	for _, m := range searchMail(t, "subject:\"AB-\" "+run).Messages {
		var idx int
		if _, err := fmt.Sscanf(strings.TrimPrefix(m.To[0].Address, "abw-"+run+"-"), "%d@", &idx); err == nil && idx >= first && !strings.HasPrefix(m.Subject, "AB-B") {
			t.Errorf("contact %d got %q after B won", idx, m.Subject)
		}
	}
}

// eventually polls cond like waitFor, but reports instead of failing, so callers can say what they saw.
func eventually(timeout time.Duration, cond func() bool) bool {
	for start := time.Now(); time.Since(start) < timeout; time.Sleep(100 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
