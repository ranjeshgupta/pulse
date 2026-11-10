package maintenance

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"pulse/internal/model"
)

type fake struct {
	idle         []uint32
	pgDeleted    []uint32
	chDeleted    []uint32
	eventCleans  []int
	auditCleaned int
}

func (f *fake) Retentions(context.Context) (map[int64]model.RetentionSettings, error) {
	return map[int64]model.RetentionSettings{1: {AnonymousDays: 30, EventDays: 365, AuditDays: 90}}, nil
}
func (f *fake) IdleAnonymous(_ context.Context, _ int64, _, limit int) ([]uint32, error) {
	n := min(limit, len(f.idle))
	out := f.idle[:n]
	f.idle = f.idle[n:]
	return out, nil
}
func (f *fake) DeleteContacts(_ context.Context, _ int64, ids []uint32) error {
	f.pgDeleted = append(f.pgDeleted, ids...)
	return nil
}
func (f *fake) PruneAudit(context.Context, int64, int) (int64, error) {
	f.auditCleaned++
	return 3, nil
}
func (f *fake) PurgeInbox(context.Context) (int64, error) { return 2, nil }

type fakeCH struct{ f *fake }

func (c fakeCH) DeleteContacts(_ context.Context, _ int64, ids []uint32) error {
	c.f.chDeleted = append(c.f.chDeleted, ids...)
	return nil
}
func (c fakeCH) DeleteEventsBefore(_ context.Context, _ int64, days int) error {
	c.f.eventCleans = append(c.f.eventCleans, days)
	return nil
}

func TestRun(t *testing.T) {
	f := &fake{}
	for i := 0; i < batch+10; i++ {
		f.idle = append(f.idle, uint32(i+1))
	}
	c := &Cleaner{Store: f, Analytics: fakeCH{f}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rep, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Anonymous != int64(batch+10) || len(f.pgDeleted) != batch+10 || len(f.chDeleted) != batch+10 {
		t.Errorf("anonymous deleted %d (pg %d, ch %d), want %d in two batches", rep.Anonymous, len(f.pgDeleted), len(f.chDeleted), batch+10)
	}
	if len(f.eventCleans) != 1 || f.eventCleans[0] != 365 || rep.Audit != 3 || rep.Inbox != 2 {
		t.Errorf("report %+v, event cleans %v", rep, f.eventCleans)
	}
}
