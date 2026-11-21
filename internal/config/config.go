// Package config reads runtime settings from PULSE_* environment variables.
package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr   string
	PGDSN      string
	CHAddr     string
	CHDB       string
	CHUser     string
	CHPassword string
	RedisAddr  string
	// RedisPassword authenticates to Redis (requirepass); empty for none.
	RedisPassword string
	KafkaBrokers  []string
	Partitions    int32
	SMTPAddr      string
	SMTPFrom      string
	AdminToken    string
	SweepInterval time.Duration
	EmailRate     float64 // per tenant, messages per second
	// Rates are the other channels' per-tenant send rates (messages per second) set by
	// PULSE_<CHANNEL>_RATE; a channel without one keeps the dispatcher's default.
	Rates     map[string]float64
	PublicURL string // where browsers reach Pulse (SSO redirects, Secure cookies)
	SecretKey string // 64 hex chars: encrypts stored secrets and SSO state
	// AllowSignup lets anyone create a workspace with POST /v1/auth/signup (PULSE_ALLOW_SIGNUP,
	// off by default: the operator creates workspaces with the admin token).
	AllowSignup bool
	// SignupRate caps signups per client IP per minute when signup is on; all signups together
	// are capped at ten times that.
	SignupRate int
	// AllowPrivateWebhooks lets every outbound call Pulse makes to a URL a workspace entered reach
	// private addresses: journey and outbound webhooks, channel providers and the custom gateway,
	// integrations (CRM, Shopify, ad platforms) and the IMAP bounce mailbox. Local development only.
	AllowPrivateWebhooks bool
	// AllowPrivateSSO lets workspace SSO discovery reach private addresses (an IdP on the internal
	// network, like the dev Keycloak). Off by default: any workspace admin can enter these URLs.
	AllowPrivateSSO bool
	// TrustProxy uses the right-most X-Forwarded-For entry (appended by your reverse proxy) as the
	// client IP for rate limits. Leave off unless Pulse is only reachable through that proxy.
	TrustProxy bool
	// TrustCloudflare (with TrustProxy) takes the client IP from Cloudflare's CF-Connecting-IP
	// header. Only safe when the origin's firewall accepts connections from Cloudflare's ranges
	// alone: anyone else could send the header.
	TrustCloudflare bool
	// PagesURL is a separate origin for landing pages, downloads and hosted forms (production).
	PagesURL string
	// AIAPIKey turns on AI help (an Anthropic API key); empty leaves it off.
	AIAPIKey string
	// GeoIPDB is a MaxMind GeoLite2-City or GeoIP2-City .mmdb file; empty turns geolocation off.
	GeoIPDB string
	// MaintenanceInterval is how often retention cleanup runs.
	MaintenanceInterval time.Duration
	// RFMInterval is how often buyers' customer value is scored (package rfm).
	RFMInterval time.Duration
	// RFMEventsPerSec caps the rfm_changed events a pass sends a second.
	RFMEventsPerSec int
	// CHQueryTimeout and CHQueryMaxMemory bound each ClickHouse query an API request runs (reports,
	// funnels, segment previews…; exports are exempt from the time limit).
	CHQueryTimeout   time.Duration
	CHQueryMaxMemory int
	// AudienceReconcile is how often each audience sync re-checks its whole segment (ad audiences).
	AudienceReconcile time.Duration
}

