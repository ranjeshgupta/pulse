package api

import (
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

func TestJourneyProblem(t *testing.T) {
	valid := []byte(`{"trigger":{"type":"event","event":"signup"},"entry":"x","nodes":{"x":{"type":"exit"}}}`)
	// Saved before stage became Pulse-managed: the workers now skip it.
	stale := []byte(`{"trigger":{"type":"event","event":"signup"},"entry":"tag","nodes":{"tag":{"type":"update_attr","attrs":{"stage":"lead"},"next":"x"},"x":{"type":"exit"}}}`)
	now := time.Now()
	for _, c := range []struct {
		name string
		j    model.Journey
		want string
	}{
		{"live and fine", model.Journey{Status: "active", Graph: valid}, ""},
		{"live and stale", model.Journey{Status: "active", Graph: stale}, "update"},
		{"draft", model.Journey{Status: "draft", Graph: stale}, ""},
		{"archived", model.Journey{Status: "active", Graph: stale, ArchivedAt: &now}, ""},
	} {
		got := journeyProblem(c.j)
		if (c.want == "") != (got == "") || !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: problem = %q", c.name, got)
		}
	}
	if got := journeyProblem(model.Journey{Status: "active", Graph: stale}); !strings.Contains(got, "stage") {
		t.Errorf("problem doesn't name the setting: %q", got)
	}
}
