//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// postForm submits a hosted form as a browser would. shownAgo backdates the page's render time.
func postForm(t *testing.T, key string, values url.Values, shownAgo time.Duration) (int, string) {
	t.Helper()
	values.Set("_ts", fmt.Sprint(time.Now().Add(-shownAgo).UnixMilli()))
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.PostForm(apiURL+"/f/"+key, values)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestForms: a hosted form updates an existing contact found by email (opting them back in to
// email with consent), joins a list and tags them; bots and drafts get nothing; the web SDK skips
// fields a known visitor already filled.
func TestForms(t *testing.T) {
	c, _ := workspace(t, "Forms")
	run := fmt.Sprint(time.Now().UnixNano())
	ext, email := "cust-"+run, "f-"+run+"@example.com"
	list := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Webinar"}, 201)["id"])
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext, "name": "identify", "email": email, "attrs": map[string]any{"first_name": "Asha"}},
		map[string]any{"external_id": ext, "name": "unsubscribed", "props": map[string]any{"channel": "email"}},
	}}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, ext)["optout_email"] == true })

	form := map[string]any{"name": "Webinar signup", "status": "active",
		"fields": []any{
			map[string]any{"key": "email", "label": "Email", "type": "email", "required": true},
			map[string]any{"key": "first_name", "label": "First name", "type": "text", "progressive": true},
			map[string]any{"key": "company", "label": "Company", "type": "text", "required": true},
			map[string]any{"key": "utm_source", "type": "hidden", "default": "site"},
		},
		"settings": map[string]any{"list_ids": []any{list}, "tags": []any{"webinar"}, "consent_text": "Email me", "resubscribe": []any{"email"}}}
	// Reserved attributes and forms that don't say who the submitter is are refused.
	bad := map[string]any{"name": "x", "fields": []any{map[string]any{"key": "optout_email", "label": "x", "type": "checkbox"}}}
	if e := jsonCall(t, c, "POST", "/v1/forms", bad, 400)["error"].(string); !strings.Contains(e, "managed by Pulse") || !strings.Contains(e, "email or phone") {
		t.Errorf("validation error = %q", e)
	}
	id := idOf(jsonCall(t, c, "POST", "/v1/forms", form, 200)["id"])
	key := jsonCall(t, c, "GET", fmt.Sprint("/v1/forms/", id), nil, 200)["form"].(map[string]any)["public_key"].(string)

	// The hosted page renders, and can be framed by any site.
	resp, err := http.Get(apiURL + "/f/" + key + "?utm_source=newsletter")
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("X-Frame-Options") != "" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors *") {
		t.Fatalf("hosted form: %v %v %v", err, resp.StatusCode, resp.Header)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), `value="newsletter"`) {
		t.Errorf("hidden field should take the URL parameter:\n%s", page)
	}

	// Invalid values come back on the page; bots (honeypot, or too fast) see success but change nothing.
	if code, body := postForm(t, key, url.Values{"email": {"nope"}}, 5*time.Second); code != 422 || !strings.Contains(body, "Enter a valid email address.") {
		t.Errorf("invalid submission: %d", code)
	}
	if code, _ := postForm(t, key, url.Values{"email": {"bot-" + run + "@example.com"}, "company": {"x"}, "_consent": {"on"}, "_hp": {"spam"}}, 5*time.Second); code != 200 {
		t.Errorf("honeypot submission: %d", code)
	}
	if code, _ := postForm(t, key, url.Values{"email": {"fast-" + run + "@example.com"}, "company": {"x"}, "_consent": {"on"}}, 0); code != 200 {
		t.Errorf("instant submission: %d", code)
	}

	code, body := postForm(t, key, url.Values{"email": {strings.ToUpper(email)}, "first_name": {"Asha R"}, "company": {"Acme"},
		"utm_source": {"newsletter"}, "_consent": {"on"}}, 5*time.Second)
	if code != 200 || !strings.Contains(body, "Thanks!") {
		t.Fatalf("submission: %d %s", code, body)
	}
	took := waitFor(t, 10*time.Second, func() bool {
		a := attrsOf(t, c, ext)
		return a["company"] == "Acme" && a["optout_email"] == false && slices.Contains(segmentsOf(t, c, ext), list)
	})
	t.Logf("form submission updated the existing contact (matched by email) in %v", took)
	a := attrsOf(t, c, ext)
	if a["utm_source"] != "newsletter" || fmt.Sprint(a["tags"]) != "[webinar]" || a["first_name"] != "Asha R" {
		t.Errorf("attributes = %v", a)
	}
	for _, who := range []string{"bot-" + run + "@example.com", "fast-" + run + "@example.com"} {
		if jsonCall(t, c, "GET", "/v1/contacts/"+url.PathEscape(who), nil, -1)["contact"] != nil {
			t.Errorf("%s was recorded", who)
		}
	}
	waitFor(t, 15*time.Second, func() bool {
		st := jsonCall(t, c, "GET", fmt.Sprint("/v1/forms/", id, "/submissions"), nil, 200)
		return st["total"] == 1.0
	})

	// Progressive profiling: the web SDK doesn't ask a known visitor for a name they already gave.
	var sdk struct {
		Fields []struct{ Key string } `json:"fields"`
	}
	getJSON(t, apiURL+"/v1/web/forms/"+key+"?id="+url.QueryEscape(ext), &sdk)
	var keys []string
	for _, f := range sdk.Fields {
		keys = append(keys, f.Key)
	}
	if strings.Join(keys, ",") != "email,company,utm_source" {
		t.Errorf("fields for a known visitor = %v, want first_name skipped", keys)
	}

	// Drafts aren't served.
	form["status"] = "draft"
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/forms/", id), form, 200)
	if resp, _ := http.Get(apiURL + "/f/" + key); resp.StatusCode != 404 {
		t.Errorf("draft form: %d, want 404", resp.StatusCode)
	}
}