// Load reads the settings. A number, duration or on/off switch that doesn't parse is an error
// naming the variable, so a typo fails startup instead of quietly running with the default.
func Load() (Config, error) {
	var r reader
	c := Config{
		HTTPAddr:             env("PULSE_HTTP_ADDR", ":7070"),
		PGDSN:                env("PULSE_PG_DSN", "postgres://pulse:pulse@localhost:55432/pulse?sslmode=disable"),
		CHAddr:               env("PULSE_CH_ADDR", "localhost:19000"),
		CHDB:                 env("PULSE_CH_DB", "pulse"),
		CHUser:               env("PULSE_CH_USER", "pulse"),
		CHPassword:           env("PULSE_CH_PASSWORD", "pulse"),
		RedisAddr:            env("PULSE_REDIS_ADDR", "localhost:16379"),
		RedisPassword:        env("PULSE_REDIS_PASSWORD", ""),
		KafkaBrokers:         strings.Split(env("PULSE_KAFKA_BROKERS", "localhost:19092"), ","),
		Partitions:           r.integer32("PULSE_KAFKA_PARTITIONS", 12),
		SMTPAddr:             env("PULSE_SMTP_ADDR", "localhost:11025"),
		SMTPFrom:             env("PULSE_SMTP_FROM", "Pulse <noreply@pulse.local>"),
		AdminToken:           env("PULSE_ADMIN_TOKEN", ""),
		SweepInterval:        r.duration("PULSE_SWEEP_INTERVAL", 5*time.Minute),
		EmailRate:            r.rate("PULSE_EMAIL_RATE", 200),
		Rates:                map[string]float64{},
		PublicURL:            env("PULSE_PUBLIC_URL", "http://localhost:7080"),
		SecretKey:            env("PULSE_SECRET_KEY", ""),
		AllowSignup:          r.boolean("PULSE_ALLOW_SIGNUP", false),
		SignupRate:           r.integer("PULSE_SIGNUP_RATE", 5),
		AllowPrivateWebhooks: r.boolean("PULSE_ALLOW_PRIVATE_WEBHOOKS", false),
		AllowPrivateSSO:      r.boolean("PULSE_ALLOW_PRIVATE_SSO", false),
		TrustProxy:           r.boolean("PULSE_TRUST_PROXY", false),
		TrustCloudflare:      r.boolean("PULSE_TRUST_CLOUDFLARE", false),
		GeoIPDB:              env("PULSE_GEOIP_DB", ""),
		PagesURL:             env("PULSE_PAGES_URL", ""),
		AIAPIKey:             env("PULSE_AI_API_KEY", ""),
		MaintenanceInterval:  r.duration("PULSE_MAINTENANCE_INTERVAL", time.Hour),
		RFMInterval:          r.duration("PULSE_RFM_INTERVAL", 24*time.Hour),
		RFMEventsPerSec:      r.integer("PULSE_RFM_EVENTS_PER_SEC", 500),
		AudienceReconcile:    r.duration("PULSE_AUDIENCE_RECONCILE_INTERVAL", 24*time.Hour),
		CHQueryTimeout:       r.duration("PULSE_CH_QUERY_TIMEOUT", 60*time.Second),
		CHQueryMaxMemory:     r.integer("PULSE_CH_QUERY_MAX_MEMORY", 4<<30),
	}
	for _, ch := range RateChannels {
		if os.Getenv(rateEnv(ch)) != "" {
			c.Rates[ch] = r.rate(rateEnv(ch), 0)
		}
	}
	r.errs = append(r.errs, devSecrets(c)...)
	return c, errors.Join(r.errs...)
}

// The local dev stack's published values (deploy/docker-compose.yml). They are public, so a server
// reachable from elsewhere must not run with them.
const (
	devAdminToken = "dev-admin-token"
	devSecretKey  = "9f1c2b7e4a6d8053c1e2f4a7b9d0c3e5f6a8b1c2d4e7f9a0b3c5d6e8f1a2b4c6"
)

// devSecrets refuses the dev stack's admin token and secret key unless the public URL is local.
func devSecrets(c Config) []error {
	u, err := url.Parse(c.PublicURL)
	host := ""
	if err == nil {
		host = strings.ToLower(u.Hostname())
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost") {
		return nil
	}
	var out []error
	if c.AdminToken == devAdminToken {
		out = append(out, fmt.Errorf("PULSE_ADMIN_TOKEN is the published dev token; set your own (e.g. openssl rand -hex 32) for %s", c.PublicURL))
	}
	if c.SecretKey == devSecretKey {
		out = append(out, fmt.Errorf("PULSE_SECRET_KEY is the published dev key; generate one with openssl rand -hex 32 for %s", c.PublicURL))
	}
	return out
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// RateChannels are the channels besides email whose per-tenant rate PULSE_<CHANNEL>_RATE sets
// (channel names as in internal/channel).
var RateChannels = []string{"sms", "whatsapp", "rcs", "push", "inbox"}

func rateEnv(ch string) string { return "PULSE_" + strings.ToUpper(ch) + "_RATE" }

// reader parses typed settings and collects every bad value, so one startup reports them all.
type reader struct{ errs []error }

func (r *reader) fail(k, v, want string) {
	r.errs = append(r.errs, fmt.Errorf("%s=%q: %s", k, v, want))
}

// integer reads a whole number greater than zero.
func (r *reader) integer(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		r.fail(k, v, "must be a whole number greater than 0")
		return def
	}
	return n
}

// integer32 is integer for an int32 setting: a value past the int32 range fails instead of wrapping.
func (r *reader) integer32(k string, def int32) int32 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 32)
	if err != nil || n <= 0 {
		r.fail(k, v, "must be a whole number from 1 to 2147483647")
		return def
	}
	return int32(n)
}

// rate reads a number greater than zero; fractions are fine (12.5 per second).
func (r *reader) rate(k string, def float64) float64 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		r.fail(k, v, "must be a number greater than 0")
		return def
	}
	return n
}

// duration reads a Go duration such as 90s, 5m or 24h, greater than zero.
func (r *reader) duration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d <= 0 {
		r.fail(k, v, "must be a duration greater than 0 such as 90s, 5m or 24h")
		return def
	}
	return d
}

// boolean reads true or false (also 1/0, yes/no, on/off).
func (r *reader) boolean(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	r.fail(k, v, "must be true or false")
	return def
}
