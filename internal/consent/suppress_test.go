package consent

import "testing"

func TestSuppressReason(t *testing.T) {
	cases := []struct {
		name           string
		attrs          map[string]any
		ch, topic      string
		bySegment      bool
		wantSuppressed bool
	}{
		{"subscribed", map[string]any{}, "email", "", false, false},
		{"email opt-out, enforced", map[string]any{"optout_email": true}, "email", "", false, true},
		{"email opt-out, by segment", map[string]any{"optout_email": true}, "email", "", true, false},
		{"opted out of all, by segment", map[string]any{"optout_all": true}, "email", "", true, false},
		{"spam complaint is always honoured", map[string]any{"email_complaints": 1}, "email", "", true, true},
		{"sms opt-out ignores the email policy", map[string]any{"optout_sms": true}, "sms", "", true, true},
		{"left the topic", map[string]any{"optout_topic_offers": true}, "email", "offers", true, true},
		{"left another topic", map[string]any{"optout_topic_offers": true}, "email", "news", false, false},
		{"topic opt-out on sms too", map[string]any{"optout_topic_offers": "true"}, "sms", "offers", false, true},
	}
	for _, c := range cases {
		if got := SuppressReason(c.attrs, c.ch, c.topic, c.bySegment) != ""; got != c.wantSuppressed {
			t.Errorf("%s: suppressed = %v", c.name, got)
		}
	}
}

func TestTopicUpdates(t *testing.T) {
	if u := Updates(Unsubscribed, map[string]any{"topic": "offers", "channel": "email"}); len(u) != 1 || u["optout_topic_offers"] != true {
		t.Errorf("unsubscribe: %v", u)
	}
	if u := Updates(Resubscribed, map[string]any{"topic": "offers"}); len(u) != 1 || u["optout_topic_offers"] != false {
		t.Errorf("resubscribe: %v", u)
	}
	if u := Updates(Unsubscribed, map[string]any{"topic": "Bad Key!"}); u != nil {
		t.Errorf("bad key: %v", u)
	}
	if !IsReserved("optout_topic_offers") {
		t.Error("topic opt-outs are reserved")
	}
}
