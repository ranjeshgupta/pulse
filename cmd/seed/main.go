// Command seed fills a local Pulse with a demo workspace: sandbox providers on every channel,
// templates, dynamic and static segments, journeys (including WhatsApp with SMS fallback),
// broadcasts, a form and a landing page, on-site and in-app content, teammates, stages,
// companies, saved analytics and a few hundred contacts' activity spread over the last weeks.
// Local development only; credentials are in deploy/dev-seed.md.
//
//	go run ./cmd/seed -api http://localhost:7080                     # make seed: "Demo Retail"
//	go run ./cmd/seed -workspace "Acme Retail" -email docs.admin@pulse.test \
//	    -password pulse-docs-pass-1 -if-exists acme-retail             # make docs-seed
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
	"strings"
	"time"
)

var (
	apiURL    = flag.String("api", "http://localhost:7080", "Pulse base URL")
	workspace = flag.String("workspace", "Demo Retail", "workspace (company) name")
	email     = flag.String("email", "demo.admin@pulse.test", "admin email for the workspace")
	password  = flag.String("password", "pulse-demo-pass-1", "admin password for the workspace")
	contacts  = flag.Int("contacts", 300, "contacts to create")
	days      = flag.Int("days", 30, "spread contacts' activity over this many past days")
	ifExists  = flag.String("if-exists", "", "workspace ID: do nothing when the admin can already sign in there")
)

var client *http.Client

func call(method, path string, body any) map[string]any {
	out, code, raw := try(method, path, body)
	if code >= 300 {
		fail("%s %s: %d %s", method, path, code, raw)
	}
	return out
}

// try is call without failing on an error status.
func try(method, path string, body any) (map[string]any, int, []byte) {
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
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out, resp.StatusCode, raw
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}

func id(m map[string]any) float64 { return m["id"].(float64) }

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func main() {
	flag.Parse()
	jar, _ := cookiejar.New(nil)
	client = &http.Client{Jar: jar, Timeout: 30 * time.Second}

	if *ifExists != "" {
		if _, code, _ := try("POST", "/v1/auth/login", map[string]any{"workspace": *ifExists, "email": *email, "password": *password}); code == http.StatusOK {
			fmt.Printf("workspace %s already exists and %s can sign in: nothing to do\n", *ifExists, *email)
			return
		}
	}

	ws := call("POST", "/v1/auth/signup", map[string]any{"workspace": *workspace, "name": "Workspace Admin", "email": *email, "password": *password})
	slug := ws["workspace"].(map[string]any)["slug"]
	fmt.Println("workspace:", slug)

	team()
	c := content()
	time.Sleep(3 * time.Second) // let workers pick up the new segments and journeys
	activity(c)
	time.Sleep(8 * time.Second) // let segments fill before broadcasts pick their audiences
	broadcasts(c)
	fmt.Printf("seeded %d contacts. Sign in at %s/login with workspace %v (credentials in deploy/dev-seed.md)\n", *contacts, *apiURL, slug)
}

// team adds a custom role and two teammates without passwords (they would sign in with SSO), so the
// Team and Roles pages aren't empty.
func team() {
	role := call("POST", "/v1/roles", map[string]any{"name": "Analyst", "description": "Reads reports and segments, changes nothing",
		"permissions": map[string]any{"reports": "view", "segments": "view", "contacts": "view"}})
	for _, u := range []map[string]any{
		{"email": "priya.shah@pulse.test", "name": "Priya Shah", "role": "editor"},
		{"email": "dev.malhotra@pulse.test", "name": "Dev Malhotra", "role": "viewer"},
		{"email": "nina.kapoor@pulse.test", "name": "Nina Kapoor", "role": role["key"]},
	} {
		call("POST", "/v1/users", u)
	}
	for _, st := range []map[string]any{{"name": "Visitor"}, {"name": "Lead", "min_points": 30}, {"name": "Engaged", "min_points": 70}, {"name": "Customer"}} {
		call("POST", "/v1/stages", st)
	}
	for _, co := range []map[string]any{
		{"name": "Northwind Traders", "domain": "northwind.test", "attrs": map[string]any{"industry": "Wholesale", "size": 240}},
		{"name": "Contoso Foods", "domain": "contoso.test", "attrs": map[string]any{"industry": "Food & beverage", "size": 85}},
		{"name": "Fabrikam Interiors", "domain": "fabrikam.test", "attrs": map[string]any{"industry": "Home & living", "size": 40}},
	} {
		call("POST", "/v1/companies", co)
	}
}

