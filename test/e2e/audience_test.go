//go:build e2e

package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// segmentsOf returns the segment and list ids a contact is in right now.
func segmentsOf(t *testing.T, c *http.Client, ext string) []int64 {
	out := jsonCall(t, c, "GET", "/v1/contacts/"+url.PathEscape(ext), nil, -1)
	var ids []int64
	for _, v := range asList(out["segments"]) {
		ids = append(ids, int64(v.(float64)))
	}
	return ids
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func attrsOf(t *testing.T, c *http.Client, ext string) map[string]any {
	ct, _ := jsonCall(t, c, "GET", "/v1/contacts/"+url.PathEscape(ext), nil, -1)["contact"].(map[string]any)
	a, _ := ct["attrs"].(map[string]any)
	return a
}

func idOf(v any) int64 { return int64(v.(float64)) }

// TestTagsPointsAndLists: scoring rules and manual points, tags, a static list, and a segment
// built from all three, with a segment that depends on it, all moving in real time.
func TestTagsPointsAndLists(t *testing.T) {
	c, _ := workspace(t, "Audience")
	run := fmt.Sprint(time.Now().UnixNano())
	ext := "aud-" + run
	jsonCall(t, c, "PUT", "/v1/scoring/rules", map[string]any{"event": "demo_booked", "points": 40}, 200)

	list := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Webinar guests"}, 201)["id"])
	hot := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Hot VIP guests", "rule": map[string]any{"all": []any{
		map[string]any{"segment": list, "op": "in"},
		map[string]any{"tag": "vip", "op": "has"},
		map[string]any{"attr": "points", "op": "gte", "value": 50},
	}}}, 201)["id"])
	notHot := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Guests to warm up", "rule": map[string]any{"all": []any{
		map[string]any{"segment": list, "op": "in"},
		map[string]any{"segment": hot, "op": "not_in"},
	}}}, 201)["id"])
	time.Sleep(3 * time.Second) // segment index refresh

	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"city": "Pune"}}, 202)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/lists/%d/members", list), map[string]any{"external_ids": []string{ext}, "action": "add"}, 202)
	waitFor(t, 10*time.Second, func() bool {
		s := segmentsOf(t, c, ext)
		return slices.Contains(s, list) && slices.Contains(s, notHot)
	})

	jsonCall(t, c, "POST", "/v1/contacts/"+ext+"/tags", map[string]any{"add": []string{"vip", "beta"}}, 202)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "demo_booked"}, 202)
	jsonCall(t, c, "POST", "/v1/contacts/"+ext+"/points", map[string]any{"delta": 15}, 202)
	took := waitFor(t, 10*time.Second, func() bool {
		s := segmentsOf(t, c, ext)
		return slices.Contains(s, hot) && !slices.Contains(s, notHot)
	})
	t.Logf("tag + scored event + points moved the contact into the dependent segment in %v", took)
	a := attrsOf(t, c, ext)
	if a["points"] != 55.0 || fmt.Sprint(a["tags"]) != "[beta vip]" {
		t.Errorf("attrs = %v, want 55 points and tags beta, vip", a)
	}
	tags := jsonCall(t, c, "GET", "/v1/tags", nil, 200)["tags"]
	if fmt.Sprint(tags) != "[beta vip]" {
		t.Errorf("tags in use = %v", tags)
	}

	// Leaving the list takes the contact out of both segments that depend on it.
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/lists/%d/members", list), map[string]any{"external_ids": []string{ext}, "action": "remove"}, 202)
	waitFor(t, 10*time.Second, func() bool {
		s := segmentsOf(t, c, ext)
		return !slices.Contains(s, list) && !slices.Contains(s, hot) && !slices.Contains(s, notHot)
	})

	// A segment can't include itself through another segment, and a list in use can't be deleted.
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/segments/%d", hot), map[string]any{"name": "loop", "rule": map[string]any{"segment": notHot, "op": "in"}}, 400)
	jsonCall(t, c, "DELETE", fmt.Sprintf("/v1/segments/%d", list), nil, 409)
	jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "missing", "rule": map[string]any{"segment": 999999999, "op": "in"}}, 400)

	// CSV import straight into a list, with tags.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "guests.csv")
	fmt.Fprintf(fw, "external_id,city,tags\nimp1-%s,Delhi,vip;speaker\nimp2-%s,Goa,\n", run, run)
	mw.Close()
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/v1/contacts/import?list_id=%d", apiURL, list), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Pulse-CSRF", "1")
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("import: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()
	waitFor(t, 10*time.Second, func() bool {
		return slices.Contains(segmentsOf(t, c, "imp1-"+run), list) && slices.Contains(segmentsOf(t, c, "imp2-"+run), list)
	})
	if got := fmt.Sprint(attrsOf(t, c, "imp1-"+run)["tags"]); got != "[speaker vip]" {
		t.Errorf("imported tags = %s", got)
	}
}

