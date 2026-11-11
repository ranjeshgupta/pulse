package integrations

// A small JSON-over-HTTP helper shared by the remote APIs. Errors carry the start of the remote's
// answer, so the sync log says why ("HTTP 401: Authentication credentials not found").

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const maxRemoteBody = 8 << 20

// RemoteError is a non-2xx answer from a remote API.
type RemoteError struct {
	Status int
	Body   string
}

func (e *RemoteError) Error() string {
	msg := strings.TrimSpace(e.Body)
	if len(msg) > 300 {
		msg = msg[:300]
		for !utf8.ValidString(msg) {
			msg = msg[:len(msg)-1]
		}
		msg += "…"
	}
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("HTTP %d, the credentials were refused: %s", e.Status, msg)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, msg)
}

// StatusOf is the HTTP status of a RemoteError (0 for other errors).
func StatusOf(err error) int {
	var re *RemoteError
	if errors.As(err, &re) {
		return re.Status
	}
	return 0
}

// Retryable reports whether an error may go away by itself (network, 429, 5xx).
func Retryable(err error) bool {
	s := StatusOf(err)
	return s == 0 || s == http.StatusTooManyRequests || s >= 500
}

type caller struct {
	client  *http.Client
	headers map[string]string
}

// do sends body (JSON, url.Values as a form, or nil) and decodes a JSON answer into out (when
// non-nil). It returns the status.
func (c caller) do(ctx context.Context, method, u string, body any, out any) (int, error) {
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		rd, ctype = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return 0, err
		}
		rd, ctype = bytes.NewReader(raw), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Pulse-Integrations/1")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteBody))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, &RemoteError{Status: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("unexpected answer: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func trimSlash(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
