package providers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"pulse/internal/channel"
)

// fakeSMTP is an in-process SMTP server on 127.0.0.1 that records connections and messages.
type fakeSMTP struct {
	ln       net.Listener
	greet    time.Duration       // per-connection setup cost before the greeting
	tls      *tls.Config         // when set, STARTTLS is advertised
	user     string              // when set, AUTH PLAIN is required before MAIL
	pass     string              //
	rcpt     func(string) string // optional reply to RCPT TO (default 250)
	dropMsgs int                 // close the connection silently after this many messages (0: never)

	mu        sync.Mutex
	conns     int // connections accepted
	open      int
	maxOpen   int
	msgs      []string
	authTLS   []bool // per AUTH: was the connection TLS
	resets    int
	perConn   []int // messages per connection, by connection number
	wg        sync.WaitGroup
	closed    bool
	liveConns map[net.Conn]bool
}

func newFakeSMTP(t testing.TB) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, liveConns: map[net.Conn]bool{}}
	return f
}

func (f *fakeSMTP) start(t testing.TB) {
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, err := f.ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			if f.closed {
				f.mu.Unlock()
				_ = c.Close()
				return
			}
			f.conns++
			f.open++
			f.maxOpen = max(f.maxOpen, f.open)
			n := len(f.perConn)
			f.perConn = append(f.perConn, 0)
			f.liveConns[c] = true
			f.wg.Add(1)
			f.mu.Unlock()
			go func() {
				defer f.wg.Done()
				f.serve(c, n)
				_ = c.Close()
				f.mu.Lock()
				f.open--
				delete(f.liveConns, c)
				f.mu.Unlock()
			}()
		}
	}()
	t.Cleanup(func() {
		closeSMTPPools()
		f.mu.Lock()
		f.closed = true
		for c := range f.liveConns {
			_ = c.Close()
		}
		f.mu.Unlock()
		_ = f.ln.Close()
		f.wg.Wait()
	})
}

func (f *fakeSMTP) addr() string { return f.ln.Addr().String() }

func (f *fakeSMTP) stats() (conns, maxOpen int, msgs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns, f.maxOpen, append([]string(nil), f.msgs...)
}

func (f *fakeSMTP) serve(nc net.Conn, n int) {
	if f.greet > 0 {
		time.Sleep(f.greet)
	}
	var conn net.Conn = nc
	tp := textproto.NewConn(conn)
	reply := func(s string) bool { return tp.PrintfLine("%s", s) == nil }
	reply("220 fake.test ESMTP")
	isTLS, authed, haveMail, haveRcpt, sent := false, false, false, false, 0
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			lines := []string{"250-fake.test"}
			if f.tls != nil && !isTLS {
				lines = append(lines, "250-STARTTLS")
			}
			if f.user != "" {
				lines = append(lines, "250-AUTH PLAIN")
			}
			lines = append(lines, "250 8BITMIME")
			reply(strings.Join(lines, "\r\n"))
		case "STARTTLS":
			if f.tls == nil || isTLS {
				reply("502 no")
				continue
			}
			reply("220 go ahead")
			tc := tls.Server(nc, f.tls)
			if tc.Handshake() != nil {
				return
			}
			isTLS, conn = true, tc
			tp = textproto.NewConn(conn)
		case "AUTH":
			f.mu.Lock()
			f.authTLS = append(f.authTLS, isTLS)
			f.mu.Unlock()
			mech, ir, _ := strings.Cut(arg, " ")
			b, _ := base64.StdEncoding.DecodeString(ir)
			if strings.ToUpper(mech) == "PLAIN" && string(b) == "\x00"+f.user+"\x00"+f.pass {
				authed = true
				reply("235 ok")
			} else {
				reply("535 bad credentials")
			}
		case "MAIL":
			if f.user != "" && !authed {
				reply("530 authentication required")
				continue
			}
			haveMail = true
			reply("250 ok")
		case "RCPT":
			if !haveMail {
				reply("503 need MAIL")
				continue
			}
			r := "250 ok"
			if f.rcpt != nil {
				r = f.rcpt(arg)
			}
			if strings.HasPrefix(r, "250") {
				haveRcpt = true
			}
			reply(r)
		case "DATA":
			if !haveRcpt {
				reply("503 need RCPT")
				continue
			}
			reply("354 go")
			b, err := io.ReadAll(tp.DotReader())
			if err != nil {
				return
			}
			f.mu.Lock()
			f.msgs = append(f.msgs, string(b))
			f.perConn[n]++
			f.mu.Unlock()
			haveMail, haveRcpt = false, false
			sent++
			reply("250 queued")
			if f.dropMsgs > 0 && sent >= f.dropMsgs {
				return // vanish without a word, like an MTA with a short idle timeout
			}
		case "RSET":
			f.mu.Lock()
			f.resets++
			f.mu.Unlock()
			haveMail, haveRcpt = false, false
			reply("250 reset")
		case "NOOP":
			reply("250 ok")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("500 unknown command")
		}
	}
}

