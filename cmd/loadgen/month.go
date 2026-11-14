package main

// The "month" scenario: a workspace with a realistic contact base and one month of website traffic,
// then timings of what people do in the app at that size.
//
//	go run ./cmd/loadgen -scenario month -contacts 5000000 -visitors 2000000 -pageviews 10000000 -events-per-page 2
//
// 1. Contacts: one identify per contact (email for all, a phone for 60%, city, plan, signup date).
// 2. Traffic: -visitors distinct visitors in the last -days days (80% known contacts, the rest new
//    anonymous visitors), -pageviews page views spread over them and over the days, each with
//    -events-per-page events in all: the page_view and product views, carts, searches, sign-ups and
//    purchases (with value and product). Timestamps are spread over the month, so dashboards,
//    filters and segments see a real month.
// 3. Timings of the app's own requests: Overview, Contacts list and filters, segment preview and a
//    new segment's backfill, the Users and Products dashboards, a funnel, the event catalog.
//
// Segments that react to the traffic are created before it, so their streaming work is part of
// the load; the probe measures event-to-segment latency throughout.

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	scenario      = flag.String("scenario", "rate", "rate (events at a fixed rate, the default) or month (contacts + a month of traffic + app timings)")
	visitors      = flag.Int("visitors", 2_000_000, "month: distinct visitors in the month")
	pageviews     = flag.Int("pageviews", 10_000_000, "month: page views in the month")
	eventsPerPage = flag.Float64("events-per-page", 2, "month: events per page view, the page_view included")
	days          = flag.Int("days", 30, "month: days the traffic is spread over (ending now)")
	skipLoad      = flag.String("measure-only", "", "month: an API key of a workspace loaded before: only take the timings")
)

func runMonth() {
	run := fmt.Sprint(time.Now().Unix())
	key := *skipLoad
	if key == "" {
		key = monthSetup(run)
		probeRun, stop, done := run, make(chan struct{}), make(chan []time.Duration)
		go func() { done <- probe(probeRun, key, stop) }()

		start := time.Now()
		n := sendAll(key, "contacts", *contacts, func(rng *rand.Rand, i int, out []map[string]any) []map[string]any {
			return append(out, contactEvent(rng, run, i))
		})
		fmt.Printf("contacts   %d identifies in %v = %.0f ev/s\n\n", n, time.Since(start).Round(time.Second), float64(n)/time.Since(start).Seconds())

		start = time.Now()
		total := int(float64(*pageviews) * *eventsPerPage)
		known := int(float64(*visitors) * 0.8)
		n = sendAll(key, "traffic", total, func(rng *rand.Rand, i int, out []map[string]any) []map[string]any {
			return append(out, trafficEvent(rng, run, i, known))
		})
		fmt.Printf("traffic    %d events in %v = %.0f ev/s\n", n, time.Since(start).Round(time.Second), float64(n)/time.Since(start).Seconds())
		time.Sleep(5 * time.Second)
		close(stop)
		fmt.Printf("end-to-end event -> segment change during the load: %s\n\n", percentiles(<-done))
		fmt.Printf("api key    %s (for -measure-only)\n\n", key)
		waitDrained(key, int64(*contacts)+int64(n))
	}
	measure(key)
}

// monthSetup creates the workspace and segments a shop would have; returns its API key.
func monthSetup(run string) string {
	_, body := post("/v1/tenants", *admin, map[string]any{"name": "month load " + run})
	var t struct {
		APIKey string `json:"api_key"`
	}
	must(json.Unmarshal(body, &t))
	if t.APIKey == "" {
		fmt.Fprintf(os.Stderr, "create tenant failed: %s\n", body)
		os.Exit(1)
	}
	for i, r := range []string{
		`{"attr":"city","op":"eq","value":"Delhi"}`,
		`{"event":"purchase","op":"gte","count":1,"days":30}`,
		`{"event":"product_viewed","op":"gte","count":5,"days":7}`,
		`{"all":[{"event":"add_to_cart","op":"gte","count":1,"days":7},{"event":"purchase","op":"eq","count":0,"days":7}]}`,
		`{"all":[{"attr":"plan","op":"eq","value":"pro"},{"event":"page_view","op":"eq","count":0,"days":30}]}`,
	} {
		code, b := post("/v1/segments", t.APIKey, map[string]any{"name": fmt.Sprint("month ", i), "rule": json.RawMessage(r)})
		if code >= 300 {
			fmt.Fprintf(os.Stderr, "segment %d: %d %s\n", i, code, b)
			os.Exit(1)
		}
	}
	_, b := post("/v1/segments", t.APIKey, map[string]any{"name": "probe", "rule": json.RawMessage(`{"attr":"probe","op":"eq","value":"on"}`)})
	var s struct{ ID float64 }
	must(json.Unmarshal(b, &s))
	probeSegment = s.ID
	time.Sleep(3 * time.Second)
	return t.APIKey
}

var monthCities = []string{"Delhi", "Mumbai", "Pune", "Bengaluru", "Chennai", "Kolkata", "Hyderabad", "Jaipur"}

