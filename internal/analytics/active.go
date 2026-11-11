package analytics

// Active users (M4): who counts as active for DAU / WAU / MAU, the contact page's activity counts
// and the live feed's "activity only" filter.

import "time"

// ActiveCondition is the SQL condition over the events table for a person's own activity: every
// event except Pulse's message outcomes and engagement, bookkeeping (tags, points, lists, merges,
// stages, owners, companies, journeys) and profile-only identify events, which imports and servers
// send for everyone. Migration 006 inlines the same text in its materialized views (a test checks).
const ActiveCondition = `contact_id > 0 AND name NOT IN ('identify', 'tag_added', 'tag_removed', 'points_changed', 'list_joined', 'list_left', 'contact_merged', 'contact_merged_into', 'merge_requested', 'stage_changed', 'owner_changed', 'company_linked', 'company_unlinked', 'company_updated', 'journey_entered', 'journey_completed', 'journey_split', 'journey_change', 'journey_jump_limit', 'dwc_requested', 'webhook_failed', 'unsubscribed', 'resubscribed', 'message_skipped', 'message_unreachable') AND NOT match(name, '^(email|sms|whatsapp|rcs|push|inbox|notification)_')`

// UsersDay is one day of the Users dashboard.
type UsersDay struct {
	Day       string  `json:"day"`
	DAU       uint64  `json:"dau"`
	WAU       uint64  `json:"wau"` // the 7 days ending that day
	MAU       uint64  `json:"mau"` // the 30 days ending that day
	New       uint64  `json:"new"` // first active that day
	Returning uint64  `json:"returning"`
	Sticky    float64 `json:"stickiness"` // DAU / MAU
}

// Ratio is a / b, 0 when b is 0.
func Ratio(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// FillUsers completes the days: returning = DAU − new (never below 0), stickiness = DAU / MAU,
// and days without activity as zero rows, so the trend has every day of [from, to].
func FillUsers(from, to time.Time, got map[string]UsersDay, newByDay map[string]uint64) []UsersDay {
	var out []UsersDay
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to); d = d.AddDate(0, 0, 1) {
		k := d.Format(time.DateOnly)
		u := got[k]
		u.Day = k
		u.New = min(newByDay[k], u.DAU)
		u.Returning = u.DAU - u.New
		u.Sticky = Ratio(u.DAU, u.MAU)
		out = append(out, u)
	}
	return out
}

// Stickiness is the headline DAU / MAU: the average DAU of the last 30 days of the range over the
// MAU of its last day.
func Stickiness(days []UsersDay) float64 {
	if len(days) == 0 {
		return 0
	}
	last := days[len(days)-1]
	start := max(0, len(days)-30)
	var sum uint64
	for _, d := range days[start:] {
		sum += d.DAU
	}
	avg := float64(sum) / float64(len(days)-start)
	if last.MAU == 0 {
		return 0
	}
	return avg / float64(last.MAU)
}
