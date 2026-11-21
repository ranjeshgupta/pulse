package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.EmailRate != 200 || c.SweepInterval != 5*time.Minute || c.Partitions != 12 || c.AllowSignup ||
		c.SignupRate != 5 || c.TrustCloudflare {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadParses(t *testing.T) {
	t.Setenv("PULSE_EMAIL_RATE", "12.5")
	t.Setenv("PULSE_SMS_RATE", "300")
	t.Setenv("PULSE_SWEEP_INTERVAL", "90s")
	t.Setenv("PULSE_KAFKA_PARTITIONS", "24")
	t.Setenv("PULSE_ALLOW_SIGNUP", "true")
	t.Setenv("PULSE_SIGNUP_RATE", "50")
	t.Setenv("PULSE_TRUST_PROXY", "1")
	t.Setenv("PULSE_TRUST_CLOUDFLARE", "yes")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Rates["sms"] != 300 || len(c.Rates) != 1 {
		t.Fatalf("rates: %v", c.Rates)
	}
	if c.EmailRate != 12.5 || c.SweepInterval != 90*time.Second || c.Partitions != 24 || !c.AllowSignup || !c.TrustProxy ||
		c.SignupRate != 50 || !c.TrustCloudflare {
		t.Fatalf("parsed: %+v", c)
	}
}

// A value that doesn't parse fails startup, naming every bad variable, instead of falling back.
func TestLoadRejectsBadValues(t *testing.T) {
	t.Setenv("PULSE_EMAIL_RATE", "fast")
	t.Setenv("PULSE_WHATSAPP_RATE", "0")
	t.Setenv("PULSE_SWEEP_INTERVAL", "5")
	t.Setenv("PULSE_KAFKA_PARTITIONS", "0")
	t.Setenv("PULSE_ALLOW_SIGNUP", "maybe")
	t.Setenv("PULSE_MAINTENANCE_INTERVAL", "-1h")
	_, err := Load()
	if err == nil {
		t.Fatal("bad values loaded")
	}
	for _, k := range []string{"PULSE_EMAIL_RATE", "PULSE_WHATSAPP_RATE", "PULSE_SWEEP_INTERVAL", "PULSE_KAFKA_PARTITIONS", "PULSE_ALLOW_SIGNUP", "PULSE_MAINTENANCE_INTERVAL"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error doesn't name %s: %v", k, err)
		}
	}
}

func TestDevSecretsRefusedOffLocalhost(t *testing.T) {
	dev := Config{PublicURL: "http://localhost:7080", AdminToken: devAdminToken, SecretKey: devSecretKey}
	if errs := devSecrets(dev); len(errs) != 0 {
		t.Errorf("local dev stack refused: %v", errs)
	}
	dev.PublicURL = "https://pulse.example.com"
	if errs := devSecrets(dev); len(errs) != 2 {
		t.Errorf("public URL with dev secrets: %v", errs)
	}
	if errs := devSecrets(Config{PublicURL: "https://pulse.example.com", AdminToken: "x", SecretKey: "y"}); len(errs) != 0 {
		t.Errorf("own secrets refused: %v", errs)
	}
}

// PULSE_KAFKA_PARTITIONS is an int32: a value past its range is rejected, not wrapped to 1 or a negative.
func TestLoadRejectsPartitionsOverflow(t *testing.T) {
	for _, v := range []string{"4294967297", "2147483648"} {
		t.Setenv("PULSE_KAFKA_PARTITIONS", v)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PULSE_KAFKA_PARTITIONS") {
			t.Errorf("%s: err = %v", v, err)
		}
	}
}
