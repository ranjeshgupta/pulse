package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"pulse/internal/auth"
	"pulse/internal/bounces"
	"pulse/internal/store/pg"
)

type mailboxMem struct {
	*memStore
	m      *bounces.Mailbox
	sealed []byte
}

func (f *mailboxMem) BounceMailbox(context.Context, int64) (bounces.Mailbox, []byte, error) {
	if f.m == nil {
		return bounces.Mailbox{}, nil, pg.ErrNotFound
	}
	m := *f.m
	m.HasPassword = len(f.sealed) > 0
	return m, f.sealed, nil
}
func (f *mailboxMem) SaveBounceMailbox(_ context.Context, m bounces.Mailbox, sealed []byte) error {
	f.m, f.sealed = &m, sealed
	return nil
}
func (f *mailboxMem) DeleteBounceMailbox(context.Context, int64) error { f.m = nil; return nil }
func (f *mailboxMem) CheckBounceMailboxNow(context.Context, int64) error {
	if f.m == nil || !f.m.Enabled {
		return pg.ErrNotFound
	}
	return nil
}

type nopSession struct{}

func (nopSession) Unseen(context.Context, int) ([]bounces.Message, error) { return nil, nil }
func (nopSession) MarkSeen(context.Context, []uint32) error               { return nil }
func (nopSession) Close() error                                           { return nil }

func TestBounceMailboxSettings(t *testing.T) {
	_, base, _ := newServer()
	st := &mailboxMem{memStore: base}
	box, _ := auth.NewBox(strings.Repeat("cd", 32))
	var dialled []string
	s := New(Deps{Store: st, Pub: &memPub{}, Box: box, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		MailboxOpener: func(_ context.Context, m bounces.Mailbox, pw string) (bounces.Session, error) {
			dialled = append(dialled, m.Host+"/"+pw)
			if pw != "right" {
				return nil, errors.New("sign-in refused: invalid credentials")
			}
			return nopSession{}, nil
		}})
	h := s.Handler()
	put := func(body string) (int, map[string]any) {
		w := do(t, h, "PUT", "/v1/settings/bounce-mailbox", "key-1", body)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, _ := put(`{"enabled":true,"host":"imap.example.com","username":"bounces"}`); code != 400 {
		t.Errorf("no password: %d, want 400", code)
	}
	if code, _ := put(`{"enabled":true,"host":"imap.example.com","username":"bounces","tls":"none","password":"right"}`); code != 400 {
		t.Errorf("plain text in production: %d, want 400", code)
	}
	code, out := put(`{"enabled":true,"host":"imap.example.com","username":"bounces","password":"right"}`)
	if code != 200 || out["password"] != "••••••••" || strings.Contains(string(mustJSON(out)), "right") {
		t.Fatalf("save: %d %v", code, out)
	}
	if pw, _ := box.Open(st.sealed); string(pw) != "right" {
		t.Fatal("password not sealed with the server key")
	}
	// The masked password sent back keeps the stored one for the same server...
	if code, _ := put(`{"enabled":true,"host":"imap.example.com","username":"bounces","folder":"Bounces","password":"••••••••"}`); code != 200 || st.m.Folder != "Bounces" {
		t.Errorf("folder change keeps the password: %d", code)
	}
	// ...but never goes to another server.
	if code, out := put(`{"enabled":true,"host":"evil.test","username":"bounces","password":"••••••••"}`); code != 400 || !strings.Contains(out["error"].(string), "password again") {
		t.Errorf("new host with the stored password: %d %v", code, out)
	}
	w := do(t, h, "POST", "/v1/settings/bounce-mailbox/test", "key-1", `{"host":"imap.example.com","username":"bounces","folder":"Bounces"}`)
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("test with the stored password: %s", w.Body.String())
	}
	w = do(t, h, "POST", "/v1/settings/bounce-mailbox/test", "key-1", `{"host":"imap.example.com","username":"bounces","password":"wrong"}`)
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "sign-in refused") {
		t.Errorf("test with a wrong password: %s", w.Body.String())
	}
	if strings.Join(dialled, ",") != "imap.example.com/right,imap.example.com/wrong" {
		t.Errorf("dialled %v", dialled)
	}
	if w := do(t, h, "POST", "/v1/settings/bounce-mailbox/check", "key-1", ""); w.Code != 202 {
		t.Errorf("check now: %d", w.Code)
	}
	if w := do(t, h, "GET", "/v1/settings/bounce-mailbox", "key-1", ""); w.Code != 200 || strings.Contains(w.Body.String(), "right") {
		t.Errorf("get: %d %s", w.Code, w.Body.String())
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
