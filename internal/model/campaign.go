package model

// Campaign settings shared by broadcasts and journeys: send deadlines and tags.

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Deadline stops late sends: a message that can't go out by the deadline is dropped (recorded as
// <channel>_failed with reason "deadline") instead of going out in a late burst, e.g. after a quota
// or quiet hours held it. At is a time of day ("21:00": the first 21:00 after the send started, in
// Timezone, UTC when empty); Hours is how late a message may be (hours after the send started).
// With both, the earlier wins.
type Deadline struct {
	At       string `json:"at,omitempty"`
	Hours    int    `json:"hours,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// MaxDeadlineHours is the longest "after X hours late".
const MaxDeadlineHours = 168

// DeadlineReason is props.reason of a message dropped by its deadline.
const DeadlineReason = "deadline"

// Validate checks a deadline (nil is none).
func (d *Deadline) Validate() error {
	if d == nil {
		return nil
	}
	if d.At == "" && d.Hours == 0 {
		return errors.New("a send deadline needs a time of day or a number of hours")
	}
	if d.At != "" {
		if _, _, ok := clock(d.At); !ok {
			return errors.New("the send deadline's time must be HH:MM")
		}
	}
	if d.Hours < 0 || d.Hours > MaxDeadlineHours {
		return fmt.Errorf("a message may be at most %d hours late", MaxDeadlineHours)
	}
	if d.Timezone != "" {
		if _, err := time.LoadLocation(d.Timezone); err != nil {
			return fmt.Errorf("unknown time zone %q", d.Timezone)
		}
	}
	return nil
}

func clock(s string) (hh, mm int, ok bool) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

// After is the absolute deadline of a send that started at start (nil: no deadline).
func (d *Deadline) After(start time.Time) *time.Time {
	if d == nil || (d.At == "" && d.Hours == 0) {
		return nil
	}
	var out time.Time
	if d.Hours > 0 {
		out = start.Add(time.Duration(d.Hours) * time.Hour)
	}
	if hh, mm, ok := clock(d.At); ok {
		loc, err := time.LoadLocation(d.Timezone)
		if err != nil || d.Timezone == "" {
			loc = time.UTC
		}
		l := start.In(loc)
		t := time.Date(l.Year(), l.Month(), l.Day(), hh, mm, 0, 0, loc)
		if !t.After(start) {
			t = t.AddDate(0, 0, 1)
		}
		if out.IsZero() || t.Before(out) {
			out = t
		}
	}
	out = out.UTC()
	return &out
}

// localClockLayout is the clock time of local-time messages (broadcast.LocalClockLayout).
const localClockLayout = "2006-01-02T15:04"

// LocalClock is the deadline of a local-time message as a clock time in the contact's zone, for a
// message due at localAt (a clock time). "" when there is none or localAt doesn't parse.
func (d *Deadline) LocalClock(localAt string) string {
	start, err := time.Parse(localClockLayout, localAt)
	if err != nil {
		return ""
	}
	// Clock times are compared as if in UTC: both are in the contact's zone.
	local := Deadline{At: d.at(), Hours: d.hours()}
	t := local.After(start)
	if t == nil {
		return ""
	}
	return t.Format(localClockLayout)
}

func (d *Deadline) at() string {
	if d == nil {
		return ""
	}
	return d.At
}

func (d *Deadline) hours() int {
	if d == nil {
		return 0
	}
	return d.Hours
}

// Tag limits.
const (
	MaxTags      = 20
	MaxTagLength = 40
)

// NormalizeTags trims tags, drops empty ones and repeats (ignoring case; the first spelling wins)
// and checks the limits. The result is never nil.
func NormalizeTags(tags []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		if utf8.RuneCountInString(t) > MaxTagLength {
			return nil, fmt.Errorf("tag %q is longer than %d characters", t, MaxTagLength)
		}
		if strings.ContainsAny(t, ",\n\t") {
			return nil, fmt.Errorf("tag %q can't contain commas", t)
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) > MaxTags {
		return nil, fmt.Errorf("at most %d tags", MaxTags)
	}
	return out, nil
}

// TestContact is someone "Send a test" can send to: an existing contact (ExternalID; the message
// is personalised with their data), or bare addresses for each channel.
type TestContact struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	ExternalID string    `json:"external_id,omitempty"`
	Email      string    `json:"email,omitempty"`
	Phone      string    `json:"phone,omitempty"`
	PushToken  string    `json:"push_token,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
