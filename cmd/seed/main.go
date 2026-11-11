// Command seed fills a local Pulse with a demo workspace: sandbox providers on every channel,
// templates, dynamic and static segments, journeys (including WhatsApp with SMS fallback),
// broadcasts, a form and a landing page, on-site and in-app content, teammates, stages,
// companies, saved analytics and a few hundred contacts' activity spread over the last weeks.
// Local development only; credentials are in deploy/dev-seed.md.
//
//	go run ./cmd/seed -api http://localhost:7080                     # make seed: "Demo Retail"
//	go run ./cmd/seed -workspace "Acme Retail" -email docs.admin@pulse.test \
//	    -password pulse-docs-pass-1 -if-exists acme-retail             # make docs-seed
//
// With -if-exists, an existing workspace only gets what later versions of the seed add (event
// schemas and property-rich events, once), so the docs workspace keeps up without being rebuilt.
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
			added := false
			if eventData() {
				fmt.Printf("workspace %s already exists: added event schemas and property-rich events\n", *ifExists)
				added = true
			}
			if personalisation() {
				fmt.Printf("workspace %s: added the personalised cart-rescue templates and journey\n", *ifExists)
				added = true
			}
			if campaigns() {
				fmt.Printf("workspace %s: added campaign tags, one-time campaigns, test contacts and SMS limits\n", *ifExists)
				added = true
			}
			if delivery() {
				fmt.Printf("workspace %s: added delivery reports, bounces, complaints, failures, product views and app activity\n", *ifExists)
				added = true
			}
			if !added {
				fmt.Printf("workspace %s already exists and %s can sign in: nothing to do\n", *ifExists, *email)
			}
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
	eventData()
	personalisation()
	time.Sleep(8 * time.Second) // let segments fill before broadcasts pick their audiences
	broadcasts(c)
	campaigns()
	delivery()
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

