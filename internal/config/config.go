// Package config reads runtime settings from PULSE_* environment variables.
package config

import (
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
	PublicURL     string  // where browsers reach Pulse (SSO redirects, Secure cookies)
	SecretKey     string  // 64 hex chars: encrypts stored secrets and SSO state
	AllowSignup   bool
	// AllowPrivateWebhooks lets journey webhooks call private addresses (local development only).
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
}

func Load() Config {
	return Config{
		HTTPAddr:             env("PULSE_HTTP_ADDR", ":7070"),
		PGDSN:                env("PULSE_PG_DSN", "postgres://pulse:pulse@localhost:55432/pulse?sslmode=disable"),
		CHAddr:               env("PULSE_CH_ADDR", "localhost:19000"),
		CHDB:                 env("PULSE_CH_DB", "pulse"),
		CHUser:               env("PULSE_CH_USER", "pulse"),
		CHPassword:           env("PULSE_CH_PASSWORD", "pulse"),
		RedisAddr:            env("PULSE_REDIS_ADDR", "localhost:16379"),
		KafkaBrokers:         strings.Split(env("PULSE_KAFKA_BROKERS", "localhost:19092"), ","),
		Partitions:           int32(envInt("PULSE_KAFKA_PARTITIONS", 12)),
		SMTPAddr:             env("PULSE_SMTP_ADDR", "localhost:11025"),
		SMTPFrom:             env("PULSE_SMTP_FROM", "Pulse <noreply@pulse.local>"),
		AdminToken:           env("PULSE_ADMIN_TOKEN", ""),
		SweepInterval:        envDuration("PULSE_SWEEP_INTERVAL", 5*time.Minute),
		EmailRate:            float64(envInt("PULSE_EMAIL_RATE", 200)),
		PublicURL:            env("PULSE_PUBLIC_URL", "http://localhost:7080"),
		SecretKey:            env("PULSE_SECRET_KEY", ""),
		AllowSignup:          env("PULSE_ALLOW_SIGNUP", "true") == "true",
		AllowPrivateWebhooks: env("PULSE_ALLOW_PRIVATE_WEBHOOKS", "false") == "true",
		AllowPrivateSSO:      env("PULSE_ALLOW_PRIVATE_SSO", "false") == "true",
		TrustProxy:           env("PULSE_TRUST_PROXY", "false") == "true",
		GeoIPDB:              env("PULSE_GEOIP_DB", ""),
		PagesURL:             env("PULSE_PAGES_URL", ""),
		MaintenanceInterval:  envDuration("PULSE_MAINTENANCE_INTERVAL", time.Hour),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
