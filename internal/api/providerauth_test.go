package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// provMem keeps providers like pg.Store: secrets sealed, a nil sealed keeps the stored ones.
type provMem struct {
	*memStore
	CampaignStore // only SetProviderLimits is used
	ChannelStore
	provs  map[int64]model.Provider
	sealed map[int64][]byte
	limits map[int64]model.ProviderLimits
}

func (m *provMem) GetProvider(_ context.Context, tenant, id int64) (model.Provider, error) {
	p, ok := m.provs[id]
	if !ok || p.TenantID != tenant {
		return p, pg.ErrNotFound
	}
	return p, nil
}
func (m *provMem) ProviderForWebhook(_ context.Context, id int64) (model.Provider, []byte, error) {
	p, ok := m.provs[id]
	if !ok {
		return p, nil, pg.ErrNotFound
	}
	return p, m.sealed[id], nil
}
func (m *provMem) SaveProvider(_ context.Context, p model.Provider, sealed []byte) (int64, error) {
	if p.ID == 0 {
		p.ID = int64(len(m.provs) + 1)
	}
	p.Secrets = nil
	m.provs[p.ID] = p
	if sealed != nil {
		m.sealed[p.ID] = sealed
	}
	return p.ID, nil
}
func (m *provMem) SetProviderLimits(_ context.Context, _, id int64, l model.ProviderLimits) error {
	m.limits[id] = l
	return nil
}
func (m *provMem) ContactByExternalID(ctx context.Context, t int64, ext string) (model.Contact, error) {
	return m.memStore.ContactByExternalID(ctx, t, ext)
}

func provServer(t *testing.T) (*Server, *provMem) {
	s, mem, _ := newServer()
	box, _ := auth.NewBox(strings.Repeat("ab", 32))
	pm := &provMem{memStore: mem, provs: map[int64]model.Provider{}, sealed: map[int64][]byte{}, limits: map[int64]model.ProviderLimits{}}
	s.Store, s.Box, s.AllowPrivateWebhooks = pm, box, true
	return s, pm
}

func (m *provMem) secrets(t *testing.T, s *Server, id int64) map[string]string {
	plain, err := s.Box.Open(m.sealed[id])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	_ = json.Unmarshal(plain, &out)
	return out
}

