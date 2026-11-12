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
	if c.EmailRate != 200 || c.SweepInterval != 5*time.Minute || c.Partitions != 12 || !c.AllowSignup {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadParses(t *testing.T) {
	t.Setenv("PULSE_EMAIL_RATE", "12.5")
	t.Setenv("PULSE_SMS_RATE", "300")
	t.Setenv("PULSE_SWEEP_INTERVAL", "90s")
	t.Setenv("PULSE_KAFKA_PARTITIONS", "24")
	t.Setenv("PULSE_ALLOW_SIGNUP", "false")
	t.Setenv("PULSE_TRUST_PROXY", "1")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Rates["sms"] != 300 || len(c.Rates) != 1 {
		t.Fatalf("rates: %v", c.Rates)
	}
	if c.EmailRate != 12.5 || c.SweepInterval != 90*time.Second || c.Partitions != 24 || c.AllowSignup || !c.TrustProxy {
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
