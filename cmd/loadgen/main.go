// Command loadgen drives a running Pulse stack at a target event rate and measures what users feel:
// ingest latency, and end-to-end latency from an event to the resulting segment change.
//
// End-to-end latency comes from a probe contact. Once a second it flips an attribute that a
// "probe" segment matches on, then polls the API until membership flips. Probe events share
// partitions with the load, so any backlog anywhere in the pipeline shows up in this number.
//
//	go run ./cmd/loadgen -contacts 100000 -rate 5000 -duration 60s
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	api      = flag.String("api", "http://localhost:7080", "Pulse API base URL")
	admin    = flag.String("admin-token", "dev-admin-token", "admin token for creating the test tenant")
	contacts = flag.Int("contacts", 100_000, "distinct contacts to spread events over")
	rate     = flag.Int("rate", 5000, "target events per second")
	batch    = flag.Int("batch", 200, "events per HTTP request")
	workers  = flag.Int("workers", 32, "concurrent HTTP senders")
	duration = flag.Duration("duration", 60*time.Second, "how long to send")
	journeys = flag.Bool("journey", true, "add a journey that emails a narrow segment, to load the dispatcher too")
)

var client = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 256}}

func main() {
	flag.Parse()
	if *scenario == "month" {
		runMonth()
		return
	}
	run := fmt.Sprint(time.Now().Unix())
	key := setup(run)

	var sent, failed atomic.Int64
	var mu sync.Mutex
	var ingest []time.Duration

	jobs := make(chan []map[string]any, *workers*2)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for evs := range jobs {
				start := time.Now()
				code, _ := post("/v1/events", key, map[string]any{"events": evs})
				d := time.Since(start)
				if code != http.StatusAccepted {
					failed.Add(int64(len(evs)))
					continue
				}
				sent.Add(int64(len(evs)))
				mu.Lock()
				ingest = append(ingest, d)
				mu.Unlock()
			}
		}()
	}

	stopProbe := make(chan struct{})
	probeDone := make(chan []time.Duration)
	go func() { probeDone <- probe(run, key, stopProbe) }()

	fmt.Printf("sending %d events/s over %d contacts for %v (batches of %d, %d workers)\n", *rate, *contacts, *duration, *batch, *workers)
	rng := rand.New(rand.NewSource(1))
	seen := make([]bool, *contacts)
	perTick := float64(*rate) / float64(*batch) / 10 // batches per 100ms tick (fractional carries over)
	owed := 0.0
	tick := time.NewTicker(100 * time.Millisecond)
	report := time.NewTicker(5 * time.Second)
	start, last, lastSent := time.Now(), time.Now(), int64(0)
	dropped := 0
loop:
	for {
		select {
		case <-tick.C:
			if time.Since(start) >= *duration {
				break loop
			}
			owed += perTick
			for ; owed >= 1; owed-- {
				select {
				case jobs <- makeBatch(rng, run, seen):
				default:
					dropped++ // workers can't keep up: the API is the bottleneck
				}
			}
		case <-report.C:
			now, s := time.Now(), sent.Load()
			fmt.Printf("  t=%3.0fs  %6.0f ev/s accepted  (failed %d, batches dropped by loadgen %d)\n",
				now.Sub(start).Seconds(), float64(s-lastSent)/now.Sub(last).Seconds(), failed.Load(), dropped)
			last, lastSent = now, s
		}
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)
	// Keep probing while the pipeline drains, so lag still in the queues is measured too.
	time.Sleep(5 * time.Second)
	close(stopProbe)
	probes := <-probeDone

	fmt.Println()
	fmt.Printf("accepted   %d events in %v = %.0f ev/s (target %d), failed %d\n",
		sent.Load(), elapsed.Round(time.Millisecond), float64(sent.Load())/elapsed.Seconds(), *rate, failed.Load())
	fmt.Printf("ingest     per %d-event request: %s\n", *batch, percentiles(ingest))
	fmt.Printf("end-to-end event -> segment change (%d probes): %s\n", len(probes), percentiles(probes))
}

var cities = []string{"Delhi", "Mumbai", "Pune", "Bengaluru", "Chennai", "Kolkata"}

