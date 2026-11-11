package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/live"
	"pulse/internal/model"
	"pulse/internal/segment"
	"pulse/internal/store/pg"
	"pulse/internal/tracking"
)

// bounceMem is a store for the email webhook: two workspaces, each with its bounce token.
type bounceMem struct {
	*memStore
	keys map[int64][]byte
	byID map[int64]map[uint32]model.Contact
}

func (b *bounceMem) TenantByBounceToken(_ context.Context, tok string) (int64, error) {
	switch tok {
	case "tok-1":
		return 1, nil
	case "tok-2":
		return 2, nil
	}
	return 0, pg.ErrNotFound
}
func (b *bounceMem) BounceToken(_ context.Context, t int64) (string, error) {
	return "tok-" + strconv.FormatInt(t, 10), nil
}
func (b *bounceMem) EmailWebhookKeys(_ context.Context, t int64) ([]byte, error) {
	return b.keys[t], nil
}
func (b *bounceMem) SetEmailWebhookKeys(_ context.Context, t int64, sealed []byte) error {
	b.keys[t] = sealed
	return nil
}
func (b *bounceMem) ContactByEmail(_ context.Context, t int64, email string) (model.Contact, error) {
	for _, c := range b.byID[t] {
		if c.Email == email {
			return c, nil
		}
	}
	return model.Contact{}, pg.ErrNotFound
}
func (b *bounceMem) ContactByID(_ context.Context, t int64, id uint32) (model.Contact, error) {
	if c, ok := b.byID[t][id]; ok {
		return c, nil
	}
	return model.Contact{}, pg.ErrNotFound
}

func pubEvents(t *testing.T, p *memPub) []event.Event {
	t.Helper()
	var out []event.Event
	for _, r := range p.recs {
		var e event.Event
		if err := json.Unmarshal(r.Value, &e); err == nil {
			out = append(out, e)
		}
	}
	p.recs = nil
	return out
}

