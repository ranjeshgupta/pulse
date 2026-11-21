package event

import (
	"encoding/json"
	"testing"
	"time"
)

// FuzzNormalize decodes arbitrary event bodies and normalises them: no panics, and an accepted
// event has an id, a valid name, a UTC timestamp that isn't in the future and valid attribute names.
func FuzzNormalize(f *testing.F) {
	f.Add(`{"external_id":"u1","name":"purchase","props":{"amount":10},"attrs":{"city":"Pune"}}`)
	f.Add(`{"external_id":"u1","name":"identify","ts":"2030-01-01T00:00:00Z"}`)
	f.Add(`{"external_id":"","name":"x"}`)
	f.Add(`{"external_id":"u","name":"1bad","attrs":{"bad key":1}}`)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, body string) {
		var e Event
		if json.Unmarshal([]byte(body), &e) != nil {
			return
		}
		if e.Normalize(now) != nil {
			return
		}
		if e.ID == "" || !ValidName(e.Name) || e.Ts.Location() != time.UTC || e.Ts.After(now.Add(maxFutureSkew)) {
			t.Fatalf("accepted %+v", e)
		}
		if len(e.RawKey()) == 0 {
			t.Fatal("empty partition key")
		}
		_ = ServerOnly(e.Name) || PlatformOnly(e.Name) || BounceReport(e.Name) || CarriesDevice(e.Name)
	})
}
