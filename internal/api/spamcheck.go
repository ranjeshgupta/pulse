package api

// POST /v1/templates/spam-check {subject, html, text}: a content spam score with fixes and the
// inbox preview line (package spamcheck, O6). Nothing is sent anywhere.

import (
	"net/http"

	"pulse/internal/spamcheck"
)

func (s *Server) spamCheckRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/templates/spam-check", s.tenant(s.spamCheck))
}

func (s *Server) spamCheck(w http.ResponseWriter, r *http.Request, _ int64) {
	var req struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
		Text    string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, spamcheck.Check(spamcheck.Email{Subject: req.Subject, HTML: req.HTML, Text: req.Text}))
}
