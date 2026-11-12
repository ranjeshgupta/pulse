//go:build e2e

package e2e

// M7, clarity: every number of contacts opens the contact list filtered to exactly those
// contacts. Click-through equivalents through the API: each audience estimate step, a broadcast's
// funnel and reasons, a journey's step and its messages, product buyers and segment sizes, each
// compared with the list's count (and the list paged to the end); who may open which list; a
// static segment made from a list; and the old Journeys / Broadcasts addresses redirecting.

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// listCount opens the Contacts list for q and returns its count, after checking that paging
// through it lists exactly that many different contacts.
func listCount(t *testing.T, c *http.Client, q string) float64 {
	t.Helper()
	first := jsonCall(t, c, "GET", "/v1/contacts?limit=200&q="+url.QueryEscape(q), nil, 200)
	n, approx := num(first["count"]), first["approximate"] == true
	if approx {
		t.Fatalf("%s: an approximate count %v on a small workspace", q, n)
	}
	seen := map[string]bool{}
	page := first
	for {
		for _, x := range page["contacts"].([]any) {
			seen[x.(map[string]any)["external_id"].(string)] = true
		}
		next, _ := page["next"].(string)
		if next == "" {
			break
		}
		page = jsonCall(t, c, "GET", "/v1/contacts?limit=200&after="+next+"&q="+url.QueryEscape(q), nil, 200)
	}
	if float64(len(seen)) != n {
		t.Errorf("%s: count %v but the pages list %d contacts", q, n, len(seen))
	}
	return n
}

