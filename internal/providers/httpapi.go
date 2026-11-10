package providers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// Limits on what adapters read: provider API responses and incoming webhook bodies.
const (
	maxResponseBytes = 1 << 20
	maxWebhookBytes  = 4 << 20
)

// call sends req and returns the status and (size-limited) body. A network error is returned as is,
// so the dispatcher retries it.
func call(c *http.Client, req *http.Request) (int, []byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// jsonRequest builds a POST with a JSON body.
func jsonRequest(ctx context.Context, url string, v any) (*http.Request, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, Permanent(err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, Permanent(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// webhookBody reads a webhook request body, size-limited.
func webhookBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxWebhookBytes {
		return nil, errors.New("webhook body too large")
	}
	return b, nil
}

// equalSecret compares two secrets in constant time.
func equalSecret(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

// digits strips the leading + of an E.164 number (APIs that want "919812345678").
func digits(e164 string) string { return strings.TrimPrefix(e164, "+") }

// ---- Google service accounts (RBM, FCM) ----

type serviceAccount struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
}

// serviceAccountTokens turns a service account key file into a cached OAuth2 token source (the
// JWT bearer grant, as google.JWTConfigFromJSON does, without pulling in the GCE metadata client).
// Token requests use the shared HTTP client.
func serviceAccountTokens(d Deps, keyJSON, scope string) (oauth2.TokenSource, error) {
	var sa serviceAccount
	if err := json.Unmarshal([]byte(keyJSON), &sa); err != nil {
		return nil, fmt.Errorf("service account JSON: %w", err)
	}
	if sa.Type != "service_account" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("service account JSON: not a service account key (needs type, client_email, private_key)")
	}
	if blk, _ := pem.Decode([]byte(sa.PrivateKey)); blk == nil {
		return nil, errors.New("service account JSON: private_key is not a PEM key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	cfg := &jwt.Config{Email: sa.ClientEmail, PrivateKey: []byte(sa.PrivateKey), PrivateKeyID: sa.PrivateKeyID,
		Scopes: []string{scope}, TokenURL: sa.TokenURI}
	ctx := context.Background()
	if d.HTTP != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, d.HTTP)
	}
	return cfg.TokenSource(ctx), nil
}

// bearer fetches an access token. A token endpoint rejecting the key (revoked, wrong key) is
// permanent; anything else (network, Google 5xx) is transient.
func bearer(provider string, ts oauth2.TokenSource) (string, error) {
	t, err := ts.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode >= 400 && re.Response.StatusCode < 500 &&
			re.Response.StatusCode != http.StatusTooManyRequests {
			return "", Permanent(provider + ": service account rejected: " + err.Error())
		}
		return "", fmt.Errorf("%s: access token: %w", provider, err)
	}
	return t.AccessToken, nil
}
