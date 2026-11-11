package journey

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// ---- fakes for the builder's collaborators ----

type fakeData struct {
	users    []model.User
	company  pg.Company
	merged   map[string]any
	scoreKey map[string]bool
	members  []string
	states   map[int64]pg.JourneyState
	devices  []model.Device
	unread   int
	form     map[string]any
}

func (f *fakeData) ListUsers(context.Context, int64) ([]model.User, error) { return f.users, nil }
func (f *fakeData) GetCompany(_ context.Context, _, id int64) (pg.Company, error) {
	if f.company.ID != id {
		return pg.Company{}, pg.ErrNotFound
	}
	return f.company, nil
}
func (f *fakeData) MergeCompanyAttrs(_ context.Context, _, id int64, attrs map[string]any) (pg.Company, error) {
	f.merged = attrs
	return f.company, nil
}
func (f *fakeData) AddCompanyScore(_ context.Context, _, id int64, key string, delta int) (pg.Company, bool, error) {
	if f.scoreKey == nil {
		f.scoreKey = map[string]bool{}
	}
	if f.scoreKey[key] {
		return f.company, false, nil
	}
	f.scoreKey[key] = true
	f.company.Attrs["score"] = f.company.Attrs["score"].(float64) + float64(delta)
	return f.company, true, nil
}
func (f *fakeData) MemberIDs(context.Context, int64, string, string, uint32, int) ([]string, uint32, error) {
	return f.members, 0, nil
}
func (f *fakeData) JourneyStates(context.Context, int64, uint32, []int64) (map[int64]pg.JourneyState, error) {
	return f.states, nil
}
func (f *fakeData) ContactDevices(context.Context, int64, uint32) ([]model.Device, error) {
	return f.devices, nil
}
func (f *fakeData) UnreadInbox(context.Context, int64, uint32) (int, error)       { return f.unread, nil }
func (f *fakeData) EnrollmentForm(context.Context, int64) (map[string]any, error) { return f.form, nil }

type fakeOnsite struct{ focus, dwc []string }

func (f *fakeOnsite) PushFocus(_ context.Context, _ int64, c uint32, id int64) error {
	f.focus = append(f.focus, fmt.Sprintf("%d:%d", c, id))
	return nil
}
func (f *fakeOnsite) PushDWC(_ context.Context, _ int64, c uint32, slot, html string, ttl time.Duration) error {
	f.dwc = append(f.dwc, fmt.Sprintf("%d:%s:%s:%v", c, slot, html, ttl))
	return nil
}

type fakeHistory struct {
	visits uint64
	form   map[string]any
}

func (f *fakeHistory) CountVisits(_ context.Context, _ int64, _ uint32, url string, days int) (uint64, error) {
	return f.visits, nil
}
func (f *fakeHistory) LatestForm(context.Context, int64, uint32, int64) (map[string]any, error) {
	return f.form, nil
}

type fakeDeleter struct{ deleted []uint32 }

func (f *fakeDeleter) DeleteContacts(_ context.Context, _ int64, ids []uint32) error {
	f.deleted = append(f.deleted, ids...)
	return nil
}

type fakeMX map[string]bool

func (m fakeMX) AcceptsMail(_ context.Context, d string) bool { return m[d] }

type fakeStats map[string]int

func (f fakeStats) Count(_ context.Context, _, _ int64, step, out string) { f[step+"|"+out]++ }

func builderExecutor(t *testing.T, attrs map[string]any) (*Executor, *fakeStore, *fakeSender, *fakeEvents, *fakeData) {
	t.Helper()
	x, store, sender, evs := executor(t, nil)
	c := model.Contact{ID: 42, TenantID: 1, ExternalID: "u42", Email: "u42@x.test", Attrs: map[string]any{"first_name": "Asha", "phone": "+919812345678"}}
	for k, v := range attrs {
		c.Attrs[k] = v
	}
	x.Contacts = fakeContacts{42: c}
	data := &fakeData{users: []model.User{{ID: 5, Email: "owner@team.test", Name: "Ravi"}, {ID: 6, Email: "sales@team.test"}},
		company: pg.Company{ID: 9, Name: "Acme", Attrs: map[string]any{"score": float64(10)}}, members: []string{"u42", "u43"}}
	x.Data, x.Onsite, x.History, x.Deleter, x.MX, x.Stats = data, &fakeOnsite{}, &fakeHistory{}, &fakeDeleter{}, fakeMX{"x.test": true}, fakeStats{}
	return x, store, sender, evs, data
}