func TestProviderSecretsMergeAndHostPinning(t *testing.T) {
	s, pm := provServer(t)
	h := s.Handler()
	w := do(t, h, "POST", "/v1/channels/providers", "key-1", `{"channel":"sms","provider":"http_sms","config":{"url":"https://gw.example.com/a",
		"auth":"{\"type\":\"basic\"}"},"secrets":{"signing_secret":"sig","auth_username":"u","auth_password":"p"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	// Changing one secret keeps the others; the response never carries secrets.
	w = do(t, h, "PUT", "/v1/channels/providers/1", "key-1", `{"config":{"url":"https://gw.example.com/b","auth":"{\"type\":\"basic\"}"},"secrets":{"auth_password":"p2"}}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "p2") {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	if got := pm.secrets(t, s, 1); got["auth_username"] != "u" || got["auth_password"] != "p2" || got["signing_secret"] != "sig" {
		t.Errorf("merged secrets %v", got)
	}
	// A new host clears the saved secrets: without new ones the save is refused.
	w = do(t, h, "PUT", "/v1/channels/providers/1", "key-1", `{"config":{"url":"https://evil.example.net/b","auth":"{\"type\":\"basic\"}"},"secrets":{}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "host changed") {
		t.Errorf("host change kept secrets: %d %s", w.Code, w.Body)
	}
	if pm.provs[1].Config["url"] != "https://gw.example.com/b" {
		t.Error("refused save changed the provider")
	}
	// With every secret entered again, the save goes through and only the new ones are stored.
	w = do(t, h, "PUT", "/v1/channels/providers/1", "key-1", `{"config":{"url":"https://new.example.net/b","auth":"{\"type\":\"bearer\"}"},"secrets":{"signing_secret":"s2","auth_token":"t"}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"secrets_cleared":true`) {
		t.Fatalf("re-entered: %d %s", w.Code, w.Body)
	}
	if got := pm.secrets(t, s, 1); got["auth_password"] != "" || got["signing_secret"] != "s2" {
		t.Errorf("old secrets followed the new host: %v", got)
	}
	// The login URL's host counts too.
	login := func(host string) string {
		return fmt.Sprintf(`{"config":{"mode":"standard","url":"https://new.example.net/b","auth":"{\"type\":\"login\",\"login\":{\"url\":\"https://%s/login\",\"body\":\"{\\\"k\\\":\\\"{{secrets.k}}\\\"}\",\"token_from\":\"json\",\"token_path\":\"t\"}}"},"secrets":{"k":"key"}}`, host)
	}
	if w = do(t, h, "PUT", "/v1/channels/providers/1", "key-1", login("auth.example.net")); w.Code != http.StatusBadRequest {
		t.Errorf("login URL added without re-entering the secrets: %d %s", w.Code, w.Body)
	}
}

func TestProviderPresetLimits(t *testing.T) {
	s, pm := provServer(t)
	h := s.Handler()
	w := do(t, h, "POST", "/v1/channels/providers", "key-1", `{"channel":"email","provider":"gworkspace_smtp","config":{"username":"me@acme.com","from":"me@acme.com"},"secrets":{"password":"app pass"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if l := pm.limits[1]; l.DailyQuota != 2000 {
		t.Errorf("preset limits %+v", l)
	}
	w = do(t, h, "POST", "/v1/channels/providers", "key-1", `{"channel":"email","provider":"gmail_smtp","config":{"username":"me@gmail.com","from":"me@gmail.com"},"secrets":{"password":"x"},"limits":{"daily_quota":100}}`)
	if w.Code != http.StatusOK || pm.limits[2].DailyQuota != 100 {
		t.Errorf("explicit limits: %d %+v", w.Code, pm.limits[2])
	}
}

func TestProviderCheckAuthAndPreview(t *testing.T) {
	logins := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logins++
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["pw"] != "secret-pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":{"token":"abcdefghijklmnopqrstuvwxyz0123456789","expires_in":600}}`)
	}))
	defer api.Close()
	s, _ := provServer(t)
	h := s.Handler()
	authCfg := fmt.Sprintf(`{\"type\":\"login\",\"login\":{\"url\":\"%s/login\",\"body\":\"{\\\"pw\\\":\\\"{{secrets.pw}}\\\"}\",\"token_from\":\"json\",\"token_path\":\"data.token\",\"expiry_from\":\"json\",\"expiry_path\":\"data.expires_in\"}}`, api.URL)
	request := `{\"method\":\"POST\",\"url\":\"https://sms.example.com/send?to={{message.to}}\",\"body\":\"{\\\"text\\\":\\\"{{message.text}}\\\",\\\"pw\\\":\\\"secret-pw\\\"}\"}`
	body := `{"provider":"http_sms","config":{"mode":"custom","auth":"` + authCfg + `","request":"` + request + `"},"secrets":{"pw":"secret-pw"}}`
	w := do(t, h, "POST", "/v1/channels/providers/check-auth", "key-1", body)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"token":"abcd…6789"`) || strings.Contains(w.Body.String(), "efghij") {
		t.Fatalf("check-auth: %d %s", w.Code, w.Body)
	}
	w = do(t, h, "POST", "/v1/channels/providers/check-auth", "key-1", strings.Replace(body, `{"pw":"secret-pw"}`, `{"pw":"wrong"}`, 1))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "refused these credentials") {
		t.Errorf("rejected: %d %s", w.Code, w.Body)
	}
	w = do(t, h, "POST", "/v1/channels/providers/preview", "key-1", body)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secret-pw") || !strings.Contains(w.Body.String(), "to=%2B919812345678") ||
		!strings.Contains(w.Body.String(), "Bearer {{token}}") {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}
	if logins != 2 {
		t.Errorf("preview logged in: %d logins", logins)
	}
	// Saved secrets are write-only: testing changed settings needs every secret entered again.
	w = do(t, h, "POST", "/v1/channels/providers", "key-1", `{"channel":"sms",`+body[1:])
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	saved := strings.Replace(body, `{"provider":"http_sms",`, fmt.Sprintf(`{"provider_id":%d,`, created.ID), 1)
	if w := do(t, h, "POST", "/v1/channels/providers/check-auth", "key-1", strings.Replace(saved, `{"pw":"secret-pw"}`, `{}`, 1)); w.Code != http.StatusOK {
		t.Errorf("saved settings with saved secrets: %d %s", w.Code, w.Body)
	}
	changed := strings.Replace(strings.Replace(saved, `{"pw":"secret-pw"}`, `{}`, 1), `data.token`, `data`, 1)
	if w := do(t, h, "POST", "/v1/channels/providers/check-auth", "key-1", changed); w.Code != http.StatusBadRequest {
		t.Errorf("changed settings with saved secrets: %d %s", w.Code, w.Body)
	}
	// Saving changed authentication settings likewise needs every secret again.
	save := strings.Replace(strings.Replace(`{"channel":"sms",`+body[1:], `{"pw":"secret-pw"}`, `{}`, 1), `data.token`, `data`, 1)
	if w := do(t, h, "PUT", fmt.Sprintf("/v1/channels/providers/%d", created.ID), "key-1", save); w.Code != http.StatusBadRequest {
		t.Errorf("saving changed auth with saved secrets: %d %s", w.Code, w.Body)
	}
}
