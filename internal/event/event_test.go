package event

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeDefaults(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	e := Event{TenantID: 1, ExternalID: "u1", Name: "purchase"}
	if err := e.Normalize(now); err != nil {
		t.Fatal(err)
	}
	if len(e.ID) != 32 {
		t.Errorf("generated id %q, want 32 hex chars", e.ID)
	}
	if !e.Ts.Equal(now) || e.Ts.Location() != time.UTC {
		t.Errorf("ts = %v, want %v in UTC", e.Ts, now)
	}
	if got := string(e.RawKey()); got != "1:u1" {
		t.Errorf("RawKey = %q", got)
	}
}

func TestNormalizeRejects(t *testing.T) {
	now := time.Now()
	cases := map[string]Event{
		"external_id": {Name: "x"},
		"event name":  {ExternalID: "u", Name: "has space"},
		"future":      {ExternalID: "u", Name: "x", Ts: now.Add(time.Hour)},
		"attribute":   {ExternalID: "u", Name: "x", Attrs: map[string]any{"bad-key": 1}},
		"255":         {ExternalID: strings.Repeat("u", 256), Name: "x"},
	}
	for want, e := range cases {
		if err := e.Normalize(now); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}
