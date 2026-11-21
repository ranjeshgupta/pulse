package api

// Campaign timelines (batch O5): GET /v1/journeys/{id}/timeline and /v1/broadcasts/{id}/timeline
// ?from=2026-11-01&to=2026-11-21&bucket=day|hour&tz=Asia/Kolkata[&step=<id>]: message outcomes by
// day or hour; for journeys also entries and finishes, the trigger segment's joins and leaves, and
// every step's outputs (yes / no / paths) per bucket.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"pulse/internal/journey"
	"pulse/internal/store/ch"
)

// CampaignTimeliner builds campaign timelines (ch.Store).
type CampaignTimeliner interface {
	CampaignTimeline(ctx context.Context, q ch.TimelineQuery) (ch.Timeline, error)
}

// maxTimelineDays bounds a timeline (journey step counts are kept 400 days, hour by hour).
const maxTimelineDays = 120

// reportPeriod reads from, to (days in tz, to inclusive; default the last 30 days), bucket and tz.
// end excludes. ok false has answered already.
func (s *Server) reportPeriod(w http.ResponseWriter, r *http.Request, maxDays int) (from, end time.Time, hourly bool, zone string, ok bool) {
	q := r.URL.Query()
	zone = q.Get("tz")
	if zone == "" {
		zone = "Asia/Kolkata"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		writeErr(w, http.StatusBadRequest, "tz: unknown time zone")
		return
	}
	today := s.now().In(loc)
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
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
	if from, ok = day("from", start.AddDate(0, 0, -29)); !ok {
		return
	}
	var to time.Time
	if to, ok = day("to", start); !ok {
		return
	}
	end = to.AddDate(0, 0, 1)
	days := int(end.Sub(from).Hours() / 24)
	hourly = q.Get("bucket") == "hour"
	ok = false
	switch {
	case !end.After(from):
		writeErr(w, http.StatusBadRequest, "from must be on or before to")
	case days > maxDays:
		writeErr(w, http.StatusBadRequest, "at most "+strconv.Itoa(maxDays)+" days")
	case hourly && days > maxHourlyDays:
		writeErr(w, http.StatusBadRequest, "hour by hour covers at most "+strconv.Itoa(maxHourlyDays)+" days")
	default:
		ok = true
	}
	return
}

func periodOut(from, end time.Time, hourly bool, zone string) map[string]any {
	return map[string]any{"from": from.Format("2006-01-02"), "to": end.AddDate(0, 0, -1).Format("2006-01-02"), "tz": zone,
		"bucket": map[bool]string{true: "hour", false: "day"}[hourly]}
}

func (s *Server) journeyTimeline(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tl, ok := s.Analytics.(CampaignTimeliner)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "reports need the analytics database")
		return
	}
	us, ok := s.Store.(UIStore)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "journeys aren't available")
		return
	}
	j, err := us.GetJourney(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	from, end, hourly, zone, ok := s.reportPeriod(w, r, maxTimelineDays)
	if !ok {
		return
	}
	var g journey.Graph
	_ = json.Unmarshal(j.Graph, &g)
	q := ch.TimelineQuery{Tenant: tenant, Source: "journey", SourceID: id, Step: r.URL.Query().Get("step"), From: from, To: end, Hourly: hourly, Zone: zone}
	if g.Trigger.Type == journey.TriggerSegmentEntered {
		q.Segment = g.Trigger.SegmentID
	}
	if q.Step != "" && !validNodeID(q.Step) {
		writeErr(w, http.StatusBadRequest, "step: not a step id")
		return
	}
	out, err := tl.CampaignTimeline(r.Context(), q)
	if err != nil {
		s.failReport(w, err)
		return
	}
	resp := periodOut(from, end, hourly, zone)
	resp["timeline"] = out
	if s.RDB != nil {
		hours, err := journey.ReadStepHours(r.Context(), s.RDB, tenant, id, from, end)
		if err != nil {
			s.fail(w, err)
			return
		}
		resp["steps"] = stepBuckets(hours, hourly, zone)
	}
	writeJSON(w, http.StatusOK, resp)
}

// stepBuckets folds hourly step counts into the period's buckets: step -> [{t, kind: output, count}].
func stepBuckets(hours []journey.HourCount, hourly bool, zone string) map[string][]ch.BucketCount {
	loc, _ := time.LoadLocation(zone)
	sum := map[string]map[[2]string]int64{}
	for _, h := range hours {
		t := h.Hour.In(loc)
		key := t.Format("2006-01-02")
		if hourly {
			key = t.Format("2006-01-02 15") + ":00"
		}
		if sum[h.Step] == nil {
			sum[h.Step] = map[[2]string]int64{}
		}
		sum[h.Step][[2]string{key, h.Output}] += h.N
	}
	out := map[string][]ch.BucketCount{}
	for step, m := range sum {
		for k, n := range m {
			out[step] = append(out[step], ch.BucketCount{T: k[0], Kind: k[1], Count: uint64(n)})
		}
		sort.Slice(out[step], func(i, j int) bool { return out[step][i].T < out[step][j].T })
	}
	return out
}

func (s *Server) broadcastTimeline(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tl, ok := s.Analytics.(CampaignTimeliner)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "reports need the analytics database")
		return
	}
	if _, err := s.bcStore().GetBroadcast(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	from, end, hourly, zone, ok := s.reportPeriod(w, r, maxReportDays)
	if !ok {
		return
	}
	out, err := tl.CampaignTimeline(r.Context(), ch.TimelineQuery{Tenant: tenant, Source: "broadcast", SourceID: id, From: from, To: end, Hourly: hourly, Zone: zone})
	if err != nil {
		s.failReport(w, err)
		return
	}
	resp := periodOut(from, end, hourly, zone)
	resp["timeline"] = out
	writeJSON(w, http.StatusOK, resp)
}

// unknownZone reports whether err is ClickHouse not knowing a time zone that Go accepted (their
// zone lists can differ).
func unknownZone(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Cannot load time zone")
}

// failReport answers a report query's error: an unknown time zone is the caller's (400).
func (s *Server) failReport(w http.ResponseWriter, err error) {
	if unknownZone(err) {
		writeErr(w, http.StatusBadRequest, "unknown time zone")
		return
	}
	s.fail(w, err)
}
