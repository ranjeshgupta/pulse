// Package config reads runtime settings from PULSE_* environment variables.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr      string
	PGDSN         string
	CHAddr        string
	CHDB          string
	CHUser        string
	CHPassword    string
	RedisAddr     string
	KafkaBrokers  []string
	Partitions    int32
	SMTPAddr      string
	SMTPFrom      string
	AdminToken    string
	SweepInterval time.Duration
	EmailRate     float64 // per tenant, messages per second
	// Rates are the other channels' per-tenant send rates (messages per second) set by
	// PULSE_<CHANNEL>_RATE; a channel without one keeps the dispatcher's default.
	Rates       map[string]float64
	PublicURL   string // where browsers reach Pulse (SSO redirects, Secure cookies)
	SecretKey   string // 64 hex chars: encrypts stored secrets and SSO state
	AllowSignup bool
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
	// PagesURL is a separate origin for landing pages, downloads and hosted forms (production).
	PagesURL string
	// GeoIPDB is a MaxMind GeoLite2-City or GeoIP2-City .mmdb file; empty turns geolocation off.
	GeoIPDB string
	// MaintenanceInterval is how often retention cleanup runs.
	MaintenanceInterval time.Duration
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
		KafkaBrokers:         strings.Split(env("PULSE_KAFKA_BROKERS", "localhost:19092"), ","),
		Partitions:           int32(r.integer("PULSE_KAFKA_PARTITIONS", 12)),
		SMTPAddr:             env("PULSE_SMTP_ADDR", "localhost:11025"),
		SMTPFrom:             env("PULSE_SMTP_FROM", "Pulse <noreply@pulse.local>"),
		AdminToken:           env("PULSE_ADMIN_TOKEN", ""),
		SweepInterval:        r.duration("PULSE_SWEEP_INTERVAL", 5*time.Minute),
		EmailRate:            r.rate("PULSE_EMAIL_RATE", 200),
		Rates:                map[string]float64{},
		PublicURL:            env("PULSE_PUBLIC_URL", "http://localhost:7080"),
		SecretKey:            env("PULSE_SECRET_KEY", ""),
		AllowSignup:          r.boolean("PULSE_ALLOW_SIGNUP", true),
		AllowPrivateWebhooks: r.boolean("PULSE_ALLOW_PRIVATE_WEBHOOKS", false),
		AllowPrivateSSO:      r.boolean("PULSE_ALLOW_PRIVATE_SSO", false),
		TrustProxy:           r.boolean("PULSE_TRUST_PROXY", false),
		GeoIPDB:              env("PULSE_GEOIP_DB", ""),
		PagesURL:             env("PULSE_PAGES_URL", ""),
		MaintenanceInterval:  r.duration("PULSE_MAINTENANCE_INTERVAL", time.Hour),
		AudienceReconcile:    r.duration("PULSE_AUDIENCE_RECONCILE_INTERVAL", 24*time.Hour),
	}
	for _, ch := range RateChannels {
		if os.Getenv(rateEnv(ch)) != "" {
			c.Rates[ch] = r.rate(rateEnv(ch), 0)
		}
	}
	return c, errors.Join(r.errs...)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// reader parses typed settings and collects every bad value, so one startup reports them all.
// RateChannels are the channels besides email whose per-tenant rate PULSE_<CHANNEL>_RATE sets
// (channel names as in internal/channel).
var RateChannels = []string{"sms", "whatsapp", "rcs", "push", "inbox"}

func rateEnv(ch string) string { return "PULSE_" + strings.ToUpper(ch) + "_RATE" }

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
