//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// mailParts reads the newest email to `to` from Mailpit and returns its alternative parts in order.
func mailParts(t *testing.T, to string) ([]string, map[string]string) {
	t.Helper()
	var search struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	waitFor(t, 20*time.Second, func() bool {
		mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+to), &search)
		return len(search.Messages) >= 1
	})
	resp, err := http.Get(mailpitURL + "/api/v1/message/" + search.Messages[0].ID + "/raw")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if mt != "multipart/alternative" {
		t.Fatalf("top-level %s", mt)
	}
	var kinds []string
	bodies := map[string]string{}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextRawPart()
		if err != nil {
			break
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		var r io.Reader = p
		if p.Header.Get("Content-Transfer-Encoding") == "quoted-printable" {
			r = quotedprintable.NewReader(p)
		}
		b, _ := io.ReadAll(r)
		kinds = append(kinds, ct)
		bodies[ct] = string(b)
	}
	return kinds, bodies
}

// TestM6AMPEmailAndPollPostBack: a template built with AMP blocks goes to Mailpit with plain-text,
// AMP and HTML parts; its poll posts back to Pulse with the signed link and the AMP CORS headers,
// and the answer is recorded as an event on the contact.
func TestM6AMPEmailAndPollPostBack(t *testing.T) {
	c, _ := workspace(t, "M6 AMP")
	run := fmt.Sprint(time.Now().UnixNano())
	ext, email := "amp-"+run, "amp-"+run+"@example.com"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": ext, "name": "identify", "email": email, "attrs": map[string]any{"first_name": "Meera"}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+ext, nil, -1)["contact"] != nil })
	blocks := map[string]any{"blocks": []any{
		map[string]any{"id": "hi", "type": "heading", "text": `Hi {{contact.first_name | default:"there"}}`},
		map[string]any{"id": "colour", "type": "poll", "text": "Which colour?", "options": []string{"Red", "Blue"}, "show_results": true},
	}}
	// Broken hand-written AMP is refused with line numbers.
	bad := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "bad amp", "channel": "email", "subject": "s", "html": "<p>x</p>",
		"content": map[string]any{"amp": "<!doctype html>\n<html>\n<body><img src=\"x\"></body></html>"}}, 400)
	if !strings.Contains(fmt.Sprint(bad["error"]), "line 2") {
		t.Errorf("bad AMP: %v", bad)
	}
	tpl := jsonCall(t, c, "POST", "/v1/templates", map[string]any{"name": "amp poll", "channel": "email", "subject": "Vote, {{contact.first_name}}",
		"html": "<p>Hi {{contact.first_name}}</p>", "content": map[string]any{"amp_blocks": blocks}}, 201)["id"]
	saved := jsonCall(t, c, "GET", fmt.Sprintf("/v1/templates/%v", tpl), nil, 200)
	if amp, _ := saved["content"].(map[string]any)["amp"].(string); !strings.Contains(amp, "⚡4email") {
		t.Fatalf("AMP not built on save: %v", saved["content"])
	}
	jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": ext}, 202)
	kinds, bodies := mailParts(t, email)
	if strings.Join(kinds, ",") != "text/plain,text/x-amp-html,text/html" {
		t.Fatalf("parts %v", kinds)
	}
	amp := bodies["text/x-amp-html"]
	if !strings.Contains(amp, "Hi Meera") || strings.Contains(amp, "pulse:submit") {
		t.Fatalf("AMP part:\n%s", amp)
	}
	m := regexp.MustCompile(`action-xhr="([^"]+)"`).FindStringSubmatch(amp)
	if m == nil {
		t.Fatal("no post-back link")
	}
	link := html.UnescapeString(m[1])
	u, _ := url.Parse(link)
	post := apiURL + u.Path // the link names the public URL; this test may reach Pulse by another address
	answer := func(sender string) *http.Response {
		req, _ := http.NewRequest("POST", post, strings.NewReader(url.Values{"answer": {"Blue"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("AMP-Email-Sender", sender)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := answer("someone@evil.example"); r.StatusCode != 403 || r.Header.Get("AMP-Email-Allow-Sender") != "" {
		t.Errorf("foreign sender: %d", r.StatusCode)
	}
	r := answer("noreply@pulse.local") // the server's default sender (no provider in this workspace)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || r.Header.Get("AMP-Email-Allow-Sender") != "noreply@pulse.local" || !strings.Contains(string(body), `"label":"Blue","count":1`) {
		t.Fatalf("answer: %d %s %v", r.StatusCode, body, r.Header)
	}
	waitFor(t, 15*time.Second, func() bool { return eventsNamed(t, c, ext, "amp_poll_answered") == 1 })
	// A second answer doesn't count again.
	r = answer("noreply@pulse.local")
	body, _ = io.ReadAll(r.Body)
	if !strings.Contains(string(body), `"already":true`) {
		t.Errorf("second answer: %s", body)
	}
	res := jsonCall(t, c, "GET", fmt.Sprintf("/v1/templates/%v/amp/results", tpl), nil, 200)
	if b, _ := json.Marshal(res); !strings.Contains(string(b), `"total":1`) {
		t.Errorf("results %s", b)
	}
}

// TestM6ContentVariantsOrderAndOverlap: variants are decided in their order, with device rules, and
// the overlap view counts contacts in several variants' audiences.
func TestM6ContentVariantsOrderAndOverlap(t *testing.T) {
	c, siteKey := workspace(t, "M6 variants")
	run := fmt.Sprint(time.Now().UnixNano())
	vip1, vip2, other := "vip1-"+run, "vip2-"+run, "other-"+run
	for _, e := range []string{vip1, vip2} {
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": e, "name": "identify", "attrs": map[string]any{"tier": "vip"}}, 202)
	}
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": other, "name": "identify", "attrs": map[string]any{"tier": "basic"}}, 202)
	seg := idOf(jsonCall(t, c, "POST", "/v1/segments", map[string]any{"name": "VIP " + run, "rule": map[string]any{"attr": "tier", "op": "eq", "value": "vip"}}, 201)["id"])
	waitFor(t, 20*time.Second, func() bool { return inSeg(t, c, vip1, seg) && inSeg(t, c, vip2, seg) })

	slot := map[string]any{"slot": "hero", "name": "Hero", "status": "active", "default_html": "default", "start_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		"variants": []any{
			map[string]any{"name": "VIP on phones", "html": "vip-mobile", "audience": map[string]any{"segment_ids": []int64{seg}, "devices": []string{"mobile"}}},
			map[string]any{"name": "VIP", "html": "vip", "audience": map[string]any{"segment_ids": []int64{seg}}},
		}}
	saved := jsonCall(t, c, "POST", "/v1/dwc", slot, 200)
	id := idOf(saved["id"])
	if vs, _ := saved["variants"].([]any); len(vs) != 2 || vs[0].(map[string]any)["id"] == "" {
		t.Fatalf("saved %v", saved)
	}
	decide := func(ext, device string) string {
		req, _ := http.NewRequest("POST", apiURL+"/v1/web/decide", strings.NewReader(fmt.Sprintf(`{"external_id":%q,"url":"https://shop.example/","slots":["hero"],"device":%q}`, ext, device)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Pulse-Site-Key", siteKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var d struct{ Content map[string]string }
		_ = json.NewDecoder(resp.Body).Decode(&d)
		return d.Content["hero"]
	}
	if got := decide(vip1, "mobile"); got != "vip-mobile" {
		t.Errorf("vip on mobile: %q", got)
	}
	if got := decide(vip1, "desktop"); got != "vip" {
		t.Errorf("vip on desktop: %q", got)
	}
	if got := decide(other, "mobile"); got != "default" {
		t.Errorf("others: %q", got)
	}
	ov := jsonCall(t, c, "POST", "/v1/dwc/overlap", map[string]any{"slot_id": id}, 200)
	vs := ov["variants"].([]any)
	v0, v1 := vs[0].(map[string]any), vs[1].(map[string]any)
	if v0["matching"] != float64(2) || v0["sees"] != float64(2) || v1["matching"] != float64(2) || v1["sees"] != float64(0) || ov["multi"] != float64(2) || ov["approximate"] != true {
		t.Errorf("overlap %v", ov)
	}
	// Reversed, the broad variant comes first and hides the phone one: it's reported as never shown,
	// and decided accordingly.
	slot["variants"] = []any{slot["variants"].([]any)[1], slot["variants"].([]any)[0]}
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/dwc/%d", id), slot, 200)
	got := jsonCall(t, c, "GET", fmt.Sprintf("/v1/dwc/%d", id), nil, 200)
	if sh, _ := got["shadowed"].([]any); len(sh) != 1 || sh[0].(map[string]any)["index"] != float64(1) {
		t.Errorf("shadowed %v", got["shadowed"])
	}
	waitFor(t, 15*time.Second, func() bool { return decide(vip1, "mobile") == "vip" }) // the decision cache lasts 10 s
}

// TestM6WebPushInstallCheck: the install check fetches a site served on the compose network (allowed
// only because PULSE_ALLOW_PRIVATE_WEBHOOKS is set in this environment) and reports each step.
func TestM6WebPushInstallCheck(t *testing.T) {
	c, siteKey := workspace(t, "M6 push")
	pulse := "http://localhost:7080"
	base := providerMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `<html><body><script src="%s/sdk/pulse.js" defer onload="Pulse.init({ siteKey: '%s' })"></script></body></html>`, pulse, siteKey)
		case "/pulse-sw.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprintf(w, `importScripts("%s/sdk/pulse-sw.js");`, pulse)
		default:
			http.NotFound(w, r)
		}
	})
	jsonCall(t, c, "PUT", "/v1/settings/web-push", map[string]any{"site_url": base + "/", "service_worker": "/pulse-sw.js",
		"prompt": map[string]any{"enabled": true, "style": "bell", "text": "Get offers", "allow": "Allow", "later": "Later", "delay_seconds": 3, "repeat_days": 7}}, 200)
	rep := jsonCall(t, c, "POST", "/v1/settings/web-push/check", map[string]any{}, 200)
	got := map[string]string{}
	for _, it := range rep["items"].([]any) {
		m := it.(map[string]any)
		got[m["key"].(string)] = m["status"].(string)
	}
	for k, v := range map[string]string{"https": "fail", "page": "ok", "snippet": "ok", "site_key": "ok", "service_worker": "ok", "sw_imports": "ok", "vapid": "ok"} {
		if got[k] != v {
			t.Errorf("%s = %s, want %s (%v)", k, got[k], v, rep["items"])
		}
	}
	// The SDK gets the prompt with its push config.
	resp, err := http.Get(apiURL + "/v1/web/push-config?site_key=" + siteKey)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"text":"Get offers"`) || !strings.Contains(string(b), `"service_worker":"/pulse-sw.js"`) {
		t.Errorf("push-config %s", b)
	}
	// The browser self-check: a link token, the SDK's report, the result in the app.
	tok := jsonCall(t, c, "POST", "/v1/settings/web-push/check-token", nil, 200)["token"].(string)
	req, _ := http.NewRequest("POST", apiURL+"/v1/web/diagnose?site_key="+siteKey, strings.NewReader(`{"token":"`+tok+`","report":{"permission":"default","push_supported":true,"https":false,"problems":["not https"]}}`))
	req.Header.Set("Content-Type", "application/json")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 204 {
		t.Fatalf("diagnose: %v %v", r, err)
	}
	d := jsonCall(t, c, "GET", "/v1/settings/web-push/diagnosis?token="+tok, nil, 200)
	if rp, _ := d["report"].(map[string]any); rp == nil || rp["permission"] != "default" {
		t.Errorf("diagnosis %v", d)
	}
}
