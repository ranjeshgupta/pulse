package segment

import (
	"errors"
	"fmt"
	"time"
)

// Refresh says when a segment's membership is computed. Live segments (the default) follow every
// event; daily segments leave the streaming index and are rebuilt from ClickHouse once a day at
// At (HH:MM) in time zone TZ, so their members change only then.
type Refresh struct {
	Mode string `json:"mode"`         // live | daily
	At   string `json:"at,omitempty"` // daily: HH:MM
	TZ   string `json:"tz,omitempty"` // daily: an IANA zone, e.g. Asia/Kolkata (default UTC)
}

// Daily reports whether the segment is rebuilt once a day.
func (r *Refresh) Daily() bool { return r != nil && r.Mode == "daily" }

// Validate checks the refresh setting and fills defaults.
func (r *Refresh) Validate() error {
	switch r.Mode {
	case "", "live":
		r.Mode, r.At, r.TZ = "live", "", ""
		return nil
	case "daily":
	default:
		return errors.New(`refresh mode must be "live" or "daily"`)
	}
	if _, err := time.Parse("15:04", r.At); err != nil || len(r.At) != 5 {
		return errors.New("daily refresh needs a time like 06:30")
	}
	if r.TZ == "" {
		r.TZ = "UTC"
	}
	if _, err := time.LoadLocation(r.TZ); err != nil {
		return fmt.Errorf("unknown time zone %q", r.TZ)
	}
	return nil
}

// Next returns the first refresh time after now.
func (r Refresh) Next(now time.Time) time.Time {
	loc, err := time.LoadLocation(r.TZ)
	if err != nil {
		loc = time.UTC
	}
	at, _ := time.Parse("15:04", r.At)
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), at.Hour(), at.Minute(), 0, 0, loc)
	if !next.After(now) {
		next = time.Date(local.Year(), local.Month(), local.Day()+1, at.Hour(), at.Minute(), 0, 0, loc)
	}
	return next.UTC()
}
