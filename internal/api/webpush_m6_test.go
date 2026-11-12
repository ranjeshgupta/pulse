package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/model"
	"pulse/internal/webcheck"
)

type pushMem struct {
	*memStore
	settings map[int64]webcheck.Settings
}

func (m *pushMem) TenantBySiteKey(_ context.Context, key string) (model.Tenant, error) {
	switch key {
	case "sk-1":
		return model.Tenant{ID: 1, SiteKey: key}, nil
	case "sk-2":
		return model.Tenant{ID: 2, SiteKey: key}, nil
	}
	return model.Tenant{}, errors.New("no")
}
func (m *pushMem) WebPushSettings(_ context.Context, t int64) (webcheck.Settings, error) {
	if s, ok := m.settings[t]; ok {
		return s, nil
	}
	return webcheck.Defaults(), nil
}
func (m *pushMem) PutWebPushSettings(_ context.Context, t int64, s webcheck.Settings) error {
	m.settings[t] = s
	return nil
}

// Settings are validated; the browser self-check only accepts a report for its own workspace's
// token, and the install check refuses private addresses.
func TestWebPushSettingsAndDiagnose(t *testing.T) {
	_, base, _ := newServer()
	st := &pushMem{memStore: base, settings: map[int64]webcheck.Settings{}}
	mr := miniredis.RunT(t)
	s := New(Deps{Store: st, Pub: &memPub{}, RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()}), PublicURL: "https://pulse.test",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	h := s.Handler()
	if w := do(t, h, "PUT", "/v1/settings/web-push", "key-1", `{"prompt":{"enabled":true,"style":"toast","text":"x","allow":"y"}}`); w.Code != 400 {
		t.Fatalf("bad style saved: %d", w.Code)
	}
	if w := do(t, h, "PUT", "/v1/settings/web-push", "key-1", `{"site_url":"https://shop.example/","prompt":{"enabled":true,"style":"bar","text":"Offers","allow":"Yes","repeat_days":3}}`); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w := do(t, h, "POST", "/v1/settings/web-push/check-token", "key-1", "")
	var tok struct{ Token, URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &tok)
	if w.Code != 200 || !strings.HasPrefix(tok.URL, "https://shop.example/?pulse_check=pc") {
		t.Fatalf("token: %d %s", w.Code, w.Body)
	}
	report := func(siteKey string) int {
		r := httptest.NewRequest("POST", "/v1/web/diagnose?site_key="+siteKey, strings.NewReader(`{"token":"`+tok.Token+`","report":{"permission":"default","https":true,"problems":["`+strings.Repeat("x", 500)+`"]}}`))
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		return rw.Code
	}
	if code := report("sk-2"); code != 403 {
		t.Errorf("another workspace's site key: %d", code)
	}
	if w := do(t, h, "GET", "/v1/settings/web-push/diagnosis?token="+tok.Token, "key-1", ""); !strings.Contains(w.Body.String(), `"report":null`) {
		t.Errorf("before: %s", w.Body)
	}
	if code := report("sk-1"); code != 204 {
		t.Fatalf("report: %d", code)
	}
	w = do(t, h, "GET", "/v1/settings/web-push/diagnosis?token="+tok.Token, "key-1", "")
	if !strings.Contains(w.Body.String(), `"permission":"default"`) || strings.Contains(w.Body.String(), strings.Repeat("x", 301)) {
		t.Errorf("diagnosis: %s", w.Body)
	}
	if w := do(t, h, "GET", "/v1/settings/web-push/diagnosis?token="+tok.Token, "key-2", ""); w.Code != 404 {
		t.Errorf("another workspace read the report: %d", w.Code)
	}
	// The install check fetches from the server, never a private address.
	w = do(t, h, "POST", "/v1/settings/web-push/check", "key-1", `{"site_url":"https://127.0.0.1/"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private or internal") {
		t.Errorf("check: %d %s", w.Code, w.Body)
	}
}