// eventData adds the suggested event schemas, a "Loyalty tier" choice field, and a history of
// e-commerce events with properties (products, payment methods, sign-up methods) for the seeded
// contacts, including a few that don't fit their schema so the Events page shows problems. It
// does nothing when the workspace already has a purchase schema, and reports whether it ran.
func eventData() bool {
	list := call("GET", "/v1/events/schemas", nil)
	for _, sc := range list["schemas"].([]any) {
		if sc.(map[string]any)["name"] == "purchase" {
			return false
		}
	}
	call("POST", "/v1/events/schemas/suggested", map[string]any{})
	call("PUT", "/v1/attributes/loyalty_tier", map[string]any{"label": "Loyalty tier", "type": "enum",
		"options": []any{map[string]any{"value": "bronze", "label": "Bronze"}, map[string]any{"value": "silver", "label": "Silver"}, map[string]any{"value": "gold", "label": "Gold"}}})
	time.Sleep(2 * time.Second) // the profile role reloads schemas (list fields fill from purchases)

	rng := rand.New(rand.NewSource(12))
	products := []struct {
		code, name, category string
		price                int
	}{
		{"KIT-101", "Cast iron skillet", "Kitchen", 1899}, {"KIT-204", "Chef's knife", "Kitchen", 2499},
		{"DEC-310", "Brass table lamp", "Lighting", 3299}, {"DEC-115", "Jute rug", "Home decor", 4599},
		{"BED-220", "Cotton bedsheet set", "Bedding", 1599}, {"BED-404", "Memory foam pillow", "Bedding", 1299},
	}
	methods := []string{"upi", "card", "netbanking", "wallet", "cod"}
	gateways := []string{"razorpay", "payu", "cashfree"}
	now := time.Now()
	var evs []map[string]any
	flush := func() {
		if len(evs) > 0 {
			call("POST", "/v1/events", map[string]any{"events": evs})
			evs = nil
		}
	}
	for i := 1; i <= *contacts; i++ {
		ext := fmt.Sprintf("cust-%04d", i)
		ts := now.Add(-time.Duration(rng.Int63n(int64(60*24*time.Hour))) - 3*time.Hour)
		add := func(name string, props map[string]any) {
			ts = ts.Add(time.Duration(5+rng.Intn(600)) * time.Minute)
			if ts.After(now) {
				ts = now
			}
			evs = append(evs, map[string]any{"external_id": ext, "name": name, "ts": rfc(ts), "props": props})
		}
		attrs := map[string]any{"loyalty_tier": []string{"bronze", "silver", "gold"}[rng.Intn(3)]}
		if i%9 == 0 { // a few customers abroad, for phone_country
			attrs["phone"] = []string{"+447700900123", "+14155550142", "+971501234567"}[rng.Intn(3)]
		}
		evs = append(evs, map[string]any{"external_id": ext, "name": "identify", "ts": rfc(ts), "attrs": attrs})
		switch rng.Intn(4) {
		case 0:
			p := map[string]any{"method": "social", "social_provider": []string{"google", "facebook", "apple", "linkedin"}[rng.Intn(4)]}
			if i%37 == 0 {
				delete(p, "social_provider") // a sign-up that doesn't fit its schema
			}
			add("signup", p)
		case 1:
			add("signup", map[string]any{"method": []string{"otp", "password"}[rng.Intn(2)]})
		}
		if rng.Intn(2) == 0 {
			add("login", map[string]any{"method": []string{"otp", "password", "social"}[rng.Intn(3)], "social_provider": "google"})
		}
		for k := rng.Intn(4); k > 0; k-- {
			p := products[rng.Intn(len(products))]
			src := []string{"web", "app"}[rng.Intn(2)]
			add("add_to_cart", map[string]any{"product_code": p.code, "product_name": p.name, "product_category": p.category,
				"price": p.price, "quantity": 1 + rng.Intn(2), "source": src, "landing_page": []string{"/", "/sale", "/kitchen"}[rng.Intn(3)]})
			switch rng.Intn(3) {
			case 0:
				add("cart_abandoned", map[string]any{"product_code": p.code, "product_name": p.name, "product_category": p.category,
					"price": p.price, "cart_value": p.price * (1 + rng.Intn(2))})
			case 1:
				add("checkout_abandoned", map[string]any{"product_code": p.code, "product_name": p.name, "product_category": p.category,
					"price": p.price, "cart_value": p.price, "payment_method": methods[rng.Intn(len(methods))],
					"payment_gateway": gateways[rng.Intn(len(gateways))], "failure_reason": []string{"payment declined", "OTP timeout", "user cancelled"}[rng.Intn(3)]})
			default:
				qty := 1 + rng.Intn(2)
				value := any(p.price * qty)
				if i%41 == 0 {
					value = fmt.Sprint(value) // a purchase whose value is text: a schema problem
				}
				add("purchase", map[string]any{"order_id": fmt.Sprintf("ORD-%d-%d", i, k), "product_code": p.code, "product_name": p.name,
					"product_category": p.category, "value": value, "amount": p.price * qty, "currency": "INR", "quantity": qty, "source": src,
					"affiliation": "Acme Online", "items": []any{map[string]any{"product_code": p.code, "quantity": qty, "price": p.price}}})
			}
		}
		if len(evs) > 400 {
			flush()
		}
	}
	flush()
	return true
}

