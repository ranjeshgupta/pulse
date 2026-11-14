package automation

import (
	"strings"
	"testing"

	"pulse/internal/reasons"
)

func TestBounceRules(t *testing.T) {
	s := Defaults()
	if r := s.BouncesFor("email").Rule(reasons.BounceHard); r != (BounceRule{1, BounceStop}) {
		t.Errorf("email hard default = %+v (stop at the first, as before)", r)
	}
	if r := s.BouncesFor("email").Rule(reasons.BounceComplaint); r.Action != BounceUnsubscribe {
		t.Errorf("email complaint default = %+v", r)
	}
	// A PUT of one channel replaces that channel only.
	out, err := Apply(s, []byte(`{"bounces":{"email":{"hard":{"count":2,"action":"leave_segments"},"soft":{"count":3,"action":"stop"},"complaint":{"count":1,"action":"do_not_contact"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Validate(); err != nil {
		t.Fatal(err)
	}
	if r := out.BouncesFor("email").Soft; r != (BounceRule{3, BounceStop}) {
		t.Errorf("email soft = %+v", r)
	}
	if r := out.BouncesFor("sms").Hard; r != (BounceRule{1, BounceStop}) {
		t.Errorf("sms kept its default: %+v", r)
	}
	// Stored settings missing a channel load with its default.
	l, err := Load([]byte(`{"bounces":{"rcs":{"hard":{"count":2,"action":"stop"},"soft":{"count":1,"action":"record"},"complaint":{"count":1,"action":"record"}}}}`))
	if err != nil || l.BouncesFor("email").Hard.Action != BounceStop || l.BouncesFor("rcs").Hard.Count != 2 {
		t.Errorf("load = %+v, %v", l.Bounces, err)
	}
	for _, bad := range []string{
		`{"bounces":{"push":{"hard":{"count":1,"action":"stop"},"soft":{"count":1,"action":"record"},"complaint":{"count":1,"action":"record"}}}}`,
		`{"bounces":{"email":{"hard":{"count":0,"action":"stop"},"soft":{"count":1,"action":"record"},"complaint":{"count":1,"action":"record"}}}}`,
		`{"bounces":{"email":{"hard":{"count":1,"action":"explode"},"soft":{"count":1,"action":"record"},"complaint":{"count":1,"action":"record"}}}}`,
	} {
		o, err := Apply(Defaults(), []byte(bad))
		if err == nil {
			err = o.Validate()
		}
		if err == nil || !strings.Contains(err.Error(), "bounces") {
			t.Errorf("%s accepted (%v)", bad, err)
		}
	}
}
