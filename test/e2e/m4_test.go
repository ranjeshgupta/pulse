//go:build e2e

package e2e

// M4, delivery truth and dashboards: a broadcast's emails carry message references; provider
// webhooks (SendGrid delivered, Mailgun bounce signed with the workspace's key, Postmark complaint)
// and a bounce email in the bounce mailbox are linked back to the message and campaign; the
// funnel, failure reasons, contact page, Campaigns / Users / Products dashboards and the live feed
// show it all.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"
)

// mailRef reads the X-Pulse-Ref header of the one email sent to an address.
func mailRef(t *testing.T, to string) (ref, msgID string) {
	t.Helper()
	var search struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	waitFor(t, 20*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+to), &search)
		return len(search.Messages) == 1
	})
	var headers map[string][]string
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID+"/headers", &headers)
	ref = strings.Join(headers["X-Pulse-Ref"], "")
	msgID = strings.Join(headers["Message-Id"], "")
	if ref == "" || !strings.Contains(msgID, ref) {
		t.Fatalf("email to %s has no message reference: %v", to, headers)
	}
	return ref, msgID
}

// bounceURL is the workspace's bounce webhook, on the API address this test talks to.
func bounceURL(t *testing.T, c *http.Client) string {
	u := jsonCall(t, c, "GET", "/v1/settings/bounces", nil, 200)["url"].(string)
	return apiURL + u[strings.Index(u, "/v1/bounces/"):]
}

