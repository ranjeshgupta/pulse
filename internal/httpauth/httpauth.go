// Package httpauth adds custom authentication to the HTTP requests channel providers send (the
// custom gateway, Gupshup, Gmail's XOAUTH2 tokens): none, basic, a static bearer token, an API key
// in a header or query parameter, OAuth2 (client credentials, or a refresh token), and a login
// flow — a login request whose answer holds an access token (read by JSON path, header or regular
// expression) and optionally its lifetime, then put into each main request (a header, a query
// parameter or a body field).
//
// Tokens are cached per provider (Options.Scope names the workspace and the provider) in a shared
// Cache (Redis, sealed with the server key), with a lock so that only one instance logs in when a
// token is missing or about to expire; the others wait for its result. A token is refreshed shortly
// before it expires, and once when the main request answers 401 (Do).
//
// Settings live in the provider's config as JSON (Config); secrets are the provider's sealed
// secrets: auth_username, auth_password, auth_token, auth_api_key, auth_client_secret,
// auth_refresh_token, and any other key a login template names as {{secrets.key}}.
package httpauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"pulse/internal/safehttp"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Auth types.
const (
	None   = "none"
	Basic  = "basic"
	Bearer = "bearer"
	APIKey = "api_key"
	OAuth2 = "oauth2"
	Login  = "login"
)

// Types lists the auth types in the order the form offers them.
var Types = []string{None, Basic, Bearer, APIKey, OAuth2, Login}

// Secret keys the auth types read.
const (
	SecretUsername     = "auth_username"
	SecretPassword     = "auth_password"
	SecretToken        = "auth_token"
	SecretAPIKey       = "auth_api_key"
	SecretClientSecret = "auth_client_secret"
	SecretRefreshToken = "auth_refresh_token"
)

// ErrRejected: the token or login endpoint refused the credentials (a 4xx other than 429), or
// the main request still answered 401 with a fresh token. Retrying won't help.
var ErrRejected = errors.New("authentication credentials rejected")

// ErrConfig: the settings can't produce a token (a secret is missing, the answer has no token
// where the settings say). Retrying won't help either.
var ErrConfig = errors.New("authentication settings")

