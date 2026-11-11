package providers

// Google email presets: Gmail and Google Workspace accounts over SMTP (smtp.gmail.com, with an
// app password or XOAUTH2), and the Google Workspace SMTP relay (smtp-relay.gmail.com, allowed
// by IP address or with SMTP auth). Gmail and Workspace accounts have small daily sending limits
// (about 500 and 2,000 recipients a day), set as the provider's daily quota when it's added; bulk
// mail belongs on the relay (about 10,000 a day per user) or a sending service.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"pulse/internal/channel"
	"pulse/internal/httpauth"
	"pulse/internal/model"
	"pulse/internal/safehttp"
)

// Google SMTP endpoints (variables for tests).
var (
	gmailSMTPHost  = "smtp.gmail.com"
	gmailRelayHost = "smtp-relay.gmail.com"
)

const gmailScope = "https://mail.google.com/"

const bulkNote = " Gmail and Workspace accounts are for low volume: send bulk mail through the Workspace SMTP relay or a sending service (SES, SendGrid, Postmark…)."

func init() {
	common := []Field{
		{Key: "username", Label: "Google account (sign-in email)", Placeholder: "news@acme.com", Required: true},
		{Key: "from", Label: "From address", Placeholder: "Acme <news@acme.com>", Required: true,
			Hint: "The account's own address, or an alias added to it in Gmail settings."},
		{Key: "port", Label: "Port", Options: []string{"587", "465"}, Default: "587", Hint: "587: STARTTLS; 465: TLS from the start."},
		{Key: "auth_method", Label: "Sign in with", Options: []string{"app_password", "oauth2"}, Default: "app_password",
			Hint: "app_password: a 16-character app password (needs 2-step verification); oauth2: XOAUTH2 with an OAuth client and a refresh token for the https://mail.google.com/ scope."},
		{Key: "client_id", Label: "OAuth client ID (oauth2 only)", Placeholder: "1234-abc.apps.googleusercontent.com"},
		{Key: "max_connections", Label: "Max connections", Placeholder: "2"},
	}
	secrets := []Field{
		{Key: "password", Label: "App password (app_password)"},
		{Key: httpauth.SecretClientSecret, Label: "OAuth client secret (oauth2)"},
		{Key: httpauth.SecretRefreshToken, Label: "OAuth refresh token (oauth2)"},
	}
	register(Kind{ID: "gmail_smtp", Channel: channel.Email, Name: "Gmail (SMTP)",
		Description: "A personal Gmail account through smtp.gmail.com.",
		Notes:       "Gmail allows about 500 recipients a day; Pulse sets that as this provider's daily quota." + bulkNote,
		Config:      common, Secrets: secrets,
		DefaultLimits: &model.ProviderLimits{DailyQuota: 500, RatePerSecond: 1},
		New:           func(d Deps, c, s map[string]string) (Provider, error) { return newGoogleSMTP(d, c, s, gmailSMTPHost) },
	})
	register(Kind{ID: "gworkspace_smtp", Channel: channel.Email, Name: "Google Workspace (SMTP)",
		Description: "A Google Workspace account through smtp.gmail.com.",
		Notes:       "A Workspace account allows about 2,000 recipients a day; Pulse sets that as this provider's daily quota." + bulkNote,
		Config:      common, Secrets: secrets,
		DefaultLimits: &model.ProviderLimits{DailyQuota: 2000, RatePerSecond: 2},
		New:           func(d Deps, c, s map[string]string) (Provider, error) { return newGoogleSMTP(d, c, s, gmailSMTPHost) },
	})
	register(Kind{ID: "gworkspace_relay", Channel: channel.Email, Name: "Google Workspace SMTP relay",
		Description: "smtp-relay.gmail.com: Workspace's relay for applications, allowed by your server's IP address or with SMTP auth.",
		Notes: "Set up the relay in the Google Admin console (Apps → Google Workspace → Gmail → Routing → SMTP relay service). " +
			"Google allows about 10,000 recipients a day per user through the relay; Pulse sets that as the daily quota. " +
			"For more, use a sending service.",
		Config: []Field{
			{Key: "from", Label: "From address", Placeholder: "Acme <news@acme.com>", Required: true, Hint: "An address in your Workspace domain."},
			{Key: "helo_domain", Label: "Your domain (sent in EHLO)", Placeholder: "acme.com", Required: true,
				Hint: "Google's relay needs a real domain name in EHLO when it allows you by IP address."},
			{Key: "port", Label: "Port", Options: []string{"587", "465", "25"}, Default: "587"},
			{Key: "max_connections", Label: "Max connections", Placeholder: "4"},
		},
		Secrets: []Field{
			{Key: "username", Label: "SMTP auth user (empty: allowed by IP address)"},
			{Key: "password", Label: "SMTP auth password"},
		},
		DefaultLimits: &model.ProviderLimits{DailyQuota: 10000},
		New: func(_ Deps, c, s map[string]string) (Provider, error) {
			port, err := googlePort(c["port"], "587", "465", "25")
			if err != nil {
				return nil, err
			}
			helo := strings.TrimSpace(c["helo_domain"])
			if helo == "" || strings.ContainsAny(helo, " \r\n") {
				return nil, errors.New("Google relay: enter your domain for EHLO")
			}
			maxConns, err := intSetting(c, "max_connections", "Max connections", defaultSMTPMaxConns, 1, 20)
			if err != nil {
				return nil, err
			}
			p := &SMTP{Addr: net.JoinHostPort(gmailRelayHost, port), From: c["from"], Helo: helo, MaxConns: maxConns, Service: ServiceOther}
			if s["username"] != "" {
				if s["password"] == "" {
					return nil, errors.New("Google relay: SMTP auth needs a password")
				}
				p.Auth = smtp.PlainAuth("", s["username"], s["password"], gmailRelayHost)
				sum := sha256.Sum256([]byte(s["password"]))
				p.username, p.passSum = s["username"], hex.EncodeToString(sum[:])
			}
			return p, nil
		},
	})
}