const start = `{"trigger":{"type":"event","event":"signup"},"entry":"s","nodes":{"s":%s,"yes":{"type":"exit"},"no":{"type":"exit"},"x":{"type":"exit"}}}`

func one(t *testing.T, node string) Graph {
	t.Helper()
	return graph(t, fmt.Sprintf(start, node))
}

// ---- validation ----

func TestBuilderStepValidation(t *testing.T) {
	bad := map[string]string{
		`{"type":"on_form","seconds":30,"yes":"yes","no":"no"}`:                                                               "1 minute to 365 days",
		`{"type":"on_page","url":"/a","page_id":2,"seconds":3600,"yes":"yes","no":"no"}`:                                      "not both",
		`{"type":"on_device","seconds":3600,"yes":"yes","no":"no"}`:                                                           "device type or",
		`{"type":"on_device","device_type":"phone","seconds":3600,"yes":"yes","no":"no"}`:                                     "desktop, mobile or tablet",
		`{"type":"on_dwc","slot":"Bad Slot","seconds":3600,"yes":"yes","no":"no"}`:                                            "slot name",
		`{"type":"if_journey","journey_state":"active","yes":"yes","no":"no"}`:                                                "1 to 20 journeys",
		`{"type":"if_journey","journey_ids":[2],"journey_state":"maybe","yes":"yes","no":"no"}`:                               "in, has completed",
		`{"type":"if_form_field","field":"bad-key","op":"eq","yes":"yes","no":"no"}`:                                          "choose a field",
		`{"type":"if_form_field","field":"city","op":"regex","yes":"yes","no":"no"}`:                                          "choose a field",
		`{"type":"if_notification","notify":"sms","yes":"yes","no":"no"}`:                                                     "push, app inbox",
		`{"type":"if_dnc","channel":"fax","yes":"yes","no":"no"}`:                                                             "channel, or any",
		`{"type":"if_before","until":"tomorrow","yes":"yes","no":"no"}`:                                                       "choose the date",
		`{"type":"if_visited","days":400,"yes":"yes","no":"no"}`:                                                              "1 to 365 days",
		`{"type":"notify_user","next":"x"}`:                                                                                   "email template",
		`{"type":"notify_user","template_id":1,"next":"x"}`:                                                                   "who gets it",
		`{"type":"notify_user","template_id":1,"emails":["not-an-address"],"next":"x"}`:                                       "isn't an email address",
		`{"type":"send_message","next":"x"}`:                                                                                  "add 1 to",
		`{"type":"send_message","channels":[{"channel":"sms","template_id":1},{"channel":"sms","template_id":2}],"next":"x"}`: "each channel once",
		`{"type":"send_message","channels":[{"channel":"email"}],"next":"x"}`:                                                 "choose a email template",
		`{"type":"show_focus","next":"x"}`:                                                                                    "focus item",
		`{"type":"push_dwc","slot":"hero","next":"x"}`:                                                                        "enter the content",
		`{"type":"update_owner","next":"x"}`:                                                                                  "choose the owner",
		`{"type":"update_company","company_attrs":{"name":"x"},"next":"x"}`:                                                   "not id or name",
		`{"type":"update_company","company_attrs":{"tier":[1]},"next":"x"}`:                                                   "text, a number",
		`{"type":"add_company","next":"x"}`:                                                                                   "choose a company",
		`{"type":"company_points","next":"x"}`:                                                                                "non-zero",
		`{"type":"change_journey","next":"x"}`:                                                                                "choose journeys",
		`{"type":"change_journey","add_journeys":[2],"remove_journeys":[2],"next":"x"}`:                                       "both added to and removed",
		`{"type":"update_list","add_lists":[3],"remove_lists":[3],"next":"x"}`:                                                "both added to and removed",
		`{"type":"update_list","next":"x"}`:                                                                                   "choose a static segment",
		`{"type":"jump","target":"nowhere"}`:                                                                                  "choose the step",
		`{"type":"jump","target":"s"}`:                                                                                        "itself",
		`{"type":"dnc_add","channel":"fax","next":"x"}`:                                                                       "channel, or all",
		`{"type":"delete_contact"}`:                                                                                           "type DELETE",
		`{"type":"delete_contact","confirm":"delete"}`:                                                                        "type DELETE",
		`{"type":"update_attr","attrs":{"nick":"{contactfield=bad key}"},"next":"x"}`:                                         "doesn't name a field",
		`{"type":"update_attr","attrs":{"nick":"{{.Attrs.first_name"},"next":"x"}`:                                            "nick",
		`{"type":"check","preset":"magic","rule":{"tag":"vip","op":"has"},"yes":"yes","no":"no"}`:                             "unknown preset",
	}
	for node, want := range bad {
		_, err := ParseGraph([]byte(fmt.Sprintf(start, node)))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", node, err, want)
		}
	}
	good := []string{
		`{"type":"on_form","form_id":3,"seconds":86400,"yes":"yes","no":"no"}`,
		`{"type":"on_asset","seconds":86400,"yes":"yes","no":"no"}`,
		`{"type":"on_page","url":"/pricing","seconds":86400,"yes":"yes","no":"no"}`,
		`{"type":"on_device","device_type":"mobile","os":"iOS","seconds":86400,"yes":"yes","no":"no"}`,
		`{"type":"on_dwc","slot":"hero","seconds":86400,"yes":"yes","no":"no"}`,
		`{"type":"if_journey","journey_ids":[2,3],"journey_state":"completed","yes":"yes","no":"no"}`,
		`{"type":"if_form_field","field":"budget","op":"gte","value":"1000","yes":"yes","no":"no"}`,
		`{"type":"if_notification","notify":"any","yes":"yes","no":"no"}`,
		`{"type":"if_valid_email","reject_disposable":true,"yes":"yes","no":"no"}`,
		`{"type":"if_dnc","channel":"any","yes":"yes","no":"no"}`,
		`{"type":"if_before","until":"2027-01-01T00:00:00Z","yes":"yes","no":"no"}`,
		`{"type":"if_visited","url":"/pricing","yes":"yes","no":"no"}`,
		`{"type":"check","preset":"tags","rule":{"any":[{"tag":"vip","op":"has"}]},"yes":"yes","no":"no"}`,
		`{"type":"notify_user","template_id":1,"to_owner":true,"next":"x"}`,
		`{"type":"send_message","channels":[{"channel":"whatsapp","template_id":1},{"channel":"sms","template_id":2}],"next":"x"}`,
		`{"type":"show_focus","focus_id":4,"next":"x"}`,
		`{"type":"push_dwc","slot":"hero","html":"<b>Hi</b>","next":"x"}`,
		`{"type":"update_owner","owner_id":0,"next":"x"}`,
		`{"type":"update_company","company_attrs":{"tier":"gold","seats":12},"next":"x"}`,
		`{"type":"add_company","company_id":9,"next":"x"}`,
		`{"type":"company_points","points":-5,"next":"x"}`,
		`{"type":"change_journey","add_journeys":[2],"next":"x"}`,
		`{"type":"update_list","add_lists":[3],"remove_lists":[4],"next":"x"}`,
		`{"type":"jump","target":"x"}`,
		`{"type":"dnc_add","channel":"all","reason":"asked by phone","next":"x"}`,
		`{"type":"dnc_remove","channel":"email","next":"x"}`,
		`{"type":"delete_contact","confirm":"DELETE"}`,
		`{"type":"update_attr","attrs":{"trial_end":"now+14days","nick":"{contactfield=first_name}"},"next":"x"}`,
	}
	for _, node := range good {
		if _, err := ParseGraph([]byte(fmt.Sprintf(start, node))); err != nil {
			t.Errorf("%s rejected: %v", node, err)
		}
	}
}