// seeded holds the ids that activity and broadcasts need.
type seeded struct {
	welcome, sale, inbox     float64
	vip, webinar, puneBuyers float64
	formKey                  string
}

func content() seeded {
	var s seeded
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

	shop := *workspace
	s.welcome = id(call("POST", "/v1/templates", map[string]any{"name": "Welcome email", "channel": "email",
		"subject": "Welcome to " + shop + ", {{.Attrs.first_name}}",
		"html":    `<div style="font-family:Arial;padding:24px"><h1>Hi {{.Attrs.first_name}},</h1><p>Thanks for joining. Here is 10% off your first order: <b>HELLO10</b>.</p></div>`}))
	s.sale = id(call("POST", "/v1/templates", map[string]any{"name": "Weekend sale", "channel": "email",
		"subject": "{{.Attrs.first_name}}, the weekend sale starts now",
		"html":    `<div style="font-family:Arial;padding:24px"><h1>Weekend sale</h1><p>Up to 30% off home and kitchen, until Sunday night.</p><p><a href="https://demo.shop/sale">Shop the sale</a></p></div>`}))
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
	s.inbox = id(call("POST", "/v1/templates", map[string]any{"name": "Order shipped (app inbox)", "channel": "inbox",
		"content": map[string]any{"title": "Your order is on its way", "body": "Track it from your orders page.", "url": "https://demo.shop/orders"}}))

	seg := func(name string, rule string) float64 {
		return id(call("POST", "/v1/segments", map[string]any{"name": name, "rule": json.RawMessage(rule)}))
	}
	newSignups := seg("New sign-ups", `{"event":"signup","op":"gte","count":1,"days":7}`)
	carts := seg("Abandoned cart", `{"all":[{"event":"add_to_cart","op":"gte","count":1,"days":3},{"event":"purchase","op":"eq","count":0,"days":3}]}`)
	s.puneBuyers = seg("Repeat buyers in Pune", `{"all":[{"attr":"city","op":"eq","value":"Pune"},{"event":"purchase","op":"gte","count":2,"days":30}]}`)
	seg("Pro plan", `{"attr":"plan","op":"in","value":["pro","team"]}`)
	seg("VIP tag", `{"tag":"vip","op":"has"}`)
	s.vip = id(call("POST", "/v1/lists", map[string]any{"name": "VIP customers"}))
	s.webinar = id(call("POST", "/v1/lists", map[string]any{"name": "Webinar attendees"}))

	journey := func(name string, graph map[string]any, activate bool) {
		j := call("POST", "/v1/journeys", map[string]any{"name": name, "graph": graph})
		if activate {
			call("POST", fmt.Sprintf("/v1/journeys/%v/activate", id(j)), nil)
		}
	}
	journey("Welcome", map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": newSignups},
		"entry":   "email1",
		"nodes": map[string]any{
			"email1": map[string]any{"type": "send_email", "template_id": s.welcome, "next": "exit", "x": 300, "y": 140},
			"exit":   map[string]any{"type": "exit", "x": 300, "y": 280},
		},
	}, true)
	journey("Cart recovery", map[string]any{
		"trigger": map[string]any{"type": "segment_entered", "segment_id": carts},
		"entry":   "wait1",
		"nodes": map[string]any{
			"wait1":     map[string]any{"type": "wait", "seconds": 5, "next": "check1", "x": 300, "y": 140},
			"check1":    map[string]any{"type": "condition", "segment_id": carts, "yes": "whatsapp1", "no": "exit", "x": 300, "y": 260},
			"whatsapp1": map[string]any{"type": "send_whatsapp", "template_id": wa, "fallback_template_id": sms, "next": "exit", "x": 160, "y": 400},
			"exit":      map[string]any{"type": "exit", "x": 420, "y": 520},
		},
	}, true)
	journey("Win back (draft)", map[string]any{
		"trigger": map[string]any{"type": "event", "event": "purchase"},
		"entry":   "wait1",
		"nodes": map[string]any{
			"wait1":  map[string]any{"type": "wait", "seconds": 30 * 24 * 3600, "next": "email1", "x": 300, "y": 140},
			"email1": map[string]any{"type": "send_email", "template_id": s.sale, "next": "exit", "x": 300, "y": 280},
			"exit":   map[string]any{"type": "exit", "x": 300, "y": 420},
		},
	}, false)

	// A form, the landing page that embeds it, and on-site / in-app content.
	form := id(call("POST", "/v1/forms", map[string]any{"name": "Newsletter sign-up", "status": "active",
		"fields": []any{
			map[string]any{"key": "first_name", "label": "First name", "type": "text", "required": true},
			map[string]any{"key": "email", "label": "Email", "type": "email", "required": true},
			map[string]any{"key": "city", "label": "City", "type": "select", "options": []string{"Pune", "Mumbai", "Delhi", "Bengaluru", "Chennai"}},
		},
		"settings": map[string]any{"submit_text": "Subscribe", "success_message": "Thanks! Check your inbox for 10% off.", "tags": []string{"newsletter"},
			"consent_text": "Email me offers and news. I can unsubscribe at any time."}}))
	s.formKey = call("GET", fmt.Sprint("/v1/forms/", form), nil)["form"].(map[string]any)["public_key"].(string)
	call("POST", "/v1/forms", map[string]any{"name": "Store feedback", "status": "draft",
		"fields": []any{map[string]any{"key": "email", "label": "Email", "type": "email", "required": true},
			map[string]any{"key": "feedback", "label": "How was your visit?", "type": "textarea"}}})
	call("POST", "/v1/landing-pages", map[string]any{"slug": "weekend-sale", "title": "Weekend sale", "meta_description": "Up to 30% off home and kitchen",
		"html":   `<section style="font-family:Arial;max-width:640px;margin:40px auto;text-align:center"><h1>Weekend sale</h1><p>Up to 30% off home and kitchen, until Sunday night. Join the list for early access.</p>{form:` + s.formKey + `}</section>`,
		"status": "published", "start_at": rfc(time.Now().Add(-time.Hour))})

	call("POST", "/v1/focus-items", map[string]any{"name": "Exit offer for new visitors", "kind": "modal", "status": "active",
		"content":   map[string]any{"title": "Before you go: 10% off", "body_html": "<p>Use code <b>HELLO10</b> on your first order.</p>", "cta_text": "Shop now", "cta_url": "https://demo.shop", "background": "#ffffff", "text_color": "#152238", "button_color": "#2747d6"},
		"targeting": map[string]any{"exclude_segment_ids": []float64{s.puneBuyers}, "trigger": "exit_intent", "frequency": "once"}})
	call("POST", "/v1/dwc", map[string]any{"slot": "home-hero", "name": "Homepage hero", "status": "active",
		"default_html": "<h2>New season, new arrivals</h2>",
		"variants":     []map[string]any{{"segment_id": s.puneBuyers, "html": "<h2>Welcome back! Free delivery in Pune this week.</h2>"}}})
	call("POST", "/v1/inapp", map[string]any{"name": "App welcome", "layout": "modal", "status": "active",
		"content":   map[string]any{"title": "Welcome to the app", "body": "Turn on notifications to hear about sales first.", "buttons": []any{map[string]any{"text": "Got it", "action": "dismiss"}}},
		"targeting": map[string]any{"platforms": []any{"android", "ios"}, "trigger_event": "app_open", "frequency": "once"}})

	// Saved analytics: a funnel, a report, a cohort, and revenue in rupees.
	call("POST", "/v1/funnels", map[string]any{"name": "Browse to purchase", "window_days": 7, "steps": []any{
		map[string]any{"event": "page_view"}, map[string]any{"event": "add_to_cart"}, map[string]any{"event": "purchase"}}})
	call("POST", "/v1/reports", map[string]any{"name": "Revenue by category", "spec": map[string]any{"source": "events",
		"metric": map[string]any{"type": "sum", "key": "amount"}, "group_by": map[string]any{"type": "prop", "key": "category"},
		"events": []string{"purchase"}, "chart": "bars", "range_days": 30}})
	call("POST", "/v1/cohorts", map[string]any{"name": "Weekly buyers", "spec": map[string]any{"by": "first_seen", "period": "week", "periods": 4,
		"return": map[string]any{"event": "purchase"}}})
	call("PUT", "/v1/revenue/settings", map[string]any{"event": "purchase", "amount_prop": "amount", "currency": "INR"})
	return s
}

