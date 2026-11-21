package api

// POST /v1/channels/domain-check {domain, service?, selectors?}: the sending domain's SPF, DKIM,
// DMARC, BIMI and MX against the Gmail / Yahoo bulk-sender rules (package domaincheck, O6).

import (
	"context"
	"net"
	"net/http"
	"time"

	"pulse/internal/domaincheck"
)

// domainResolver is the DNS the checks use (tests replace it).
var domainResolver domaincheck.Resolver = &net.Resolver{PreferGo: true}

const domainChecksPerMinute = 20

func (s *Server) domainCheckRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/channels/domain-check", s.tenant(s.domainCheck))
}

func (s *Server) domainCheck(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Domain    string   `json:"domain"`
		Service   string   `json:"service"`
		Selectors []string `json:"selectors"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Selectors) > 5 {
		req.Selectors = req.Selectors[:5]
	}
	if !s.allowTenant(r.Context(), "domaincheck", tenant, domainChecksPerMinute) {
		writeErr(w, http.StatusTooManyRequests, "too many checks; try again in a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := domaincheck.Run(ctx, domainResolver, req.Domain, req.Service, req.Selectors)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
