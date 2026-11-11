package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"pulse/internal/automation"
	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/providers"
)

// autoStore is the test store with per-workspace automation settings.
type autoStore struct {
	*memStore
	settings map[int64]automation.Settings
	reads    int
}

func (a *autoStore) AutomationSettings(_ context.Context, tenant int64) (automation.Settings, error) {
	a.reads++
	if s, ok := a.settings[tenant]; ok {
		return s, nil
	}
	return automation.Defaults(), nil
}

func withSettings(t *testing.T, s map[int64]automation.Settings) (fixture, *autoStore) {
	f := setup(t)
	as := &autoStore{memStore: f.st, settings: s}
	f.d.store = as
	return f, as
}

// A workspace with 3 tries and a 5-minute longest wait gives up after the third try; another
// workspace keeps the default 8 tries.
func TestRetriesFollowWorkspaceSettings(t *testing.T) {
	s := automation.Defaults()
	s.Retries = automation.Retries{Attempts: 3, MaxBackoffSeconds: 300}
	f, _ := withSettings(t, map[int64]automation.Settings{1: s})
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.d.now = func() time.Time { return now }
	f.mail.err = errors.New("connection reset")
	rec := msgRecord(model.Message{ID: "r3", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})
	tries := 0
	for ; tries < 20; tries++ {
		if err := f.d.Handle(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if len(f.pub.recs) > 0 {
			break
		}
		now = now.Add(2 * time.Hour)
		f.d.releaseRetries(ctx)
		rec, f.pub.recs = f.pub.recs[0], nil
	}
	if tries+1 != 3 {
		t.Errorf("gave up after %d tries, want 3", tries+1)
	}
	var ev event.Event
	_ = json.Unmarshal(f.pub.recs[0].Value, &ev)
	if reason, _ := ev.Props["reason"].(string); ev.Name != "email_failed" || !strings.Contains(reason, "gave up after 3 tries") {
		t.Errorf("event %s, reason %v", ev.Name, ev.Props["reason"])
	}
}

// The workspace's longest wait also caps the provider's Retry-After.
func TestRetryAfterCappedByWorkspace(t *testing.T) {
	s := automation.Defaults()
	s.Retries.MaxBackoffSeconds = 120
	f, as := withSettings(t, map[int64]automation.Settings{1: s})
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.d.now = func() time.Time { return now }
	f.mail.err = &providers.RetryAfterError{Err: errors.New("429"), After: time.Hour}
	if err := f.d.Handle(ctx, msgRecord(model.Message{ID: "ra", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1})); err != nil {
		t.Fatal(err)
	}
	// cached: a second message within 30 seconds doesn't read the settings again
	reads := as.reads
	_ = f.d.Handle(ctx, msgRecord(model.Message{ID: "ra2", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 1}))
	if as.reads != reads {
		t.Errorf("settings read %d times, want cached", as.reads-reads)
	}
	now = now.Add(2 * time.Minute)
	f.d.releaseRetries(ctx)
	if len(f.pub.recs) != 2 {
		t.Fatalf("released %d after the 2-minute cap, want both", len(f.pub.recs))
	}
}

// App inbox messages expire after the workspace's default unless the template sets its own.
func TestInboxExpiryFromSettings(t *testing.T) {
	s := automation.Defaults()
	s.Messages.InboxExpiryDays = 7
	f, _ := withSettings(t, map[int64]automation.Settings{1: s})
	f.st.templates[5] = model.Template{ID: 5, Channel: channel.Inbox, Content: json.RawMessage(`{"title":"Hi","body":"There"}`)}
	f.st.templates[6] = model.Template{ID: 6, Channel: channel.Inbox, Content: json.RawMessage(`{"title":"Hi","body":"There","expires_days":2}`)}
	ctx := context.Background()
	for _, id := range []int64{5, 6} {
		if err := f.d.Handle(ctx, msgRecord(model.Message{ID: "in" + string(rune('0'+id)), TenantID: 1, ContactID: 1, Channel: channel.Inbox, TemplateID: id})); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.st.inbox) != 2 {
		t.Fatalf("inbox %d", len(f.st.inbox))
	}
	for i, want := range []int{7, 2} {
		got := time.Until(f.st.inbox[i].ExpiresAt).Hours() / 24
		if got < float64(want)-0.1 || got > float64(want)+0.1 {
			t.Errorf("message %d expires in %.1f days, want %d", i, got, want)
		}
	}
}

// Without settings (or a store without them) the defaults are today's behaviour.
func TestAutomationDefaultsWithoutStore(t *testing.T) {
	f := setup(t)
	if got := f.d.automation(context.Background(), 1); got.Retries.Attempts != 8 || got.Messages.InboxExpiryDays != 30 {
		t.Errorf("defaults %+v", got)
	}
	if len(automation.Defaults().Retries.Schedule()) != len(RetryBackoff) {
		t.Error("the default schedule is RetryBackoff")
	}
}