// Config is the authentication of one provider (config["auth"], JSON).
type Config struct {
	Type string `json:"type"`
	// api_key: where the key goes (header or query) and under which name.
	KeyIn   string `json:"key_in,omitempty"`
	KeyName string `json:"key_name,omitempty"`
	// oauth2: the token endpoint, client and what is asked for. Grant is client_credentials
	// (default) or refresh_token (Gmail XOAUTH2). ClientAuth sends the client secret in the form
	// body (default) or as HTTP basic auth.
	TokenURL   string `json:"token_url,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Audience   string `json:"audience,omitempty"`
	Grant      string `json:"grant,omitempty"`
	ClientAuth string `json:"client_auth,omitempty"`
	// login: the login request and how its answer is read.
	Login *LoginRequest `json:"login,omitempty"`
	// Inject is where a token (bearer, oauth2, login) goes in each main request.
	Inject *Inject `json:"inject,omitempty"`
}

// LoginRequest is the request that returns an access token. URL, header values and the body may
// name secrets as {{secrets.key}}.
type LoginRequest struct {
	Method      string   `json:"method,omitempty"` // POST by default
	URL         string   `json:"url"`
	Headers     []Header `json:"headers,omitempty"`
	ContentType string   `json:"content_type,omitempty"` // application/json by default
	Body        string   `json:"body,omitempty"`
	// TokenFrom is json (TokenPath is a dotted path, e.g. data.access_token or items.0.token),
	// header (TokenPath is the header name) or regex (TokenPath is an expression on the body;
	// its first group, or the whole match).
	TokenFrom string `json:"token_from"`
	TokenPath string `json:"token_path"`
	// ExpiryFrom is json (ExpiryPath: seconds left, a Unix time or an RFC 3339 time), fixed
	// (ExpirySeconds) or empty (DefaultLifetime, refreshed on 401).
	ExpiryFrom    string `json:"expiry_from,omitempty"`
	ExpiryPath    string `json:"expiry_path,omitempty"`
	ExpirySeconds int    `json:"expiry_seconds,omitempty"`
}

// Header is one request header.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Inject puts the token into a request: In is header (Name defaults to Authorization),
// query or body (a top-level field of a JSON object or form body); Template is the value with
// {{token}} (default "Bearer {{token}}" in the Authorization header, "{{token}}" elsewhere).
type Inject struct {
	In       string `json:"in"`
	Name     string `json:"name,omitempty"`
	Template string `json:"template,omitempty"`
}

// DefaultLifetime is how long a token without a known expiry is used (it is refreshed sooner
// when a request answers 401).
const DefaultLifetime = time.Hour

// Limits on what is read and how long the lock is held.
const (
	maxTokenResponse = 1 << 20
	lockTTL          = 15 * time.Second
	lockWait         = 8 * time.Second
	lockPoll         = 100 * time.Millisecond
)

// Parse reads config["auth"]; empty means none.
func Parse(raw string) (Config, error) {
	var c Config
	if strings.TrimSpace(raw) == "" {
		return Config{Type: None}, nil
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, fmt.Errorf("authentication settings: %w", err)
	}
	if c.Type == "" {
		c.Type = None
	}
	return c, nil
}

// URLs returns the endpoints this configuration sends credentials to (token and login URLs).
func (c Config) URLs() []string {
	var out []string
	if c.Type == OAuth2 && c.TokenURL != "" {
		out = append(out, c.TokenURL)
	}
	if c.Type == Login && c.Login != nil && c.Login.URL != "" {
		out = append(out, c.Login.URL)
	}
	return out
}

// TokenBased reports whether requests carry a fetched token (refreshed on 401).
func (c Config) TokenBased() bool { return c.Type == OAuth2 || c.Type == Login }

// SecretKeys lists the secrets this configuration reads, for the form and validation.
func (c Config) SecretKeys() []string {
	switch c.Type {
	case Basic:
		return []string{SecretUsername, SecretPassword}
	case Bearer:
		return []string{SecretToken}
	case APIKey:
		return []string{SecretAPIKey}
	case OAuth2:
		if c.Grant == "refresh_token" {
			return []string{SecretClientSecret, SecretRefreshToken}
		}
		return []string{SecretClientSecret}
	case Login:
		var keys []string
		if c.Login != nil {
			seen := map[string]bool{}
			add := func(s string) {
				for _, m := range secretRe.FindAllStringSubmatch(s, -1) {
					if !seen[m[1]] {
						seen[m[1]] = true
						keys = append(keys, m[1])
					}
				}
			}
			add(c.Login.URL)
			add(c.Login.Body)
			for _, h := range c.Login.Headers {
				add(h.Value)
			}
		}
		sort.Strings(keys)
		return keys
	}
	return nil
}

var secretRe = regexp.MustCompile(`\{\{\s*secrets\.([A-Za-z0-9_\-]+)\s*\}\}`)
var tokenRe = regexp.MustCompile(`\{\{\s*token\s*\}\}`)
var headerName = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// Validate checks the settings (and, when checkSecrets, that every secret they read is present).
// allowHTTP permits plain http endpoints (local development).
func (c Config) Validate(secrets map[string]string, checkSecrets, allowHTTP bool) error {
	switch c.Type {
	case None, Basic, Bearer:
	case APIKey:
		if c.KeyIn != "" && c.KeyIn != "header" && c.KeyIn != "query" {
			return errors.New("the API key goes in a header or a query parameter")
		}
		if c.KeyName != "" && c.KeyIn != "query" && !headerName.MatchString(c.KeyName) {
			return errors.New("the API key header name isn't a valid header name")
		}
	case OAuth2:
		if err := checkURL("Token URL", c.TokenURL, allowHTTP); err != nil {
			return err
		}
		if c.ClientID == "" {
			return errors.New("OAuth2 needs a client ID")
		}
		if c.Grant != "" && c.Grant != "client_credentials" && c.Grant != "refresh_token" {
			return errors.New("OAuth2 grant must be client_credentials or refresh_token")
		}
		if c.ClientAuth != "" && c.ClientAuth != "body" && c.ClientAuth != "basic" {
			return errors.New("the client secret goes in the body or as basic auth")
		}
	case Login:
		l := c.Login
		if l == nil {
			return errors.New("the login flow needs a login request")
		}
		if err := fixedOrigin("Login URL", l.URL); err != nil {
			return err
		}
		if err := checkURL("Login URL", secretRe.ReplaceAllString(l.URL, "x"), allowHTTP); err != nil {
			return err
		}
		switch strings.ToUpper(l.Method) {
		case "", "POST", "GET", "PUT":
		default:
			return errors.New("the login request's method must be POST, GET or PUT")
		}
		for _, h := range l.Headers {
			if !headerName.MatchString(h.Name) || strings.ContainsAny(h.Value, "\r\n") {
				return fmt.Errorf("login header %q isn't valid", h.Name)
			}
		}
		switch l.TokenFrom {
		case "json", "header":
			if strings.TrimSpace(l.TokenPath) == "" {
				return errors.New("say where the token is in the login answer")
			}
		case "regex":
			re, err := regexp.Compile(l.TokenPath)
			if err != nil {
				return fmt.Errorf("token expression: %w", err)
			}
			if re.NumSubexp() > 1 {
				return errors.New("the token expression may have at most one group")
			}
		default:
			return errors.New("read the token from json, a header or a regex")
		}
		switch l.ExpiryFrom {
		case "", "none":
		case "json":
			if strings.TrimSpace(l.ExpiryPath) == "" {
				return errors.New("say where the expiry is in the login answer")
			}
		case "fixed":
			if l.ExpirySeconds < 10 || l.ExpirySeconds > 30*86400 {
				return errors.New("a fixed token lifetime is 10 seconds to 30 days")
			}
		default:
			return errors.New("the expiry comes from json, a fixed lifetime or nowhere")
		}
	default:
		return fmt.Errorf("unknown authentication type %q", c.Type)
	}
	if in := c.Inject; in != nil && (c.TokenBased() || c.Type == Bearer) {
		switch in.In {
		case "", "header":
			if in.Name != "" && !headerName.MatchString(in.Name) {
				return errors.New("the token header name isn't a valid header name")
			}
		case "query", "body":
			if in.Name == "" {
				return errors.New("name the query parameter or body field that takes the token")
			}
		default:
			return errors.New("the token goes in a header, a query parameter or the body")
		}
		if strings.ContainsAny(in.Template, "\r\n") {
			return errors.New("the token template can't span lines")
		}
	}
	if checkSecrets {
		for _, k := range c.SecretKeys() {
			if secrets[k] == "" {
				return fmt.Errorf("the secret %s is required", k)
			}
		}
	}
	return nil
}

// origin is the scheme://host[:port] part of a URL as written (before any template is filled).
func origin(raw string) string {
	raw = strings.TrimSpace(raw)
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	if j := strings.IndexAny(raw[i+3:], "/?#"); j >= 0 {
		return raw[:i+3+j]
	}
	return raw
}

// fixedOrigin refuses a URL whose scheme or host holds a template: only the path and query may
// name {{secrets.x}}, so a secret can never choose (or leak through) the host that is called.
func fixedOrigin(label, raw string) error {
	if strings.Contains(origin(raw), "{{") || strings.Contains(origin(raw), "}}") {
		return fmt.Errorf("%s: variables may only be used in the path and query, not in the scheme or host", label)
	}
	return nil
}

func checkURL(label, raw string, allowHTTP bool) error {
	if err := fixedOrigin(label, raw); err != nil {
		return err
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(allowHTTP && u.Scheme == "http")) {
		if allowHTTP {
			return fmt.Errorf("%s must be an http(s) URL", label)
		}
		return fmt.Errorf("%s must be an https URL", label)
	}
	return nil
}

// Token is an access token and when it expires.
type Token struct {
	Value   string    `json:"t"`
	Expires time.Time `json:"e"`
	Issued  time.Time `json:"i,omitempty"`
}

// fresh reports whether the token can still be used at now: before its expiry minus a margin of
// a tenth of its lifetime, at most a minute.
func (t Token) fresh(now time.Time) bool {
	if t.Value == "" {
		return false
	}
	margin := min(time.Minute, t.Expires.Sub(t.Issued)/10)
	return now.Before(t.Expires.Add(-margin))
}

// Options are what an Auth needs besides its settings.
type Options struct {
	HTTP *http.Client // for the login and token requests (a safehttp client)
	// Cache shares tokens between instances; nil keeps them in this Auth only.
	Cache Cache
	// Scope is "<tenant>:<provider>"; empty disables the shared cache (tests of unsaved settings).
	Scope     string
	AllowHTTP bool
	Now       func() time.Time
}

// Auth authenticates one provider's requests.
type Auth struct {
	cfg     Config
	secrets map[string]string
	opt     Options
	key     string // cache key: scope and a fingerprint of the settings and secrets

	mu    sync.Mutex
	local Token
}

// New builds an Auth; the settings are validated with their secrets.
func New(cfg Config, secrets map[string]string, opt Options) (*Auth, error) {
	if err := cfg.Validate(secrets, true, opt.AllowHTTP); err != nil {
		return nil, err
	}
	if opt.HTTP == nil {
		opt.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	// Login and token requests carry secrets: never follow a redirect to another site.
	opt.HTTP = safehttp.SameOrigin(opt.HTTP)
	if opt.Now == nil {
		opt.Now = time.Now
	}
	a := &Auth{cfg: cfg, secrets: secrets, opt: opt}
	if opt.Scope != "" && opt.Cache != nil {
		a.key = "httpauth:" + opt.Scope + ":" + fingerprint(cfg, secrets)
	}
	return a, nil
}

// Type is the configured auth type.
func (a *Auth) Type() string { return a.cfg.Type }

// fingerprint changes whenever the settings or secrets change, so a new password never reuses a
// token fetched with the old one.
func fingerprint(cfg Config, secrets map[string]string) string {
	h := sha256.New()
	b, _ := json.Marshal(cfg)
	h.Write(b)
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "\x00%s\x00%s", k, secrets[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// Token returns a usable token: this process's copy, the shared cache's, or a new one fetched
// under the lock. Static types have none.
func (a *Auth) Token(ctx context.Context) (Token, error) {
	switch a.cfg.Type {
	case Bearer:
		return Token{Value: a.secrets[SecretToken]}, nil
	case OAuth2, Login:
	default:
		return Token{}, nil
	}
	now := a.opt.Now()
	a.mu.Lock()
	t := a.local
	a.mu.Unlock()
	if t.fresh(now) {
		return t, nil
	}
	if a.key == "" {
		return a.refresh(ctx)
	}
	if t, ok := a.cached(ctx, now); ok {
		return t, nil
	}
	got, err := a.opt.Cache.Lock(ctx, a.key+":lock", lockTTL)
	if err != nil {
		return a.refresh(ctx) // cache down: still send, without sharing
	}
	if got {
		defer a.opt.Cache.Unlock(context.WithoutCancel(ctx), a.key+":lock")
		if t, ok := a.cached(ctx, a.opt.Now()); ok { // another instance may have just stored one
			return t, nil
		}
		return a.refresh(ctx)
	}
	// Another instance is logging in: wait for its token.
	deadline := time.Now().Add(lockWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return Token{}, ctx.Err()
		case <-time.After(lockPoll):
		}
		if t, ok := a.cached(ctx, a.opt.Now()); ok {
			return t, nil
		}
	}
	return a.refresh(ctx)
}

func (a *Auth) cached(ctx context.Context, now time.Time) (Token, bool) {
	raw, err := a.opt.Cache.Get(ctx, a.key)
	if err != nil || raw == "" {
		return Token{}, false
	}
	var t Token
	if json.Unmarshal([]byte(raw), &t) != nil || !t.fresh(now) {
		return Token{}, false
	}
	a.mu.Lock()
	a.local = t
	a.mu.Unlock()
	return t, true
}

// refresh fetches a new token and stores it here and in the shared cache.
func (a *Auth) refresh(ctx context.Context) (Token, error) {
	t, err := a.fetch(ctx)
	if err != nil {
		return Token{}, err
	}
	t.Issued = a.opt.Now()
	a.mu.Lock()
	a.local = t
	a.mu.Unlock()
	if a.key != "" {
		if ttl := t.Expires.Sub(a.opt.Now()); ttl > 0 {
			b, _ := json.Marshal(t)
			_ = a.opt.Cache.Set(ctx, a.key, string(b), ttl)
		}
	}
	return t, nil
}

// Invalidate forgets a token the server refused, here and in the shared cache (unless another
// instance has already replaced it).
func (a *Auth) Invalidate(ctx context.Context, stale Token) {
	a.mu.Lock()
	if a.local.Value == stale.Value {
		a.local = Token{}
	}
	a.mu.Unlock()
	if a.key != "" {
		b, _ := json.Marshal(stale)
		_ = a.opt.Cache.DeleteIf(ctx, a.key, string(b))
	}
}

// fetch runs the OAuth2 or login request.
func (a *Auth) fetch(ctx context.Context) (Token, error) {
	if a.cfg.Type == OAuth2 {
		return a.oauth2(ctx)
	}
	return a.login(ctx)
}

func (a *Auth) oauth2(ctx context.Context) (Token, error) {
	form := url.Values{}
	if a.cfg.Grant == "refresh_token" {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", a.secrets[SecretRefreshToken])
	} else {
		form.Set("grant_type", "client_credentials")
	}
	if a.cfg.Scope != "" {
		form.Set("scope", a.cfg.Scope)
	}
	if a.cfg.Audience != "" {
		form.Set("audience", a.cfg.Audience)
	}
	if a.cfg.ClientAuth != "basic" {
		form.Set("client_id", a.cfg.ClientID)
		form.Set("client_secret", a.secrets[SecretClientSecret])
	}
	if err := fixedOrigin("Token URL", a.cfg.TokenURL); err != nil { // checked again at request time
		return Token{}, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if a.cfg.ClientAuth == "basic" {
		req.SetBasicAuth(url.QueryEscape(a.cfg.ClientID), url.QueryEscape(a.secrets[SecretClientSecret]))
	}
	body, hdr, err := a.send(req, "token")
	if err != nil {
		return Token{}, err
	}
	_ = hdr
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return Token{}, errors.New("token endpoint: the answer isn't JSON")
	}
	tok, _ := lookup(v, "access_token")
	s := scalar(tok)
	if s == "" {
		return Token{}, fmt.Errorf("%w: token endpoint: no access_token in the answer", ErrConfig)
	}
	exp := a.opt.Now().Add(DefaultLifetime)
	if e, ok := lookup(v, "expires_in"); ok {
		if t, ok := expiry(e, a.opt.Now()); ok {
			exp = t
		}
	}
	return Token{Value: s, Expires: exp}, nil
}

func (a *Auth) login(ctx context.Context) (Token, error) {
	req, err := a.LoginRequest(ctx, a.secrets)
	if err != nil {
		return Token{}, err
	}
	body, hdr, err := a.send(req, "login")
	if err != nil {
		return Token{}, err
	}
	return a.extract(body, hdr)
}

// LoginRequest builds the login request with the given secrets (the preview passes masked ones).
func (a *Auth) LoginRequest(ctx context.Context, secrets map[string]string) (*http.Request, error) {
	l := a.cfg.Login
	ct := l.ContentType
	if ct == "" {
		ct = "application/json"
	}
	if err := fixedOrigin("Login URL", l.URL); err != nil { // checked again at request time
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	u, err := fill(l.URL, secrets, url.QueryEscape)
	if err != nil {
		return nil, err
	}
	if pu, err := url.Parse(u); err != nil || !strings.EqualFold(origin(u), origin(l.URL)) || pu.Host == "" {
		return nil, fmt.Errorf("%w: the login URL's host changed once filled in", ErrConfig)
	}
	esc := func(s string) string { return s }
	switch {
	case strings.Contains(ct, "json"):
		esc = jsonEsc
	case strings.Contains(ct, "x-www-form-urlencoded"):
		esc = url.QueryEscape
	}
	body, err := fill(l.Body, secrets, esc)
	if err != nil {
		return nil, err
	}
	if strings.Contains(ct, "json") && strings.TrimSpace(body) != "" && !json.Valid([]byte(body)) {
		return nil, errors.New("login body isn't valid JSON once the secrets are filled in")
	}
	method := strings.ToUpper(l.Method)
	if method == "" {
		method = http.MethodPost
	}
	var rd io.Reader
	if body != "" && method != http.MethodGet {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if rd != nil {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Accept", "application/json, */*")
	for _, h := range l.Headers {
		v, err := fill(h.Value, secrets, func(s string) string { return s })
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("login header %s can't contain line breaks", h.Name)
		}
		req.Header.Set(h.Name, v)
	}
	return req, nil
}

func jsonEsc(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// fill replaces {{secrets.key}} with escaped secret values; a missing secret is an error.
func fill(src string, secrets map[string]string, esc func(string) string) (string, error) {
	var missing string
	out := secretRe.ReplaceAllStringFunc(src, func(m string) string {
		k := secretRe.FindStringSubmatch(m)[1]
		v, ok := secrets[k]
		if !ok || v == "" {
			missing = k
		}
		return esc(v)
	})
	if missing != "" {
		return "", fmt.Errorf("%w: the secret %s is not set", ErrConfig, missing)
	}
	return out, nil
}

// send runs a login or token request: 2xx returns the body; 4xx other than 429 is ErrRejected.
func (a *Auth) send(req *http.Request, what string) ([]byte, http.Header, error) {
	resp, err := a.opt.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s request: %w", what, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse))
	if err != nil {
		return nil, nil, fmt.Errorf("%s request: %w", what, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, resp.Header, nil
	}
	msg := fmt.Sprintf("%s request: HTTP %d: %.200s", what, resp.StatusCode, body)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return nil, nil, fmt.Errorf("%w: %s", ErrRejected, msg)
	}
	return nil, nil, errors.New(msg)
}

// extract reads the token and its expiry from a login answer.
func (a *Auth) extract(body []byte, hdr http.Header) (Token, error) {
	l := a.cfg.Login
	now := a.opt.Now()
	var doc any
	parsed := json.Unmarshal(body, &doc) == nil
	var tok string
	switch l.TokenFrom {
	case "json":
		if !parsed {
			return Token{}, fmt.Errorf("%w: login: the answer isn't JSON", ErrConfig)
		}
		v, _ := lookup(doc, l.TokenPath)
		tok = scalar(v)
	case "header":
		tok = strings.TrimSpace(hdr.Get(l.TokenPath))
		if strings.HasPrefix(strings.ToLower(tok), "bearer ") {
			tok = strings.TrimSpace(tok[7:])
		}
	case "regex":
		re, err := regexp.Compile(l.TokenPath)
		if err != nil {
			return Token{}, err
		}
		if m := re.FindSubmatch(body); m != nil {
			tok = string(m[len(m)-1])
		}
	}
	if tok == "" {
		return Token{}, fmt.Errorf("%w: login: no token at %s %q in the answer", ErrConfig, l.TokenFrom, l.TokenPath)
	}
	exp := now.Add(DefaultLifetime)
	switch l.ExpiryFrom {
	case "fixed":
		exp = now.Add(time.Duration(l.ExpirySeconds) * time.Second)
	case "json":
		if v, ok := lookup(doc, l.ExpiryPath); parsed && ok {
			if t, ok := expiry(v, now); ok {
				exp = t
			}
		}
	}
	return Token{Value: tok, Expires: exp}, nil
}

// lookup follows a dotted path (a leading "$." is allowed; numbers index arrays).
func lookup(v any, path string) (any, bool) {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), ".")
	if path == "" {
		return v, true
	}
	for _, part := range strings.Split(path, ".") {
		if n, err := strconv.Atoi(part); err == nil {
			arr, ok := v.([]any)
			if !ok || n < 0 || n >= len(arr) {
				return nil, false
			}
			v = arr[n]
			continue
		}
		// items[0] is read as items.0
		for part != "" {
			name, rest, _ := strings.Cut(part, "[")
			if name != "" {
				m, ok := v.(map[string]any)
				if !ok {
					return nil, false
				}
				if v, ok = m[name]; !ok {
					return nil, false
				}
			}
			if rest == "" {
				break
			}
			idx, after, _ := strings.Cut(rest, "]")
			n, err := strconv.Atoi(idx)
			arr, ok := v.([]any)
			if err != nil || !ok || n < 0 || n >= len(arr) {
				return nil, false
			}
			v, part = arr[n], after
		}
	}
	return v, true
}

// Lookup is lookup for other packages (custom request success and receipt paths).
func Lookup(v any, path string) (any, bool) { return lookup(v, path) }

// scalar is a JSON string or number as text ("" for anything else).
func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// Scalar is scalar for other packages.
func Scalar(v any) string { return scalar(v) }

// expiry reads seconds left (under 10^9), a Unix time in seconds or milliseconds, or an RFC 3339
// time.
func expiry(v any, now time.Time) (time.Time, bool) {
	s := strings.TrimSpace(scalar(v))
	if s == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		switch {
		case n <= 0:
			return time.Time{}, false
		case n < 1e9:
			return now.Add(time.Duration(n * float64(time.Second))), true
		case n < 1e12:
			return time.Unix(int64(n), 0), true
		default:
			return time.UnixMilli(int64(n)), true
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// Apply authenticates a request whose body is body (nil for none) with token t (from Token) and
// returns the body to send (changed only when the token goes in the body).
func (a *Auth) Apply(req *http.Request, body []byte, t Token) ([]byte, error) {
	switch a.cfg.Type {
	case Basic:
		req.SetBasicAuth(a.secrets[SecretUsername], a.secrets[SecretPassword])
		return body, nil
	case APIKey:
		name := a.cfg.KeyName
		if a.cfg.KeyIn == "query" {
			if name == "" {
				name = "api_key"
			}
			q := req.URL.Query()
			q.Set(name, a.secrets[SecretAPIKey])
			req.URL.RawQuery = q.Encode()
			return body, nil
		}
		if name == "" {
			name = "X-API-Key"
		}
		req.Header.Set(name, a.secrets[SecretAPIKey])
		return body, nil
	case Bearer, OAuth2, Login:
	default:
		return body, nil
	}
	in := Inject{In: "header"}
	if a.cfg.Inject != nil {
		in = *a.cfg.Inject
	}
	switch in.In {
	case "", "header":
		name, tpl := in.Name, in.Template
		if name == "" {
			name = "Authorization"
		}
		if tpl == "" {
			tpl = "{{token}}"
			if strings.EqualFold(name, "Authorization") {
				tpl = "Bearer {{token}}"
			}
		}
		req.Header.Set(name, tokenRe.ReplaceAllLiteralString(tpl, t.Value))
	case "query":
		tpl := in.Template
		if tpl == "" {
			tpl = "{{token}}"
		}
		q := req.URL.Query()
		q.Set(in.Name, tokenRe.ReplaceAllLiteralString(tpl, t.Value))
		req.URL.RawQuery = q.Encode()
	case "body":
		tpl := in.Template
		if tpl == "" {
			tpl = "{{token}}"
		}
		val := tokenRe.ReplaceAllLiteralString(tpl, t.Value)
		ct := req.Header.Get("Content-Type")
		if strings.Contains(ct, "x-www-form-urlencoded") {
			form, err := url.ParseQuery(string(body))
			if err != nil {
				return nil, errors.New("the token goes in the body, but the body isn't a form")
			}
			form.Set(in.Name, val)
			body = []byte(form.Encode())
		} else {
			var obj map[string]any
			if len(bytes.TrimSpace(body)) == 0 {
				obj = map[string]any{}
			} else if err := json.Unmarshal(body, &obj); err != nil {
				return nil, errors.New("the token goes in the body, but the body isn't a JSON object")
			}
			obj[in.Name] = val
			body, _ = json.Marshal(obj)
		}
	}
	return body, nil
}

// Do authenticates and sends the request newReq builds (with its body), and on a 401 answer to a
// fetched token refreshes it and tries once more. The caller closes the response body.
func (a *Auth) Do(ctx context.Context, client *http.Client, newReq func() (*http.Request, []byte, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		t, err := a.Token(ctx)
		if err != nil {
			return nil, err
		}
		req, body, err := newReq()
		if err != nil {
			return nil, err
		}
		if body, err = a.Apply(req, body, t); err != nil {
			return nil, err
		}
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
			b := body
			req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusUnauthorized || !a.cfg.TokenBased() || attempt > 0 {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		a.Invalidate(ctx, t)
	}
}

// TestResult is what "Test connection" shows: the token masked and when it expires.
type TestResult struct {
	Type    string    `json:"type"`
	Token   string    `json:"token,omitempty"`
	Expires time.Time `json:"expires,omitempty"`
	Static  bool      `json:"static"`
	// Server: a mail server Test connection reached (SMTP), and LoggedIn whether it also logged in.
	Server   string `json:"server,omitempty"`
	LoggedIn bool   `json:"logged_in,omitempty"`
}

// Test runs the login or token request now (never from the cache) and reports the result.
func (a *Auth) Test(ctx context.Context) (TestResult, error) {
	r := TestResult{Type: a.cfg.Type, Static: !a.cfg.TokenBased()}
	if r.Static {
		switch a.cfg.Type {
		case Bearer:
			r.Token = Mask(a.secrets[SecretToken])
		case APIKey:
			r.Token = Mask(a.secrets[SecretAPIKey])
		}
		return r, nil
	}
	t, err := a.fetch(ctx)
	if err != nil {
		return r, err
	}
	r.Token, r.Expires = Mask(t.Value), t.Expires
	return r, nil
}

// Mask shows a secret's first and last four characters at most (nothing of a short one).
func Mask(s string) string {
	switch {
	case s == "":
		return ""
	case len(s) < 12:
		return "••••"
	case len(s) < 24:
		return s[:2] + "…" + s[len(s)-2:]
	}
	return s[:4] + "…" + s[len(s)-4:]
}

// MaskSecrets returns a map whose values are placeholders naming each key (for previews).
func MaskSecrets(secrets map[string]string, keys []string) map[string]string {
	out := map[string]string{}
	for k := range secrets {
		out[k] = "••••" + k + "••••"
	}
	for _, k := range keys {
		out[k] = "••••" + k + "••••"
	}
	return out
}
