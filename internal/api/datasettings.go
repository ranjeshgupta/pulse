package api

// Workspace data settings: whether anonymous visitors are tracked, whether visitors are located by
// IP (GeoIP), and how long data is kept (cleanup runs in internal/maintenance).

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"pulse/internal/model"
)

type DataSettingsStore interface {
	DataSettings(ctx context.Context, tenant int64) (model.DataSettings, error)
	PutDataSettings(ctx context.Context, tenant int64, d model.DataSettings) error
}

const dataSettingsTTL = 30 * time.Second

func (s *Server) dataSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/settings/data", s.authed(s.getDataSettings))
	mux.HandleFunc("PUT /v1/settings/data", s.admin(s.putDataSettings))
}

func (s *Server) getDataSettings(w http.ResponseWriter, r *http.Request, p model.Principal) {
	d, err := s.Store.(DataSettingsStore).DataSettings(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": d, "geoip_available": s.Geo != nil})
}

func validRetention(r model.RetentionSettings) error {
	for _, d := range []struct {
		n    int
		what string
		min  int
	}{{r.AnonymousDays, "anonymous visitors", 7}, {r.EventDays, "events", 30}, {r.AuditDays, "the audit log", 30}} {
		if d.n != 0 && (d.n < d.min || d.n > 3650) {
			return errors.New("keep " + d.what + " for " + strconv.Itoa(d.min) + " to 3650 days, or 0 for forever")
		}
	}
	return nil
}

func (s *Server) putDataSettings(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var d model.DataSettings
	if !decode(w, r, &d) {
		return
	}
	if err := validRetention(d.Retention); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.(DataSettingsStore).PutDataSettings(r.Context(), p.TenantID, d); err != nil {
		s.fail(w, err)
		return
	}
	s.mu.Lock()
	delete(s.dataCache, p.TenantID)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"settings": d})
}

// trackingFor returns a workspace's tracking settings, cached briefly (it's read on every web event).
func (s *Server) trackingFor(ctx context.Context, tenant int64) model.TrackingSettings {
	s.mu.Lock()
	c, ok := s.dataCache[tenant]
	s.mu.Unlock()
	if ok && s.now().Before(c.exp) {
		return c.v
	}
	ds, ok := s.Store.(DataSettingsStore)
	if !ok {
		return model.DefaultDataSettings().Tracking
	}
	d, err := ds.DataSettings(ctx, tenant)
	if err != nil {
		return model.DefaultDataSettings().Tracking
	}
	s.mu.Lock()
	s.dataCache[tenant] = cachedTracking{v: d.Tracking, exp: s.now().Add(dataSettingsTTL)}
	s.mu.Unlock()
	return d.Tracking
}

type cachedTracking struct {
	v   model.TrackingSettings
	exp time.Time
}
