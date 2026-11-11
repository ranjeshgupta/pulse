//go:build e2e

package e2e

// M1, events and segments: event schemas (warn and enforce), property paths and absolute date
// ranges in segments, "none of" groups, list and choice contact fields, phone_country, journey
// trigger property filters, daily-refresh segments and "why is this contact in this segment".

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func inSeg(t *testing.T, c *http.Client, ext string, seg int64) bool {
	return slices.Contains(segmentsOf(t, c, ext), seg)
}

func day(daysAgo int) string { return time.Now().UTC().AddDate(0, 0, -daysAgo).Format(time.DateOnly) }

func TestEventSchemaWarnAndEnforce(t *testing.T) {
	c, _ := workspace(t, "Schemas")
	run := fmt.Sprint(time.Now().UnixNano())
	added := jsonCall(t, c, "POST", "/v1/events/schemas/suggested", map[string]any{"names": []string{"signup", "purchase"}}, 200)
	if fmt.Sprint(added["added"]) != "[purchase signup]" {
		t.Fatalf("added %v", added)
	}
	time.Sleep(1500 * time.Millisecond) // other roles reload schemas within a second of the bump

	// Warn: a sign-up with method social but no provider is kept and counted as a problem.
	bad := map[string]any{"external_id": "s-" + run, "name": "signup", "props": map[string]any{"method": "social"}}
	jsonCall(t, c, "POST", "/v1/events", bad, 202)
	waitFor(t, 10*time.Second, func() bool {
		sum := jsonCall(t, c, "GET", "/v1/events/signup/summary", nil, 200)
		checks, _ := sum["checks"].(map[string]any)
		return checks != nil && checks["bad"] == 1.0
	})
	sum := jsonCall(t, c, "GET", "/v1/events/signup/summary", nil, 200)
	if !strings.Contains(fmt.Sprint(sum["checks"]), "social_provider is required") {
		t.Errorf("no sample with the problem: %v", sum["checks"])
	}

	// Enforce needs the typed confirmation; then the server API refuses events that don't fit.
	schema := jsonCall(t, c, "GET", "/v1/events/signup/schema", nil, 200)
	schema["mode"] = "enforce"
	jsonCall(t, c, "PUT", "/v1/events/signup/schema", schema, 400)
	schema["confirm"] = "ENFORCE"
	jsonCall(t, c, "PUT", "/v1/events/signup/schema", schema, 200)
	out := jsonCall(t, c, "POST", "/v1/events", bad, 400)
	if !strings.Contains(fmt.Sprint(out["error"]), "social_provider is required") {
		t.Errorf("refusal: %v", out)
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "s-" + run, "name": "signup",
		"props": map[string]any{"method": "social", "social_provider": "google"}}, 202)

	// Purchases add their product codes to the contact's "products" list field.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "s-" + run, "name": "purchase", "props": map[string]any{
		"order_id": "o1", "value": 499, "product_code": "KIT-1", "items": []any{map[string]any{"product_code": "BED-2"}}}}, 202)
	waitFor(t, 10*time.Second, func() bool {
		return fmt.Sprint(attrsOf(t, c, "s-"+run)["products"]) == "[KIT-1 BED-2]"
	})
	cat := jsonCall(t, c, "GET", "/v1/events/catalog", nil, 200)
	if !strings.Contains(fmt.Sprint(cat["events"]), "mode:enforce") {
		t.Errorf("catalog: %v", cat)
	}
}

