//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"
)

// TestBounceMailbox: an IMAP server (go-imap's in-memory backend, in the compose-network test
// container) holds a delivery status notification and a feedback-loop complaint. Pulse reads the
// mailbox, marks the hard-bounced address and unsubscribes the complainer, and flags both
// messages \Seen.
func TestBounceMailbox(t *testing.T) {
	host := env("PULSE_HOOK_HOST", "")
	if host == "" {
		t.Skip("set PULSE_HOOK_HOST: run inside the compose network (make e2e)")
	}
	c, _ := workspace(t, "Bounce mailbox")
	run := fmt.Sprint(time.Now().UnixNano())
	gone, angry := "gone-"+run+"@example.org", "angry-"+run+"@example.com"
	jsonCall(t, c, "POST", "/v1/events", map[string]any{"events": []any{
		map[string]any{"external_id": "gone-" + run, "name": "identify", "email": gone},
		map[string]any{"external_id": "angry-" + run, "name": "identify", "email": angry}}}, 202)
	waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/angry-"+run, nil, -1)["contact"] != nil })

	be := memory.New() // one user: username / password, with an INBOX
	user, err := be.Login(nil, "username", "password")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := user.GetMailbox("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"dsn_postfix.eml", "arf_yahoo.eml"} {
		raw, err := os.ReadFile("../../internal/bounces/testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("gone@example.org"), []byte(gone)), []byte("Gone@Example.org"), []byte(gone))
		raw = bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("angry@example.com"), []byte(angry)), []byte("Angry@Example.com"), []byte(angry))
		raw = bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
		if err := inbox.CreateMessage([]string{}, time.Now(), bytes.NewBuffer(raw)); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(be)
	srv.AllowInsecureAuth = true
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	settings := map[string]any{"enabled": true, "host": host, "port": port, "tls": "none", "username": "username", "password": "wrong"}
	if r := jsonCall(t, c, "POST", "/v1/settings/bounce-mailboxes/test", settings, 200); r["ok"] != false {
		t.Errorf("test with a wrong password: %v", r)
	}
	settings["password"] = "password"
	if r := jsonCall(t, c, "POST", "/v1/settings/bounce-mailboxes/test", settings, 200); r["ok"] != true {
		t.Fatalf("test: %v", r)
	}
	saved := jsonCall(t, c, "POST", "/v1/settings/bounce-mailboxes", settings, 200)
	mid := saved["mailbox"].(map[string]any)["id"]
	if saved["password"] != "••••••••" {
		t.Errorf("saved password shown as %v", saved["password"])
	}
	jsonCall(t, c, "POST", fmt.Sprintf("/v1/settings/bounce-mailboxes/%v/check", mid), nil, 202)

	waitFor(t, 40*time.Second, func() bool {
		return attrsOf(t, c, "gone-"+run)["email_bounced"] == true && attrsOf(t, c, "angry-"+run)["optout_email"] == true
	})
	waitFor(t, 15*time.Second, func() bool {
		mb := jsonCall(t, c, "GET", "/v1/settings/bounce-mailboxes", nil, 200)["mailboxes"].([]any)[0].(map[string]any)["mailbox"].(map[string]any)
		return mb["processed"] == 3.0 && mb["last_error"] == ""
	})
	if n := seenCount(t, inbox); n != 3 { // the backend's welcome message (skipped) and the two reports
		t.Errorf("%d messages marked seen, want 3", n)
	}
	waitFor(t, 15*time.Second, func() bool { // the timeline reads ClickHouse, a moment behind
		return strings.Contains(strings.Join(contactEvents(t, c, "angry-"+run), " "), "email_complained")
	})
}

func seqAll() *imap.SeqSet {
	s := new(imap.SeqSet)
	s.AddRange(1, 0)
	return s
}

func seenCount(t *testing.T, mb backend.Mailbox) int {
	ch := make(chan *imap.Message, 10)
	go func() { _ = mb.ListMessages(true, seqAll(), []imap.FetchItem{imap.FetchFlags}, ch) }()
	n := 0
	for m := range ch {
		for _, f := range m.Flags {
			if f == imap.SeenFlag {
				n++
			}
		}
	}
	return n
}
