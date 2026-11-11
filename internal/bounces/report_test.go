package bounces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pulse/internal/event"
)

func sample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseReport(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []Bounce // Reason checked by prefix
	}{
		{"dsn_postfix.eml", []Bounce{
			{Email: "gone@example.org", Kind: Hard, Reason: "5.1.1 smtp; 550 5.1.1 <gone@example.org>: Recipient address rejected", Source: "dsn"},
			{Email: "full@example.org", Kind: Soft, Reason: "4.2.2 smtp; 452", Source: "dsn"},
		}},
		{"dsn_delayed_nested.eml", []Bounce{{Email: "late@example.net", Kind: Soft, Reason: "4.4.7", Source: "dsn"}}},
		{"arf_yahoo.eml", []Bounce{{Email: "angry@example.com", Kind: Complaint, Reason: "feedback: abuse (SomeGenerator/1.0)", Source: "arf"}}},
		{"arf_no_rcpt.eml", []Bounce{{Email: "someone@example.com", Kind: Complaint, Reason: "feedback: fraud", Source: "arf"}}},
		{"arf_not_spam.eml", nil},
	} {
		got, err := ParseReport(sample(t, tc.file))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %+v, want %+v", tc.file, got, tc.want)
		}
		for i, w := range tc.want {
			g := got[i]
			if g.Email != w.Email || g.Kind != w.Kind || g.Source != w.Source || !strings.HasPrefix(g.Reason, w.Reason) {
				t.Errorf("%s[%d]: got %+v, want %+v", tc.file, i, g, w)
			}
		}
	}
	for _, raw := range [][]byte{sample(t, "autoreply.eml"), []byte("not an email at all"), {}} {
		if bs, err := ParseReport(raw); !errors.Is(err, ErrNotReport) || len(bs) != 0 {
			t.Errorf("%.30q: %v %v, want ErrNotReport", raw, bs, err)
		}
	}
}

// fakeSession is a mailbox in memory.
type fakeSession struct {
	msgs   []Message
	seen   map[uint32]bool
	failMk bool
	closed bool
}