func getJSON(t *testing.T, u string, out any) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("GET %s: %d %v", u, resp.StatusCode, err)
	}
}

// TestReplyKeywordsAndPreferences: STOP and START replies change SMS consent; the preference center
// shows the contact's channels and public lists and saves their choices as events.
func TestReplyKeywordsAndPreferences(t *testing.T) {
	c, _ := workspace(t, "Prefs")
	run := fmt.Sprint(time.Now().UnixNano())
	ext, phone := "pref-"+run, "+9198"+run[len(run)-8:]
	list := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Weekly digest"}, 201)["id"])
	jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Internal VIPs"}, 201) // not public
	jsonCall(t, c, "PUT", fmt.Sprint("/v1/lists/", list), map[string]any{"name": "Weekly digest", "public": true, "description": "Our best posts"}, 200)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "email": ext + "@example.com", "attrs": map[string]any{"phone": phone}}, 202)
	jsonCall(t, c, "POST", fmt.Sprint("/v1/lists/", list, "/members"), map[string]any{"external_ids": []string{ext}, "action": "add"}, 202)
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, ext), list) })

	r := jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "sms", "from": phone, "text": " Stop. "}, 202)
	if r["action"] != "opt_out" || r["keyword"] != "STOP" {
		t.Errorf("STOP = %v", r)
	}
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, ext)["optout_sms"] == true })
	jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "sms", "from": phone, "text": "please stop texting so late"}, 202)
	jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "sms", "from": phone, "text": "START"}, 202)
	waitFor(t, 10*time.Second, func() bool { return attrsOf(t, c, ext)["optout_sms"] == false })
	if evs := contactEvents(t, c, ext); !slices.Contains(evs, "sms_received") {
		t.Errorf("replies are recorded as sms_received: %v", evs)
	}

	// Custom keywords replace the defaults.
	rules := jsonCall(t, c, "GET", "/v1/settings/delivery", nil, 200)
	rules["keywords"] = map[string]any{"opt_out": []any{"stop", "band"}, "opt_in": []any{"start"}}
	jsonCall(t, c, "PUT", "/v1/settings/delivery", rules, 200)
	if r := jsonCall(t, c, "POST", "/v1/sandbox/inbound", map[string]any{"channel": "sms", "from": phone, "text": "BAND"}, 202); r["action"] != "opt_out" {
		t.Errorf("custom keyword = %v", r)
	}

	link := jsonCall(t, c, "GET", "/v1/contacts/"+ext+"/preferences-link", nil, 200)["url"].(string)
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "Weekly digest") || strings.Contains(string(page), "Internal VIPs") || !strings.Contains(string(page), `name="list_`+fmt.Sprint(list)+`" checked`) {
		t.Errorf("preference center should offer only public lists, with membership:\n%s", page)
	}
	// Keep email and WhatsApp, drop SMS (already off) and leave the digest.
	resp, err = http.PostForm(link, url.Values{"action": {"preferences"}, "ch_email": {"on"}, "ch_whatsapp": {"on"}})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("save preferences: %v %v", err, resp.StatusCode)
	}
	waitFor(t, 10*time.Second, func() bool {
		a := attrsOf(t, c, ext)
		return !slices.Contains(segmentsOf(t, c, ext), list) && a["optout_rcs"] == true
	})
	if a := attrsOf(t, c, ext); a["optout_email"] == true || a["optout_whatsapp"] == true {
		t.Errorf("channels left on were turned off: %v", a)
	}
}

