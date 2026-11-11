package providers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":     0,
		"120":  2 * time.Minute,
		" 5 ":  5 * time.Second,
		"0":    0,
		"-3":   0,
		"soon": 0,
		now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
		now.Add(-time.Minute).Format(http.TimeFormat):     0, // in the past
	}
	for in, want := range cases {
		if got := ParseRetryAfter(in, now); got != want {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStatusErrorClassification(t *testing.T) {
	h := http.Header{"Retry-After": {"30"}}
	for _, code := range []int{429, 500, 503} {
		err := StatusError("x", code, "busy", h)
		if !Transient(err) {
			t.Errorf("%d must be transient", code)
		}
		if after, ok := RetryAfter(fmt.Errorf("send: %w", err)); !ok || after != 30*time.Second {
			t.Errorf("%d: Retry-After %v %v, want 30s", code, after, ok)
		}
	}
	if _, ok := RetryAfter(StatusError("x", 503, "busy")); ok {
		t.Error("no header, no Retry-After")
	}
	if err := StatusError("x", 503, "busy", nil); !Transient(err) {
		t.Error("nil header: still transient")
	}
	for _, code := range []int{400, 401, 403, 422} {
		if err := StatusError("x", code, "bad", h); Transient(err) || !errors.Is(err, ErrPermanent) {
			t.Errorf("%d must be permanent", code)
		}
	}
}

func TestTransient(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connection reset by peer"), true},
		{Permanent("template rejected"), false},
		{BadNumber("not a number"), false},
		{Unreachable("not on WhatsApp"), false},
		{&RetryAfterError{Err: errors.New("429"), After: time.Second}, true},
	}
	for _, c := range cases {
		if got := Transient(c.err); got != c.want {
			t.Errorf("Transient(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
