// Command seed fills a local Pulse with a demo workspace: sandbox providers on every channel,
// templates, segments, journeys (including WhatsApp with SMS fallback), on-site content and a few
// hundred contacts' activity. Local development only; credentials are in deploy/dev-seed.md.
//
//	go run ./cmd/seed -api http://localhost:7080
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"os"
	"time"
)

var (
	apiURL   = flag.String("api", "http://localhost:7080", "Pulse base URL")
	email    = flag.String("email", "demo.admin@pulse.test", "admin email for the demo workspace")
	password = flag.String("password", "pulse-demo-pass-1", "admin password for the demo workspace")
	contacts = flag.Int("contacts", 300, "contacts to create")
)

var client *http.Client

func call(method, path string, body any) map[string]any {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *apiURL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pulse-CSRF", "1")
	resp, err := client.Do(req)
	if err != nil {
		fail("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fail("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}

func id(m map[string]any) float64 { return m["id"].(float64) }

func main() {
	flag.Parse()
	jar, _ := cookiejar.New(nil)
	client = &http.Client{Jar: jar, Timeout: 30 * time.Second}

	ws := call("POST", "/v1/auth/signup", map[string]any{"workspace": "Demo Retail", "name": "Demo Admin", "email": *email, "password": *password})
	slug := ws["workspace"].(map[string]any)["slug"]
	fmt.Println("workspace:", slug)

	// Sandbox providers. On WhatsApp and RCS, numbers ending in 0000 are "not on the channel", so
	// those contacts exercise the SMS fallback; SMS itself reaches everyone.
	for _, ch := range []string{"sms", "whatsapp", "rcs", "push"} {
		cfg := map[string]string{}
		if ch == "whatsapp" || ch == "rcs" {
			cfg["unreachable_suffix"] = "0000"
		}
		call("POST", "/v1/channels/providers", map[string]any{"channel": ch, "provider": "sandbox_" + ch, "name": "Sandbox",
			"config": cfg, "is_default": true})
	}

	welcome := id(call("POST", "/v1/templates", map[string]any{"name": "Welcome email", "channel": "email",
		"subject": "Welcome to Demo Retail, {{.Attrs.first_name}}",
		"html":    `<div style="font-family:Arial;padding:24px"><h1>Hi {{.Attrs.first_name}},</h1><p>Thanks for joining. Here is 10% off your first order: <b>HELLO10</b>.</p></div>`}))
	sms := id(call("POST", "/v1/templates", map[string]any{"name": "Cart reminder (SMS)", "channel": "sms",
		"content": map[string]any{"text": "Hi {{.Attrs.first_name}}, your cart is waiting. Complete your order: https://demo.shop/cart", "sender_id": "DEMORT"}}))
	wa := id(call("POST", "/v1/templates", map[string]any{"name": "Cart reminder (WhatsApp)", "channel": "whatsapp",
		"content": map[string]any{"template_name": "cart_reminder", "language": "en", "body_params": []string{"{{.Attrs.first_name}}"},
			"preview_text": "Hi {{1}}, you left something in your cart. Tap below to finish your order."}}))
	call("POST", "/v1/templates", map[string]any{"name": "Festive sale (RCS)", "channel": "rcs",
		"content": map[string]any{"card": map[string]any{"title": "Festive sale is live", "description": "Up to 40% off for {{.Attrs.city}}", "image_url": "https://picsum.photos/600/300"},
			"suggestions": []map[string]string{{"type": "url", "text": "Shop now", "value": "https://demo.shop/sale"}, {"type": "reply", "text": "Not now"}}}})
	call("POST", "/v1/templates", map[string]any{"name": "Back in stock (push)", "channel": "push",
		"content": map[string]any{"title": "Back in stock", "body": "{{.Attrs.first_name}}, the item you liked is available again.", "url": "https://demo.shop/p/42"}})

	seg := func(name string, rule string) float64 {
		return id(call("POST", "/v1/segments", map[string]any{"name": name, "rule": json.RawMessage(rule)}))
	}
	newSignups := seg("New sign-ups", `{"event":"signup","op":"gte","count":1,"days":7}`)
	carts := seg("Abandoned cart", `{"all":[{"event":"add_to_cart","op":"gte","count":1,"days":3},{"event":"purchase","op":"eq","count":0,"days":3}]}`)
	puneBuyers := seg("Repeat buyers in Pune", `{"all":[{"attr":"city","op":"eq","value":"Pune"},{"event":"purchase","op":"gte","count":2,"days":30}]}`)
	seg("Pro plan", `{"attr":"plan","op":"in","value":["pro","team"]}`)

	journey := func(name string, graph map[string]any) {
		j := call("POST", "/v1/journeys", map[string]any{"name": name, "graph": graph})
		call("POST", fmt.Sprintf("/v1/journeys/%v/activate", id(j)), nil)
	}
	journey("Welcome", map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": newSignups},
		"entry":   "email1",
		"nodes": map[string]any{
			"email1": map[string]any{"type": "send_email", "template_id": welcome, "next": "exit", "x": 300, "y": 140},
			"exit":   map[string]any{"type": "exit", "x": 300, "y": 280},
		},
	})
	journey("Cart recovery", map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": carts},
		"entry":   "wait1",
		"nodes": map[string]any{
			"wait1":     map[string]any{"type": "wait", "seconds": 5, "next": "check1", "x": 300, "y": 140},
			"check1":    map[string]any{"type": "condition", "segment_id": carts, "yes": "whatsapp1", "no": "exit", "x": 300, "y": 260},
			"whatsapp1": map[string]any{"type": "send_whatsapp", "template_id": wa, "fallback_template_id": sms, "next": "exit", "x": 160, "y": 400},
			"exit":      map[string]any{"type": "exit", "x": 420, "y": 520},
		},
	})

	call("POST", "/v1/focus-items", map[string]any{"name": "Exit offer for new visitors", "kind": "modal", "status": "active",
		"content":   map[string]any{"title": "Before you go: 10% off", "body_html": "<p>Use code <b>HELLO10</b> on your first order.</p>", "cta_text": "Shop now", "cta_url": "https://demo.shop", "background": "#ffffff", "text_color": "#152238", "button_color": "#2747d6"},
		"targeting": map[string]any{"exclude_segment_ids": []float64{puneBuyers}, "trigger": "exit_intent", "frequency": "once"}})
	call("POST", "/v1/dwc", map[string]any{"slot": "home-hero", "name": "Homepage hero", "status": "active",
		"default_html": "<h2>New season, new arrivals</h2>",
		"variants":     []map[string]any{{"segment_id": puneBuyers, "html": "<h2>Welcome back! Free delivery in Pune this week.</h2>"}}})

	time.Sleep(3 * time.Second) // let workers pick up the new segments and journeys

	rng := rand.New(rand.NewSource(7))
	cities := []string{"Pune", "Mumbai", "Delhi", "Bengaluru", "Chennai"}
	names := []string{"Asha", "Ravi", "Meera", "Arjun", "Kavya", "Rohan", "Isha", "Vikram", "Neha", "Kabir"}
	var evs []map[string]any
	flush := func() {
		if len(evs) > 0 {
			call("POST", "/v1/events", map[string]any{"events": evs})
			evs = nil
		}
	}
	for i := 1; i <= *contacts; i++ {
		ext := fmt.Sprintf("cust-%04d", i)
		phone := fmt.Sprintf("+9198%08d", rng.Intn(100000000))
		if i%10 == 0 {
			phone = phone[:len(phone)-4] + "0000" // not on WhatsApp: exercises the SMS fallback
		}
		evs = append(evs, map[string]any{"external_id": ext, "name": "identify", "email": ext + "@demo.test",
			"attrs": map[string]any{"first_name": names[rng.Intn(len(names))], "city": cities[rng.Intn(len(cities))],
				"phone": phone, "plan": []string{"free", "pro", "team"}[rng.Intn(3)]}})
		if rng.Intn(3) == 0 {
			evs = append(evs, map[string]any{"external_id": ext, "name": "signup"})
		}
		for k := rng.Intn(6); k > 0; k-- {
			evs = append(evs, map[string]any{"external_id": ext, "name": "page_view", "props": map[string]any{"path": "/p/" + fmt.Sprint(rng.Intn(90))}})
		}
		if rng.Intn(4) == 0 {
			evs = append(evs, map[string]any{"external_id": ext, "name": "add_to_cart"})
		}
		for k := rng.Intn(3); k > 0; k-- {
			evs = append(evs, map[string]any{"external_id": ext, "name": "purchase", "props": map[string]any{"amount": 199 + rng.Intn(3000)}})
		}
		if len(evs) > 400 {
			flush()
		}
	}
	flush()
	fmt.Printf("seeded %d contacts. Sign in at %s/login with workspace %v (credentials in deploy/dev-seed.md)\n", *contacts, *apiURL, slug)
}