// TestBroadcastABTest: an email A/B test goes to a test group split between two variants; opens
// count per variant; the winner (picked by hand here) goes to everyone else exactly once; the
// report shows it all, with conversions.
func TestBroadcastABTest(t *testing.T) {
	c, _ := workspace(t, "AB")
	run := fmt.Sprint(time.Now().UnixNano())
	tplA := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "A", "channel": "email", "subject": "Variant A " + run, "html": "<p>A</p>"}, 201)["id"]
	tplB := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "B", "channel": "email", "subject": "Variant B " + run, "html": "<p>B</p>"}, 201)["id"]
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "ab", "rule": map[string]any{"attr": "cohort", "op": "eq", "value": run}}, 201)["id"]
	time.Sleep(3 * time.Second)
	const n = 30
	var evs []any
	for i := 0; i < n; i++ {
		evs = append(evs, map[string]any{"external_id": fmt.Sprintf("ab-%s-%d", run, i), "name": "identify",
			"email": fmt.Sprintf("ab-%s-%d@example.com", run, i), "attrs": map[string]any{"cohort": run}})
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": evs}, 202)
	waitFor(t, 15*time.Second, func() bool {
		return jsonCall(t, c, "GET", fmt.Sprint("/v1/segments/", seg), nil, 200)["size"] == float64(n)
	})

	ab := map[string]any{"variants": []any{map[string]any{"label": "A", "template_id": tplA}, map[string]any{"label": "B", "template_id": tplB}},
		"test_percent": 50, "metric": "opens", "wait_hours": 0}
	body := map[string]any{"name": "ab", "channel": "email", "segment_id": seg, "start_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		"repeat": "none", "ab": ab, "conversion_event": "purchase", "conversion_days": 3}
	if e := jsonCall(t, c, "POST", "/v1/broadcasts", body, 400)["error"]; !strings.Contains(fmt.Sprint(e), "1-72 hours") {
		t.Errorf("wait 0 hours = %v", e)
	}
	ab["metric"] = "manual"
	b := idOf(jsonCall(t, c, "POST", "/v1/broadcasts", body, 200)["id"])
	jsonCall(t, c, "POST", fmt.Sprint("/v1/broadcasts/", b, "/schedule"), nil, 200)

	var detail map[string]any
	waitFor(t, 15*time.Second, func() bool {
		detail = jsonCall(t, c, "GET", fmt.Sprint("/v1/broadcasts/", b), nil, 200)
		return detail["broadcast"].(map[string]any)["status"] == "testing"
	})
	runRow := detail["runs"].([]any)[0].(map[string]any)
	tested := int(runRow["test_recipients"].(float64))
	if tested < 6 || tested > 24 {
		t.Fatalf("test group = %d of %d, want about half", tested, n)
	}
	// Open every B email in the test group.
	var search struct {
		Messages []struct{ ID, Subject string } `json:"messages"`
	}
	waitFor(t, 20*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("subject:\"Variant\" "+run), &search)
		return len(search.Messages) == tested
	})
	openRe := regexp.MustCompile(`src="(` + regexp.QuoteMeta(apiURL) + `/t/o/[^"]+)"`)
	opensB := 0
	for _, m := range search.Messages {
		if !strings.HasPrefix(m.Subject, "Variant B") {
			continue
		}
		var msg struct{ HTML string }
		mailpitGet(t, "/api/v1/message/"+m.ID, &msg)
		if px := openRe.FindStringSubmatch(msg.HTML); px != nil {
			if resp, err := http.Get(html.UnescapeString(px[1])); err == nil {
				resp.Body.Close()
				opensB++
			}
		}
	}
	report := func() map[string]any {
		return jsonCall(t, c, "GET", fmt.Sprint("/v1/broadcasts/", b, "/report"), nil, 200)
	}
	waitFor(t, 15*time.Second, func() bool {
		v, _ := report()["by_variant"].(map[string]any)
		bv, _ := v["B"].(map[string]any)
		return bv != nil && bv["opened"] == float64(opensB)
	})

	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%d/runs/%d/winner", b, idOf(runRow["id"])), map[string]any{"variant": "B"}, 200)
	waitFor(t, 15*time.Second, func() bool {
		detail = jsonCall(t, c, "GET", fmt.Sprint("/v1/broadcasts/", b), nil, 200)
		return detail["broadcast"].(map[string]any)["status"] == "completed"
	})
	runRow = detail["runs"].([]any)[0].(map[string]any)
	if runRow["winner"] != "B" || int(runRow["winner_recipients"].(float64)) != n-tested {
		t.Errorf("winner run = %v, want B to %d", runRow, n-tested)
	}
	// Everyone got exactly one email.
	waitFor(t, 20*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("subject:\"Variant\" "+run), &search)
		return len(search.Messages) >= n
	})
	time.Sleep(2 * time.Second)
	mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("subject:\"Variant\" "+run), &search)
	if len(search.Messages) != n {
		t.Errorf("%d emails for %d contacts", len(search.Messages), n)
	}

	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": fmt.Sprintf("ab-%s-0", run), "name": "purchase"}, 202)
	waitFor(t, 15*time.Second, func() bool {
		tot, _ := report()["totals"].(map[string]any)
		return tot["sent"] == float64(n) && tot["converted"] == 1.0
	})
	v := report()["by_variant"].(map[string]any)
	if v["winner"].(map[string]any)["sent"] != float64(n-tested) {
		t.Errorf("by variant = %v", v)
	}
}