func postHook(t *testing.T, u, body string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func num(v any) float64 { f, _ := v.(float64); return f }

func TestDeliveryTruthAndDashboards(t *testing.T) {
	c, _ := workspace(t, "Delivery")
	run := fmt.Sprint(time.Now().UnixNano())
	plan := "m4-" + run
	addr := func(k string) string { return "m4-" + k + "-" + run + "@example.com" }
	ext := func(k string) string { return "m4" + k + run }
	// a, b, c have email; d only a phone; e unsubscribed from email.
	var ev []any
	for _, k := range []string{"a", "b", "c", "e"} {
		ev = append(ev, map[string]any{"external_id": ext(k), "name": "identify", "email": addr(k), "attrs": map[string]any{"plan": plan}})
	}
	ev = append(ev, map[string]any{"external_id": ext("d"), "name": "identify", "attrs": map[string]any{"plan": plan, "phone": "+919800004444"}})
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": ev}, 202)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext("e"), "name": "unsubscribed", "props": map[string]any{"channel": "email"}}, 202)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M4 audience", "rule": map[string]any{"attr": "plan", "op": "eq", "value": plan}}, 201)["id"]
	optedOut := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "M4 opted out", "rule": map[string]any{"all": []any{
		map[string]any{"attr": "plan", "op": "eq", "value": plan}, map[string]any{"attr": "optout_email", "op": "eq", "value": "true"}}}}, 201)["id"]
	waitSegmentSize(t, c, seg, 5)
	waitSegmentSize(t, c, optedOut, 1)
	waitReachReady(t, c, seg)
	time.Sleep(time.Second)

	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "Guide", "channel": "email", "subject": "Your guide",
		"html": `<html><body><a href="https://shop.test/sale">Shop</a> <a href="https://files.test/guide.pdf">Guide (PDF)</a></body></html>`}, 201)["id"]
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Guide " + run, "channel": "email", "template_id": tpl,
		"segment_id": seg, "repeat": "none", "start_at": time.Now().UTC().Format(time.RFC3339), "tags": []string{"m4"}}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)
	refA, _ := mailRef(t, addr("a"))
	refB, _ := mailRef(t, addr("b"))
	_, msgC := mailRef(t, addr("c")) // c is linked through its Message-ID

	// The provider webhooks.
	hook := bounceURL(t, c)
	if code := postHook(t, hook, `[{"email":"`+addr("a")+`","event":"delivered","pulse_ref":"`+refA+`"},{"email":"`+addr("b")+
		`","event":"delivered","smtp-id":"<`+refB+`@example.com>"}]`, nil); code != 200 {
		t.Fatalf("sendgrid delivered: %d", code)
	}
	if code := postHook(t, hook, `{"RecordType":"SpamComplaint","Email":"`+addr("b")+`","Metadata":{"pulse_ref":"`+refB+`"}}`, nil); code != 200 {
		t.Fatalf("postmark complaint: %d", code)
	}
	// Once a signing key is saved, the URL fails closed: unsigned formats are refused (91a365e).
	jsonCall(t, c, "PUT", "/v1/settings/bounces/signing", map[string]any{"mailgun_signing_key": "key-m4-" + run}, 200)
	if code := postHook(t, hook, `{"RecordType":"SpamComplaint","Email":"`+addr("a")+`","Metadata":{"pulse_ref":"`+refA+`"}}`, nil); code != 401 {
		t.Errorf("an unsigned Postmark post after a key was saved: %d, want 401", code)
	}
	ts := fmt.Sprint(time.Now().Unix())
	mac := hmac.New(sha256.New, []byte("key-m4-"+run))
	mac.Write([]byte(ts + "tok" + run))
	mg, _ := json.Marshal(map[string]any{"signature": map[string]string{"timestamp": ts, "token": "tok" + run, "signature": hex.EncodeToString(mac.Sum(nil))},
		"event-data": map[string]any{"event": "failed", "severity": "permanent", "recipient": addr("c"),
			"delivery-status": map[string]any{"message": "550 5.1.1 no such user"},
			"message":         map[string]any{"headers": map[string]any{"message-id": strings.Trim(msgC, "<>")}}}})
	bad := bytes.Replace(mg, []byte(`"token":"tok`), []byte(`"token":"xok`), 1)
	if code := postHook(t, hook, string(bad), nil); code != 401 {
		t.Errorf("a Mailgun webhook with a bad signature: %d, want 401", code)
	}
	if code := postHook(t, hook, string(mg), nil); code != 200 {
		t.Fatalf("mailgun bounce: %d", code)
	}
	replay := bytes.Replace(mg, []byte(`"severity":"permanent"`), []byte(`"severity":"temporary"`), 1)
	if code := postHook(t, hook, string(replay), nil); code != 401 {
		t.Errorf("a replayed Mailgun signature with another body: %d, want 401", code)
	}

	// a opens and clicks both links (one is a file: a download too).
	var msg struct{ HTML string }
	var search struct{ Messages []struct{ ID string } }
	mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+addr("a")), &search)
	mailpitGet(t, "/api/v1/message/"+search.Messages[0].ID, &msg)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, m := range regexp.MustCompile(`href="([^"]*/t/c/[^"]+)"`).FindAllStringSubmatch(msg.HTML, -1) {
		u := strings.ReplaceAll(m[1], "&amp;", "&")
		u = apiURL + u[strings.Index(u, "/t/c/"):]
		resp, err := noFollow.Get(u)
		if err != nil || resp.StatusCode != 302 {
			t.Fatalf("click %s: %v %v", u, err, resp)
		}
		resp.Body.Close()
	}

	// The broadcast report: the funnel and the reasons.
	var rep map[string]any
	waitFor(t, 40*time.Second, func() bool {
		rep = jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil, 200)
		f, _ := rep["funnel"].(map[string]any)
		return num(f["delivered"]) == 2 && num(f["hard_bounced"]) == 1 && num(f["complained"]) == 1 && num(f["downloaded"]) == 1 &&
			num(f["clicked"]) == 1 && num(f["sent"]) == 3
	})
	f := rep["funnel"].(map[string]any)
	aud, _ := f["audience"].(map[string]any)
	if num(aud["audience"]) != 5 || num(aud["after_consent"]) != 4 || num(aud["reachable"]) != 3 {
		t.Errorf("audience at send time: %v", aud)
	}
	sk, _ := f["skipped"].(map[string]any)
	if num(sk["consent"]) != 1 || num(sk["reachability"]) != 1 || num(f["targeted"]) != 5 {
		t.Errorf("skipped: %v (funnel %v)", sk, f)
	}
	reasons := fmt.Sprint(rep["reasons"])
	for _, code := range []string{"hard_bounce", "complaint", "no_address", "consent"} {
		if !strings.Contains(reasons, "code:"+code) {
			t.Errorf("reason %s missing: %s", code, reasons)
		}
	}
	if attrsOf(t, c, ext("c"))["email_bounced"] != true || attrsOf(t, c, ext("b"))["optout_email"] != true {
		t.Error("the bounce and the complaint must act on the contacts")
	}

	// The contact page.
	msgs := func(k string) []any {
		m, _ := jsonCall(t, c, "GET", "/v1/contacts/"+ext(k)+"/messages", nil, 200)["messages"].([]any)
		return m
	}
	ma := msgs("a")
	if len(ma) != 1 || ma[0].(map[string]any)["status"] != "clicked" || ma[0].(map[string]any)["campaign"] != "Guide "+run {
		t.Errorf("a's messages: %v", ma)
	}
	mc := msgs("c")
	if len(mc) != 1 || mc[0].(map[string]any)["status"] != "bounced" || mc[0].(map[string]any)["reason_code"] != "hard_bounce" {
		t.Errorf("c's messages: %v", mc)
	}
	md := msgs("d")
	if len(md) != 1 || md[0].(map[string]any)["status"] != "failed" || md[0].(map[string]any)["reason_code"] != "no_address" {
		t.Errorf("d's messages: %v", md)
	}
	prof := jsonCall(t, c, "GET", "/v1/contacts/"+ext("b")+"/profile", nil, 200)
	for _, r := range prof["reach"].([]any) {
		r := r.(map[string]any)
		if r["channel"] == "email" && (r["reachable"] != false || r["opted_out"] != true) {
			t.Errorf("b's email reach: %v", r)
		}
	}
	if _, ok := prof["activity"].(map[string]any)["events"]; !ok {
		t.Errorf("profile activity: %v", prof["activity"])
	}

	// The Campaigns dashboard.
	waitFor(t, 70*time.Second, func() bool { // answers are cached for a minute
		rows, _ := jsonCall(t, c, "GET", "/v1/analytics/campaigns?tag=m4", nil, 200)["campaigns"].([]any)
		if len(rows) != 1 {
			return false
		}
		cn := rows[0].(map[string]any)["counts"].(map[string]any)
		return num(cn["sent"]) == 3 && num(cn["delivered"]) == 2 && num(cn["bounced"]) == 1 && num(cn["complained"]) == 1
	})

	// Users: a and b viewed pages today on the web, c three days ago in the app; identify and
	// Pulse's own events don't count.
	old := time.Now().UTC().AddDate(0, 0, -3).Format(time.RFC3339)
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": ext("a"), "name": "page_view", "props": map[string]any{"platform": "web", "utm_source": "newsletter"}},
		map[string]any{"external_id": ext("b"), "name": "page_view", "props": map[string]any{"platform": "web"}},
		map[string]any{"external_id": ext("c"), "name": "screen_view", "ts": old, "props": map[string]any{"platform": "ios"}},
		map[string]any{"external_id": ext("a"), "name": "product_viewed", "props": map[string]any{"product_code": "SKU-" + run, "product_category": "books"}},
		map[string]any{"external_id": ext("b"), "name": "product_viewed", "props": map[string]any{"product_code": "SKU-" + run, "product_category": "books"}},
		map[string]any{"external_id": ext("b"), "name": "add_to_cart", "props": map[string]any{"product_code": "SKU-" + run, "product_category": "books"}},
		map[string]any{"external_id": ext("a"), "name": "add_to_cart", "props": map[string]any{"product_code": "SKU-" + run, "product_category": "books"}},
		map[string]any{"external_id": ext("a"), "name": "purchase", "props": map[string]any{"product_code": "SKU-" + run, "product_category": "books", "amount": 499}},
	}}, 202)
	var users map[string]any
	waitFor(t, 75*time.Second, func() bool { // answers are cached for a minute
		users, _ = jsonCall(t, c, "GET", "/v1/analytics/users?from="+time.Now().UTC().AddDate(0, 0, -6).Format(time.DateOnly)+"&to="+
			time.Now().UTC().Format(time.DateOnly), nil, 200)["report"].(map[string]any)
		return num(users["dau"]) == 2 && num(users["wau"]) == 3
	})
	if num(users["mau"]) != 3 || num(users["new"]) != 3 || num(users["stickiness"]) <= 0 {
		t.Errorf("users: dau %v wau %v mau %v new %v stickiness %v", users["dau"], users["wau"], users["mau"], users["new"], users["stickiness"])
	}
	plat := fmt.Sprint(users["by_platform"])
	if !strings.Contains(plat, "key:web") || !strings.Contains(plat, "key:ios") {
		t.Errorf("by platform: %v", plat)
	}
	web, _ := jsonCall(t, c, "GET", "/v1/analytics/users?platform=web", nil, 200)["report"].(map[string]any)
	if num(web["dau"]) != 2 || num(web["mau"]) != 2 {
		t.Errorf("web only: dau %v mau %v", web["dau"], web["mau"])
	}

	// Products.
	var prod map[string]any
	waitFor(t, 70*time.Second, func() bool {
		prod, _ = jsonCall(t, c, "GET", "/v1/analytics/products?to="+time.Now().UTC().Format(time.DateOnly)+"&from="+
			time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly), nil, 200)["report"].(map[string]any)
		ps, _ := prod["products"].([]any)
		return len(ps) == 1 && num(ps[0].(map[string]any)["purchases"]) == 1
	})
	p := prod["products"].([]any)[0].(map[string]any)
	if p["code"] != "SKU-"+run || p["category"] != "books" || num(p["views"]) != 2 || num(p["carters"]) != 2 || num(p["buyers"]) != 1 ||
		num(p["revenue"]) != 499 || num(p["abandoned"]) != 1 {
		t.Errorf("product: %v", p)
	}

	// The live feed: the newest events, filtered by name, then only what is new after the cursor.
	var feed map[string]any
	waitFor(t, 20*time.Second, func() bool {
		feed = jsonCall(t, c, "GET", "/v1/live/events?name=purchase", nil, 200)
		evs, _ := feed["events"].([]any)
		return len(evs) == 1
	})
	cur := feed["cursor"].(string)
	if evs, _ := jsonCall(t, c, "GET", "/v1/live/events?cursor="+cur, nil, 200)["events"].([]any); len(evs) != 0 {
		t.Errorf("nothing is new after the cursor: %v", evs)
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext("a"), "name": "signup", "props": map[string]any{"method": "email"}}, 202)
	waitFor(t, 20*time.Second, func() bool {
		evs, _ := jsonCall(t, c, "GET", "/v1/live/events?cursor="+cur+"&contact="+url.QueryEscape(ext("a")), nil, 200)["events"].([]any)
		return len(evs) == 1 && evs[0].(map[string]any)["name"] == "signup"
	})
	// Another workspace sees none of it.
	other, _ := workspace(t, "Delivery other")
	if evs, _ := jsonCall(t, other, "GET", "/v1/live/events", nil, 200)["events"].([]any); len(evs) != 0 {
		t.Errorf("another workspace's live feed shows %v", evs)
	}
	if rows, _ := jsonCall(t, other, "GET", "/v1/analytics/campaigns", nil, 200)["campaigns"].([]any); len(rows) != 0 {
		t.Errorf("another workspace's campaigns dashboard shows %v", rows)
	}
}

