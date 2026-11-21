package api

// AI help (O6, package ai): subject line ideas, rewrites, translations and a segment from a
// sentence. Off unless the server has PULSE_AI_API_KEY. Each workspace may make 20 requests a
// minute; what is sent is the text being worked on (and, for segments, attribute and event
// names), never contacts' data.
//
//	GET  /v1/ai/status         {enabled, model}
//	POST /v1/ai/subject-lines  {content, subject, n}       -> {subjects}
//	POST /v1/ai/rewrite        {text, style, channel}      -> {text}
//	POST /v1/ai/translate      {fields, locale}            -> {fields}
//	POST /v1/ai/segment        {description}               -> {rule, note}

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/ai"
	"pulse/internal/segment"
)

const aiPerMinute = 20

func (s *Server) aiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/ai/status", s.tenant(s.aiStatus))
	mux.HandleFunc("POST /v1/ai/subject-lines", s.tenant(s.aiSubjects))
	mux.HandleFunc("POST /v1/ai/rewrite", s.tenant(s.aiRewrite))
	mux.HandleFunc("POST /v1/ai/translate", s.tenant(s.aiTranslate))
	mux.HandleFunc("POST /v1/ai/segment", s.tenant(s.aiSegment))
}

func (s *Server) aiStatus(w http.ResponseWriter, _ *http.Request, _ int64) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.AI != nil, "model": ai.Model})
}

// aiReady answers when AI help is off or the workspace is over its rate (false: answered).
func (s *Server) aiReady(w http.ResponseWriter, r *http.Request, tenant int64) bool {
	if s.AI == nil {
		writeErr(w, http.StatusServiceUnavailable, "AI help isn't set up on this server (PULSE_AI_API_KEY)")
		return false
	}
	if s.RDB != nil {
		k := "airate:" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(s.now().Unix()/60, 10)
		n, err := s.RDB.Incr(r.Context(), k).Result()
		if err == nil && n == 1 {
			s.RDB.Expire(r.Context(), k, 2*time.Minute)
		}
		if err == nil && n > aiPerMinute {
			writeErr(w, http.StatusTooManyRequests, "that's a lot of AI requests this minute; try again shortly")
			return false
		}
	}
	return true
}

// aiCtx bounds an AI call (the client gives up before the browser does).
func aiCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 90*time.Second)
}

func (s *Server) aiFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ai.ErrRefused):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "the AI took too long; try again")
	case strings.HasPrefix(err.Error(), "the AI ") || strings.HasPrefix(err.Error(), "style:") || strings.HasPrefix(err.Error(), "there's nothing"):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.Log.Error("ai help", "err", err)
		writeErr(w, http.StatusBadGateway, "the AI service didn't answer; try again in a moment")
	}
}

func (s *Server) aiSubjects(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Content string `json:"content"`
		Subject string `json:"subject"`
		N       int    `json:"n"`
	}
	if !decode(w, r, &req) || !s.aiReady(w, r, tenant) {
		return
	}
	if strings.TrimSpace(req.Content+req.Subject) == "" {
		writeErr(w, http.StatusBadRequest, "write some of the message first")
		return
	}
	ctx, cancel := aiCtx(r)
	defer cancel()
	out, err := s.AI.SubjectLines(ctx, req.Content, req.Subject, max(req.N, 5))
	if err != nil {
		s.aiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subjects": out})
}

func (s *Server) aiRewrite(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Text    string `json:"text"`
		Style   string `json:"style"`
		Channel string `json:"channel"`
	}
	if !decode(w, r, &req) || !s.aiReady(w, r, tenant) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "there's nothing to rewrite")
		return
	}
	ctx, cancel := aiCtx(r)
	defer cancel()
	out, err := s.AI.Rewrite(ctx, req.Text, req.Style, req.Channel)
	if err != nil {
		s.aiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": out})
}

func (s *Server) aiTranslate(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Fields map[string]string `json:"fields"`
		Locale string            `json:"locale"`
	}
	if !decode(w, r, &req) || !s.aiReady(w, r, tenant) {
		return
	}
	if len(req.Fields) == 0 || len(req.Fields) > 10 || strings.TrimSpace(req.Locale) == "" {
		writeErr(w, http.StatusBadRequest, "send 1 to 10 fields and the language")
		return
	}
	ctx, cancel := aiCtx(r)
	defer cancel()
	out, err := s.AI.Translate(ctx, req.Fields, req.Locale)
	if err != nil {
		s.aiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": out})
}

func (s *Server) aiSegment(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Description string `json:"description"`
	}
	if !decode(w, r, &req) || !s.aiReady(w, r, tenant) {
		return
	}
	if strings.TrimSpace(req.Description) == "" {
		writeErr(w, http.StatusBadRequest, "describe who should be in the segment")
		return
	}
	var attrs, events []string
	if as, ok := s.Store.(AudienceStore); ok {
		if defs, err := as.AttributeDefs(r.Context(), tenant); err == nil {
			for _, d := range defs {
				attrs = append(attrs, d.Key+" ("+d.Type+")")
			}
		}
	}
	if ra, ok := s.Analytics.(interface {
		EventNames(ctx context.Context, tenant int64) ([]string, error)
	}); ok {
		events, _ = ra.EventNames(r.Context(), tenant)
	}
	ctx, cancel := aiCtx(r)
	defer cancel()
	raw, note, err := s.AI.SegmentRule(ctx, req.Description, attrs, events)
	if err != nil {
		s.aiFail(w, err)
		return
	}
	// The same check as a rule built by hand; a rule that fails comes back with why.
	if _, err := segment.Parse(raw); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the AI's rule doesn't work: " + err.Error() + "; try rewording", "rule": raw})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule": raw, "note": note})
}
