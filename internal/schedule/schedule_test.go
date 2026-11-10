package schedule

import (
	"testing"
	"time"
)

var ist = time.FixedZone("IST", 5*3600+1800)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, ist)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(t time.Time) *time.Time { return &t }

func TestWindow(t *testing.T) {
	w := Window{Start: at("2026-11-12 09:00")}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Window{}).Validate(); err == nil {
		t.Error("missing start accepted")
	}
	if err := (Window{Start: at("2026-11-12 09:00"), End: ptr(at("2026-11-12 08:00"))}).Validate(); err == nil {
		t.Error("end before start accepted")
	}
	cases := map[string]string{"2026-11-12 08:59": "upcoming", "2026-11-12 09:00": "live", "2030-01-01 00:00": "live"}
	for when, want := range cases {
		if got := w.State(at(when)); got != want {
			t.Errorf("no end, %s: %s, want %s (empty end means it never ends)", when, got, want)
		}
	}
	w.End = ptr(at("2026-11-20 00:00"))
	if w.Active(at("2026-11-20 00:00")) || w.State(at("2026-11-21 00:00")) != "ended" {
		t.Error("end is exclusive and ends the window")
	}
}

func TestNext(t *testing.T) {
	w := Window{Start: at("2026-11-12 09:00")}
	if got, ok := Next(w, None, at("2026-11-10 00:00")); !ok || !got.Equal(w.Start) {
		t.Errorf("first run = %v %v, want the start", got, ok)
	}
	if _, ok := Next(w, None, w.Start); ok {
		t.Error("a one-time broadcast has no second run")
	}
	if got, _ := Next(w, Daily, w.Start); !got.Equal(at("2026-11-13 09:00")) {
		t.Errorf("daily = %v", got)
	}
	if got, _ := Next(w, Weekly, at("2026-11-20 10:00")); !got.Equal(at("2026-11-26 09:00")) {
		t.Errorf("weekly after a missed run = %v, want the next slot (no backlog)", got)
	}
	w.End = ptr(at("2026-11-14 09:00"))
	if _, ok := Next(w, Daily, at("2026-11-13 09:00")); ok {
		t.Error("run at the end time must not happen (end is exclusive)")
	}

	m := Window{Start: at("2026-01-31 18:00")}
	got := []time.Time{}
	after := m.Start
	for i := 0; i < 3; i++ {
		after, _ = Next(m, Monthly, after)
		got = append(got, after)
	}
	want := []time.Time{at("2026-02-28 18:00"), at("2026-03-31 18:00"), at("2026-04-30 18:00")}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("monthly run %d = %v, want %v (clamped to month end)", i+1, got[i], want[i])
		}
	}
}
