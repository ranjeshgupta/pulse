package bounces

import (
	"context"
	"strings"
	"testing"

	"pulse/internal/auth"
	"pulse/internal/tracking"
)

func TestParse(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []Bounce
	}{
		"sendgrid": {`[{"email":"a@x.co","event":"bounce","type":"bounce","reason":"550","pulse_ref":"p1R.s"},{"email":"b@x.co","event":"spamreport"},{"email":"c@x.co","event":"open"},` +
			`{"email":"d@x.co","event":"delivered","response":"250 ok","smtp-id":"<p1M.s@acme.com>"}]`,
			[]Bounce{{Email: "a@x.co", Kind: Hard, Reason: "550", Source: "sendgrid", Ref: "p1R.s"}, {Email: "b@x.co", Kind: Complaint, Source: "sendgrid"},
				{Email: "d@x.co", Kind: Delivered, Reason: "250 ok", Source: "sendgrid", Ref: "p1M.s"}}},
		"postmark": {`{"RecordType":"Bounce","Type":"SoftBounce","Email":"a@x.co","Description":"full","Metadata":{"pulse_ref":"p1A","pulse_ref2":"B.s"}}`,
			[]Bounce{{Email: "a@x.co", Kind: Soft, Reason: "full", Source: "postmark", Ref: "p1AB.s"}}},
		"postmark delivery": {`{"RecordType":"Delivery","Recipient":"a@x.co","Details":"accepted","Metadata":{"pulse_ref":"p1D.s"}}`,
			[]Bounce{{Email: "a@x.co", Kind: Delivered, Reason: "accepted", Source: "postmark", Ref: "p1D.s"}}},
		"mailgun": {`{"signature":{},"event-data":{"event":"failed","severity":"permanent","recipient":"a@x.co","delivery-status":{"message":"no user"},"user-variables":{"pulse_ref":"p1G.s"}}}`,
			[]Bounce{{Email: "a@x.co", Kind: Hard, Reason: "no user", Source: "mailgun", Ref: "p1G.s"}}},
		"mailgun delivered": {`{"event-data":{"event":"delivered","recipient":"a@x.co","message":{"headers":{"message-id":"p1H.s@acme.com"}}}}`,
			[]Bounce{{Email: "a@x.co", Kind: Delivered, Source: "mailgun", Ref: "p1H.s"}}},
		"ses": {`{"Type":"Notification","Message":"{\"notificationType\":\"Bounce\",\"mail\":{\"tags\":{\"pulse_ref\":[\"p1S.s\"]}},\"bounce\":{\"bounceType\":\"Permanent\",\"bouncedRecipients\":[{\"emailAddress\":\"a@x.co\",\"diagnosticCode\":\"550\"}]}}"}`,
			[]Bounce{{Email: "a@x.co", Kind: Hard, Reason: "550", Source: "ses", Ref: "p1S.s"}}},
		"ses delivery": {`{"eventType":"Delivery","mail":{"headers":[{"name":"X-Pulse-Ref","value":"p1T.s"}]},"delivery":{"recipients":["a@x.co"],"smtpResponse":"250"}}`,
			[]Bounce{{Email: "a@x.co", Kind: Delivered, Reason: "250", Source: "ses", Ref: "p1T.s"}}},
		"sparkpost": {`[{"msys":{"message_event":{"type":"bounce","bounce_class":"10","rcpt_to":"a@x.co","reason":"550 unknown","rcpt_meta":{"pulse_ref":"p1P.s"}}}},` +
			`{"msys":{"message_event":{"type":"bounce","bounce_class":"21","rcpt_to":"b@x.co"}}},` +
			`{"msys":{"message_event":{"type":"delivery","rcpt_to":"c@x.co","raw_reason":"250","rcpt_meta":{"pulse_ref":"p1Q.s"}}}},` +
			`{"msys":{"message_event":{"type":"spam_complaint","rcpt_to":"d@x.co","fbtype":"abuse"}}},{"msys":{"track_event":{"type":"open","rcpt_to":"e@x.co"}}}]`,
			[]Bounce{{Email: "a@x.co", Kind: Hard, Reason: "550 unknown", Source: "sparkpost", Ref: "p1P.s"}, {Email: "b@x.co", Kind: Soft, Source: "sparkpost"},
				{Email: "c@x.co", Kind: Delivered, Reason: "250", Source: "sparkpost", Ref: "p1Q.s"}, {Email: "d@x.co", Kind: Complaint, Reason: "abuse", Source: "sparkpost"}}},
		"generic": {`{"email":"a@x.co","type":"complaint","ref":"p1X.s"}`, []Bounce{{Email: "a@x.co", Kind: Complaint, Source: "generic", Ref: "p1X.s"}}},
	} {
		got, confirm, err := Parse([]byte(tc.body))
		if err != nil || confirm != nil || len(got) != len(tc.want) {
			t.Fatalf("%s: %v %v %v", name, got, confirm, err)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: %+v, want %+v", name, got[i], tc.want[i])
			}
		}
	}
	if _, c, err := Parse([]byte(`{"Type":"SubscriptionConfirmation","SubscribeURL":"https://sns.ap-south-1.amazonaws.com/?Action=ConfirmSubscription"}`)); err != nil || c == nil {
		t.Errorf("SNS confirmation: %v %v", c, err)
	}
	for _, evil := range []string{"https://sns.x.amazonaws.com.evil.test/", "https://sns.x.amazonaws.com:8443/", "http://sns.x.amazonaws.com/",
		"https://user@sns.x.amazonaws.com/", "https://sns.evil.test/.amazonaws.com", "https://169.254.169.254/"} {
		if _, c, err := Parse([]byte(`{"Type":"SubscriptionConfirmation","SubscribeURL":"` + evil + `"}`)); err == nil || c != nil {
			t.Errorf("SubscribeURL %s was accepted", evil)
		}
	}
	if _, _, err := Parse([]byte(`{"hello":1}`)); err != ErrUnknown {
		t.Errorf("unknown body: %v", err)
	}
	for body, want := range map[string]string{`[{"event":"bounce"}]`: FormatSendGrid, `[{"msys":{}}]`: FormatSparkPost, `{"RecordType":"Bounce"}`: FormatPostmark,
		`{"event-data":{}}`: FormatMailgun, `{"Type":"Notification"}`: FormatSES, `{"eventType":"Bounce"}`: FormatSESDirect, `{"email":"a"}`: FormatGeneric} {
		if got := Detect([]byte(body)); got != want {
			t.Errorf("Detect(%s) = %s, want %s", body, got, want)
		}
	}
}