func TestPropertyPathsRangesAndNoneOf(t *testing.T) {
	c, _ := workspace(t, "Paths")
	run := fmt.Sprint(time.Now().UnixNano())
	a, b, old := "a-"+run, "b-"+run, "o-"+run
	// History first: a purchase 400 days ago, for a range only the batch path can read.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": old, "name": "identify", "attrs": map[string]any{"city": "Pune"}},
		map[string]any{"external_id": old, "name": "purchase", "ts": day(400) + "T10:00:00Z", "props": map[string]any{"items": []any{map[string]any{"code": "A1"}}}},
	}}, 202)
	time.Sleep(3 * time.Second) // into ClickHouse before the backfill reads it

	byItem := newSegment(t, c, "Bought A1 last month", map[string]any{"event": "purchase", "op": "gte", "count": 1,
		"from": day(45), "to": day(30), "where": []any{map[string]any{"prop": "items[].code", "op": "eq", "value": "A1"}}})
	nested := newSegment(t, c, "Expensive products", map[string]any{"event": "view", "op": "gte", "count": 1, "days": 7,
		"where": []any{map[string]any{"prop": "product.price", "op": "gt", "value": 1000}}})
	longAgo := newSegment(t, c, "Bought A1 over a year ago", map[string]any{"event": "purchase", "op": "gte", "count": 1,
		"from": day(420), "to": day(380), "where": []any{map[string]any{"prop": "items[].code", "op": "eq", "value": "A1"}}})
	notVIP := newSegment(t, c, "Pune, not VIP", map[string]any{"all": []any{
		map[string]any{"attr": "city", "op": "eq", "value": "Pune"},
		map[string]any{"none": []any{map[string]any{"tag": "vip", "op": "has"}, map[string]any{"attr": "plan", "op": "eq", "value": "free"}}},
	}})
	if !inSeg(t, c, old, longAgo) {
		t.Errorf("a range over a year back is filled by the backfill")
	}

	// Streaming: an event dated inside the range enters at once; one outside doesn't.
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": a, "name": "identify", "attrs": map[string]any{"city": "Pune", "plan": "pro"}},
		map[string]any{"external_id": a, "name": "purchase", "ts": day(40) + "T09:00:00Z", "props": map[string]any{"items": []any{map[string]any{"code": "B2"}, map[string]any{"code": "A1"}}}},
		map[string]any{"external_id": a, "name": "view", "props": map[string]any{"product": map[string]any{"code": "LAMP", "price": 3299}}},
		map[string]any{"external_id": b, "name": "identify", "attrs": map[string]any{"city": "Pune", "plan": "free"}},
		map[string]any{"external_id": b, "name": "purchase", "ts": day(10) + "T09:00:00Z", "props": map[string]any{"items": []any{map[string]any{"code": "A1"}}}},
		map[string]any{"external_id": b, "name": "view", "props": map[string]any{"product": map[string]any{"price": 300}}},
	}}, 202)
	waitFor(t, 10*time.Second, func() bool {
		return inSeg(t, c, a, byItem) && inSeg(t, c, a, nested) && inSeg(t, c, a, notVIP)
	})
	if segs := segmentsOf(t, c, b); slices.Contains(segs, byItem) || slices.Contains(segs, nested) || slices.Contains(segs, notVIP) {
		t.Errorf("b matched: %v", segs)
	}

	// The none-of group: a VIP tag takes a out.
	jsonCall(t, c, "POST", "/v1/contacts/"+a+"/tags", map[string]any{"add": []string{"vip"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return !inSeg(t, c, a, notVIP) })

	// Why: the explanation names each filter's result.
	ex := jsonCall(t, c, "GET", fmt.Sprintf("/v1/contacts/%s/segments/%d/explain", a, notVIP), nil, 200)
	if ex["member"] != false || !strings.Contains(fmt.Sprint(ex["explanation"]), "kind:none") {
		t.Errorf("explain: %v", ex)
	}
	ex = jsonCall(t, c, "GET", fmt.Sprintf("/v1/contacts/%s/segments/%d/explain", old, longAgo), nil, 200)
	if !strings.Contains(fmt.Sprint(ex["explanation"]), "sweep:true") {
		t.Errorf("explain an old range: %v", ex)
	}
	// Date between on an attribute.
	n := segmentPreview(t, c, map[string]any{"attr": "date_added", "op": "between", "value": []any{day(1), day(-1)}})
	if n < 3 {
		t.Errorf("date_added between yesterday and tomorrow: %v", n)
	}
}

func TestListEnumFieldsAndPhoneCountry(t *testing.T) {
	c, _ := workspace(t, "Fields")
	run := fmt.Sprint(time.Now().UnixNano())
	jsonCall(t, c, "PUT", "/v1/attributes/products", map[string]any{"label": "Products", "type": "list"}, 200)
	jsonCall(t, c, "PUT", "/v1/attributes/tier", map[string]any{"label": "Tier", "type": "enum",
		"options": []any{map[string]any{"value": "gold"}, map[string]any{"value": "silver"}}}, 200)
	jsonCall(t, c, "PUT", "/v1/attributes/bad", map[string]any{"type": "enum"}, 400)
	has := newSegment(t, c, "Has KIT-1", map[string]any{"attr": "products", "op": "has", "value": "KIT-1"})
	gold := newSegment(t, c, "Gold", map[string]any{"attr": "tier", "op": "eq", "value": "gold"})
	uk := newSegment(t, c, "UK phones", map[string]any{"attr": "phone_country", "op": "eq", "value": "GB"})
	notIN := newSegment(t, c, "Not Indian phones", map[string]any{"attr": "phone_country", "op": "ne", "value": "IN"})
	u := "f-" + run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": u, "name": "identify",
		"attrs": map[string]any{"products": []string{"KIT-1", "BED-2"}, "tier": "gold", "phone": "+44 7700 900123", "phone_country": "IN"}}, 202)
	waitFor(t, 10*time.Second, func() bool {
		return inSeg(t, c, u, has) && inSeg(t, c, u, gold) && inSeg(t, c, u, uk) && inSeg(t, c, u, notIN)
	})
	if a := attrsOf(t, c, u); a["phone_country"] != "GB" {
		t.Errorf("phone_country %v", a["phone_country"])
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": u, "name": "identify", "attrs": map[string]any{"products": []string{"BED-2"}, "phone": "+919812345678"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return !inSeg(t, c, u, has) && !inSeg(t, c, u, uk) && !inSeg(t, c, u, notIN) })
}