// TestBounceMailboxLinks: a bounce email quoting the original message's headers, read from the
// bounce mailbox, is linked to the broadcast that sent it.
func TestBounceMailboxLinks(t *testing.T) {
	host := env("PULSE_HOOK_HOST", "")
	if host == "" {
		t.Skip("set PULSE_HOOK_HOST: run inside the compose network (make e2e)")
	}
	c, _ := workspace(t, "Bounce links")
	run := fmt.Sprint(time.Now().UnixNano())
	gone := "gone-m4-" + run + "@example.org"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": "gone-m4-" + run, "name": "identify", "email": gone, "attrs": map[string]any{"plan": "m4b" + run}}, 202)
	seg := jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "One", "rule": map[string]any{"attr": "plan", "op": "eq", "value": "m4b" + run}}, 201)["id"]
	waitSegmentSize(t, c, seg, 1)
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "x", "channel": "email", "subject": "Hi", "html": "<p>Hi</p>"}, 201)["id"]
	bc := jsonCall(t, c, "POST", "/v1/broadcasts", map[string]any{"name": "Bounce me", "channel": "email", "template_id": tpl,
		"segment_id": seg, "repeat": "none", "start_at": time.Now().UTC().Format(time.RFC3339)}, 200)["id"]
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/broadcasts/%v/schedule", bc), nil, 200)
	ref, _ := mailRef(t, gone)

	raw, err := os.ReadFile("../../internal/bounces/testdata/dsn_postfix.eml")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("gone@example.org"), []byte(gone))
	raw = bytes.Replace(raw, []byte("Subject: November offers"), []byte("Subject: November offers\nX-Pulse-Ref: "+ref), 1)
	raw = bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	be := memory.New()
	user, _ := be.Login(nil, "username", "password")
	inbox, _ := user.GetMailbox("INBOX")
	if err := inbox.CreateMessage([]string{}, time.Now(), bytes.NewBuffer(raw)); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(be)
	srv.AllowInsecureAuth = true
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	jsonCall(t, c, "PUT", "/v1/settings/bounce-mailbox", map[string]any{"enabled": true, "host": host, "port": ln.Addr().(*net.TCPAddr).Port,
		"tls": "none", "username": "username", "password": "password"}, 200)
	jsonCall(t, c, "POST", "/v1/settings/bounce-mailbox/check", nil, 202)
	waitFor(t, 60*time.Second, func() bool {
		m, _ := jsonCall(t, c, "GET", "/v1/contacts/gone-m4-"+run+"/messages", nil, 200)["messages"].([]any)
		return len(m) == 1 && m[0].(map[string]any)["status"] == "bounced" && m[0].(map[string]any)["source"] == "broadcast"
	})
	waitFor(t, 30*time.Second, func() bool {
		f, _ := jsonCall(t, c, "GET", fmt.Sprintf("/v1/broadcasts/%v/report", bc), nil, 200)["funnel"].(map[string]any)
		return num(f["hard_bounced"]) == 1
	})
	_ = io.Discard
}
