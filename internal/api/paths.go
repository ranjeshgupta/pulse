package api

// POST /v1/analytics/paths {start | end, steps, days, exclude}: path analysis (O6) — what people
// did after an event (or before one), step by step: the most common paths and, per step, how
// many went from each event to each next one (the flow chart), with where they stopped.

import (
	"context"
	"net/http"
	"strings"

	"pulse/internal/store/ch"
)

// PathSource runs path queries (ch.Store).
type PathSource interface {
	Paths(ctx context.Context, q ch.PathQuery) ([]ch.PathRow, uint64, error)
}

// pathLink is people moving from one event to the next at a step ("" To: they stopped there).
type pathLink struct {
	Step   int    `json:"step"`
	From   string `json:"from"`
	To     string `json:"to"`
	People uint64 `json:"people"`
}

// pathLinks folds paths into per-step links (a stop is a link to "").
func pathLinks(paths []ch.PathRow, steps int) []pathLink {
	type key struct {
		step     int
		from, to string
	}
	sum := map[key]uint64{}
	var order []key
	add := func(k key, n uint64) {
		if _, ok := sum[k]; !ok {
			order = append(order, k)
		}
		sum[k] += n
	}
	for _, p := range paths {
		for i := 0; i+1 < len(p.Steps); i++ {
			add(key{i, p.Steps[i], p.Steps[i+1]}, p.People)
		}
		if len(p.Steps)-1 < steps {
			add(key{len(p.Steps) - 1, p.Steps[len(p.Steps)-1], ""}, p.People)
		}
	}
	out := make([]pathLink, 0, len(order))
	for _, k := range order {
		out = append(out, pathLink{Step: k.step, From: k.from, To: k.to, People: sum[k]})
	}
	return out
}

func (s *Server) pathAnalysis(w http.ResponseWriter, r *http.Request, tenant int64) {
	src, ok := s.Analytics.(PathSource)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "reports need the analytics database")
		return
	}
	var req struct {
		Start   string   `json:"start"`
		End     string   `json:"end"`
		Steps   int      `json:"steps"`
		Days    int      `json:"days"`
		Exclude []string `json:"exclude"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Start, req.End = strings.TrimSpace(req.Start), strings.TrimSpace(req.End)
	if (req.Start == "") == (req.End == "") {
		writeErr(w, http.StatusBadRequest, "pick a start event or an end event")
		return
	}
	if req.Steps == 0 {
		req.Steps = 4
	}
	if req.Days == 0 {
		req.Days = 30
	}
	if req.Steps < 1 || req.Steps > 6 || req.Days < 1 || req.Days > 90 || len(req.Exclude) > 50 {
		writeErr(w, http.StatusBadRequest, "steps: 1 to 6; days: 1 to 90; exclude at most 50 events")
		return
	}
	to := s.now()
	paths, people, err := src.Paths(r.Context(), ch.PathQuery{Tenant: tenant, Start: req.Start, End: req.End, Steps: req.Steps,
		From: to.AddDate(0, 0, -req.Days), To: to, Exclude: req.Exclude})
	if err != nil {
		s.fail(w, err)
		return
	}
	if paths == nil {
		paths = []ch.PathRow{}
	}
	if req.End != "" { // read backwards: steps count back from the end event
		for i := range paths {
			p := paths[i].Steps
			rev := make([]string, len(p))
			for j := range p {
				rev[j] = p[len(p)-1-j]
			}
			paths[i].Steps = rev
		}
	}
	links := pathLinks(paths, req.Steps)
	if req.End != "" {
		for i := range paths { // back to time order for the list
			p := paths[i].Steps
			for a, b := 0, len(p)-1; a < b; a, b = a+1, b-1 {
				p[a], p[b] = p[b], p[a]
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"start": req.Start, "end": req.End, "steps": req.Steps, "days": req.Days,
		"people": people, "paths": paths, "links": links})
}