func TestJourneyTriggerPropertyFilters(t *testing.T) {
	c, _ := workspace(t, "Triggers")
	run := fmt.Sprint(time.Now().UnixNano())
	j := jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "UPI drop-offs", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "checkout_abandoned", "reentry": "always",
			"where": []any{map[string]any{"prop": "payment_method", "op": "eq", "value": "upi"}, map[string]any{"prop": "cart_value", "op": "gte", "value": 500}}},
		"entry": "tag",
		"nodes": map[string]any{"tag": map[string]any{"type": "update_tags", "add_tags": []string{"upi_dropoff"}, "next": "done"}, "done": map[string]any{"type": "exit"}},
	}}, 201)
	jsonCall(t, c, "POST", "/v1/journeys", map[string]any{"name": "bad", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "x", "where": []any{map[string]any{"prop": "a..b", "op": "exists"}}},
		"entry":   "e", "nodes": map[string]any{"e": map[string]any{"type": "exit"}}}}, 400)
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/journeys/%v/activate", j["id"]), nil, 200)
	time.Sleep(3 * time.Second)
	upi, card, small := "upi-"+run, "card-"+run, "small-"+run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": upi, "name": "checkout_abandoned", "props": map[string]any{"payment_method": "upi", "cart_value": 1899}},
		map[string]any{"external_id": card, "name": "checkout_abandoned", "props": map[string]any{"payment_method": "card", "cart_value": 1899}},
		map[string]any{"external_id": small, "name": "checkout_abandoned", "props": map[string]any{"payment_method": "upi", "cart_value": 99}},
	}}, 202)
	waitFor(t, 10*time.Second, func() bool { return strings.Contains(fmt.Sprint(attrsOf(t, c, upi)["tags"]), "upi_dropoff") })
	time.Sleep(time.Second)
	for _, ext := range []string{card, small} {
		if strings.Contains(fmt.Sprint(attrsOf(t, c, ext)["tags"]), "upi_dropoff") {
			t.Errorf("%s entered the journey", ext)
		}
	}
}

// TestDailyRefreshSegment: a daily segment is built once when saved, then changes only at its time.
func TestDailyRefreshSegment(t *testing.T) {
	c, _ := workspace(t, "Daily")
	run := fmt.Sprint(time.Now().UnixNano())
	early, late := "early-"+run, "late-"+run
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": early, "name": "identify", "attrs": map[string]any{"plan": "pro"}}, 202)
	time.Sleep(3 * time.Second)
	// The refresh time is the next whole minute at least 20 s away, in Kolkata time.
	loc, _ := time.LoadLocation("Asia/Kolkata")
	at := time.Now().Add(20 * time.Second).Truncate(time.Minute).Add(time.Minute)
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "Pro (daily)", "rule": map[string]any{"attr": "plan", "op": "eq", "value": "pro"},
		"refresh": map[string]any{"mode": "daily", "at": at.In(loc).Format("15:04"), "tz": "Asia/Kolkata"}}, 201)["id"])
	waitFor(t, 15*time.Second, func() bool { return inSeg(t, c, early, seg) })
	got := jsonCall(t, c, "GET", fmt.Sprintf("/v1/segments/%d", seg), nil, 200)
	if r, _ := got["refresh"].(map[string]any); r["mode"] != "daily" {
		t.Errorf("refresh %v", got["refresh"])
	}

	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": late, "name": "identify", "attrs": map[string]any{"plan": "pro"}}, 202)
	time.Sleep(4 * time.Second)
	if inSeg(t, c, late, seg) {
		t.Fatalf("a daily segment changed before its refresh time")
	}
	ex := jsonCall(t, c, "GET", "/v1/contacts/"+url.PathEscape(late)+fmt.Sprintf("/segments/%d/explain", seg), nil, 200)
	if ex["member"] != false || ex["explanation"].(map[string]any)["match"] != true {
		t.Errorf("explain before the refresh: %v", ex)
	}
	took := waitFor(t, time.Until(at)+40*time.Second, func() bool { return inSeg(t, c, late, seg) })
	if time.Now().Before(at) {
		t.Errorf("joined %v before the refresh time", took)
	}
}
