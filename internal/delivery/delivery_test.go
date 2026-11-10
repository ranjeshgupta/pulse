package delivery

import (
	"strings"
	"testing"
	"time"
)

func TestQuietHours(t *testing.T) {
	r := Default()
	r.Quiet.Enabled = true // 21:00-09:00 Asia/Kolkata on SMS, WhatsApp, RCS, push
	at := func(utc string) time.Time { t, _ := time.Parse(time.RFC3339, utc); return t }

	// 23:30 IST (18:00 UTC): quiet until 09:00 IST next day (03:30 UTC).
	until, quiet := r.QuietUntil("whatsapp", "", at("2026-11-10T18:00:00Z"))
	if !quiet || !until.Equal(at("2026-11-11T03:30:00Z")) {
		t.Errorf("23:30 IST: %v %v", until.UTC(), quiet)
	}
	// 07:00 IST (01:30 UTC): still quiet, until 09:00 the same day.
	if until, quiet := r.QuietUntil("sms", "", at("2026-11-11T01:30:00Z")); !quiet || !until.Equal(at("2026-11-11T03:30:00Z")) {
		t.Errorf("07:00 IST: %v %v", until.UTC(), quiet)
	}
	// 12:00 IST: fine. Email isn't covered by default.
	if _, quiet := r.QuietUntil("sms", "", at("2026-11-11T06:30:00Z")); quiet {
		t.Error("noon is not quiet")
	}
	if _, quiet := r.QuietUntil("email", "", at("2026-11-10T18:00:00Z")); quiet {
		t.Error("email is not in the quiet channels")
	}
	// The contact's own zone wins: 18:00 UTC is 13:00 in New York.
	if _, quiet := r.QuietUntil("sms", "America/New_York", at("2026-11-10T18:00:00Z")); quiet {
		t.Error("contact time zone ignored")
	}
}

func TestValidate(t *testing.T) {
	r := Default()
	r.Caps["email"] = Cap{Max: 3, Hours: 24}
	r.Quiet.Enabled = true
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Rules){
		"unknown channel": func(r *Rules) { r.Caps["fax"] = Cap{Max: 1, Hours: 1} },
		"at least 1":      func(r *Rules) { r.Caps["sms"] = Cap{Max: 0, Hours: 1} },
		"different":       func(r *Rules) { r.Quiet.End = r.Quiet.Start },
		"time zone":       func(r *Rules) { r.Quiet.Timezone = "Nowhere/Land" },
	}
	for want, mut := range bad {
		x := Default()
		x.Quiet.Enabled = true
		mut(&x)
		if err := x.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}