func contactEvent(rng *rand.Rand, run string, i int) map[string]any {
	ext := fmt.Sprintf("m%s-%d", run, i)
	attrs := map[string]any{
		"first_name":  []string{"Asha", "Ravi", "Meera", "Arjun", "Priya", "Kiran"}[rng.Intn(6)],
		"city":        monthCities[rng.Intn(len(monthCities))],
		"plan":        []string{"free", "free", "free", "pro"}[rng.Intn(4)],
		"signup_date": time.Now().AddDate(0, 0, -rng.Intn(720)).Format(time.DateOnly),
	}
	if rng.Intn(10) < 6 {
		attrs["phone"] = fmt.Sprintf("+9198%08d", i%100_000_000)
	}
	return map[string]any{"external_id": ext, "name": "identify", "email": ext + "@month.load.test", "attrs": attrs}
}

// trafficEvent is the i-th event of the month: a page_view, or one of the events that come with
// page views. Visitors 0..known-1 are contacts, the rest anonymous visitors new this month.
func trafficEvent(rng *rand.Rand, run string, i, known int) map[string]any {
	v := rng.Intn(*visitors)
	var ext string
	if v < known {
		ext = fmt.Sprintf("m%s-%d", run, v*(*contacts/max(known, 1))%*contacts) // spread over the contact base
	} else {
		ext = fmt.Sprintf("anon-m%s-%d", run, v)
	}
	// Within the app's "last N days" (today and the N-1 whole UTC days before it), so every event counts.
	now := time.Now().UTC()
	window := time.Duration(*days-1)*24*time.Hour + now.Sub(now.Truncate(24*time.Hour))
	ts := now.Add(-time.Duration(rng.Int63n(int64(window)))).Format(time.RFC3339)
	product := fmt.Sprintf("SKU-%03d", rng.Intn(400))
	// Of every eventsPerPage events, one is the page_view.
	if rng.Float64() < 1 / *eventsPerPage {
		return map[string]any{"external_id": ext, "name": "page_view", "ts": ts,
			"props": map[string]any{"url": fmt.Sprintf("https://shop.example.com/p/%d", rng.Intn(2000)), "platform": "web"}}
	}
	switch r := rng.Intn(100); {
	case r < 60:
		return map[string]any{"external_id": ext, "name": "product_viewed", "ts": ts, "props": map[string]any{"product_code": product, "price": 199 + rng.Intn(4800)}}
	case r < 80:
		return map[string]any{"external_id": ext, "name": "add_to_cart", "ts": ts, "props": map[string]any{"product_code": product, "price": 199 + rng.Intn(4800), "quantity": 1 + rng.Intn(3)}}
	case r < 93:
		return map[string]any{"external_id": ext, "name": "search", "ts": ts, "props": map[string]any{"query": strings.ToLower(monthCities[rng.Intn(len(monthCities))])}}
	case r < 97:
		return map[string]any{"external_id": ext, "name": "purchase", "ts": ts, "props": map[string]any{"order_id": fmt.Sprintf("o%s-%d", run, i),
			"value": 299 + rng.Intn(9700), "currency": "INR", "product_code": product}}
	default:
		return map[string]any{"external_id": ext, "name": "signup", "ts": ts, "props": map[string]any{"method": "otp"}}
	}
}

// sendAll sends n events made by gen as fast as the API takes them, with -workers senders and
// batches of -batch, reporting progress every 10 s.
func sendAll(key, label string, n int, gen func(*rand.Rand, int, []map[string]any) []map[string]any) int64 {
	var sent, failed atomic.Int64
	jobs := make(chan []map[string]any, *workers*2)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for evs := range jobs {
				for try := 0; ; try++ {
					code, b := post("/v1/events", key, map[string]any{"events": evs})
					if code == http.StatusAccepted {
						sent.Add(int64(len(evs)))
						break
					}
					if try == 5 {
						failed.Add(int64(len(evs)))
						fmt.Fprintf(os.Stderr, "batch failed: %d %s\n", code, b)
						break
					}
					time.Sleep(time.Duration(try+1) * 500 * time.Millisecond) // 429 / 503: back off
				}
			}
		}()
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		start, last, lastN := time.Now(), time.Now(), int64(0)
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				s := sent.Load()
				fmt.Printf("  %-8s t=%4.0fs  %9d/%d  %6.0f ev/s  failed %d\n", label, now.Sub(start).Seconds(), s, n, float64(s-lastN)/now.Sub(last).Seconds(), failed.Load())
				last, lastN = now, s
			}
		}
	}()
	rng := rand.New(rand.NewSource(int64(n)))
	evs := make([]map[string]any, 0, *batch)
	for i := 0; i < n; i++ {
		evs = gen(rng, i, evs)
		if len(evs) == *batch {
			jobs <- evs
			evs = make([]map[string]any, 0, *batch)
		}
	}
	if len(evs) > 0 {
		jobs <- evs
	}
	close(jobs)
	wg.Wait()
	close(stop)
	if f := failed.Load(); f > 0 {
		fmt.Printf("  %s: %d events failed\n", label, f)
	}
	return sent.Load()
}