// TestEventPropertyFilters: a filtered event rule counts only matching events, both for history
// (seeded from ClickHouse when the segment is created) and for new events.
func TestEventPropertyFilters(t *testing.T) {
	c, _ := workspace(t, "Filters")
	run := fmt.Sprint(time.Now().UnixNano())
	big, small := "big-"+run, "small-"+run
	buy := func(ext string, amount float64) map[string]any {
		return map[string]any{"external_id": ext, "name": "purchase", "props": map[string]any{"amount": amount, "sku": "A1"}}
	}
	// History before the segment exists: one big purchase each... and small ones for the other.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{buy(big, 1200), buy(small, 90), buy(small, 80)}}, 202)
	time.Sleep(2 * time.Second) // into ClickHouse

	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Two big orders", "rule": map[string]any{
		"event": "purchase", "op": "gte", "count": 2, "days": 30, "where": []any{map[string]any{"prop": "amount", "op": "gte", "value": 500}},
	}}, 201)["id"])
	waitFor(t, 15*time.Second, func() bool {
		s := jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%d", seg), nil, 200)
		return s["status"] == "ready"
	})
	time.Sleep(2500 * time.Millisecond) // the streaming index picks the filter up

	// One more big order: 2 big in total, so in. Small orders never count.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{buy(big, 700), buy(small, 100), buy(small, 120)}}, 202)
	took := waitFor(t, 10*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, big), seg) })
	t.Logf("filtered count (1 from history + 1 live) entered in %v", took)
	time.Sleep(time.Second)
	if slices.Contains(segmentsOf(t, c, small), seg) {
		t.Error("four small orders must not count as big ones")
	}
	preview := jsonCall(t, c, "POST", "/v1/segments/preview", map[string]any{"rule": map[string]any{
		"event": "purchase", "op": "gte", "count": 2, "days": 30, "where": []any{map[string]any{"prop": "amount", "op": "gte", "value": 500}},
	}}, 200)
	if preview["count"] != 1.0 {
		t.Errorf("preview count = %v, want 1", preview["count"])
	}
}