// TestFunnel: contacts who complete steps in order, within the window, count at each step.
func TestFunnel(t *testing.T) {
	c, _ := workspace(t, "Funnel")
	run := fmt.Sprint(time.Now().UnixNano())
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(time.Duration(m-60) * time.Minute).Format(time.RFC3339) }
	ev := func(who, name string, minute int, props map[string]any) map[string]any {
		return map[string]any{"external_id": "fn-" + run + "-" + who, "name": name, "ts": at(minute), "props": props}
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		ev("a", "view_pricing", 1, nil), ev("a", "signup", 2, nil), ev("a", "purchase", 3, map[string]any{"amount": 900}),
		ev("b", "view_pricing", 1, nil), ev("b", "signup", 2, nil), ev("b", "purchase", 3, map[string]any{"amount": 100}),
		ev("c", "view_pricing", 1, nil), ev("c", "signup", 2, nil),
		ev("d", "signup", 1, nil), ev("d", "view_pricing", 2, nil), // wrong order: only step 1
		ev("e", "view_pricing", 1, nil),
	}}, 202)
	steps := []any{map[string]any{"event": "view_pricing"}, map[string]any{"event": "signup"},
		map[string]any{"event": "purchase", "where": []any{map[string]any{"prop": "amount", "op": "gte", "value": 500}}}}
	var got []float64
	waitFor(t, 20*time.Second, func() bool {
		res := jsonCall(t, c, "POST", "/v1/funnels/run", map[string]any{"steps": steps, "window_days": 1}, 200)
		got = nil
		for _, s := range asList(res["steps"]) {
			got = append(got, s.(map[string]any)["contacts"].(float64))
		}
		return fmt.Sprint(got) == "[5 3 1]"
	})
	id := idOf(jsonCall(t, c, "POST", "/v1/funnels", map[string]any{"name": "Pricing to purchase", "steps": steps, "window_days": 1}, 200)["id"])
	if f := jsonCall(t, c, "GET", fmt.Sprint("/v1/funnels/", id), nil, 200); len(asList(f["steps"])) != 3 {
		t.Errorf("saved funnel = %v", f)
	}
	jsonCall(t, c, "POST", "/v1/funnels/run", map[string]any{"steps": steps[:1], "window_days": 1}, 400)
}
