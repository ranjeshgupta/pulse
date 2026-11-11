package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/model"
)

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ in, cc, want string }{
		{"+91 98765-43210", "", "+919876543210"},
		{"098765 43210", "91", "+919876543210"},
		{"0044 20 7946 0958", "", "+442079460958"},
		{"98765 43210", "", ""}, // no country code to add
		{"12", "91", ""},
		{"", "91", ""},
	} {
		if got := E164(tc.in, tc.cc); got != tc.want {
			t.Errorf("E164(%q, %q) = %q, want %q", tc.in, tc.cc, got, tc.want)
		}
	}
	if NormalizeEmail("  Asha.Rao@Example.COM ") != "asha.rao@example.com" || NormalizeEmail("not-an-email") != "" {
		t.Error("NormalizeEmail")
	}
	fb := HashFor(KindFacebook, "A.B@gmail.com", "+91 98765 43210", "")
	if fb.EmailHash != Sha256Hex("a.b@gmail.com") || fb.PhoneHash != Sha256Hex("919876543210") {
		t.Errorf("facebook hashes = %+v", fb)
	}
	g := HashFor(KindGoogleAds, "A.B@gmail.com", "9876543210", "91")
	if g.EmailHash != Sha256Hex("ab@gmail.com") || g.PhoneHash != Sha256Hex("+919876543210") {
		t.Errorf("google hashes = %+v", g)
	}
	if !HashFor(KindFacebook, "", "", "").Empty() {
		t.Error("no identifiers should be empty")
	}
}

func TestValidate(t *testing.T) {
	base := Connector{Kind: KindHubSpot, Name: "CRM", Config: map[string]string{}, SyncMinutes: 60}
	secrets := map[string]string{"access_token": "pat-1"}
	if err := Validate(base, secrets); err != nil {
		t.Fatalf("valid connector: %v", err)
	}
	if err := Validate(base, nil); err == nil || !strings.Contains(err.Error(), "access token is required") {
		t.Errorf("missing secret: %v", err)
	}
	bad := base
	bad.Mapping = []FieldMap{{Remote: "hs_lead_score", Attr: "points"}}
	if err := Validate(bad, secrets); err == nil || !strings.Contains(err.Error(), "kept by Pulse") {
		t.Errorf("pulling into a reserved attribute must fail: %v", err)
	}
	ok := base
	ok.Mapping = []FieldMap{{Remote: "lifecyclestage", Attr: "stage", Dir: "push"}, {Remote: "email", Attr: "email"}}
	if err := Validate(ok, secrets); err != nil {
		t.Errorf("pushing a reserved attribute is fine: %v", err)
	}
	sf := Connector{Kind: KindSalesforce, Name: "SF", Config: map[string]string{"login_url": "https://x.my.salesforce.com"},
		Mapping: []FieldMap{{Remote: "Email; DROP", Attr: "email"}}}
	if err := Validate(sf, map[string]string{"client_id": "a", "client_secret": "b"}); err == nil || !strings.Contains(err.Error(), "Salesforce field") {
		t.Errorf("SOQL-unsafe field must fail: %v", err)
	}
	shop := Connector{Kind: KindShopify, Name: "S", Config: map[string]string{"shop": "evil.example.com"}}
	if err := Validate(shop, map[string]string{"webhook_secret": "x"}); err == nil {
		t.Error("a shop domain must be *.myshopify.com")
	}
	every := base
	every.SyncMinutes = 5
	if Validate(every, secrets) == nil {
		t.Error("pulls more often than every 15 minutes must fail")
	}
}

func TestSecretsMasking(t *testing.T) {
	stored := map[string]string{"access_token": "pat-na1-abcdefgh1234"}
	masked := MaskAll(stored)
	if strings.Contains(masked["access_token"], "abcdefgh") || !strings.HasSuffix(masked["access_token"], "1234") {
		t.Errorf("mask = %q", masked["access_token"])
	}
	// Sending the masked value (or nothing) back keeps the stored secret.
	if got := MergeSecrets(stored, masked); got["access_token"] != stored["access_token"] {
		t.Errorf("masked edit replaced the secret: %v", got)
	}
	if got := MergeSecrets(stored, map[string]string{"access_token": "new-token"}); got["access_token"] != "new-token" {
		t.Errorf("edit = %v", got)
	}
}

