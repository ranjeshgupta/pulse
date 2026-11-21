package api

// GET /v1/templates/{id}/report?from=2026-11-01&to=2026-11-21&bucket=day|hour&tz=Asia/Kolkata:
// a template's outcomes across every campaign that sent it (batch O4). Dates are days in tz, to
// inclusive; the default is the last 30 days. Hourly series cover at most 14 days.

import (
	"context"
	"net/http"

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
	from, end, hourly, zone, ok := s.reportPeriod(w, r, maxReportDays)
	if !ok {
		return
	}
	rep, err := tr.TemplateReport(r.Context(), ch.TemplateQuery{Tenant: tenant, Template: id, From: from, To: end, Hourly: hourly, Zone: zone})
	if err != nil {
		s.fail(w, err)
		return
	}
	out := periodOut(from, end, hourly, zone)
	out["template"] = map[string]any{"id": t.ID, "name": t.Name, "channel": t.Channel}
	out["report"] = rep
	writeJSON(w, http.StatusOK, out)
}