// personalisation adds (once) templates written with the standard variables, from the contact and
// from the cart_abandoned event, and an active "Cart rescue" journey whose first step picks the
// channel by rule (an Indian number gets SMS, anyone else WhatsApp), then a few carts left now so
// its step shows which channel each contact got. It reports whether it added anything.
func personalisation() bool {
	for _, t := range call("GET", "/v1/templates", nil)["templates"].([]any) {
		if t.(map[string]any)["name"] == "Cart rescue email" {
			return false
		}
	}
	email := id(call("POST", "/v1/templates", map[string]any{"name": "Cart rescue email", "channel": "email", "missing_values": "empty",
		"subject": `{{contact.first_name | default:"Hello"}}, your {{event.product_name | default:"cart"}} is waiting`,
		"html": `<div style="font-family:Arial;padding:24px;color:#152238"><h1 style="font-size:22px">Still thinking it over, {{contact.first_name | default:"there"}}?</h1>` +
			`<p>You left a <b>{{event.product_name}}</b> ({{event.product_category | lower}}) in your cart: {{event.cart_value | money:"INR"}}.</p>` +
			`<p><a href="https://acme.example.com/cart?item={{event.product_code | urlencode}}" style="background:#2747d6;color:#fff;padding:10px 16px;border-radius:6px;text-decoration:none">Back to my cart</a></p>` +
			`<p style="color:#5b6577;font-size:12px">{{campaign.name}} · <a href="{{links.preferences}}">Email preferences</a></p></div>`}))
	sms := id(call("POST", "/v1/templates", map[string]any{"name": "Cart rescue SMS", "channel": "sms", "missing_values": "skip",
		"content": map[string]any{"text": `Hi {{contact.first_name | default:"there"}}, your {{event.product_name | truncate:30}} is still in your cart. Finish your order: https://acme.example.com/cart`, "sender_id": "ACMERT", "track_links": true}}))
	wa := id(call("POST", "/v1/templates", map[string]any{"name": "Cart rescue WhatsApp", "channel": "whatsapp",
		"content": map[string]any{"template_name": "cart_reminder_v2", "language": "en",
			"body_params":  []string{`{{contact.first_name | default:"there"}}`, "{{event.product_name}}", `{{event.cart_value | money:"INR"}}`},
			"preview_text": "Hi {{1}}, your {{2}} ({{3}}) is still in your cart. Tap below to finish your order."}}))
	j := call("POST", "/v1/journeys", map[string]any{"name": "Cart rescue", "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "cart_abandoned", "reentry": "after_days", "days": 7},
		"entry":   "s1",
		"nodes": map[string]any{
			"s1": map[string]any{"type": "send_message", "channels": []any{
				map[string]any{"channel": "sms", "template_id": sms, "when": []any{map[string]any{"prop": "contact.phone_country", "op": "eq", "value": "IN"}}},
				map[string]any{"channel": "whatsapp", "template_id": wa, "fallback_template_id": sms},
			}, "next": "w1", "x": 300, "y": 160},
			"w1": map[string]any{"type": "wait", "seconds": 3600, "next": "e1", "x": 300, "y": 300},
			"e1": map[string]any{"type": "send_email", "template_id": email, "next": "x", "x": 300, "y": 440},
			"x":  map[string]any{"type": "exit", "x": 300, "y": 580},
		},
	}})
	call("POST", fmt.Sprintf("/v1/journeys/%v/activate", id(j)), nil)
	time.Sleep(3 * time.Second) // the journey workers load it

	// A few carts left right now: Indian numbers, and customers abroad.
	phones := []string{"+919812300011", "+919812300012", "+919812300013", "+919812300014", "+447700900311", "+14155550312", "+971501230313"}
	var evs []map[string]any
	for i, ph := range phones {
		ext := fmt.Sprintf("cust-%04d", 201+i)
		evs = append(evs, map[string]any{"external_id": ext, "name": "identify", "attrs": map[string]any{"phone": ph}})
	}
	call("POST", "/v1/events", map[string]any{"events": evs})
	time.Sleep(2 * time.Second) // phone_country is set before the carts arrive
	evs = nil
	for i := range phones {
		evs = append(evs, map[string]any{"external_id": fmt.Sprintf("cust-%04d", 201+i), "name": "cart_abandoned", "props": map[string]any{
			"product_code": "DEC-310", "product_name": "Brass table lamp", "product_category": "Lighting", "price": 3299, "cart_value": 3299 + 500*i}})
	}
	call("POST", "/v1/events", map[string]any{"events": evs})
	return true
}