func TestShopify(t *testing.T) {
	body := SampleShopifyOrder("Asha@Example.com")
	sig := SignShopify("shh", body)
	if !VerifyShopify("shh", body, sig) || VerifyShopify("other", body, sig) || VerifyShopify("shh", append(body, ' '), sig) {
		t.Fatal("HMAC check")
	}
	in, err := ParseShopify("orders/create", body)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != EventOrderCompleted || in.Props["amount"] != 1499.0 || in.Props["currency"] != "INR" || in.Props["item_count"] != 2 {
		t.Errorf("order = %+v", in)
	}
	if in.Email != "Asha@Example.com" || in.Attrs["first_name"] != "Asha" || in.Attrs["shopify_customer_id"] != "115310627314723954" {
		t.Errorf("customer = %+v", in)
	}
	done := `{"id":1,"token":"t","email":"a@example.com","total_price":"10.00","completed_at":"2024-01-01T00:00:00Z"}`
	if _, err := ParseShopify("checkouts/update", []byte(done)); err == nil {
		t.Error("a completed checkout must be skipped")
	}
	open := `{"id":2,"token":"t2","email":"a@example.com","total_price":"10.00","currency":"USD","abandoned_checkout_url":"https://s/c","line_items":[{"title":"Mug","quantity":1,"price":"10.00"}]}`
	c, err := ParseShopify("checkouts/update", []byte(open))
	if err != nil || c.Name != EventCartUpdated || c.Props["url"] != "https://s/c" || c.Props["amount"] != 10.0 {
		t.Errorf("cart = %+v %v", c, err)
	}
	if cu, err := ParseShopify("customers/create", []byte(`{"id":5,"first_name":"Lin"}`)); err != nil || cu.CustomerID != "5" || cu.Name != EventCustomerCreated {
		t.Errorf("customer without email or phone = %+v %v", cu, err)
	}
	if _, err := ParseShopify("orders/create", []byte(`{"id":6,"total_price":"1.00"}`)); err == nil {
		t.Error("an order with nobody to record it on must be skipped")
	}
}

// ---- CRM against mock servers ----

type memStore struct {
	mu      sync.Mutex
	conns   map[int64]Connector
	log     []LogEntry
	byEmail map[string]string
	cursor  string
	status  string
}

func (m *memStore) GetIntegration(_ context.Context, tenant, id int64) (Connector, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	if !ok || c.TenantID != tenant {
		return c, ErrNotFound
	}
	return c, nil
}
func (m *memStore) ClaimDuePulls(context.Context, int) ([]Connector, error) { return nil, nil }
func (m *memStore) FinishPull(_ context.Context, _ int64, cursor, status, _ string, _ bool) error {
	m.cursor, m.status = cursor, status
	return nil
}
func (m *memStore) SetIntegrationStatus(_ context.Context, _ int64, status, _ string) error {
	m.status = status
	return nil
}
func (m *memStore) LogIntegration(_ context.Context, _ int64, e LogEntry) error {
	m.mu.Lock()
	m.log = append(m.log, e)
	m.mu.Unlock()
	return nil
}
func (m *memStore) ExternalIDByEmail(_ context.Context, _ int64, email string) (string, error) {
	return m.byEmail[strings.ToLower(email)], nil
}

type pub struct {
	mu  sync.Mutex
	evs []event.Event
}

func (p *pub) Publish(_ context.Context, recs ...*kgo.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range recs {
		var e event.Event
		_ = json.Unmarshal(r.Value, &e)
		p.evs = append(p.evs, e)
	}
	return nil
}

type hit struct {
	Method, Path, Auth string
	Body               map[string]any
}