func TestEmailWebhookLinksAndVerifies(t *testing.T) {
	_, base, _ := newServer()
	box, _ := auth.NewBox(strings.Repeat("cd", 32))
	codec := tracking.New(box, strings.Repeat("cd", 32), "https://pulse.test")
	st := &bounceMem{memStore: base, keys: map[int64][]byte{}, byID: map[int64]map[uint32]model.Contact{
		1: {11: {TenantID: 1, ID: 11, ExternalID: "ann", Email: "ann@x.co"}},
		2: {11: {TenantID: 2, ID: 11, ExternalID: "zed", Email: "zed@y.co"}},
	}}
	pub := &memPub{}
	s := New(Deps{Store: st, Pub: pub, Box: box, Tracking: codec, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	h := s.Handler()
	post := func(tok, body string, hdr map[string]string) int {
		req := httptest.NewRequest("POST", "/v1/bounces/"+tok, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	ref := codec.Ref(tracking.Ref{TenantID: 1, ContactID: 11, MessageID: "bc4:9:11", TemplateID: 2, BroadcastID: 4, Run: 9})
	body := `[{"email":"ann@x.co","event":"delivered","pulse_ref":"` + ref + `"},{"email":"ann@x.co","event":"bounce","type":"bounce","reason":"550 5.1.1","pulse_ref":"` + ref + `"}]`
	if code := post("tok-1", body, nil); code != 200 {
		t.Fatalf("webhook: %d", code)
	}
	evs := pubEvents(t, pub)
	if len(evs) != 2 || evs[0].Name != "email_delivered" || evs[1].Name != "email_bounced" || evs[0].TenantID != 1 ||
		evs[1].Props["broadcast_id"] != float64(4) || evs[1].Props["reason_code"] != "hard_bounce" || evs[0].ExternalID != "ann" {
		t.Fatalf("linked events: %+v", evs)
	}
	// Workspace 2 posting workspace 1's reference: never linked to workspace 1's contact.
	if code := post("tok-2", body, nil); code != 200 {
		t.Fatalf("webhook 2: %d", code)
	}
	for _, e := range pubEvents(t, pub) {
		if e.ExternalID == "ann" || e.TenantID != 2 || e.Props["message_id"] != nil {
			t.Errorf("cross-workspace link: %+v", e)
		}
	}
	// Once a SendGrid key is saved, unsigned SendGrid posts are refused; signed ones pass.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	w := do(t, h, "PUT", "/v1/settings/bounces/signing", "key-1", `{"sendgrid_public_key":"`+base64.StdEncoding.EncodeToString(der)+`"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sendgrid":true`) || strings.Contains(w.Body.String(), "MFk") {
		t.Fatalf("save key: %d %s", w.Code, w.Body)
	}
	if code := post("tok-1", body, nil); code != 401 {
		t.Errorf("unsigned after the key: %d, want 401", code)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256(append([]byte(ts), body...))
	sig, _ := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if code := post("tok-1", body, map[string]string{"X-Twilio-Email-Event-Webhook-Signature": base64.StdEncoding.EncodeToString(sig),
		"X-Twilio-Email-Event-Webhook-Timestamp": ts}); code != 200 {
		t.Errorf("signed: %d", code)
	}
	// Other formats aren't affected by the SendGrid key; basic auth then guards them.
	if code := post("tok-1", `{"RecordType":"SpamComplaint","Email":"ann@x.co"}`, nil); code != 200 {
		t.Errorf("postmark: %d", code)
	}
	do(t, h, "PUT", "/v1/settings/bounces/signing", "key-1", `{"basic_user":"pm","basic_password":"long-enough-pass"}`)
	if code := post("tok-1", `{"RecordType":"SpamComplaint","Email":"ann@x.co"}`, nil); code != 401 {
		t.Errorf("postmark without basic auth: %d", code)
	}
	if code := post("tok-1", `{"RecordType":"SpamComplaint","Email":"ann@x.co"}`, map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("pm:long-enough-pass"))}); code != 200 {
		t.Errorf("postmark with basic auth: %d", code)
	}
	if w := do(t, h, "GET", "/v1/settings/bounces", "key-1", ""); !strings.Contains(w.Body.String(), `"basic":true`) || strings.Contains(w.Body.String(), "long-enough-pass") {
		t.Errorf("settings show status, never secrets: %s", w.Body)
	}
	if w := do(t, h, "PUT", "/v1/settings/bounces/signing", "key-1", `{"clear":["sendgrid","basic"]}`); !strings.Contains(w.Body.String(), `"sendgrid":false`) {
		t.Errorf("clear: %s", w.Body)
	}
	// SES through SNS is always signature-checked.
	if code := post("tok-1", `{"Type":"Notification","Message":"{}","SigningCertURL":"https://evil.test/x.pem"}`, nil); code != 401 {
		t.Errorf("unsigned SNS: %d", code)
	}
}

func TestContactReach(t *testing.T) {
	c := model.Contact{Email: "a@x.co", Attrs: map[string]any{"phone": "+919812345678", "optout_whatsapp": true, "sms_bounced": true}}
	got := map[string]ChannelReach{}
	for _, r := range ContactReach(c, []model.Device{{Token: "t", Platform: "android"}}) {
		got[r.Channel] = r
	}
	if !got["email"].Reachable || got["sms"].Reachable || got["sms"].Why != "bounced" || got["whatsapp"].Reachable ||
		got["whatsapp"].Why != "opted out" || !got["rcs"].Reachable || !got["push"].Reachable || got["push"].Address != "1 device" {
		t.Errorf("reach: %+v", got)
	}
	none := ContactReach(model.Contact{Attrs: map[string]any{"phone": "9812345678"}}, nil)
	for _, r := range none {
		if r.Channel == "sms" && (r.Reachable || !strings.Contains(r.Why, "international")) {
			t.Errorf("local number: %+v", r)
		}
		if r.Channel == "email" && r.Why != "no email address" {
			t.Errorf("no email: %+v", r)
		}
	}
}

func TestLiveFilterAndBuyersRule(t *testing.T) {
	_ = liveMatch
	if !liveMatch(liveItem("purchase", "Ann@x.co"), "purchase", "ann") || liveMatch(liveItem("page_view", "a"), "purchase", "") ||
		!liveMatch(liveItem("email_opened", "a"), "email_*", "") || liveMatch(liveItem("x", "bob"), "", "ann") {
		t.Error("live filters")
	}
	r := BuyersRule("purchase", "SKU-1", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC))
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"from":"2026-11-01"`) || !strings.Contains(string(b), `"value":"SKU-1"`) {
		t.Errorf("rule %s", b)
	}
	if _, err := segment.Parse(b); err != nil {
		t.Errorf("the buyers rule must be a valid segment rule: %v", err)
	}
}

func liveItem(name, ext string) live.Item { return live.Item{Name: name, ExternalID: ext} }