func TestM7ContactsBehindNumbers(t *testing.T) {
	c, _ := workspace(t, "Clarity")
	run := fmt.Sprint(time.Now().UnixNano())
	plan := "m7-" + run
	addr := func(k string) string { return "m7-" + k + "-" + run + "@example.com" }
	ext := func(k string) string { return "m7" + k + run }
	// a, b, c reachable by email; d excluded (VIP); e unsubscribed from email; f no email; g bounced.
	var ev []any
	for _, k := range []string{"a", "b", "c", "d", "e", "g"} {
		attrs := map[string]any{"plan": plan}
		if k == "d" {
			attrs["vip"] = "yes"
		}
		if k == "g" {
			attrs["email_bounced"] = true
		}
		ev = append(ev, map[string]any{"external_id": ext(k), "name": "identify", "email": addr(k), "attrs": attrs})
	}
	ev = append(ev, map[string]any{"external_id": ext("f"), "name": "identify", "attrs": map[string]any{"plan": plan, "phone": "+919800007777"}})
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": ev}, 202)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext("e"), "name": "unsubscribed", "props": map[string]any{"channel": "email"}}, 202)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M7 audience", "rule": map[string]any{"attr": "plan", "op": "eq", "value": plan}}, 201)["id"]
	vip := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M7 VIPs", "rule": map[string]any{"attr": "vip", "op": "eq", "value": "yes"}}, 201)["id"]
	optedOut := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M7 opted out", "rule": map[string]any{"all": []any{
		map[string]any{"attr": "plan", "op": "eq", "value": plan}, map[string]any{"attr": "optout_email", "op": "eq", "value": "true"}}}}, 201)["id"]
	waitSegmentSize(t, c, seg, 7)
	waitSegmentSize(t, c, vip, 1)
	waitSegmentSize(t, c, optedOut, 1)
	waitReachReady(t, c, seg)
	time.Sleep(time.Second)

	// Segment size and every audience estimate step → the list has exactly that many.
	if n := listCount(t, c, fmt.Sprintf("in:%v", seg)); n != 7 {
		t.Errorf("segment list: %v, want its size 7", n)
	}
	est := jsonCall(t, c, "POST", "/v1/segments/estimate", map[string]any{"include": []any{seg}, "exclude": []any{vip}, "channel": "email"}, 200)
	for q, k := range map[string]string{
		fmt.Sprintf("in:%v", seg):                                                "audience",
		fmt.Sprintf("in:%v not_in:%v", seg, vip):                                 "after_exclude",
		fmt.Sprintf("in:%v not_in:%v subscribed:email", seg, vip):                "after_consent",
		fmt.Sprintf("in:%v not_in:%v reachable:email", seg, vip):                 "reachable",
		fmt.Sprintf("in:%v not_in:%v reachable:email under_cap:email", seg, vip): "will_send",
	} {
		if n := listCount(t, c, q); n != num(est[k]) {
			t.Errorf("estimate step %s = %v, list %q has %v", k, est[k], q, n)
		}
	}
	reach := jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%v/reach", seg), nil, 200)
	for _, x := range reach["channels"].([]any) {
		ch := x.(map[string]any)
		if n := listCount(t, c, fmt.Sprintf("in:%v reachable:%v", seg, ch["channel"])); n != num(ch["reachable"]) {
			t.Errorf("reach by %v: %v, list %v", ch["channel"], ch["reachable"], n)
		}
	}
	// Set filters combine with rule filters: the reachable ones who aren't VIPs, by email domain.
	if n := listCount(t, c, fmt.Sprintf("in:%v reachable:email email:%s", seg, "-a-"+run)); n != 1 {
		t.Errorf("in: with email: = %v, want 1", n)
	}

	// A broadcast: every funnel number and reason opens its contacts.
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Spring", "channel": "email", "subject": "Spring",
		"html": `<p>Hello</p>`}, 201)["id"]
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Spring " + run, "channel": "email", "template_id": tpl,
		"segment_id": seg, "exclude_segment_ids": []any{vip}, "repeat": "none", "start_at": time.Now().UTC().Format(time.RFC3339)}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)
	var rep map[string]any
	waitFor(t, 40*time.Second, func() bool {
		rep = jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil, 200)
		f, _ := rep["funnel"].(map[string]any)
		return f != nil && num(f["sent"]) == 3 && num(f["targeted"]) >= 5
	})
	time.Sleep(2 * time.Second)
	rep = jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil, 200)
	f := rep["funnel"].(map[string]any)
	checked := 0
	for _, k := range []string{"targeted", "sent", "delivered", "opened", "clicked", "bounced", "complained", "failed", "unsubscribed"} {
		if num(f[k]) == 0 {
			continue
		}
		checked++
		if n := listCount(t, c, fmt.Sprintf("campaign:broadcast/%v outcome:%s", bc, k)); n != num(f[k]) {
			t.Errorf("funnel %s = %v, list %v", k, f[k], n)
		}
	}
	for g, v := range f["skipped"].(map[string]any) {
		if num(v) == 0 {
			continue
		}
		checked++
		if n := listCount(t, c, fmt.Sprintf("campaign:broadcast/%v outcome:skipped_%s", bc, g)); n != num(v) {
			t.Errorf("skipped %s = %v, list %v", g, v, n)
		}
	}
	for _, x := range rep["reasons"].([]any) {
		r := x.(map[string]any)
		checked++
		if n := listCount(t, c, fmt.Sprintf("campaign:broadcast/%v outcome:%v reason:%v", bc, r["kind"], r["code"])); n != num(r["contacts"]) {
			t.Errorf("reason %v/%v = %v, list %v", r["kind"], r["code"], r["contacts"], n)
		}
	}
	if checked < 3 {
		t.Errorf("only %d funnel numbers to check: %v", checked, f)
	}
	// The hub's "Sent to" is the same list.
	hub := jsonCall(t, c, "GET", "/v1/campaigns", nil, 200)["campaigns"].([]any)
	for _, x := range hub {
		r := x.(map[string]any)
		if r["kind"] == "broadcast" && num(r["id"]) == num(bc) {
			if n := listCount(t, c, fmt.Sprintf("campaign:broadcast/%v outcome:sent", bc)); n != num(r["sent"]) {
				t.Errorf("hub sent %v, list %v", r["sent"], n)
			}
		}
	}

	// A journey: who is at a step now, and who got its message.
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "Nudge", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "m7_signup"},
		"entry":   "mail",
		"nodes": map[string]any{
			"mail": map[string]any{"type": "send_email", "template_id": tpl, "next": "hold"},
			"hold": map[string]any{"type": "wait", "seconds": 3600, "next": "x"},
			"x":    map[string]any{"type": "exit"},
		},
	}}, 201)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j), nil, 200)
	time.Sleep(3 * time.Second)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext("a"), "name": "m7_signup"}, map[string]any{"external_id": ext("b"), "name": "m7_signup"},
	}}, 202)
	var jr map[string]any
	waitFor(t, 40*time.Second, func() bool {
		jr = jsonCall(t, c, "GET", fmt.Sprintf("/v1/journeys/%v/report", j), nil, 200)
		node, _ := jr["enrollments"].(map[string]any)["node"].(map[string]any)
		byNode, _ := jr["messages"].(map[string]any)["by_node"].(map[string]any)
		mail, _ := byNode["mail"].(map[string]any)
		return num(node["hold"]) == 2 && mail != nil && num(mail["sent"]) == 2
	})
	if n := listCount(t, c, fmt.Sprintf("journey:%v at:hold", j)); n != 2 {
		t.Errorf("here now at the wait: list %v, want 2", n)
	}
	if n := listCount(t, c, fmt.Sprintf("journey:%v journey_status:in_progress", j)); n != 2 {
		t.Errorf("in progress: list %v, want 2", n)
	}
	if n := listCount(t, c, fmt.Sprintf("campaign:journey/%v step:mail outcome:sent", j)); n != 2 {
		t.Errorf("step messages sent: list %v, want 2", n)
	}
	// Errors name the problem; another workspace's ids are unknown here.
	jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape("at:hold"), nil, 400)
	jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape("campaign:broadcast/999999999"), nil, 400)
	other, _ := workspace(t, "Clarity other")
	jsonCall(t, other, "GET", fmt.Sprintf("/v1/contacts?q=%s", url.QueryEscape(fmt.Sprintf("journey:%v", j))), nil, 400)
	jsonCall(t, other, "GET", fmt.Sprintf("/v1/contacts?q=%s", url.QueryEscape(fmt.Sprintf("in:%v", seg))), nil, 400)

	// Product buyers on the Products dashboard → the list of buyers.
	day := time.Now().UTC().Format(time.DateOnly)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext("a"), "name": "purchase", "props": map[string]any{"product_code": "SKU-" + run, "amount": 10}},
		map[string]any{"external_id": ext("b"), "name": "purchase", "props": map[string]any{"product_code": "SKU-" + run, "amount": 20}},
		map[string]any{"external_id": ext("c"), "name": "add_to_cart", "props": map[string]any{"product_code": "SKU-" + run}},
	}}, 202)
	// The dashboard caches its answer for a minute: ask once the events are stored.
	waitFor(t, 30*time.Second, func() bool {
		return num(jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape("event:add_to_cart product:SKU-"+run), nil, 200)["count"]) == 1 &&
			num(jsonCall(t, c, "GET", "/v1/contacts?q="+url.QueryEscape("event:purchase product:SKU-"+run), nil, 200)["count"]) == 2
	})
	var buyers, abandoned float64
	p := jsonCall(t, c, "GET", "/v1/analytics/products?from="+day+"&to="+day, nil, 200)
	for _, x := range p["report"].(map[string]any)["products"].([]any) {
		row := x.(map[string]any)
		if row["code"] == "SKU-"+run {
			buyers, abandoned = num(row["buyers"]), num(row["abandoned"])
		}
	}
	if buyers != 2 || abandoned != 1 {
		t.Errorf("products dashboard: %v buyers, %v abandoned; want 2 and 1", buyers, abandoned)
	}
	if n := listCount(t, c, fmt.Sprintf("event:purchase product:SKU-%s period:%s", run, day)); n != buyers {
		t.Errorf("buyers %v, list %v", buyers, n)
	}
	if abandoned > 0 {
		if n := listCount(t, c, fmt.Sprintf("event:add_to_cart no_event:purchase product:SKU-%s period:%s", run, day)); n != abandoned {
			t.Errorf("abandoned %v, list %v", abandoned, n)
		}
	}

	// "Create segment from this list": a static segment of the people who got the broadcast.
	sent := num(f["sent"])
	made := jsonCall(t, c, "POST", "/v1/contacts/view-segment", map[string]any{"q": fmt.Sprintf("campaign:broadcast/%v outcome:sent", bc), "name": "Got Spring " + run}, 202)
	if num(made["contacts"]) != sent {
		t.Errorf("view segment counted %v, want %v", made["contacts"], sent)
	}
	waitSegmentSize(t, c, made["id"], sent)
	jsonCall(t, c, "POST", "/v1/contacts/view-segment", map[string]any{"q": "", "name": "everyone"}, 400)

	// Permissions: a list behind a campaign's number needs contacts view and broadcasts or reports view.
	me := jsonCall(t, c, "GET", "/v1/auth/me", nil, 200)
	slug := me["workspace"].(map[string]any)["slug"].(string)
	login := func(perms map[string]any, who string) *http.Client {
		role := jsonCall(t, c, "POST", "/v1/roles", map[string]any{"name": who, "permissions": perms}, 200)
		email := who + "-" + run + "@example.com"
		jsonCall(t, c, "POST", "/v1/users", map[string]any{"email": email, "name": who, "role": role["key"], "password": "m7 password one"}, 201)
		jar, _ := cookiejar.New(nil)
		u := &http.Client{Jar: jar, Timeout: 15 * time.Second}
		jsonCall(t, u, "POST", "/v1/auth/login", map[string]any{"workspace": slug, "email": email, "password": "m7 password one"}, 200)
		return u
	}
	q := url.QueryEscape(fmt.Sprintf("campaign:broadcast/%v outcome:sent", bc))
	reportsOnly := login(map[string]any{"reports": "view"}, "reports")
	jsonCall(t, reportsOnly, "GET", "/v1/contacts?q="+q, nil, 403)
	contactsOnly := login(map[string]any{"contacts": "view"}, "contacts")
	if e := jsonCall(t, contactsOnly, "GET", "/v1/contacts?q="+q, nil, 400); !strings.Contains(fmt.Sprint(e["error"]), "view access") {
		t.Errorf("contacts-only role: %v", e)
	}
	jsonCall(t, contactsOnly, "GET", "/v1/contacts?q="+url.QueryEscape(fmt.Sprintf("in:%v", seg)), nil, 200)
	jsonCall(t, contactsOnly, "POST", "/v1/contacts/view-segment", map[string]any{"q": fmt.Sprintf("in:%v", seg), "name": "x"}, 403)
	both := login(map[string]any{"contacts": "view", "reports": "view"}, "analyst")
	if n := num(jsonCall(t, both, "GET", "/v1/contacts?q="+q, nil, 200)["count"]); n != sent {
		t.Errorf("analyst list %v, want %v", n, sent)
	}
}

// The old Journeys and Broadcasts lists redirect to Campaigns; a campaign's own page doesn't move.
func TestM7OldCampaignAddresses(t *testing.T) {
	noFollow := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for path, want := range map[string]string{"/journeys": "/campaigns", "/broadcasts": "/campaigns?tab=one_time"} {
		resp, err := noFollow.Get(apiURL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
			t.Errorf("%s: %d to %q, want %q", path, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
	for _, path := range []string{"/journeys/7", "/broadcasts/new", "/campaigns"} {
		resp, err := noFollow.Get(apiURL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d, want the app", path, resp.StatusCode)
		}
	}
}