// campaigns (M3) tags the broadcasts and journeys for the Campaigns hub, adds two one-time
// campaigns written in the wizard (their templates hidden), saved test contacts, sending limits on
// the SMS sandbox, and a few unsubscribes and bounces so audience estimates show drop-offs. It does
// nothing when the test contacts are already there, and reports whether it ran.
func campaigns() bool {
	for _, t := range call("GET", "/v1/test-contacts", nil)["test_contacts"].([]any) {
		if t.(map[string]any)["name"] == "Priya (QA)" {
			return false
		}
	}
	byName := func(path, key string) map[string]float64 {
		out := map[string]float64{}
		for _, x := range call("GET", path, nil)[key].([]any) {
			m := x.(map[string]any)
			out[m["name"].(string)] = id(m)
		}
		return out
	}
	tag := func(path string, id float64, tags ...string) {
		if id > 0 {
			call("PUT", fmt.Sprintf("%s/%v/tags", path, id), map[string]any{"tags": tags})
		}
	}
	bcs := byName("/v1/broadcasts", "broadcasts")
	tag("/v1/broadcasts", bcs["Weekend sale (VIPs)"], "Weekend sale")
	tag("/v1/broadcasts", bcs["Festive preview (Pune)"], "Festive", "Diwali 2026")
	js := byName("/v1/journeys", "journeys")
	tag("/v1/journeys", js["Cart rescue"], "Retention")
	tag("/v1/journeys", js["Cart recovery"], "Retention")
	tag("/v1/journeys", js["Welcome"], "Onboarding")
	segs := byName("/v1/segments", "segments")

	// Unsubscribes and bounces among the shoppers, so estimates drop at consent and reachability.
	var evs []map[string]any
	for i := 3; i <= 60; i += 7 {
		evs = append(evs, map[string]any{"external_id": fmt.Sprintf("cust-%04d", i), "name": "unsubscribed", "props": map[string]any{"channel": "email", "source": "email_link"}})
	}
	for _, i := range []int{5, 19, 44} {
		evs = append(evs, map[string]any{"external_id": fmt.Sprintf("cust-%04d", i), "name": "identify", "attrs": map[string]any{"email_bounced": true}})
	}
	call("POST", "/v1/events", map[string]any{"events": evs})

	// One-time campaigns made in the wizard: the message is written inside the campaign.
	sms := id(call("POST", "/v1/templates", map[string]any{"name": "Diwali offer (SMS)", "channel": "sms", "hidden": true,
		"content": map[string]any{"text": `Happy Diwali {{contact.first_name | default:"there"}}! 20% off lamps and decor till Sunday: https://acme.example.com/diwali`, "sender_id": "ACMERT"}}))
	diwali := call("POST", "/v1/broadcasts", map[string]any{"name": "Diwali offer", "channel": "sms", "template_id": sms,
		"segment_id": segs["Pro plan"], "exclude_segment_ids": []float64{segs["VIP tag"]}, "repeat": "none",
		"start_at": rfc(time.Now().Add(5 * 24 * time.Hour).Truncate(time.Hour)), "tags": []string{"Festive", "Diwali 2026"},
		"deadline": map[string]any{"at": "21:00", "timezone": "Asia/Kolkata"}})
	call("POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", id(diwali)), nil)
	wa := id(call("POST", "/v1/templates", map[string]any{"name": "Monsoon clearance (WhatsApp)", "channel": "whatsapp", "hidden": true,
		"content": map[string]any{"template_name": "monsoon_clearance", "language": "en", "body_params": []string{`{{contact.first_name | default:"there"}}`},
			"preview_text": "Hi {{1}}, the monsoon clearance starts tomorrow: up to 40% off."}}))
	call("POST", "/v1/broadcasts", map[string]any{"name": "Monsoon clearance", "channel": "whatsapp", "template_id": wa,
		"segment_id": segs["Repeat buyers in Pune"], "repeat": "none", "start_at": rfc(time.Now().Add(10 * 24 * time.Hour).Truncate(time.Hour)),
		"tags": []string{"Clearance"}})

	// Saved test contacts, offered by every "Send a test" dialog.
	call("POST", "/v1/test-contacts", map[string]any{"name": "Priya (QA)", "email": "priya.qa@acme-retail.test", "phone": "+919812399001"})
	call("POST", "/v1/test-contacts", map[string]any{"name": "Dev's phone", "phone": "+919812399002", "push_token": "sandbox-device-dev-0001"})
	call("POST", "/v1/test-contacts", map[string]any{"name": "Asha (customer)", "external_id": "cust-0001"})

	// The SMS sandbox stands in for a DLT route with a throughput and a daily quota.
	for _, p := range call("GET", "/v1/channels/providers", nil)["providers"].([]any) {
		m := p.(map[string]any)
		if m["channel"] == "sms" {
			call("PUT", fmt.Sprintf("/v1/channels/providers/%v/limits", id(m)), map[string]any{"rate_per_second": 20, "daily_quota": 5000, "timezone": "Asia/Kolkata"})
		}
	}
	return true
}

