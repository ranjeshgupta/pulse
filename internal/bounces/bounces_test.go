package bounces

import "testing"

func TestParse(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []Bounce
	}{
		"sendgrid": {`[{"email":"a@x.co","event":"bounce","type":"bounce","reason":"550"},{"email":"b@x.co","event":"spamreport"},{"email":"c@x.co","event":"open"}]`,
			[]Bounce{{"a@x.co", Hard, "550", "sendgrid"}, {"b@x.co", Complaint, "", "sendgrid"}}},
		"postmark": {`{"RecordType":"Bounce","Type":"SoftBounce","Email":"a@x.co","Description":"full"}`, []Bounce{{"a@x.co", Soft, "full", "postmark"}}},
		"mailgun":  {`{"event-data":{"event":"failed","severity":"permanent","recipient":"a@x.co","delivery-status":{"message":"no user"}}}`, []Bounce{{"a@x.co", Hard, "no user", "mailgun"}}},
		"ses": {`{"Type":"Notification","Message":"{\"notificationType\":\"Bounce\",\"bounce\":{\"bounceType\":\"Permanent\",\"bouncedRecipients\":[{\"emailAddress\":\"a@x.co\",\"diagnosticCode\":\"550\"}]}}"}`,
			[]Bounce{{"a@x.co", Hard, "550", "ses"}}},
		"generic": {`{"email":"a@x.co","type":"complaint"}`, []Bounce{{"a@x.co", Complaint, "", "generic"}}},
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
}
