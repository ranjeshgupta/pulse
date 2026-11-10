package journey

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

const welcome = `{
	"trigger": {"type": "segment_entered", "segment_id": 4},
	"entry": "send1",
	"nodes": {
		"send1": {"type": "send_email", "template_id": 2, "next": "w1"},
		"w1":    {"type": "wait", "seconds": 3600, "next": "c1"},
		"c1":    {"type": "condition", "segment_id": 9, "yes": "send2", "no": "x"},
		"send2": {"type": "send_email", "template_id": 3, "next": "x"},
		"x":     {"type": "exit"}
	}
}`

func TestParseGraphValid(t *testing.T) {
	g, err := ParseGraph([]byte(welcome))
	if err != nil {
		t.Fatal(err)
	}
	if g.Entry != "send1" || len(g.Nodes) != 5 {
		t.Fatalf("unexpected graph %+v", g)
	}
}

func TestParseGraphInvalid(t *testing.T) {
	cases := map[string]string{
		"unknown trigger": `{"trigger":{"type":"cron"},"entry":"x","nodes":{"x":{"type":"exit"}}}`,
		"segment_id":      `{"trigger":{"type":"segment_entered"},"entry":"x","nodes":{"x":{"type":"exit"}}}`,
		"invalid event":   `{"trigger":{"type":"event","event":"bad name"},"entry":"x","nodes":{"x":{"type":"exit"}}}`,
		"entry node":      `{"trigger":{"type":"event","event":"signup"},"entry":"nope","nodes":{"x":{"type":"exit"}}}`,
		"unknown node":    `{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"wait","seconds":5,"next":"zzz"}}}`,
		"missing an edge": `{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"send_email","template_id":1}}}`,
		"seconds":         `{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"wait","next":"x"},"x":{"type":"exit"}}}`,
		"template_id":     `{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"send_email","next":"x"},"x":{"type":"exit"}}}`,
		"cycle":           `{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"wait","seconds":5,"next":"b"},"b":{"type":"wait","seconds":5,"next":"a"}}}`,
		"unknown type":    `{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"teleport"}}}`,
		"unknown field":   `{"trigger":{"type":"event","event":"signup"},"entry":"a","nodes":{"a":{"type":"exit","color":"red"}}}`,
	}
	for want, js := range cases {
		if _, err := ParseGraph([]byte(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

// ---- executor ----

type fakeStore struct {
	eventWaits []string
	nodes      map[int64][2]string // enrollment -> {node, status}
	timers     map[int64]struct {
		node string
		due  time.Time
	}
}

func newFakeStore() *fakeStore {
	return &fakeStore{nodes: map[int64][2]string{}, timers: map[int64]struct {
		node string
		due  time.Time
	}{}}
}

func (f *fakeStore) SetNode(_ context.Context, en int64, node, status string) error {
	f.nodes[en] = [2]string{node, status}
	return nil
}

func (f *fakeStore) WaitForEvent(_ context.Context, en model.Enrollment, waitNode, ev, yes, timeout string, due time.Time) error {
	f.eventWaits = append(f.eventWaits, ev+":"+yes+":"+timeout)
	return f.Wait(context.Background(), en.ID, waitNode, timeout, due)
}

func (f *fakeStore) Wait(_ context.Context, en int64, waitNode, resume string, due time.Time) error {
	f.nodes[en] = [2]string{waitNode, StatusWaiting}
	f.timers[en] = struct {
		node string
		due  time.Time
	}{resume, due}
	return nil
}

type fakeMembers map[int64]bool

func (m fakeMembers) IsMember(_ context.Context, _, seg int64, _ uint32) (bool, error) {
	return m[seg], nil
}

type fakeSender struct {
	sent []model.Message
	err  error
}

func (s *fakeSender) Send(_ context.Context, m model.Message) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, m)
	return nil
}

func TestExecutorRunsUntilWait(t *testing.T) {
	g, _ := ParseGraph([]byte(welcome))
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	store, sender := newFakeStore(), &fakeSender{}
	x := &Executor{Store: store, Membership: fakeMembers{}, Sender: sender, Now: func() time.Time { return now }}
	en := model.Enrollment{ID: 7, TenantID: 1, JourneyID: 3, ContactID: 42}

	if err := x.Run(context.Background(), g, en, g.Entry); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].ID != "7:send1" || sender.sent[0].TemplateID != 2 {
		t.Fatalf("sent = %+v, want one welcome email with id 7:send1", sender.sent)
	}
	if got := store.nodes[7]; got != [2]string{"w1", StatusWaiting} {
		t.Errorf("enrollment = %v, want waiting at w1", got)
	}
	if tm := store.timers[7]; tm.node != "c1" || !tm.due.Equal(now.Add(time.Hour)) {
		t.Errorf("timer = %+v, want resume at c1 in 1h", tm)
	}
}

func TestExecutorConditionBranches(t *testing.T) {
	g, _ := ParseGraph([]byte(welcome))
	for _, tc := range []struct {
		member bool
		sends  int
	}{{true, 1}, {false, 0}} {
		store, sender := newFakeStore(), &fakeSender{}
		x := &Executor{Store: store, Membership: fakeMembers{9: tc.member}, Sender: sender, Now: time.Now}
		en := model.Enrollment{ID: 1, TenantID: 1, JourneyID: 3, ContactID: 42}
		if err := x.Run(context.Background(), g, en, "c1"); err != nil { // resumed from the timer
			t.Fatal(err)
		}
		if len(sender.sent) != tc.sends {
			t.Errorf("member=%v: %d sends, want %d", tc.member, len(sender.sent), tc.sends)
		}
		if got := store.nodes[1]; got != [2]string{"x", StatusCompleted} {
			t.Errorf("member=%v: enrollment = %v, want completed at x", tc.member, got)
		}
	}
}

func TestExecutorPropagatesSendErrors(t *testing.T) {
	g, _ := ParseGraph([]byte(welcome))
	store := newFakeStore()
	x := &Executor{Store: store, Membership: fakeMembers{}, Sender: &fakeSender{err: errors.New("kafka down")}, Now: time.Now}
	err := x.Run(context.Background(), g, model.Enrollment{ID: 1}, g.Entry)
	if err == nil {
		t.Fatal("want error")
	}
	if _, moved := store.nodes[1]; moved {
		t.Error("enrollment advanced although the send failed")
	}
}
