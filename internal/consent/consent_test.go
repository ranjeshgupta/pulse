package consent

import (
	"reflect"
	"testing"
)

func TestOptedOut(t *testing.T) {
	cases := []struct {
		attrs map[string]any
		ch    string
		want  bool
	}{
		{map[string]any{}, "email", false},
		{map[string]any{"optout_email": true}, "email", true},
		{map[string]any{"optout_email": true}, "sms", false},
		{map[string]any{"optout_all": true}, "push", true},
		{map[string]any{"optout_sms": "true"}, "sms", true},
		{map[string]any{"optout_sms": false}, "sms", false},
	}
	for _, c := range cases {
		if got := OptedOut(c.attrs, c.ch); got != c.want {
			t.Errorf("OptedOut(%v, %s) = %v", c.attrs, c.ch, got)
		}
	}
}

func TestUpdates(t *testing.T) {
	if got := Updates(Unsubscribed, map[string]any{"channel": "email"}); !reflect.DeepEqual(got, map[string]any{"optout_email": true}) {
		t.Errorf("unsubscribe email = %v", got)
	}
	if got := Updates(Unsubscribed, nil); !reflect.DeepEqual(got, map[string]any{"optout_all": true}) {
		t.Errorf("unsubscribe without channel = %v, want all", got)
	}
	if got := Updates(Resubscribed, map[string]any{"channel": "sms"}); !reflect.DeepEqual(got, map[string]any{"optout_sms": false, "optout_all": false}) {
		t.Errorf("resubscribe sms = %v", got)
	}
	if got := Updates(Resubscribed, map[string]any{"channel": "all"}); len(got) != 7 || got["optout_push"] != false {
		t.Errorf("resubscribe all = %v", got)
	}
	if Updates(Unsubscribed, map[string]any{"channel": "fax"}) != nil || Updates("purchase", nil) != nil {
		t.Error("unknown channel or non-consent event must change nothing")
	}
	if !IsReserved("optout_whatsapp") || IsReserved("optout_newsletter") || IsReserved("city") {
		t.Error("IsReserved")
	}
	for _, k := range []string{"stage", "stage_changed_at", "company_id", "company_name", "company_industry", "merged_into"} {
		if !IsReserved(k) {
			t.Errorf("%s should be reserved: only its events change it", k)
		}
	}
	if IsReserved("company") || IsReserved("stages") {
		t.Error("company (a name an identify can carry) and other keys are not reserved")
	}
}
