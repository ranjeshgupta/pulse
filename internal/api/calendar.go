package api

// GET /v1/campaigns/calendar?from=2026-11-01&to=2026-11-30 (O6 marketing calendar): every send of
// a one-time or repeating campaign in the period (scheduled, sending or sent), and the live
// periods of triggered / automation campaigns, for the roles that may view each.

import (
	"net/http"
	"slices"
	"time"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/schedule"
)

// CalendarItem is one campaign on the calendar.
type CalendarItem struct {
	Kind    string      `json:"kind"` // broadcast | journey
	ID      int64       `json:"id"`
	Name    string      `json:"name"`
	Channel string      `json:"channel,omitempty"`
	Status  string      `json:"status"`
	Tags    []string    `json:"tags,omitempty"`
	Sends   []time.Time `json:"sends,omitempty"` // broadcasts: each run in the period
	Start   *time.Time  `json:"start,omitempty"` // journeys: from…
	End     *time.Time  `json:"end,omitempty"`   // …until (nil: open-ended)
}

const maxCalendarDays = 93

func (s *Server) campaignCalendar(w http.ResponseWriter, r *http.Request, p model.Principal) {
	q := r.URL.Query()
	from, err1 := time.Parse("2006-01-02", q.Get("from"))
	to, err2 := time.Parse("2006-01-02", q.Get("to"))
	if err1 != nil || err2 != nil || to.Before(from) || to.Sub(from) > maxCalendarDays*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "from and to: dates like 2026-11-01, at most 93 days apart")
		return
	}
	end := to.Add(24 * time.Hour)
	items := []CalendarItem{}
	if p.Perms.Allows(auth.AreaBroadcasts, auth.View) {
		bs, err := s.bcStore().ListBroadcasts(r.Context(), p.TenantID)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, b := range bs {
			it := CalendarItem{Kind: "broadcast", ID: b.ID, Name: b.Name, Channel: b.Channel, Status: b.Status, Tags: b.Tags}
			if b.Status == "draft" || b.Status == "cancelled" {
				// Drafts show at their planned time, so plans are visible before they're scheduled.
				if !b.StartAt.Before(from) && b.StartAt.Before(end) {
					it.Sends = []time.Time{b.StartAt}
				}
			} else {
				win := schedule.Window{Start: b.StartAt, End: b.EndAt}
				t, ok := schedule.Next(win, b.Repeat, from.Add(-time.Nanosecond))
				for n := 0; ok && t.Before(end) && n < 200; n++ {
					it.Sends = append(it.Sends, t)
					t, ok = schedule.Next(win, b.Repeat, t)
				}
			}
			if len(it.Sends) > 0 {
				items = append(items, it)
			}
		}
	}
	if p.Perms.Allows(auth.AreaJourneys, auth.View) {
		js, err := s.uiStore().ListJourneys(r.Context(), p.TenantID)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, j := range js {
			if j.ArchivedAt != nil || (j.Status != "active" && j.Status != "paused") {
				continue
			}
			if !j.StartAt.Before(end) || (j.EndAt != nil && j.EndAt.Before(from)) {
				continue
			}
			start := j.StartAt
			items = append(items, CalendarItem{Kind: "journey", ID: j.ID, Name: j.Name, Status: j.Status, Start: &start, End: j.EndAt})
		}
	}
	slices.SortFunc(items, func(a, b CalendarItem) int { return firstAt(a).Compare(firstAt(b)) })
	writeJSON(w, http.StatusOK, map[string]any{"from": q.Get("from"), "to": q.Get("to"), "items": items})
}

func firstAt(c CalendarItem) time.Time {
	if len(c.Sends) > 0 {
		return c.Sends[0]
	}
	if c.Start != nil {
		return *c.Start
	}
	return time.Time{}
}