// activity sends each contact's history: an identify, a session, page views, carts, purchases and
// email engagement, at times spread over the last -days days (each contact's events in order).
func activity(s seeded) {
	rng := rand.New(rand.NewSource(7))
	cities := []string{"Pune", "Mumbai", "Delhi", "Bengaluru", "Chennai"}
	devices := []map[string]any{
		{"platform": "web", "device_type": "mobile", "os": "Android", "os_version": "14", "browser": "Chrome", "device_brand": "Samsung", "device_model": "SM-S918B", "locale": "en-IN"},
		{"platform": "web", "device_type": "mobile", "os": "iOS", "os_version": "17.4", "browser": "Safari", "device_brand": "Apple", "device_model": "iPhone", "locale": "en-IN"},
		{"platform": "web", "device_type": "desktop", "os": "Windows", "os_version": "11", "browser": "Chrome", "locale": "en-IN"},
		{"platform": "web", "device_type": "desktop", "os": "macOS", "os_version": "10.15", "browser": "Safari", "locale": "en-GB"},
		{"platform": "web", "device_type": "tablet", "os": "iPadOS", "os_version": "17.4", "browser": "Safari", "device_brand": "Apple", "device_model": "iPad", "locale": "hi-IN"},
	}
	names := []string{"Asha", "Ravi", "Meera", "Arjun", "Kavya", "Rohan", "Isha", "Vikram", "Neha", "Kabir"}
	surnames := []string{"Rao", "Iyer", "Mehta", "Nair", "Gupta", "Kulkarni", "Singh", "Das", "Joshi", "Reddy"}
	companies := []string{"Northwind Traders", "Contoso Foods", "Fabrikam Interiors"}
	categories := []string{"Kitchen", "Home decor", "Bedding", "Lighting"}
	now := time.Now()
	var evs []map[string]any
	flush := func() {
		if len(evs) > 0 {
			call("POST", "/v1/events", map[string]any{"events": evs})
			evs = nil
		}
	}
	var vips, webinar []string
	for i := 1; i <= *contacts; i++ {
		ext := fmt.Sprintf("cust-%04d", i)
		// The contact's first visit, then one event a few minutes after another.
		ts := now.Add(-time.Duration(rng.Int63n(int64(*days)*24*int64(time.Hour)) + int64(2*time.Hour)))
		if *days <= 0 {
			ts = now.Add(-2 * time.Hour)
		}
		add := func(e map[string]any) {
			ts = ts.Add(time.Duration(1+rng.Intn(20)) * time.Minute)
			if ts.After(now) {
				ts = now
			}
			e["external_id"], e["ts"] = ext, rfc(ts)
			evs = append(evs, e)
		}
		phone := fmt.Sprintf("+9198%08d", rng.Intn(100000000))
		if i%10 == 0 {
			phone = phone[:len(phone)-4] + "0000" // not on WhatsApp: exercises the SMS fallback
		}
		first, last := names[rng.Intn(len(names))], surnames[rng.Intn(len(surnames))]
		attrs := map[string]any{"first_name": first, "last_name": last, "city": cities[rng.Intn(len(cities))],
			"phone": phone, "plan": []string{"free", "pro", "team"}[rng.Intn(3)]}
		if i%15 == 0 {
			attrs["company"] = companies[rng.Intn(len(companies))]
		}
		add(map[string]any{"name": "identify", "email": fmt.Sprintf("%s.%s%d@example.com", strings.ToLower(first), strings.ToLower(last), i), "attrs": attrs})
		if rng.Intn(3) == 0 {
			add(map[string]any{"name": "signup"})
		}
		// The device the web SDK would report, on a session start and its page views.
		dev := devices[rng.Intn(len(devices))]
		withDev := func(p map[string]any) map[string]any {
			for k, v := range dev {
				p[k] = v
			}
			return p
		}
		add(map[string]any{"name": "session_start", "props": withDev(map[string]any{"session_id": fmt.Sprint("s", i)})})
		for k := 1 + rng.Intn(6); k > 0; k-- {
			add(map[string]any{"name": "page_view", "props": withDev(map[string]any{"path": "/p/" + fmt.Sprint(rng.Intn(90))})})
		}
		// Engagement with the welcome email, VIP tags and points, for the segment builder's filters.
		if rng.Intn(10) < 4 {
			add(map[string]any{"name": "email_sent", "props": map[string]any{"template_id": s.welcome}})
			if rng.Intn(2) == 0 {
				add(map[string]any{"name": "email_opened", "props": map[string]any{"template_id": s.welcome}})
			}
		}
		if rng.Intn(6) == 0 {
			add(map[string]any{"name": "tag_added", "props": map[string]any{"tags": []string{"vip"}}})
			vips = append(vips, ext)
		}
		if rng.Intn(8) == 0 {
			webinar = append(webinar, ext)
		}
		add(map[string]any{"name": "points_changed", "props": map[string]any{"delta": rng.Intn(101), "reason": "seed"}})
		if rng.Intn(2) == 0 {
			add(map[string]any{"name": "add_to_cart", "props": map[string]any{"category": categories[rng.Intn(len(categories))]}})
		}
		for k := rng.Intn(3); k > 0; k-- {
			add(map[string]any{"name": "purchase", "props": map[string]any{"amount": 199 + rng.Intn(3000), "category": categories[rng.Intn(len(categories))]}})
		}
		if len(evs) > 400 {
			flush()
		}
	}
	flush()
	for list, members := range map[float64][]string{s.vip: vips, s.webinar: webinar} {
		if len(members) > 0 {
			call("POST", fmt.Sprintf("/v1/lists/%v/members", list), map[string]any{"external_ids": members, "action": "add"})
		}
	}
}

// broadcasts: one sent to the VIP static segment, one scheduled for next week, one draft.
func broadcasts(s seeded) {
	sent := call("POST", "/v1/broadcasts", map[string]any{"name": "Weekend sale (VIPs)", "channel": "email", "template_id": s.sale,
		"segment_id": s.vip, "start_at": rfc(time.Now().Add(-time.Minute)), "repeat": "none", "conversion_event": "purchase", "conversion_days": 7})
	call("POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", id(sent)), nil)
	later := call("POST", "/v1/broadcasts", map[string]any{"name": "Festive preview (Pune)", "channel": "email", "template_id": s.sale,
		"segment_id": s.puneBuyers, "start_at": rfc(time.Now().Add(7 * 24 * time.Hour).Truncate(time.Hour)), "repeat": "none"})
	call("POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", id(later)), nil)
	call("POST", "/v1/broadcasts", map[string]any{"name": "Order updates in the app inbox", "channel": "inbox", "template_id": s.inbox,
		"segment_id": s.webinar, "start_at": rfc(time.Now().Add(24 * time.Hour).Truncate(time.Hour)), "repeat": "weekly"})
}
