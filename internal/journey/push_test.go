package journey

import (
	"context"
	"errors"
	"testing"

	"pulse/internal/event"
	"pulse/internal/model"
)

type fakePusher struct {
	pushed []string
	err    error
}

func (f *fakePusher) PushContact(_ context.Context, _, integration int64, c model.Contact, eventID string) (string, error) {
	f.pushed = append(f.pushed, c.ExternalID+"|"+eventID)
	return "r1", f.err
}

func TestPushIntegration(t *testing.T) {
	if _, err := ParseGraph([]byte(`{"trigger":{"type":"event","event":"signup"},"entry":"s","nodes":{"s":{"type":"push_integration","next":"x"},"x":{"type":"exit"}}}`)); err == nil {
		t.Error("a push step without an integration must not validate")
	}
	x, store, _, evs, _ := builderExecutor(t, nil)
	p := &fakePusher{}
	x.Integrations = p
	g := one(t, `{"type":"push_integration","integration_id":4,"next":"x"}`)
	if err := x.Run(context.Background(), g, en, "s"); err != nil {
		t.Fatal(err)
	}
	if len(p.pushed) != 1 || p.pushed[0] != "u42|jpush:"+StepKey(en, "s") || store.nodes[7][0] != "x" {
		t.Errorf("pushed %v, at %v", p.pushed, store.nodes[7])
	}
	// A failed push is recorded and the journey carries on.
	p.err = errors.New("HTTP 503")
	if err := x.Run(context.Background(), g, en, "s"); err != nil {
		t.Fatal(err)
	}
	var failed *event.Event
	for i := range evs.evs {
		if evs.evs[i].Name == EventPushFailed {
			failed = &evs.evs[i]
		}
	}
	if failed == nil || failed.Props["error"] != "HTTP 503" || failed.Props["integration_id"] != int64(4) || store.nodes[7][0] != "x" {
		t.Errorf("failure = %+v, at %v", failed, store.nodes[7])
	}
	x.Integrations = nil
	if err := x.Run(context.Background(), g, en, "s"); err == nil {
		t.Error("without integrations configured the step fails instead of skipping")
	}
}
