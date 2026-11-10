package api

// Web push: the SDK asks for the workspace's VAPID public key to subscribe a browser. The key
// pair is created the first time anyone asks.

import (
	"context"
	"errors"
	"net/http"

	"pulse/internal/providers"
)

type VAPIDStore interface {
	VAPIDKeys(ctx context.Context, tenant int64) (string, []byte, error)
	SetVAPIDKeys(ctx context.Context, tenant int64, pub string, sealedPriv []byte) (string, error)
}

func (s *Server) webPushRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/web/push-config", s.webPushConfig)
}

// vapidPublic returns the workspace's VAPID public key, creating the pair if needed.
func (s *Server) vapidPublic(ctx context.Context, tenant int64) (string, error) {
	vs := s.Store.(VAPIDStore)
	pub, _, err := vs.VAPIDKeys(ctx, tenant)
	if err != nil || pub != "" {
		return pub, err
	}
	if s.Box == nil {
		return "", errors.New("web push needs the server's secret key")
	}
	priv, pub, err := providers.GenerateVAPIDKeys()
	if err != nil {
		return "", err
	}
	return vs.SetVAPIDKeys(ctx, tenant, pub, s.Box.Seal([]byte(priv)))
}

func (s *Server) webPushConfig(w http.ResponseWriter, r *http.Request) {
	t, ok := s.siteTenant(w, r)
	if !ok {
		return
	}
	pub, err := s.vapidPublic(r.Context(), t.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{"vapid_public_key": pub})
}