func TestJumpsMayLoopButOtherCyclesMayNot(t *testing.T) {
	// A jump back to an earlier step is allowed: the per-enrollment cap ends the loop.
	graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"w","nodes":{
		"w":{"type":"wait","seconds":60,"next":"j"},"j":{"type":"jump","target":"w"}}}`)
	if _, err := ParseGraph([]byte(`{"trigger":{"type":"event","event":"signup"},"entry":"j1","nodes":{
		"j1":{"type":"jump","target":"j2"},"j2":{"type":"jump","target":"j1"}}}`)); err == nil || !strings.Contains(err.Error(), "another jump") {
		t.Errorf("jump to a jump: %v", err)
	}
}

func TestDecisionsNeedTheirSendAbove(t *testing.T) {
	// The watched email is on another branch, not above the decision: Mautic's rule that "opens
	// email" follows a send email action.
	_, err := ParseGraph([]byte(`{"trigger":{"type":"event","event":"signup"},"entry":"c","nodes":{
		"c":{"type":"if_before","until":"2030-01-01T00:00:00Z","yes":"mail","no":"d"},
		"mail":{"type":"send_email","template_id":1,"next":"x"},
		"d":{"type":"message_decision","step":"mail","outcome":"opened","seconds":3600,"yes":"x","no":"x"},"x":{"type":"exit"}}}`))
	if err == nil || !strings.Contains(err.Error(), "above it") {
		t.Errorf("decision without its send above: %v", err)
	}
	for node, want := range map[string]string{
		`{"type":"message_decision","step":"mail","outcome":"replied","seconds":3600,"yes":"x","no":"x"}`:                  "",
		`{"type":"message_decision","step":"mail","outcome":"clicked","url":"/pricing","seconds":3600,"yes":"x","no":"x"}`: "",
		`{"type":"message_decision","step":"mail","outcome":"opened","url":"/pricing","seconds":3600,"yes":"x","no":"x"}`:  "only applies to clicks",
	} {
		_, err := ParseGraph([]byte(`{"trigger":{"type":"event","event":"signup"},"entry":"mail","nodes":{
			"mail":{"type":"send_email","template_id":1,"next":"d"},"d":` + node + `,"x":{"type":"exit"}}}`))
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: err %v, want %q", node, err, want)
		}
	}
	sms := `{"trigger":{"type":"event","event":"signup"},"entry":"m","nodes":{"m":{"type":"send_sms","template_id":1,"next":"d"},
		"d":{"type":"message_decision","step":"m","outcome":"replied","seconds":3600,"yes":"x","no":"x"},"x":{"type":"exit"}}}`
	if _, err := ParseGraph([]byte(sms)); err == nil {
		t.Error("only emails can be replied to here")
	}
}

// ---- execution ----

func TestDecisionsParkOnTheirEvent(t *testing.T) {
	for node, want := range map[string]string{
		`{"type":"on_form","form_id":3,"seconds":3600,"yes":"yes","no":"no"}`:      "form_submitted:yes:no",
		`{"type":"on_asset","asset_id":2,"seconds":3600,"yes":"yes","no":"no"}`:    "asset_downloaded:yes:no",
		`{"type":"on_page","url":"/pricing","seconds":3600,"yes":"yes","no":"no"}`: "page_view:yes:no",
		`{"type":"on_page","page_id":5,"seconds":3600,"yes":"yes","no":"no"}`:      "landing_page_viewed:yes:no",
		`{"type":"on_device","os":"iOS","seconds":3600,"yes":"yes","no":"no"}`:     "page_view:yes:no",
		`{"type":"on_dwc","slot":"hero","seconds":3600,"yes":"yes","no":"no"}`:     "dwc_requested:yes:no",
	} {
		x, store, _, _, _ := builderExecutor(t, nil)
		if err := x.Run(context.Background(), one(t, node), en, "s"); err != nil {
			t.Fatal(err)
		}
		if len(store.eventWaits) != 1 || store.eventWaits[0] != want || store.timers[7].node != "no" {
			t.Errorf("%s: waits %v, timer %+v", node, store.eventWaits, store.timers[7])
		}
	}
}

func TestDecisionMatching(t *testing.T) {
	cases := []struct {
		n     Node
		props map[string]any
		want  bool
	}{
		{Node{Type: NodeOnForm, FormID: 3}, map[string]any{"form_id": float64(3)}, true},
		{Node{Type: NodeOnForm, FormID: 3}, map[string]any{"form_id": float64(4)}, false},
		{Node{Type: NodeOnForm}, map[string]any{"form_id": float64(4)}, true},
		{Node{Type: NodeOnAsset, AssetID: 2}, map[string]any{"asset_id": "2"}, true},
		{Node{Type: NodeOnPage, URL: "/pricing"}, map[string]any{"url": "https://x.test/pricing?a=1"}, true},
		{Node{Type: NodeOnPage, URL: "/pricing"}, map[string]any{"url": "https://x.test/about"}, false},
		{Node{Type: NodeOnPage, PageID: 5}, map[string]any{"page_id": float64(5)}, true},
		{Node{Type: NodeOnDevice, DeviceType: "mobile", OS: "ios"}, map[string]any{"device_type": "mobile", "os": "iOS"}, true},
		{Node{Type: NodeOnDevice, DeviceType: "mobile"}, map[string]any{"device_type": "desktop"}, false},
		{Node{Type: NodeOnDWC, Slot: "hero"}, map[string]any{"slot": "hero"}, true},
		{Node{Type: NodeOnDWC, Slot: "hero"}, map[string]any{"slot": "footer"}, false},
		{Node{Type: NodeMessageDecision, Outcome: "clicked", URL: "/buy"}, map[string]any{"url": "https://x.test/buy"}, true},
		{Node{Type: NodeMessageDecision, Outcome: "clicked", URL: "/buy"}, map[string]any{"url": "https://x.test/help"}, false},
		{Node{Type: NodeWaitForEvent, Event: "purchase"}, nil, true},
	}
	for _, c := range cases {
		if got := c.n.Matches(c.props); got != c.want {
			t.Errorf("%+v with %v: %v, want %v", c.n, c.props, got, c.want)
		}
	}
}

func TestConditions(t *testing.T) {
	future, past := "2026-12-01T00:00:00Z", "2026-11-01T00:00:00Z" // executor's now is 2026-11-10
	cases := []struct {
		name  string
		node  string
		attrs map[string]any
		setup func(*Executor, *fakeData)
		want  string
	}{
		{"before: still before", `{"type":"if_before","until":"` + future + `","yes":"yes","no":"no"}`, nil, nil, "yes"},
		{"before: date passed", `{"type":"if_before","until":"` + past + `","yes":"yes","no":"no"}`, nil, nil, "no"},
		{"dnc email: unsubscribed", `{"type":"if_dnc","channel":"email","yes":"yes","no":"no"}`, map[string]any{"optout_email": true}, nil, "yes"},
		{"dnc email: bounced", `{"type":"if_dnc","channel":"email","yes":"yes","no":"no"}`, map[string]any{consent.AttrEmailBounced: true}, nil, "yes"},
		{"dnc sms: only email is", `{"type":"if_dnc","channel":"sms","yes":"yes","no":"no"}`, map[string]any{"optout_email": true}, nil, "no"},
		{"dnc any", `{"type":"if_dnc","channel":"any","yes":"yes","no":"no"}`, map[string]any{"optout_push": "true"}, nil, "yes"},
		{"dnc all opts out of sms", `{"type":"if_dnc","channel":"sms","yes":"yes","no":"no"}`, map[string]any{"optout_all": true}, nil, "yes"},
		{"valid email", `{"type":"if_valid_email","yes":"yes","no":"no"}`, nil, nil, "yes"},
		{"email domain takes no mail", `{"type":"if_valid_email","yes":"yes","no":"no"}`, nil,
			func(x *Executor, _ *fakeData) { x.MX = fakeMX{} }, "no"},
		{"journey active", `{"type":"if_journey","journey_ids":[2],"journey_state":"active","yes":"yes","no":"no"}`, nil,
			func(_ *Executor, d *fakeData) { d.states = map[int64]pg.JourneyState{2: {Active: true}} }, "yes"},
		{"journey not completed", `{"type":"if_journey","journey_ids":[2],"journey_state":"completed","yes":"yes","no":"no"}`, nil,
			func(_ *Executor, d *fakeData) { d.states = map[int64]pg.JourneyState{2: {Active: true}} }, "no"},
		{"journey entered", `{"type":"if_journey","journey_ids":[2,3],"journey_state":"entered","yes":"yes","no":"no"}`, nil,
			func(_ *Executor, d *fakeData) { d.states = map[int64]pg.JourneyState{3: {}} }, "yes"},
		{"push device", `{"type":"if_notification","notify":"push","yes":"yes","no":"no"}`, nil,
			func(_ *Executor, d *fakeData) { d.devices = []model.Device{{Token: "t"}} }, "yes"},
		{"push device but push opted out", `{"type":"if_notification","notify":"push","yes":"yes","no":"no"}`, map[string]any{"optout_push": true},
			func(_ *Executor, d *fakeData) { d.devices = []model.Device{{Token: "t"}} }, "no"},
		{"unread inbox", `{"type":"if_notification","notify":"any","yes":"yes","no":"no"}`, nil,
			func(_ *Executor, d *fakeData) { d.unread = 2 }, "yes"},
		{"nothing to notify", `{"type":"if_notification","notify":"any","yes":"yes","no":"no"}`, nil, nil, "no"},
		{"visited", `{"type":"if_visited","url":"/pricing","yes":"yes","no":"no"}`, nil,
			func(x *Executor, _ *fakeData) { x.History = &fakeHistory{visits: 3} }, "yes"},
		{"not visited", `{"type":"if_visited","url":"/pricing","days":7,"yes":"yes","no":"no"}`, nil, nil, "no"},
		{"form field from the enrollment", `{"type":"if_form_field","form_id":3,"field":"budget","op":"gte","value":"1000","yes":"yes","no":"no"}`, nil,
			func(_ *Executor, d *fakeData) {
				d.form = map[string]any{"form_id": float64(3), "fields": map[string]any{"budget": float64(5000)}}
			}, "yes"},
		{"form field from history", `{"type":"if_form_field","form_id":4,"field":"city","op":"eq","value":"Pune","yes":"yes","no":"no"}`, nil,
			func(x *Executor, d *fakeData) {
				d.form = map[string]any{"form_id": float64(3), "fields": map[string]any{"city": "Delhi"}}
				x.History = &fakeHistory{form: map[string]any{"form_id": float64(4), "fields": map[string]any{"city": "Pune"}}}
			}, "yes"},
		{"no submission", `{"type":"if_form_field","field":"city","op":"exists","yes":"yes","no":"no"}`, nil, nil, "no"},
	}
	for _, c := range cases {
		x, store, _, _, data := builderExecutor(t, c.attrs)
		if c.setup != nil {
			c.setup(x, data)
		}
		if err := x.Run(context.Background(), one(t, c.node), en, "s"); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if store.nodes[7][0] != c.want {
			t.Errorf("%s: went to %v, want %s", c.name, store.nodes[7], c.want)
		}
		if st := x.Stats.(fakeStats); st["s|entered"] != 1 || st["s|"+c.want] != 1 {
			t.Errorf("%s: step stats %v", c.name, st)
		}
	}
}

func TestEmailValidity(t *testing.T) {
	mx := fakeMX{"ok.test": true, "mailinator.com": true}
	for email, want := range map[string]bool{
		"a@ok.test": true, "A <a@ok.test>": false, "a@nomail.test": false, "a@localhost": false, "not an email": false,
	} {
		if got := ValidEmail(context.Background(), mx, email, false); got != want {
			t.Errorf("%q: %v, want %v", email, got, want)
		}
	}
	if !ValidEmail(context.Background(), mx, "x@mailinator.com", false) || ValidEmail(context.Background(), mx, "x@mailinator.com", true) {
		t.Error("disposable domains are refused only when asked")
	}
}

func TestContactActions(t *testing.T) {
	x, store, _, evs, data := builderExecutor(t, map[string]any{"company_id": float64(9)})
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"o","nodes":{
		"o":{"type":"update_owner","owner_id":5,"next":"co"},
		"co":{"type":"add_company","company_id":9,"next":"uc"},
		"uc":{"type":"update_company","company_attrs":{"tier":"gold"},"next":"sc"},
		"sc":{"type":"company_points","points":5,"next":"cj"},
		"cj":{"type":"change_journey","add_journeys":[2],"remove_journeys":[4],"next":"dn"},
		"dn":{"type":"dnc_add","channel":"sms","reason":"asked","next":"dr"},
		"dr":{"type":"dnc_remove","channel":"email","next":"c"},
		"c":{"type":"check","rule":{"all":[{"attr":"owner_id","op":"eq","value":5},{"attr":"optout_sms","op":"eq","value":true}]},"yes":"x","no":"n"},
		"n":{"type":"exit"},"x":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "o"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs.evs {
		got = append(got, e.Name+":"+e.ExternalID)
	}
	want := "owner_changed:u42,company_linked:u42,company_updated:u42,company_updated:u43,company_updated:u42,company_updated:u43," +
		"journey_change:u42,unsubscribed:u42,resubscribed:u42,journey_completed:u42"
	if strings.Join(got, ",") != want {
		t.Errorf("events\n got %s\nwant %s", strings.Join(got, ","), want)
	}
	if e := evs.evs[0]; e.Props["owner_id"] != int64(5) || e.Props["owner_name"] != "Ravi" || e.Props["source"] != "journey" {
		t.Errorf("owner event %+v", e.Props)
	}
	if e := evs.evs[7]; e.Props["channel"] != "sms" || e.Props["method"] != consent.MethodManual || e.Props["reason"] != "asked" {
		t.Errorf("dnc event %+v", e.Props)
	}
	if data.merged["tier"] != "gold" || data.company.Attrs["score"] != float64(15) {
		t.Errorf("company %v / %v", data.merged, data.company.Attrs)
	}
	if store.nodes[7][0] != "x" {
		t.Errorf("the check should see the new owner and the SMS opt-out: ended at %v", store.nodes[7])
	}
	// A retried run adds to the company's score once.
	evs.evs = nil
	if err := x.Run(context.Background(), g, en, "sc"); err != nil {
		t.Fatal(err)
	}
	if data.company.Attrs["score"] != float64(15) {
		t.Errorf("retry added the score again: %v", data.company.Attrs)
	}
}

func TestUpdateVariables(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	c := model.Contact{ExternalID: "u1", Email: "a@x.test", Attrs: map[string]any{"first_name": "Asha", "plan": "pro"}}
	got := ResolveAttrs(map[string]any{
		"greeting": "Hi {contactfield=first_name} ({contactfield=email})", "tpl": "{{.Attrs.plan}}-{{.Attrs.missing}}",
		"t1": "now", "t2": "now+3hours", "t3": "now-2days", "t4": "now+1month", "n": float64(3), "lit": "nowhere",
	}, c, now)
	want := map[string]any{"greeting": "Hi Asha (a@x.test)", "tpl": "pro-", "t1": "2026-11-10T12:00:00Z", "t2": "2026-11-10T15:00:00Z",
		"t3": "2026-11-08T12:00:00Z", "t4": "2026-12-10T12:00:00Z", "n": float64(3), "lit": "nowhere"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestMessagingActions(t *testing.T) {
	// The marketing message goes out on the first channel the contact can receive: no push device,
	// email unsubscribed, so SMS.
	x, _, sender, _, _ := builderExecutor(t, map[string]any{"optout_email": true, "owner_id": float64(5)})
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"m","nodes":{
		"m":{"type":"send_message","channels":[{"channel":"push","template_id":1},{"channel":"email","template_id":2},{"channel":"sms","template_id":3}],"next":"u"},
		"u":{"type":"notify_user","template_id":7,"to_owner":true,"user_ids":[6,5],"emails":["boss@team.test"],"next":"f"},
		"f":{"type":"show_focus","focus_id":4,"next":"d"},
		"d":{"type":"push_dwc","slot":"hero","html":"<b>Hi</b>","next":"x"},"x":{"type":"exit"}}}`)
	if err := x.Run(context.Background(), g, en, "m"); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("sent %+v", sender.sent)
	}
	if m := sender.sent[0]; m.Channel != "sms" || m.TemplateID != 3 || m.ID != "7:m" || len(m.To) != 0 {
		t.Errorf("marketing message %+v", m)
	}
	if m := sender.sent[1]; m.Channel != "email" || m.TemplateID != 7 || strings.Join(m.To, ",") != "boss@team.test,owner@team.test,sales@team.test" {
		t.Errorf("notification %+v", m)
	}
	o := x.Onsite.(*fakeOnsite)
	if strings.Join(o.focus, ",") != "42:4" || len(o.dwc) != 1 || !strings.HasPrefix(o.dwc[0], "42:hero:<b>Hi</b>:720h") {
		t.Errorf("onsite %v %v", o.focus, o.dwc)
	}

	// Reachable nowhere: recorded, and the journey carries on.
	x, store, sender, evs, _ := builderExecutor(t, map[string]any{"optout_all": true})
	if err := x.Run(context.Background(), one(t, `{"type":"send_message","channels":[{"channel":"email","template_id":2}],"next":"x"}`), en, "s"); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 || evs.evs[0].Name != "message_unreachable" || store.nodes[7][0] != "x" {
		t.Errorf("sent %v, events %v, at %v", sender.sent, evs.evs, store.nodes[7])
	}
}