func webEvents(t *testing.T, siteKey string, events ...map[string]any) int {
	b, _ := json.Marshal(map[string]any{"events": events})
	req, _ := http.NewRequest("POST", apiURL+"/v1/web/events", bytes.NewReader(b))
	req.Header.Set("X-Pulse-Site-Key", siteKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestIdentityMergeAndVerification: a visitor browses anonymously, then signs in. Their profile,
// history and counts move to the known contact, later anonymous events follow, and with identity
// verification on, only the user's server-signed hash lets a browser act as them.
func TestIdentityMergeAndVerification(t *testing.T) {
	c, siteKey := workspace(t, "Identity")
	run := fmt.Sprint(time.Now().UnixNano())
	anon, user := "anon-"+run, "user-"+run
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Pricing lookers", "rule": map[string]any{
		"event": "pricing_viewed", "op": "gte", "count": 2, "days": 7}}, 201)["id"])
	time.Sleep(3 * time.Second)

	if code := webEvents(t, siteKey,
		map[string]any{"external_id": anon, "name": "pricing_viewed", "attrs": map[string]any{"utm_source": "ads"}},
		map[string]any{"external_id": anon, "name": "pricing_viewed"}); code != 202 {
		t.Fatalf("anonymous events: %d", code)
	}
	waitFor(t, 10*time.Second, func() bool { return slices.Contains(segmentsOf(t, c, anon), seg) })

	if code := webEvents(t, siteKey, map[string]any{"external_id": user, "name": "identify",
		"attrs": map[string]any{"name": "Asha"}, "props": map[string]any{"anonymous_id": anon}}); code != 202 {
		t.Fatalf("identify: %d", code)
	}
	took := waitFor(t, 10*time.Second, func() bool {
		return slices.Contains(segmentsOf(t, c, user), seg) && !slices.Contains(segmentsOf(t, c, anon), seg)
	})
	t.Logf("merged visitor's counts made the known contact a member in %v", took)
	a := attrsOf(t, c, user)
	if a["utm_source"] != "ads" || a["name"] != "Asha" {
		t.Errorf("known profile = %v: want the visitor's utm_source and the new name", a)
	}
	if attrsOf(t, c, anon)["merged_into"] != user {
		t.Error("the visitor should point at the known contact")
	}
	waitFor(t, 10*time.Second, func() bool {
		evs := contactEvents(t, c, user)
		n := 0
		for _, e := range evs {
			if e == "pricing_viewed" {
				n++
			}
		}
		return n == 2 && slices.Contains(evs, "contact_merged")
	})

	// A browser can't send platform events.
	if code := webEvents(t, siteKey, map[string]any{"external_id": user, "name": "points_changed", "props": map[string]any{"delta": 1000}}); code != 400 {
		t.Errorf("points from a browser: %d, want 400", code)
	}

	// Identity verification.
	id := jsonCall(t, c, "PUT", "/v1/settings/identity", map[string]any{"required": true}, 200)
	secret := id["secret"].(string)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(user))
	hash := hex.EncodeToString(m.Sum(nil))
	if code := webEvents(t, siteKey, map[string]any{"external_id": user, "name": "page_view"}); code != 403 {
		t.Errorf("unsigned event for a user: %d, want 403", code)
	}
	if code := webEvents(t, siteKey, map[string]any{"external_id": user, "name": "page_view", "props": map[string]any{"user_hash": strings.ToUpper(hash[:10]) + hash[10:]}}); code != 202 {
		t.Errorf("signed event: %d, want 202", code)
	}
	if code := webEvents(t, siteKey, map[string]any{"external_id": "other-" + run, "name": "page_view", "props": map[string]any{"user_hash": hash}}); code != 403 {
		t.Errorf("another user's hash: %d, want 403", code)
	}
	if code := webEvents(t, siteKey, map[string]any{"external_id": "anon-x" + run, "name": "page_view"}); code != 202 {
		t.Errorf("anonymous visitors need no hash: %d", code)
	}
	jsonCall(t, c, "PUT", "/v1/settings/identity", map[string]any{"required": false}, 200)
}

// TestJourneyAudienceSteps: tag, points and list steps change the contact like the API does.
func TestJourneyAudienceSteps(t *testing.T) {
	c, _ := workspace(t, "AudSteps")
	list := idOf(jsonCall(t, c, "POST", "/v1/lists", map[string]any{"name": "Onboarded"}, 201)["id"])
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "onboard", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "signup"}, "entry": "t",
		"nodes": map[string]any{
			"t": map[string]any{"type": "update_tags", "add_tags": []string{"onboarded"}, "next": "p"},
			"p": map[string]any{"type": "adjust_points", "points": 25, "next": "l"},
			"l": map[string]any{"type": "update_list", "list_id": list, "list_action": "add", "next": "x"},
			"x": map[string]any{"type": "exit"},
		},
	}}, 201)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j["id"]), nil, 200)
	time.Sleep(3 * time.Second)
	ext := fmt.Sprint("js-", time.Now().UnixNano())
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "signup"}, 202)
	waitFor(t, 10*time.Second, func() bool {
		a := attrsOf(t, c, ext)
		return slices.Contains(segmentsOf(t, c, ext), list) && a["points"] == 25.0 && fmt.Sprint(a["tags"]) == "[onboarded]"
	})
}