// makeBatch mixes first-seen contacts (identify with attributes) with behavioral events.
func makeBatch(rng *rand.Rand, run string, seen []bool) []map[string]any {
	evs := make([]map[string]any, 0, *batch)
	for len(evs) < *batch {
		i := rng.Intn(len(seen))
		ext := fmt.Sprintf("lg-%s-%d", run, i)
		if !seen[i] {
			seen[i] = true
			evs = append(evs, map[string]any{"external_id": ext, "name": "identify", "email": ext + "@load.test",
				"attrs": map[string]any{"city": cities[rng.Intn(len(cities))], "plan": []string{"free", "pro"}[rng.Intn(2)]}})
			continue
		}
		name := []string{"view", "view", "view", "login", "purchase"}[rng.Intn(5)]
		evs = append(evs, map[string]any{"external_id": ext, "name": name, "props": map[string]any{"page": rng.Intn(50)}})
	}
	return evs
}

// setup creates a tenant with a realistic set of segments (attribute, event-window, negative and
// combined rules), the probe segment, and optionally a journey.
func setup(run string) string {
	_, body := post("/v1/tenants", *admin, map[string]any{"name": "loadgen " + run})
	var t struct {
		APIKey string `json:"api_key"`
	}
	must(json.Unmarshal(body, &t))
	if t.APIKey == "" {
		fmt.Fprintf(os.Stderr, "create tenant failed: %s\n", body)
		os.Exit(1)
	}
	rules := []string{
		`{"attr":"city","op":"eq","value":"Delhi"}`,
		`{"attr":"plan","op":"eq","value":"pro"}`,
		`{"event":"purchase","op":"gte","count":3,"days":7}`,
		`{"event":"login","op":"eq","count":0,"days":30}`,
		`{"all":[{"attr":"city","op":"in","value":["Mumbai","Pune"]},{"event":"view","op":"gte","count":5,"days":1}]}`,
	}
	var target float64
	for i, r := range rules {
		_, b := post("/v1/segments", t.APIKey, map[string]any{"name": fmt.Sprint("load ", i), "rule": json.RawMessage(r)})
		var s struct{ ID float64 }
		must(json.Unmarshal(b, &s))
		if i == 2 {
			target = s.ID
		}
	}
	_, b := post("/v1/segments", t.APIKey, map[string]any{"name": "probe", "rule": json.RawMessage(`{"attr":"probe","op":"eq","value":"on"}`)})
	var s struct{ ID float64 }
	must(json.Unmarshal(b, &s))
	probeSegment = s.ID

	if *journeys {
		_, b = post("/v1/templates", t.APIKey, map[string]any{"name": "loyal", "subject": "Thanks for buying again", "html": "<p>Thanks</p>"})
		var tpl struct{ ID float64 }
		must(json.Unmarshal(b, &tpl))
		_, b = post("/v1/journeys", t.APIKey, map[string]any{"name": "repeat buyers", "graph": map[string]any{
			"trigger": map[string]any{"type": "segment_entered", "segment_id": target},
			"entry":   "send",
			"nodes": map[string]any{
				"send": map[string]any{"type": "send_email", "template_id": tpl.ID, "next": "done"},
				"done": map[string]any{"type": "exit"},
			},
		}})
		var j struct{ ID float64 }
		must(json.Unmarshal(b, &j))
		post(fmt.Sprintf("/v1/journeys/%v/activate", j.ID), t.APIKey, nil)
	}
	time.Sleep(3 * time.Second) // workers refresh their segment/journey indexes every 2s
	return t.APIKey
}

var probeSegment float64

func probe(run, key string, stop chan struct{}) []time.Duration {
	ext := "probe-" + run
	var out []time.Duration
	on := false
	for {
		select {
		case <-stop:
			return out
		case <-time.After(time.Second):
		}
		on = !on
		val := map[bool]string{true: "on", false: "off"}[on]
		start := time.Now()
		if code, _ := post("/v1/events", key, map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"probe": val}}); code != http.StatusAccepted {
			continue
		}
		for time.Since(start) < 2*time.Minute {
			_, b := get("/v1/contacts/"+ext, key)
			var c struct{ Segments []float64 }
			_ = json.Unmarshal(b, &c)
			if slices.Contains(c.Segments, probeSegment) == on {
				out = append(out, time.Since(start))
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func percentiles(ds []time.Duration) string {
	if len(ds) == 0 {
		return "no samples"
	}
	s := slices.Clone(ds)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	p := func(q float64) time.Duration { return s[min(len(s)-1, int(q*float64(len(s))))].Round(time.Millisecond) }
	return fmt.Sprintf("p50 %v  p95 %v  p99 %v  max %v", p(0.50), p(0.95), p(0.99), s[len(s)-1].Round(time.Millisecond))
}

func post(path, key string, body any) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest("POST", *api+path, rd)
	return do(req, key)
}

func get(path, key string) (int, []byte) {
	req, _ := http.NewRequest("GET", *api+path, nil)
	return do(req, key)
}

func do(req *http.Request, key string) (int, []byte) {
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