func (f *fakeSession) Unseen(_ context.Context, max int) ([]Message, error) {
	var out []Message
	for _, m := range f.msgs {
		if !f.seen[m.UID] && len(out) < max {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeSession) MarkSeen(_ context.Context, uids []uint32) error {
	if f.failMk {
		return errors.New("store failed")
	}
	for _, u := range uids {
		f.seen[u] = true
	}
	return nil
}
func (f *fakeSession) Close() error { f.closed = true; return nil }

type fakeMailStore struct{ known map[string]string }

func (f fakeMailStore) ClaimDueMailboxes(context.Context, int, time.Duration) ([]Mailbox, [][]byte, error) {
	return nil, nil, nil
}
func (f fakeMailStore) FinishMailboxCheck(context.Context, int64, int, int, string) error { return nil }
func (f fakeMailStore) ExternalIDByEmail(_ context.Context, _ int64, email string) (string, error) {
	return f.known[email], nil
}

type fakePub struct {
	evs []event.Event
	err error
}

func (f *fakePub) PublishEvents(_ context.Context, _ int64, evs []event.Event) error {
	if f.err != nil {
		return f.err
	}
	f.evs = append(f.evs, evs...)
	return nil
}

func TestPollerCheck(t *testing.T) {
	sess := &fakeSession{seen: map[uint32]bool{}, msgs: []Message{
		{UID: 1, Raw: sample(t, "dsn_postfix.eml")}, {UID: 2, Raw: sample(t, "autoreply.eml")}, {UID: 3, Raw: sample(t, "arf_yahoo.eml")},
	}}
	pub := &fakePub{}
	var gotPassword string
	p := &Poller{Store: fakeMailStore{known: map[string]string{"gone@example.org": "u1", "angry@example.com": "u2"}}, Pub: pub,
		Unseal: func(b []byte) (string, error) { return strings.TrimPrefix(string(b), "sealed:"), nil },
		Open: func(_ context.Context, m Mailbox, pw string) (Session, error) { gotPassword = pw; return sess, nil }}
	m := Mailbox{TenantID: 7, Host: "imap.example.com", Port: 993, TLS: TLSImplicit, Username: "bounces", Folder: "INBOX"}

	processed, skipped, err := p.Check(context.Background(), m, []byte("sealed:pw-1"))
	if err != nil || processed != 3 || skipped != 1 || gotPassword != "pw-1" || !sess.closed {
		t.Fatalf("check: processed %d skipped %d err %v password %q closed %v", processed, skipped, err, gotPassword, sess.closed)
	}
	var names []string
	for _, e := range pub.evs {
		names = append(names, e.ExternalID+":"+e.Name)
		if e.ID == "" {
			t.Errorf("%s has no stable id", e.Name)
		}
	}
	// full@example.org isn't a contact: skipped.
	want := "u1:email_bounced u2:email_complained u2:unsubscribed"
	if strings.Join(names, " ") != want {
		t.Errorf("events %v, want %s", names, want)
	}
	if pub.evs[0].Attrs[AttrEmailBounced] != true {
		t.Error("a hard bounce must mark the address bounced")
	}
	if len(sess.seen) != 3 {
		t.Errorf("every read message is marked seen: %v", sess.seen)
	}
	// A second check finds nothing new.
	if n, s, err := p.Check(context.Background(), m, []byte("sealed:pw-1")); n != 0 || s != 0 || err != nil {
		t.Errorf("second check: %d %d %v", n, s, err)
	}

	// When recording fails, nothing is marked seen, so the next check reads them again.
	sess2 := &fakeSession{seen: map[uint32]bool{}, msgs: []Message{{UID: 9, Raw: sample(t, "dsn_postfix.eml")}}}
	p.Open = func(context.Context, Mailbox, string) (Session, error) { return sess2, nil }
	p.Pub = &fakePub{err: errors.New("kafka down")}
	if _, _, err := p.Check(context.Background(), m, []byte("sealed:pw-1")); err == nil || sess2.seen[9] {
		t.Errorf("publish failure: err %v, seen %v", err, sess2.seen)
	}
	if _, _, err := p.Check(context.Background(), m, nil); err == nil {
		t.Error("a mailbox without a password can't be read")
	}
}

func TestMailboxValidate(t *testing.T) {
	m := Mailbox{Host: " imap.example.com ", Username: "b"}
	if err := m.Validate(false); err != nil || m.Port != 993 || m.TLS != TLSImplicit || m.Folder != "INBOX" || m.Host != "imap.example.com" {
		t.Fatalf("defaults: %+v %v", m, err)
	}
	for _, bad := range []Mailbox{
		{Host: "imap.example.com", Username: "b", TLS: TLSNone},
		{Host: "imap.example.com:993", Username: "b"},
		{Host: "imap.example.com", Username: ""},
		{Host: "imap.example.com", Username: "b", Port: 70000},
		{Host: "imap.example.com", Username: "b", TLS: "ssl"},
		{Host: "imap.example.com", Username: "b", Folder: "a\r\nb"},
	} {
		if err := bad.Validate(false); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	plain := Mailbox{Host: "greenmail", Username: "b", TLS: TLSNone}
	if err := plain.Validate(true); err != nil || plain.Port != 143 {
		t.Errorf("development plain connection: %v %+v", err, plain)
	}
	a := Mailbox{Host: "imap.example.com", Port: 993, TLS: TLSImplicit, Username: "b"}
	for _, other := range []Mailbox{{Host: "evil.test", Port: 993, TLS: TLSImplicit, Username: "b"}, {Host: "imap.example.com", Port: 143, TLS: TLSStart, Username: "b"},
		{Host: "imap.example.com", Port: 993, TLS: TLSImplicit, Username: "c"}} {
		if a.SameServer(other) {
			t.Errorf("%+v counts as the same server", other)
		}
	}
	if !a.SameServer(Mailbox{Host: "IMAP.example.com", Port: 993, TLS: TLSImplicit, Username: "b", Folder: "Other"}) {
		t.Error("a folder change keeps the password")
	}
}
