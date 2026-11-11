package model

import (
	"testing"
	"time"
)

func TestDeadlineAfter(t *testing.T) {
	start := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC) // 15:30 in India
	ist := time.FixedZone("IST", 5*3600+1800)
	for name, tc := range map[string]struct {
		d    *Deadline
		want time.Time
	}{
		"hours":             {&Deadline{Hours: 3}, start.Add(3 * time.Hour)},
		"time today":        {&Deadline{At: "21:00", Timezone: "Asia/Kolkata"}, time.Date(2026, 11, 11, 21, 0, 0, 0, ist)},
		"time passed: next": {&Deadline{At: "09:00", Timezone: "Asia/Kolkata"}, time.Date(2026, 11, 12, 9, 0, 0, 0, ist)},
		"earlier of both":   {&Deadline{At: "21:00", Hours: 2, Timezone: "Asia/Kolkata"}, start.Add(2 * time.Hour)},
		"time earlier":      {&Deadline{At: "16:00", Hours: 5, Timezone: "Asia/Kolkata"}, time.Date(2026, 11, 11, 16, 0, 0, 0, ist)},
		"utc when no zone":  {&Deadline{At: "12:00"}, time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)},
	} {
		got := tc.d.After(start)
		if got == nil || !got.Equal(tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
	var none *Deadline
	if none.After(start) != nil || (&Deadline{}).After(start) != nil {
		t.Error("no deadline")
	}
	if got := (&Deadline{At: "21:00", Timezone: "Asia/Kolkata"}).LocalClock("2026-11-11T09:00"); got != "2026-11-11T21:00" {
		t.Errorf("local clock %q", got)
	}
	if got := (&Deadline{Hours: 20}).LocalClock("2026-11-11T09:00"); got != "2026-11-12T05:00" {
		t.Errorf("local hours %q", got)
	}
}

func TestDeadlineValidate(t *testing.T) {
	for _, bad := range []*Deadline{{}, {At: "25:00"}, {At: "9:00"}, {Hours: -1}, {Hours: 500}, {At: "10:00", Timezone: "Nowhere/City"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v should be invalid", bad)
		}
	}
	var none *Deadline
	for _, ok := range []*Deadline{none, {At: "21:30"}, {Hours: 4}, {At: "06:00", Hours: 168, Timezone: "Asia/Kolkata"}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]string{"  Diwali  2026 ", "diwali 2026", "", "Retention", "retention"})
	if err != nil || len(got) != 2 || got[0] != "Diwali 2026" || got[1] != "Retention" {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := NormalizeTags(nil); got == nil {
		t.Error("never nil")
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	if _, err := NormalizeTags(many); err == nil {
		t.Error("21 tags")
	}
	if _, err := NormalizeTags([]string{"a,b"}); err == nil {
		t.Error("comma")
	}
	if _, err := NormalizeTags([]string{string(make([]byte, 41))}); err == nil {
		t.Error("too long")
	}
}