func googlePort(v string, allowed ...string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return allowed[0], nil
	}
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", fmt.Errorf("port must be %s", strings.Join(allowed, ", "))
}

// newGoogleSMTP builds a Gmail / Workspace account sender.
func newGoogleSMTP(d Deps, c, s map[string]string, host string) (Provider, error) {
	port, err := googlePort(c["port"], "587", "465")
	if err != nil {
		return nil, err
	}
	user := strings.TrimSpace(c["username"])
	if _, err := mail.ParseAddress(user); err != nil {
		return nil, errors.New("enter the Google account's email address")
	}
	maxConns, err := intSetting(c, "max_connections", "Max connections", 2, 1, 10)
	if err != nil {
		return nil, err
	}
	p := &SMTP{Addr: net.JoinHostPort(host, port), From: c["from"], MaxConns: maxConns, Service: ServiceOther, username: user}
	switch c["auth_method"] {
	case "", "app_password":
		pw := strings.ReplaceAll(s["password"], " ", "") // Google shows app passwords in groups of four
		if pw == "" {
			return nil, errors.New("enter the app password")
		}
		p.Auth = smtp.PlainAuth("", user, pw, host)
		sum := sha256.Sum256([]byte(pw))
		p.passSum = hex.EncodeToString(sum[:])
	case "oauth2":
		a, err := httpauth.New(httpauth.Config{Type: httpauth.OAuth2, Grant: "refresh_token", TokenURL: googleTokenURL,
			ClientID: strings.TrimSpace(c["client_id"]), Scope: gmailScope},
			s, httpauth.Options{HTTP: safehttp.Client(15*time.Second, d.AllowPrivate), Cache: d.Tokens, Scope: d.Scope,
				AllowHTTP: d.AllowPrivate})
		if err != nil {
			return nil, fmt.Errorf("XOAUTH2: %w", err)
		}
		p.Auth = &xoauth2{user: user, auth: a}
		sum := sha256.Sum256([]byte(s[httpauth.SecretRefreshToken]))
		p.passSum = "oauth:" + hex.EncodeToString(sum[:])
	default:
		return nil, errors.New("sign in with app_password or oauth2")
	}
	return p, nil
}

// xoauth2 is SMTP AUTH XOAUTH2 with an access token from a refresh token (cached per provider,
// refreshed before it expires).
type xoauth2 struct {
	user string
	auth *httpauth.Auth
}

func (x *xoauth2) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("XOAUTH2 needs TLS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t, err := x.auth.Token(ctx)
	if err != nil {
		return "", nil, err
	}
	return "XOAUTH2", XOAuth2Response(x.user, t.Value), nil
}

// Next answers the server's challenge after a refused token (a JSON error) with an empty line,
// as Google asks; the refused token is dropped so the next connection gets a new one.
func (x *xoauth2) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		x.auth.Invalidate(context.Background(), x.lastToken())
		return []byte{}, nil
	}
	return nil, nil
}

func (x *xoauth2) lastToken() httpauth.Token {
	t, _ := x.auth.Token(context.Background())
	return t
}

// XOAuth2Response is the initial client response of AUTH XOAUTH2.
func XOAuth2Response(user, token string) []byte {
	return []byte("user=" + user + "\x01auth=Bearer " + token + "\x01\x01")
}

// TestAuth fetches an access token now (XOAUTH2 accounts), for "Test connection".
func (s *SMTP) TestAuth(ctx context.Context) (httpauth.TestResult, error) {
	if x, ok := s.Auth.(*xoauth2); ok {
		return x.auth.Test(ctx)
	}
	return httpauth.TestResult{Type: "smtp", Static: true}, nil
}