// testCert returns a self-signed certificate for mail.test, and a pool trusting it.
func testCert(t testing.TB) (tls.Certificate, *x509.CertPool) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.test"},
		DNSNames: []string{"mail.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, roots
}

func newPooledSMTP(t testing.TB, f *fakeSMTP, maxConns, perConn int) *SMTP {
	t.Helper()
	host, port, _ := net.SplitHostPort(f.addr())
	p, err := New("smtp", Deps{}, map[string]string{"host": host, "port": port, "from": "Acme <hello@acme.test>",
		"max_connections": fmt.Sprint(maxConns), "messages_per_connection": fmt.Sprint(perConn)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*SMTP)
}

func outbound(i int) Outbound {
	return Outbound{MessageID: fmt.Sprintf("m%d", i), Channel: channel.Email, To: fmt.Sprintf("u%d@example.com", i),
		Rendered: channel.Rendered{Subject: fmt.Sprintf("Hello %d", i), HTML: fmt.Sprintf("<p>message %d</p>\n.dot line\n%s", i, strings.Repeat("x", 300))}}
}

// checkMessage parses what the server received and checks it is message i, byte for byte in the body.
func checkMessage(t *testing.T, raw string, i int) {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("message %d unreadable: %v", i, err)
	}
	if got := m.Header.Get("X-Pulse-Message-Id"); got != fmt.Sprintf("m%d", i) {
		t.Fatalf("message id = %q, want m%d", got, i)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil {
		t.Fatal(err)
	}
	want := outbound(i).Rendered.HTML + "\n" // the server's DotReader turns CRLF into LF
	if string(body) != want {
		t.Fatalf("message %d body = %q, want %q", i, body, want)
	}
}

func TestSMTPPoolReusesConnections(t *testing.T) {
	f := newFakeSMTP(t)
	f.start(t)
	s := newPooledSMTP(t, f, 4, 100)
	for i := range 250 {
		// A fresh adapter every 50 messages, as the dispatcher rebuilds them: same settings, same pool.
		if i%50 == 0 {
			s = newPooledSMTP(t, f, 4, 100)
		}
		res, err := s.Send(context.Background(), outbound(i))
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if res.ProviderMessageID != fmt.Sprintf("m%d", i) {
			t.Fatalf("result = %+v", res)
		}
	}
	conns, _, msgs := f.stats()
	if conns != 3 {
		t.Errorf("connections = %d, want 3 (100 + 100 + 50 messages)", conns)
	}
	if len(msgs) != 250 {
		t.Fatalf("messages = %d", len(msgs))
	}
	for i, raw := range msgs {
		checkMessage(t, raw, i)
	}
	f.mu.Lock()
	if fmt.Sprint(f.perConn) != "[100 100 50]" {
		t.Errorf("messages per connection = %v", f.perConn)
	}
	f.mu.Unlock()
}

func TestSMTPPoolBoundsConnections(t *testing.T) {
	f := newFakeSMTP(t)
	f.greet = 5 * time.Millisecond
	f.start(t)
	s := newPooledSMTP(t, f, 3, 100)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Send(context.Background(), outbound(i)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	conns, maxOpen, msgs := f.stats()
	if maxOpen > 3 || conns > 3 {
		t.Errorf("max open = %d, total = %d; want at most 3", maxOpen, conns)
	}
	if len(msgs) != 40 {
		t.Errorf("messages = %d", len(msgs))
	}
}

func TestSMTPPoolReplacesDroppedConnection(t *testing.T) {
	f := newFakeSMTP(t)
	f.dropMsgs = 1 // the server hangs up after every message
	f.start(t)
	s := newPooledSMTP(t, f, 1, 100)
	for i := range 3 {
		if _, err := s.Send(context.Background(), outbound(i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond) // let the close land
	}
	conns, _, msgs := f.stats()
	if conns != 3 || len(msgs) != 3 {
		t.Errorf("connections = %d, messages = %d; want 3 and 3", conns, len(msgs))
	}
	for i, raw := range msgs {
		checkMessage(t, raw, i)
	}
}

func TestSMTPPoolRejectedRecipient(t *testing.T) {
	f := newFakeSMTP(t)
	f.rcpt = func(arg string) string {
		switch {
		case strings.Contains(arg, "<u1@"):
			return "550 5.1.1 no such user"
		case strings.Contains(arg, "<u2@"):
			return "451 4.3.0 try again later"
		}
		return "250 ok"
	}
	f.start(t)
	s := newPooledSMTP(t, f, 1, 100)
	if _, err := s.Send(context.Background(), outbound(0)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Send(context.Background(), outbound(1))
	if !errors.Is(err, ErrPermanent) || !strings.Contains(err.Error(), "550") {
		t.Errorf("550 on RCPT: err = %v, want permanent", err)
	}
	_, err = s.Send(context.Background(), outbound(2))
	if err == nil || errors.Is(err, ErrPermanent) || !Transient(err) {
		t.Errorf("451 on RCPT: err = %v, want transient", err)
	}
	if _, err := s.Send(context.Background(), outbound(3)); err != nil {
		t.Fatal(err)
	}
	conns, _, msgs := f.stats()
	f.mu.Lock()
	resets := f.resets
	f.mu.Unlock()
	if conns != 1 || len(msgs) != 2 || resets != 2 {
		t.Errorf("connections = %d, messages = %d, resets = %d; want 1, 2, 2", conns, len(msgs), resets)
	}
	checkMessage(t, msgs[1], 3)
}

func TestSMTPPoolSTARTTLSAndAuth(t *testing.T) {
	cert, roots := testCert(t)
	newServer := func(withTLS bool) *fakeSMTP {
		f := newFakeSMTP(t)
		f.user, f.pass = "apikey", "s3cret"
		if withTLS {
			f.tls = &tls.Config{Certificates: []tls.Certificate{cert}}
		}
		f.start(t)
		return f
	}
	adapter := func(f *fakeSMTP) *SMTP {
		// The relay is "mail.test" (not localhost, so PlainAuth insists on TLS); dial the fake server.
		p, err := New("smtp", Deps{}, map[string]string{"host": "mail.test", "port": "587", "from": "hello@acme.test"},
			map[string]string{"username": "apikey", "password": "s3cret"})
		if err != nil {
			t.Fatal(err)
		}
		s := p.(*SMTP)
		s.tlsConfig = &tls.Config{RootCAs: roots}
		s.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, f.addr())
		}
		return s
	}

	f := newServer(true)
	s := adapter(f)
	for i := range 2 {
		if _, err := s.Send(context.Background(), outbound(i)); err != nil {
			t.Fatalf("send over STARTTLS: %v", err)
		}
	}
	conns, _, msgs := f.stats()
	f.mu.Lock()
	authTLS := append([]bool(nil), f.authTLS...)
	f.mu.Unlock()
	if conns != 1 || len(msgs) != 2 || fmt.Sprint(authTLS) != "[true]" {
		t.Errorf("connections = %d, messages = %d, auth over TLS = %v; want 1, 2, [true]", conns, len(msgs), authTLS)
	}

	// A server that doesn't offer STARTTLS never gets the password.
	plain := newServer(false)
	s = adapter(plain)
	if _, err := s.Send(context.Background(), outbound(9)); err == nil {
		t.Fatal("sent with AUTH PLAIN over an unencrypted connection")
	}
	plain.mu.Lock()
	if len(plain.authTLS) != 0 || len(plain.msgs) != 0 {
		t.Errorf("server without TLS saw AUTH %v, %d messages", plain.authTLS, len(plain.msgs))
	}
	plain.mu.Unlock()

	// An untrusted certificate fails the send (transiently: the relay may fix it).
	untrusted := newServer(true)
	s = adapter(untrusted)
	s.tlsConfig = &tls.Config{RootCAs: x509.NewCertPool()}
	if _, err := s.Send(context.Background(), outbound(10)); err == nil || errors.Is(err, ErrPermanent) {
		t.Errorf("untrusted certificate: err = %v", err)
	}
}

func TestSMTPPoolConfigValidation(t *testing.T) {
	base := func(kv ...string) map[string]string {
		c := map[string]string{"host": "smtp.acme.test", "port": "587", "from": "hello@acme.test"}
		for i := 0; i < len(kv); i += 2 {
			c[kv[i]] = kv[i+1]
		}
		return c
	}
	p, err := New("smtp", Deps{}, base(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := p.(*SMTP); s.MaxConns != 4 || s.MessagesPerConn != 100 {
		t.Errorf("defaults = %d, %d", s.MaxConns, s.MessagesPerConn)
	}
	p, err = New("smtp", Deps{}, base("max_connections", " 50 ", "messages_per_connection", "10000"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := p.(*SMTP); s.MaxConns != 50 || s.MessagesPerConn != 10000 {
		t.Errorf("parsed = %d, %d", s.MaxConns, s.MessagesPerConn)
	}
	for _, kv := range [][2]string{
		{"max_connections", "0"}, {"max_connections", "51"}, {"max_connections", "four"}, {"max_connections", "2.5"},
		{"messages_per_connection", "0"}, {"messages_per_connection", "10001"}, {"messages_per_connection", "-1"},
	} {
		if _, err := New("smtp", Deps{}, base(kv[0], kv[1]), nil); err == nil {
			t.Errorf("%s=%q accepted", kv[0], kv[1])
		}
	}
	k, _ := Lookup("smtp")
	var keys []string
	for _, f := range k.Config {
		keys = append(keys, f.Key)
		if (f.Key == "max_connections" || f.Key == "messages_per_connection") && f.Required {
			t.Errorf("%s is required", f.Key)
		}
	}
	if strings.Join(keys, ",") != "host,port,from,max_connections,messages_per_connection,service" {
		t.Errorf("config fields = %v", keys)
	}
}

func TestSMTPPoolWaitHonoursContext(t *testing.T) {
	// A server that accepts the connection but never greets: the one slot stays busy.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	defer func() {
		_ = ln.Close()
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
		closeSMTPPools()
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, err := New("smtp", Deps{}, map[string]string{"host": host, "port": port, "from": "hello@acme.test", "max_connections": "1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := p.(*SMTP)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := s.Send(firstCtx, outbound(0)) // holds the slot, stuck waiting for the greeting
		first <- err
	}()
	c := <-accepted
	accepted <- c // for the deferred close

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = s.Send(ctx, outbound(1))
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPermanent) {
		t.Errorf("err = %v, want a transient deadline error", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("waited %v", d)
	}
	cancelFirst() // the stuck send's deadline is the ctx's: it gives up too
	select {
	case err := <-first:
		if err == nil {
			t.Error("the stuck send succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Error("the stuck send did not give up")
	}
}

func benchMessage() []byte {
	o := outbound(1)
	msg, _ := BuildMessage(Mail{ID: o.MessageID, From: "hello@acme.test", To: o.To, Subject: o.Rendered.Subject, HTML: o.Rendered.HTML, Date: time.Now()})
	return msg
}

// The fake server sleeps 3 ms before greeting each connection: a realistic share of a TCP + TLS +
// AUTH setup to a relay (real ones cost tens of milliseconds more).
const benchGreet = 3 * time.Millisecond

func BenchmarkSMTPPooled(b *testing.B) {
	f := newFakeSMTP(b)
	f.greet = benchGreet
	f.start(b)
	host, port, _ := net.SplitHostPort(f.addr())
	p, err := New("smtp", Deps{}, map[string]string{"host": host, "port": port, "from": "hello@acme.test"}, nil)
	if err != nil {
		b.Fatal(err)
	}
	o := outbound(1)
	b.ResetTimer()
	start := time.Now()
	for range b.N {
		if _, err := p.Send(context.Background(), o); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N)/time.Since(start).Seconds(), "msgs/s")
}

func BenchmarkSMTPUnpooled(b *testing.B) {
	f := newFakeSMTP(b)
	f.greet = benchGreet
	f.start(b)
	msg := benchMessage()
	b.ResetTimer()
	start := time.Now()
	for range b.N {
		if err := smtp.SendMail(f.addr(), nil, "hello@acme.test", []string{"u1@example.com"}, msg); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N)/time.Since(start).Seconds(), "msgs/s")
}
