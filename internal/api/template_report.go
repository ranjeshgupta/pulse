package api

// GET /v1/templates/{id}/report?from=2026-11-01&to=2026-11-21&bucket=day|hour&tz=Asia/Kolkata:
// a template's outcomes across every campaign that sent it (batch O4). Dates are days in tz, to
// inclusive; the default is the last 30 days. Hourly series cover at most 14 days.

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"pulse/internal/store/ch"
)

// TemplateReporter builds template reports (ch.Store).
type TemplateReporter interface {
	TemplateReport(ctx context.Context, q ch.TemplateQuery) (ch.TemplateReport, error)
}

const (
	maxReportDays = 400
	maxHourlyDays = 14
)

func (s *Server) templateReport(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tr, ok := s.Analytics.(TemplateReporter)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "reports need the analytics database")
		return
	}
	t, err := s.uiStore().Template(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	q := r.URL.Query()
	zone := q.Get("tz")
	if zone == "" {
		zone = "Asia/Kolkata"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		writeErr(w, http.StatusBadRequest, "tz: unknown time zone")
		return
	}
	today := s.now().In(loc)
	day := func(name string, def time.Time) (time.Time, bool) {
		v := q.Get(name)
		if v == "" {
			return def, true
		}
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			writeErr(w, http.StatusBadRequest, name+": use a date like 2026-11-21")
			return d, false
		}
		return d, true
	}
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	from, ok := day("from", start.AddDate(0, 0, -29))
	if !ok {
		return
	}
	to, ok := day("to", start)
	if !ok {
		return
	}
	end := to.AddDate(0, 0, 1) // to is inclusive
	days := int(end.Sub(from).Hours() / 24)
	hourly := q.Get("bucket") == "hour"
	switch {
	case !end.After(from):
		writeErr(w, http.StatusBadRequest, "from must be on or before to")
		return
	case days > maxReportDays:
		writeErr(w, http.StatusBadRequest, "at most "+strconv.Itoa(maxReportDays)+" days")
		return
	case hourly && days > maxHourlyDays:
		writeErr(w, http.StatusBadRequest, "hour by hour covers at most "+strconv.Itoa(maxHourlyDays)+" days")
		return
	}
	rep, err := tr.TemplateReport(r.Context(), ch.TemplateQuery{Tenant: tenant, Template: id, From: from, To: end, Hourly: hourly, Zone: zone})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"template": map[string]any{"id": t.ID, "name": t.Name, "channel": t.Channel},
		"from":     from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "tz": zone, "bucket": map[bool]string{true: "hour", false: "day"}[hourly],
		"report": rep,
	})
}