func TestJumpCap(t *testing.T) {
	x, store, sender, evs, _ := builderExecutor(t, nil)
	// A loop without a wait: each pass sends again (a new step key), until the cap ends it.
	g := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"m","nodes":{
		"m":{"type":"send_sms","template_id":1,"next":"j"},"j":{"type":"jump","target":"m"}}}`)
	if err := x.Run(context.Background(), g, en, "m"); err != nil {
		t.Fatal(err)
	}
	if store.jumps != MaxJumps || len(sender.sent) != MaxJumps+1 {
		t.Fatalf("jumps %d, sends %d", store.jumps, len(sender.sent))
	}
	if sender.sent[0].ID != "7:m" || sender.sent[1].ID != "7:m:j1" || sender.sent[MaxJumps].ID != fmt.Sprintf("7:m:j%d", MaxJumps) {
		t.Errorf("send ids %s %s %s", sender.sent[0].ID, sender.sent[1].ID, sender.sent[MaxJumps].ID)
	}
	if store.nodes[7] != [2]string{"j", StatusCompleted} {
		t.Errorf("enrollment %v", store.nodes[7])
	}
	var names []string
	for _, e := range evs.evs {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != event.JourneyJumpLimit+",journey_completed" {
		t.Errorf("events %v", names)
	}
	// A message decision after a jump waits for that pass's message.
	g2 := graph(t, `{"trigger":{"type":"event","event":"signup"},"entry":"m","nodes":{
		"m":{"type":"send_email","template_id":1,"next":"d"},
		"d":{"type":"message_decision","step":"m","outcome":"opened","seconds":3600,"yes":"x","no":"x"},"x":{"type":"exit"}}}`)
	if _, id := g2.MessageEventFor(model.Enrollment{ID: 7, Jumps: 2}, g2.Nodes["d"]); id != "7:m:j2" {
		t.Errorf("message id after jumps: %s", id)
	}
}

func TestDeleteContact(t *testing.T) {
	x, store, _, evs, _ := builderExecutor(t, nil)
	if err := x.Run(context.Background(), one(t, `{"type":"delete_contact","confirm":"DELETE"}`), en, "s"); err != nil {
		t.Fatal(err)
	}
	if d := x.Deleter.(*fakeDeleter).deleted; len(d) != 1 || d[0] != 42 {
		t.Errorf("deleted %v", d)
	}
	if len(evs.evs) != 0 || len(store.nodes) != 0 {
		t.Errorf("nothing is recorded for a deleted contact: %v %v", evs.evs, store.nodes)
	}
	// Without a deleter configured the step fails instead of silently skipping.
	x.Deleter = nil
	if err := x.Run(context.Background(), one(t, `{"type":"delete_contact","confirm":"DELETE"}`), en, "s"); err == nil {
		t.Error("want an error without a deleter")
	}
	g := one(t, `{"type":"delete_contact","confirm":"DELETE"}`)
	if !g.HasType(NodeDeleteContact) {
		t.Error("HasType")
	}
}