// delivery (M4) sends a catalogue email and a WhatsApp preview now and gives them what providers
// would report: deliveries, opens, clicks (some on the PDF), hard and soft bounces and a spam
// complaint for the email (recorded as the linked events the provider webhooks record), and
// delivered, read and failed receipts (one "undeliverable (possibly blocked)") for WhatsApp through
// the sandbox's receipts. It also adds product views and app screen views for the dashboards. It
// does nothing when the "Spring catalogue" broadcast exists, and reports whether it ran.
func delivery() bool {
	existing := map[string]map[string]any{}
	for _, b := range call("GET", "/v1/broadcasts", nil)["broadcasts"].([]any) {
		existing[b.(map[string]any)["name"].(string)] = b.(map[string]any)
	}
	if b, ok := existing["Spring catalogue"]; ok { // done unless an earlier run stopped before the outcomes
		rep := call("GET", fmt.Sprintf("/v1/broadcasts/%v/report", id(b)), nil)
		if tot, _ := rep["totals"].(map[string]any); tot != nil && tot["delivered"].(float64) > 0 {
			return false
		}
	}
	segs := map[string]float64{}
	for _, x := range call("GET", "/v1/segments", nil)["segments"].([]any) {
		m := x.(map[string]any)
		segs[m["name"].(string)] = id(m)
	}
	rng := rand.New(rand.NewSource(11))
	now := time.Now()
	clamp := func(t time.Time) time.Time {
		if t.After(now) {
			return now
		}
		return t
	}
	var tpl, bc, wbc float64
	if b, ok := existing["Spring catalogue"]; ok {
		bc, tpl = id(b), b["template_id"].(float64)
		if w, ok := existing["Monsoon preview"]; ok {
			wbc = id(w)
		}
	} else {
		tpl, bc, wbc = sendDeliveryBroadcasts(segs, now)
	}

	// Wait for the sends to be recorded.
	for i := 0; i < 60; i++ {
		rep := call("GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil)
		if tot, _ := rep["totals"].(map[string]any); tot != nil && tot["sent"].(float64) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	time.Sleep(5 * time.Second)

	// Email outcomes, as the sending service's webhooks (or bounce emails) record them.
	var evs []map[string]any
	flush := func() {
		if len(evs) > 0 {
			call("POST", "/v1/events", map[string]any{"events": evs})
			evs = nil
		}
	}
	complained := 0
	for i := 1; i <= *contacts; i++ {
		ext := fmt.Sprintf("cust-%04d", i)
		msgs, code, _ := try("GET", "/v1/contacts/"+ext+"/messages?limit=20", nil)
		if code != http.StatusOK {
			continue
		}
		for _, x := range msgs["messages"].([]any) {
			m := x.(map[string]any)
			if m["source"] != "broadcast" || m["source_id"].(float64) != bc || m["status"] != "sent" {
				continue
			}
			mid := m["message_id"].(string)
			var run int64
			if parts := strings.Split(mid, ":"); len(parts) == 3 {
				fmt.Sscan(parts[1], &run)
			}
			props := func(extra map[string]any) map[string]any {
				p := map[string]any{"message_id": mid, "template_id": tpl, "journey_id": 0, "broadcast_id": bc, "run": run}
				for k, v := range extra {
					p[k] = v
				}
				return p
			}
			at := now.Add(-time.Duration(30+rng.Intn(30)) * time.Second)
			add := func(name string, ts time.Time, p map[string]any, attrs map[string]any) {
				ev := map[string]any{"id": "seed:" + mid + ":" + name, "external_id": ext, "name": name, "ts": rfc(clamp(ts)), "props": p}
				if attrs != nil {
					ev["attrs"] = attrs
				}
				evs = append(evs, ev)
			}
			switch r := rng.Intn(100); {
			case r < 4: // hard bounce
				add("email_bounced", at, props(map[string]any{"kind": "hard", "reason_code": "hard_bounce", "source": "sendgrid",
					"reason": "550 5.1.1 The email account that you tried to reach does not exist"}), map[string]any{"email_bounced": true})
			case r < 7: // soft bounce
				add("email_bounced", at, props(map[string]any{"kind": "soft", "reason_code": "soft_bounce", "source": "sendgrid",
					"reason": "452 4.2.2 Mailbox full"}), nil)
			default:
				add("email_delivered", at, props(map[string]any{"source": "sendgrid", "reason": "250 2.0.0 OK"}), nil)
				if rng.Intn(100) < 45 {
					add("email_opened", at.Add(time.Duration(1+rng.Intn(10))*time.Second), props(nil), nil)
					if rng.Intn(100) < 35 {
						url := "https://acme.example.com/spring"
						if rng.Intn(2) == 0 {
							url = catalogPDF
						}
						add("email_clicked", at.Add(time.Duration(12+rng.Intn(10))*time.Second), props(map[string]any{"url": url}), nil)
					}
				}
				if complained < 2 && rng.Intn(100) < 3 {
					complained++
					add("email_complained", at.Add(25*time.Second), props(map[string]any{"kind": "complaint", "reason_code": "complaint", "source": "postmark"}), nil)
					evs = append(evs, map[string]any{"external_id": ext, "name": "unsubscribed", "props": map[string]any{"channel": "email", "source": "complaint"}})
				}
			}
		}
		if len(evs) > 400 {
			flush()
		}
	}
	flush()

	// WhatsApp receipts through the sandbox (the same path as Meta's status webhooks).
	sb := call("GET", "/v1/sandbox/messages?channel=whatsapp", nil)
	n := 0
	for _, x := range sb["messages"].([]any) {
		m := x.(map[string]any)
		mid, _ := m["message_id"].(string)
		if !strings.HasPrefix(mid, fmt.Sprintf("bc%v:", wbc)) {
			continue
		}
		n++
		pmid := "sandbox-" + mid
		switch {
		case n%9 == 0:
			call("POST", "/v1/sandbox/receipts", map[string]any{"provider_message_id": pmid, "status": "failed",
				"reason": "meta whatsapp 131026: Message undeliverable", "code": "blocked"})
		case n%13 == 0:
			call("POST", "/v1/sandbox/receipts", map[string]any{"provider_message_id": pmid, "status": "failed",
				"reason": "meta whatsapp 131049: This message was not delivered to maintain healthy ecosystem engagement.", "code": "spam_policy"})
		default:
			call("POST", "/v1/sandbox/receipts", map[string]any{"provider_message_id": pmid, "status": "delivered"})
			if n%3 != 0 {
				call("POST", "/v1/sandbox/receipts", map[string]any{"provider_message_id": pmid, "status": "read"})
			}
		}
	}

	// Product views (the Products dashboard) and app screen views (Users by platform), recent weeks.
	products := []struct{ code, category string }{{"KIT-101", "Kitchen"}, {"KIT-204", "Kitchen"}, {"DEC-310", "Lighting"},
		{"DEC-115", "Home decor"}, {"BED-220", "Bedding"}, {"BED-404", "Bedding"}}
	sources := []string{"google", "instagram", "newsletter", "", ""}
	for i := 1; i <= *contacts; i++ {
		ext := fmt.Sprintf("cust-%04d", i)
		for k := rng.Intn(6); k > 0; k-- {
			p := products[rng.Intn(len(products))]
			ts := now.Add(-time.Duration(rng.Int63n(int64(28 * 24 * time.Hour))))
			evs = append(evs, map[string]any{"external_id": ext, "name": "product_viewed", "ts": rfc(ts), "props": map[string]any{
				"product_code": p.code, "product_category": p.category, "platform": "web", "utm_source": sources[rng.Intn(len(sources))]}})
		}
		if i%3 == 0 {
			platform := []string{"android", "ios"}[rng.Intn(2)]
			for k := 2 + rng.Intn(8); k > 0; k-- {
				ts := now.Add(-time.Duration(rng.Int63n(int64(45 * 24 * time.Hour))))
				evs = append(evs, map[string]any{"external_id": ext, "name": "screen_view", "ts": rfc(ts),
					"props": map[string]any{"platform": platform, "screen": []string{"home", "product", "cart", "orders"}[rng.Intn(4)]}})
			}
		}
		if len(evs) > 400 {
			flush()
		}
	}
	flush()
	return true
}

const catalogPDF = "https://acme.example.com/files/spring-catalogue.pdf"

// sendDeliveryBroadcasts creates and sends the M4 seed's email and WhatsApp broadcasts.
func sendDeliveryBroadcasts(segs map[string]float64, now time.Time) (tpl, bc, wbc float64) {
	pdf := catalogPDF
	tpl = id(call("POST", "/v1/templates", map[string]any{"name": "Spring catalogue", "channel": "email", "hidden": true,
		"subject": `{{contact.first_name | default:"Hi"}}, the spring catalogue is here`,
		"html": `<div style="font-family:Arial;padding:24px"><h1>Spring at Acme</h1><p>New lamps, rugs and bedding.</p>` +
			`<p><a href="https://acme.example.com/spring">Shop the collection</a> · <a href="` + pdf + `">Download the catalogue (PDF)</a></p></div>`}))
	bc = id(call("POST", "/v1/broadcasts", map[string]any{"name": "Spring catalogue", "channel": "email", "template_id": tpl,
		"segment_id": segs["Pro plan"], "repeat": "none", "start_at": rfc(now.Add(-time.Minute)), "tags": []string{"Catalogue"}}))
	call("POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil)
	wtpl := id(call("POST", "/v1/templates", map[string]any{"name": "Monsoon preview (WhatsApp)", "channel": "whatsapp", "hidden": true,
		"content": map[string]any{"template_name": "monsoon_preview", "language": "en", "body_params": []string{`{{contact.first_name | default:"there"}}`},
			"preview_text": "Hi {{1}}, a first look at the monsoon range."}}))
	wbc = id(call("POST", "/v1/broadcasts", map[string]any{"name": "Monsoon preview", "channel": "whatsapp", "template_id": wtpl,
		"segment_id": segs["Repeat buyers in Pune"], "repeat": "none", "start_at": rfc(now.Add(-time.Minute)), "tags": []string{"Clearance"}}))
	call("POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", wbc), nil)
	return tpl, bc, wbc
}