func mock(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body map[string]any)) (*httptest.Server, *[]hit) {
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body == nil && len(raw) > 0 {
			body = map[string]any{"raw": string(raw)}
		}
		mu.Lock()
		hits = append(hits, hit{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		h(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestHubSpotPullAndPush(t *testing.T) {
	srv, hits := mock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch {
		case r.URL.Path == "/crm/v3/objects/contacts/search":
			io.WriteString(w, `{"results":[
				{"id":"101","properties":{"email":"asha@example.com","firstname":"Asha","lastmodifieddate":"2026-01-02T10:00:00Z"}},
				{"id":"102","properties":{"email":"","firstname":"NoMail","lastmodifieddate":"2026-01-03T10:00:00Z"}}]}`)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/crm/v3/objects/contacts/ravi"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/crm/v3/objects/contacts":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"555"}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	k, _ := Lookup(KindHubSpot)
	conn := Connector{ID: 7, TenantID: 1, Kind: KindHubSpot, Name: "HS", Active: true, Config: map[string]string{"base_url": srv.URL},
		Secrets: map[string]string{"access_token": "pat-1"}, Mapping: append(k.Mapping, FieldMap{Remote: "hs_score", Attr: "points"})}
	st := &memStore{conns: map[int64]Connector{7: conn}, byEmail: map[string]string{"asha@example.com": "u-asha"}}
	p := &pub{}
	svc := &Service{Store: st, HTTP: srv.Client(), Pub: p}
	n, cursor, more, err := svc.Pull(context.Background(), conn)
	if err != nil || n != 2 || more {
		t.Fatalf("pull = %d %v %v", n, more, err)
	}
	if !strings.HasPrefix(cursor, "2026-01-03T10:00:00Z 102") {
		t.Errorf("cursor = %q", cursor)
	}
	if (*hits)[0].Auth != "Bearer pat-1" {
		t.Errorf("auth = %q", (*hits)[0].Auth)
	}
	e := p.evs[0]
	if e.ExternalID != "u-asha" || e.Name != EventCRMSynced || e.Attrs["first_name"] != "Asha" || e.Attrs["hubspot_id"] != "101" {
		t.Errorf("event = %+v", e)
	}
	if _, set := e.Attrs["points"]; set {
		t.Error("a reserved attribute was pulled")
	}
	if p.evs[1].ExternalID != "hubspot-102" {
		t.Errorf("no-email record = %q", p.evs[1].ExternalID)
	}
	// The same records again at the cursor are skipped.
	conn.Cursor = cursor
	p.evs = nil
	if n, _, _, _ = svc.Pull(context.Background(), conn); n != 1 || p.evs[0].Attrs["hubspot_id"] != "101" {
		t.Errorf("second pull recorded %d", n)
	}

	// Push: no known id, no record with that email → created; the id comes back as hubspot_id.
	p.evs = nil
	remote, err := svc.PushContact(context.Background(), 1, 7, model.Contact{TenantID: 1, ID: 3, ExternalID: "ravi", Email: "ravi@example.com",
		Attrs: map[string]any{"first_name": "Ravi", "points": 40.0}}, "step-1")
	if err != nil || remote != "555" {
		t.Fatalf("push = %q %v", remote, err)
	}
	last := (*hits)[len(*hits)-1]
	props := last.Body["properties"].(map[string]any)
	if props["email"] != "ravi@example.com" || props["firstname"] != "Ravi" {
		t.Errorf("pushed = %v", props)
	}
	if len(p.evs) != 1 || p.evs[0].Name != EventCRMPushed || p.evs[0].Attrs["hubspot_id"] != "555" || p.evs[0].ID != "step-1" {
		t.Errorf("recorded = %+v", p.evs)
	}
	if _, err := svc.PushContact(context.Background(), 2, 7, model.Contact{}, ""); err == nil {
		t.Error("another workspace's connector must not be usable")
	}
}

func TestSalesforcePush(t *testing.T) {
	var srvURL string
	srv, hits := mock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch {
		case r.URL.Path == "/services/oauth2/token":
			io.WriteString(w, `{"access_token":"sf-tok","instance_url":"`+srvURL+`"}`)
		case strings.HasSuffix(r.URL.Path, "/query"):
			if strings.Contains(r.URL.Query().Get("q"), "Email = 'o\\'neil@example.com'") {
				io.WriteString(w, `{"records":[]}`)
				return
			}
			io.WriteString(w, `{"records":[{"attributes":{},"Id":"003A","Email":"z@example.com","FirstName":"Zoe","LastModifiedDate":"2026-01-02T10:00:00.000+0000"}]}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sobjects/Contact"):
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"003NEW","success":true}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	srvURL = srv.URL
	k, _ := Lookup(KindSalesforce)
	conn := Connector{ID: 8, TenantID: 1, Kind: KindSalesforce, Name: "SF", Active: true, Config: map[string]string{"login_url": srv.URL},
		Secrets: map[string]string{"client_id": "id", "client_secret": "sec"}, Mapping: k.Mapping}
	st := &memStore{conns: map[int64]Connector{8: conn}, byEmail: map[string]string{}}
	p := &pub{}
	svc := &Service{Store: st, HTTP: srv.Client(), Pub: p}
	remote, err := svc.PushContact(context.Background(), 1, 8, model.Contact{ID: 4, ExternalID: "x", Email: "o'neil@example.com",
		Attrs: map[string]any{"first_name": "Pat"}}, "")
	if err != nil || remote != "003NEW" {
		t.Fatalf("push = %q %v", remote, err)
	}
	created := (*hits)[len(*hits)-1]
	if created.Auth != "Bearer sf-tok" || created.Body["LastName"] != "[not provided]" || created.Body["Email"] != "o'neil@example.com" {
		t.Errorf("created = %+v", created)
	}
	n, _, _, err := svc.Pull(context.Background(), conn)
	if err != nil || n != 1 || p.evs[len(p.evs)-1].Attrs["salesforce_id"] != "003A" || p.evs[len(p.evs)-1].Email != "z@example.com" {
		t.Errorf("pull = %d %v %+v", n, err, p.evs)
	}
}

// ---- audience sync ----

type audStore struct {
	memStore
	syncs    []AudienceSync
	queue    map[int64]map[uint32]bool
	members  map[int64]map[uint32]Identity
	contacts map[uint32]model.Contact
	applied  []Outcome
}

func (a *audStore) ActiveAudienceSyncs(context.Context) ([]AudienceSync, error) { return a.syncs, nil }
func (a *audStore) QueueAudience(_ context.Context, sync int64, cs []uint32) error {
	if a.queue[sync] == nil {
		a.queue[sync] = map[uint32]bool{}
	}
	for _, c := range cs {
		a.queue[sync][c] = true
	}
	return nil
}
func (a *audStore) QueuedAudienceSyncs(context.Context) ([]int64, error) {
	var out []int64
	for id, q := range a.queue {
		if len(q) > 0 {
			out = append(out, id)
		}
	}
	return out, nil
}
func (a *audStore) ClaimAudienceQueue(_ context.Context, sync int64, _ int) ([]uint32, error) {
	var out []uint32
	for c := range a.queue[sync] {
		out = append(out, c)
	}
	a.queue[sync] = nil
	return out, nil
}
func (a *audStore) AudienceMembers(_ context.Context, sync int64, cs []uint32) (map[uint32]Identity, error) {
	out := map[uint32]Identity{}
	for _, c := range cs {
		if m, ok := a.members[sync][c]; ok {
			out[c] = m
		}
	}
	return out, nil
}
func (a *audStore) UploadedAfter(_ context.Context, sync int64, after uint32, _ int) ([]uint32, error) {
	var out []uint32
	for c := range a.members[sync] {
		if c > after {
			out = append(out, c)
		}
	}
	return out, nil
}
func (a *audStore) ApplyAudience(_ context.Context, sync int64, o Outcome) error {
	if a.members[sync] == nil {
		a.members[sync] = map[uint32]Identity{}
	}
	for _, k := range o.Keep {
		a.members[sync][k.ContactID] = k
	}
	for _, c := range o.Forget {
		delete(a.members[sync], c)
	}
	a.applied = append(a.applied, o)
	return nil
}
func (a *audStore) AudienceError(context.Context, int64, string) error { return nil }
func (a *audStore) ClaimDueReconciles(context.Context, time.Duration, int) ([]AudienceSync, error) {
	return nil, nil
}
func (a *audStore) FinishReconcile(context.Context, int64, int64) error { return nil }
func (a *audStore) ContactsByIDs(_ context.Context, _ int64, ids []uint32) ([]model.Contact, error) {
	var out []model.Contact
	for _, id := range ids {
		if c, ok := a.contacts[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

type fakeMembers map[uint32]bool

func (f fakeMembers) AreMembers(_ context.Context, _, _ int64, ids []uint32) ([]bool, error) {
	out := make([]bool, len(ids))
	for i, id := range ids {
		out[i] = f[id]
	}
	return out, nil
}
func (f fakeMembers) Load(context.Context, int64, int64) ([]byte, error) { return nil, nil }

func TestFacebookAudienceSync(t *testing.T) {
	srv, hits := mock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		io.WriteString(w, `{"audience_id":"99","num_received":1}`)
	})
	conn := Connector{ID: 9, TenantID: 1, Kind: KindFacebook, Name: "FB", Active: true,
		Config: map[string]string{"ad_account_id": "123", "base_url": srv.URL, "default_country": "91"}}
	st := &audStore{memStore: memStore{conns: map[int64]Connector{9: conn}}, queue: map[int64]map[uint32]bool{},
		members: map[int64]map[uint32]Identity{}, contacts: map[uint32]model.Contact{
			1: {ID: 1, Email: "asha@example.com", Attrs: map[string]any{}},
			2: {ID: 2, Email: "out@example.com", Attrs: map[string]any{"optout_all": true}},
			3: {ID: 3, Attrs: map[string]any{"phone": "98765 43210"}},
			4: {ID: 4, Attrs: map[string]any{}}, // nothing to match on
		}}
	sync := AudienceSync{ID: 5, TenantID: 1, IntegrationID: 9, SegmentID: 3, AudienceID: "99"}
	st.syncs = []AudienceSync{sync}
	members := fakeMembers{1: true, 2: true, 3: true, 4: true}
	s := &Syncer{Store: st, Members: members, Service: &Service{Store: st, HTTP: srv.Client()}}

	// A membership change queues the contact for the audience of its segment.
	rec := func(c uint32, entered bool) *kgo.Record {
		b, _ := json.Marshal(model.MembershipChange{TenantID: 1, SegmentID: 3, ContactID: c, Entered: entered})
		return &kgo.Record{Topic: event.TopicMembership, Value: b}
	}
	if err := s.HandleBatch(context.Background(), []*kgo.Record{rec(1, true), rec(2, true), rec(3, true), rec(4, true)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*hits) != 1 || (*hits)[0].Method != http.MethodPost || (*hits)[0].Path != "/99/users" {
		t.Fatalf("calls = %+v", *hits)
	}
	data := (*hits)[0].Body["payload"].(map[string]any)["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("uploaded %d, want 2 (the unsubscribed contact and the one without identifiers stay out)", len(data))
	}
	for _, row := range data {
		for _, v := range row.([]any) {
			if s := v.(string); s != "" && (len(s) != 64 || strings.Contains(s, "@")) {
				t.Errorf("not a SHA-256: %q", s)
			}
		}
	}
	if o := st.applied[0]; o.Added != 2 || o.Skipped != 2 {
		t.Errorf("outcome = %+v", o)
	}

	// Contact 1 leaves the segment: removed with the same hashes.
	delete(members, 1)
	_ = s.HandleBatch(context.Background(), []*kgo.Record{rec(1, false)})
	_ = s.Flush(context.Background())
	last := (*hits)[len(*hits)-1]
	if last.Method != http.MethodDelete {
		t.Fatalf("removal = %+v", last)
	}
	row := last.Body["payload"].(map[string]any)["data"].([]any)[0].([]any)
	if row[0] != Sha256Hex("asha@example.com") {
		t.Errorf("removed %v", row)
	}
	if _, still := st.members[5][1]; still {
		t.Error("contact 1 is still recorded as uploaded")
	}

	// Unsubscribing from everything (a changed attribute on events.enriched) takes contact 3 out.
	c3 := st.contacts[3]
	c3.Attrs = map[string]any{"phone": "98765 43210", "optout_all": true}
	st.contacts[3] = c3
	b, _ := json.Marshal(event.Event{TenantID: 1, ContactID: 3, Name: "unsubscribed", ChangedAttrs: []string{"optout_all"}})
	_ = s.HandleBatch(context.Background(), []*kgo.Record{{Topic: event.TopicEnriched, Value: b}})
	_ = s.Flush(context.Background())
	if last := (*hits)[len(*hits)-1]; last.Method != http.MethodDelete || len(st.members[5]) != 0 {
		t.Errorf("unsubscribed contact still uploaded: %+v %v", last, st.members[5])
	}
}

func TestGoogleUpload(t *testing.T) {
	srv, hits := mock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch {
		case r.URL.Path == "/token":
			io.WriteString(w, `{"access_token":"g-tok","expires_in":3600}`)
		case strings.HasSuffix(r.URL.Path, "offlineUserDataJobs:create"):
			io.WriteString(w, `{"resourceName":"customers/1234567890/offlineUserDataJobs/77"}`)
		default:
			io.WriteString(w, `{}`)
		}
	})
	conn := Connector{Kind: KindGoogleAds, Config: map[string]string{"customer_id": "123-456-7890", "base_url": srv.URL, "token_url": srv.URL + "/token",
		"login_customer_id": "999"}, Secrets: map[string]string{"developer_token": "dev", "client_id": "c", "client_secret": "s", "refresh_token": "r"}}
	p, _ := NewPlatform(conn, srv.Client())
	if err := p.Add(context.Background(), "42", []Identity{{EmailHash: Sha256Hex("a@example.com")}}); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, h := range *hits {
		paths = append(paths, h.Path)
	}
	want := []string{"/token", "/customers/1234567890/offlineUserDataJobs:create", "/customers/1234567890/offlineUserDataJobs/77:addOperations",
		"/customers/1234567890/offlineUserDataJobs/77:run"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("calls = %v", paths)
	}
	job := (*hits)[1].Body["job"].(map[string]any)["customerMatchUserListMetadata"].(map[string]any)
	if job["userList"] != "customers/1234567890/userLists/42" {
		t.Errorf("list = %v", job)
	}
	ops := (*hits)[2].Body["operations"].([]any)
	if _, ok := ops[0].(map[string]any)["create"]; !ok {
		t.Errorf("ops = %v", ops)
	}
}