// waitDrained waits until the pipeline has written (nearly) every accepted event to ClickHouse,
// reading the workspace's event count from the Overview's 14-day totals and the catalog.
func waitDrained(key string, want int64) {
	start := time.Now()
	for time.Since(start) < 60*time.Minute {
		_, b := get("/v1/events/catalog", key)
		var c struct {
			Events []struct{ Count int64 } `json:"events"`
		}
		_ = json.Unmarshal(b, &c)
		var got int64
		for _, e := range c.Events {
			got += e.Count
		}
		fmt.Printf("  drain    t=%4.0fs  %d of %d events in ClickHouse (last 30 days)\n", time.Since(start).Seconds(), got, want)
		if float64(got) >= 0.999*float64(want) {
			fmt.Printf("drained    in %v after the last event was accepted\n\n", time.Since(start).Round(time.Second))
			return
		}
		time.Sleep(15 * time.Second)
	}
}

// measure times the app's own requests at this size, three times each (first = cold).
func measure(key string) {
	today := time.Now().UTC().Format(time.DateOnly)
	month := time.Now().UTC().AddDate(0, 0, -29).Format(time.DateOnly)
	q := func(s string) string { return "/v1/contacts?limit=50&q=" + url.QueryEscape(s) }
	fmt.Println("timings (first call, then the best of two more; ms)")
	timed := func(label, method, path string, body any) {
		var ds []time.Duration
		var code int
		var b []byte
		for i := 0; i < 3; i++ {
			start := time.Now()
			if method == "GET" {
				code, b = get(path, key)
			} else {
				code, b = post(path, key, body)
			}
			ds = append(ds, time.Since(start))
		}
		best := min(ds[1], ds[2])
		note := ""
		if code >= 300 {
			note = fmt.Sprintf("  HTTP %d %s", code, strings.TrimSpace(string(b))[:min(120, len(strings.TrimSpace(string(b))))])
		}
		fmt.Printf("  %-58s %7d %7d%s\n", label, ds[0].Milliseconds(), best.Milliseconds(), note)
	}
	timed("Overview: live numbers (GET /v1/stats/overview)", "GET", "/v1/stats/overview?days=14", nil)
	timed("Overview: summary (GET /v1/stats/summary)", "GET", "/v1/stats/summary", nil)
	timed("Contacts: first page", "GET", "/v1/contacts?limit=50", nil)
	timed("Contacts: email contains", "GET", q("email:month.load"), nil)
	timed("Contacts: city attribute (segment)", "GET", q(`segment:"month 0"`), nil)
	timed("Contacts: did purchase in the last 30 days", "GET", q("event:purchase period:"+month+".."+today), nil)
	timed("Contacts: did add_to_cart, never purchase (30 days)", "GET", q("event:add_to_cart no_event:purchase period:"+month+".."+today), nil)
	timed("Contacts: active on the web (30 days)", "GET", q("active:web period:"+month+".."+today), nil)
	timed("Segment preview: Delhi and purchased in 30 days", "POST", "/v1/segments/preview", map[string]any{"rule": json.RawMessage(
		`{"all":[{"attr":"city","op":"eq","value":"Delhi"},{"event":"purchase","op":"gte","count":1,"days":30}]}`)})
	timed("Events: catalog (30 days)", "GET", "/v1/events/catalog", nil)
	timed("Users dashboard (DAU/WAU/MAU, 30 days)", "GET", "/v1/analytics/users?from="+month+"&to="+today, nil)
	timed("Products dashboard (30 days)", "GET", "/v1/analytics/products?from="+month+"&to="+today, nil)
	timed("Funnel: page_view > product_viewed > add_to_cart > purchase", "POST", "/v1/funnels/run", map[string]any{
		"steps":       []map[string]string{{"event": "page_view"}, {"event": "product_viewed"}, {"event": "add_to_cart"}, {"event": "purchase"}},
		"window_days": 7, "from": month + "T00:00:00Z", "to": today + "T23:59:59Z"})

	// A new segment over the whole base: time until its backfill is done.
	start := time.Now()
	code, b := post("/v1/segments", key, map[string]any{"name": "new: bought twice in 30 days", "rule": json.RawMessage(`{"event":"purchase","op":"gte","count":2,"days":30}`)})
	var s struct {
		ID float64 `json:"id"`
	}
	_ = json.Unmarshal(b, &s)
	if code >= 300 || s.ID == 0 {
		fmt.Printf("  new segment: %d %s\n", code, b)
		return
	}
	for time.Since(start) < 30*time.Minute {
		_, b = get(fmt.Sprintf("/v1/segments/%v", s.ID), key)
		var seg struct {
			Status string `json:"status"`
			Size   int64  `json:"size"`
		}
		_ = json.Unmarshal(b, &seg)
		if seg.Status == "ready" {
			fmt.Printf("  %-58s %7d          (%d members)\n", "New segment: backfill over every contact", time.Since(start).Milliseconds(), seg.Size)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	fmt.Println("  new segment: still building after 30 minutes")
}