func refCodec(t *testing.T) *tracking.Codec {
	t.Helper()
	box, err := auth.NewBox(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	return tracking.New(box, strings.Repeat("cd", 32), "https://pulse.test")
}

func TestEventsLinkToTheMessage(t *testing.T) {
	codec := refCodec(t)
	store := fakeMailStore{known: map[string]string{"a@x.co": "u1", "new@x.co": "u2"}, ids: map[string]uint32{"u1": 11, "u2": 12}}
	ref := codec.Ref(tracking.Ref{TenantID: 7, ContactID: 11, MessageID: "bc5:100:11", TemplateID: 3, BroadcastID: 5, Run: 100})
	stale := codec.Ref(tracking.Ref{TenantID: 7, ContactID: 12, MessageID: "44:send:1", TemplateID: 4, JourneyID: 9})
	otherTenant := codec.Ref(tracking.Ref{TenantID: 8, ContactID: 11, MessageID: "bc6:1:11", BroadcastID: 6, Run: 1})
	evs, err := Events([]Bounce{
		{Email: "a@x.co", Kind: Hard, Reason: "550 gone", Source: "sendgrid", Ref: ref},
		{Email: "a@x.co", Kind: Delivered, Source: "sendgrid", Ref: ref},
		{Email: "old@x.co", Kind: Hard, Source: "dsn", Ref: stale},          // contact 12 has another address now
		{Email: "a@x.co", Kind: Complaint, Source: "arf", Ref: otherTenant}, // another workspace's message: unlinked
		{Email: "a@x.co", Kind: Delivered, Source: "ses"},                   // a delivery without a reference counts nowhere
		{Email: "nobody@x.co", Kind: Soft, Source: "generic"},
	}, Link(context.Background(), 7, codec, store))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs {
		got = append(got, e.ExternalID+":"+e.Name+":"+e.ID)
	}
	want := []string{"u1:email_bounced:fb:bc5:100:11:hard:b", "u1:email_delivered:fb:bc5:100:11:delivered",
		"u2:email_bounced:fb:44:send:1:hard:b", "u1:email_complained:", "u1:unsubscribed:"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("events\n%v\nwant\n%v", got, want)
	}
	if evs[0].Props["broadcast_id"] != int64(5) || evs[0].Props["reason_code"] != "hard_bounce" || evs[0].Attrs[AttrEmailBounced] != true {
		t.Errorf("linked hard bounce: %+v %+v", evs[0].Props, evs[0].Attrs)
	}
	if evs[1].Props["message_id"] != "bc5:100:11" || evs[1].Props["kind"] != nil {
		t.Errorf("delivered: %+v", evs[1].Props)
	}
	if evs[2].Attrs != nil || evs[2].Props["journey_id"] != int64(9) {
		t.Errorf("a bounce of an old address must not mark the new one: %+v", evs[2])
	}
	if evs[3].Props["message_id"] != nil || evs[3].Props["reason_code"] != "complaint" {
		t.Errorf("another workspace's reference must not link: %+v", evs[3].Props)
	}
}

func TestReportRefs(t *testing.T) {
	raw := strings.Replace(string(sample(t, "dsn_postfix.eml")), "Subject: November offers", "Subject: November offers\nMessage-ID: <p1FROMDSN.sig@acme.test>", 1)
	bs, err := ParseReport([]byte(raw))
	if err != nil || len(bs) == 0 {
		t.Fatal(bs, err)
	}
	for _, b := range bs {
		if b.Ref != "p1FROMDSN.sig" {
			t.Errorf("DSN reference: %+v", b)
		}
	}
	arf := strings.Replace(string(sample(t, "arf_yahoo.eml")), "Subject: November offers", "X-Pulse-Ref: p1FROMARF.sig\nSubject: November offers", 1)
	if cs, err := ParseReport([]byte(arf)); err != nil || len(cs) != 1 || cs[0].Ref != "p1FROMARF.sig" {
		t.Errorf("ARF reference: %+v %v", cs, err)
	}
}
