package providers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captured is one request a fake provider API received.
type captured struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   []byte
	User, Pass             string
	HasBasicAuth           bool
}

// fakeAPI answers every request with status/body and records what it got.
func fakeAPI(t *testing.T, status int, body string) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u, p, ok := r.BasicAuth()
		*got = captured{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(),
			Body: b, User: u, Pass: p, HasBasicAuth: ok}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// errClass names how the dispatcher treats err.
func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrUnreachable):
		return "unreachable"
	case errors.Is(err, ErrPermanent):
		return "permanent"
	default:
		return "transient"
	}
}

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("request body is not JSON: %v: %s", err, b)
	}
	return m
}

// jsonPath walks nested maps/slices: jsonPath(m, "message", "notification", "title").
func jsonPath(v any, keys ...any) any {
	for _, k := range keys {
		switch k := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

// testServiceAccount returns a service account key file whose token endpoint is tokenURL.
func testServiceAccount(t *testing.T, tokenURL string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "pulse@acme.iam.gserviceaccount.com", "private_key_id": "k1",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": tokenURL,
	})
	return string(b)
}
