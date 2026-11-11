package providers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"sync"
	"time"
)

// SMTP connection pooling. Opening a connection costs a TCP handshake, the greeting, EHLO,
// STARTTLS (a TLS handshake) and AUTH: several round trips per message when every message gets
// its own connection. A pool keeps up to MaxConns authenticated connections per relay and sends
// up to MessagesPerConn messages over each before replacing it.
//
// Pools live in a package registry, not in the *SMTP value: the dispatcher rebuilds its adapters
// from the saved settings every 30 s, and adapters with the same settings must share one pool.
// Changed settings give a different key, hence a new pool; the old one is closed by the janitor
// once it has been idle for smtpIdleTimeout.

const (
	defaultSMTPMaxConns        = 4
	defaultSMTPMessagesPerConn = 100
)

var (
	smtpDialTimeout  = 15 * time.Second
	smtpCmdTimeout   = 60 * time.Second // each command and its reply
	smtpDataTimeout  = 5 * time.Minute  // the message body (attachments can be large)
	smtpIdleTimeout  = 60 * time.Second // idle connections are closed after this
	smtpJanitorEvery = 15 * time.Second

	longAgo = time.Unix(1, 0)
)

// poolKey is everything that identifies a connection: where it goes, who it logs in as, how it
// is secured, and the pool's limits.
type poolKey struct {
	addr, serverName  string
	implicitTLS       bool
	username, passSum string
	auth              string // identity of an Auth set without a username (adapters built in code)
	maxConns, perConn int
	tlsConfig         *tls.Config // tests only: a custom root CA
	dial              string      // tests only: a custom dialer
	helo              string      // the name given in EHLO (default localhost)
}

type smtpConn struct {
	c        *smtp.Client
	nc       net.Conn // the TCP (or implicit TLS) connection, for deadlines
	sent     int
	lastUsed time.Time
}

func (sc *smtpConn) deadline(ctx context.Context, d time.Duration) {
	t := time.Now().Add(d)
	if dl, ok := ctx.Deadline(); ok && dl.Before(t) {
		t = dl
	}
	_ = sc.nc.SetDeadline(t)
}

// quit says goodbye politely (bounded by a short deadline) and closes the connection.
func (sc *smtpConn) quit() {
	_ = sc.nc.SetDeadline(time.Now().Add(5 * time.Second))
	if err := sc.c.Quit(); err != nil {
		_ = sc.c.Close()
	}
}

type smtpPool struct {
	key  poolKey
	host string
	auth smtp.Auth
	tls  *tls.Config
	dial func(ctx context.Context, network, addr string) (net.Conn, error)

	slots chan struct{} // one per sender holding (or about to hold) a connection: at most maxConns

	mu       sync.Mutex
	idle     []*smtpConn // most recently used last
	open     int         // connections open or being dialled
	users    int         // senders between acquire and release (keeps the pool registered)
	lastUsed time.Time
}

var smtpPools = struct {
	sync.Mutex
	m    map[poolKey]*smtpPool
	stop chan struct{} // closes the running janitor; nil when none is running
	done chan struct{}
}{m: map[poolKey]*smtpPool{}}

// poolKey computes the registry key for this adapter.
func (s *SMTP) poolKey() (poolKey, string, error) {
	host, port, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return poolKey{}, "", fmt.Errorf("smtp: address %q: %w", s.Addr, err)
	}
	k := poolKey{addr: s.Addr, serverName: host, implicitTLS: port == "465",
		username: s.username, passSum: s.passSum, maxConns: s.MaxConns, perConn: s.MessagesPerConn,
		tlsConfig: s.tlsConfig, helo: s.Helo}
	if k.maxConns <= 0 {
		k.maxConns = defaultSMTPMaxConns
	}
	if k.perConn <= 0 {
		k.perConn = defaultSMTPMessagesPerConn
	}
	if s.Auth != nil && s.username == "" {
		k.auth = fmt.Sprintf("%T%p", s.Auth, s.Auth)
	}
	if s.dial != nil {
		k.dial = fmt.Sprintf("%p", s.dial)
	}
	return k, host, nil
}

// acquirePool returns the pool for s, registering it on first use, with a user counted.
func (s *SMTP) acquirePool() (*smtpPool, error) {
	k, host, err := s.poolKey()
	if err != nil {
		return nil, err
	}
	smtpPools.Lock()
	defer smtpPools.Unlock()
	p := smtpPools.m[k]
	if p == nil {
		cfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		if s.tlsConfig != nil {
			cfg = s.tlsConfig.Clone()
			cfg.ServerName = host
		}
		dial := s.dial
		if dial == nil {
			d := &net.Dialer{}
			dial = d.DialContext
		}
		p = &smtpPool{key: k, host: host, auth: s.Auth, tls: cfg, dial: dial,
			slots: make(chan struct{}, k.maxConns), lastUsed: time.Now()}
		smtpPools.m[k] = p
		if smtpPools.stop == nil {
			smtpPools.stop, smtpPools.done = make(chan struct{}), make(chan struct{})
			go smtpJanitor(smtpPools.stop, smtpPools.done)
		}
	}
	p.mu.Lock()
	p.users++
	p.mu.Unlock()
	return p, nil
}

func (p *smtpPool) release() {
	p.mu.Lock()
	p.users--
	p.lastUsed = time.Now()
	p.mu.Unlock()
}

// send delivers one message over a pooled connection. A reused connection that turns out to be
// dead (the server dropped it while idle) is replaced once, transparently: nothing was sent on it.
func (p *smtpPool) send(ctx context.Context, from, to string, msg []byte) error {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("smtp: waiting for a free connection: %w", ctx.Err())
	}
	defer func() { <-p.slots }()

	for attempt := 0; ; attempt++ {
		sc, reused, err := p.checkout(ctx)
		if err != nil {
			return err
		}
		// Cancelling ctx interrupts the exchange (a past deadline fails the pending read or write);
		// a connection interrupted that way is never reused.
		stop := context.AfterFunc(ctx, func() { _ = sc.nc.SetDeadline(longAgo) })
		stage, err := p.transact(ctx, sc, from, to, msg)
		if !stop() {
			p.discard(sc, false)
			return err
		}
		if err == nil {
			sc.sent++
			if sc.sent >= p.key.perConn {
				p.discard(sc, true)
			} else {
				p.checkin(sc)
			}
			return nil
		}
		var te *textproto.Error
		isReply := errors.As(err, &te)
		if reused && stage == "mail" && attempt == 0 && (!isReply || te.Code == 421) {
			p.discard(sc, false) // stale: dropped by the server or timed out on its side
			continue
		}
		if isReply && te.Code != 421 {
			sc.deadline(ctx, smtpCmdTimeout)
			if sc.c.Reset() == nil {
				p.checkin(sc)
				return err
			}
		}
		p.discard(sc, false)
		return err
	}
}

// transact runs MAIL, RCPT and DATA; stage names the step that failed.
func (p *smtpPool) transact(ctx context.Context, sc *smtpConn, from, to string, msg []byte) (string, error) {
	sc.deadline(ctx, smtpCmdTimeout)
	if err := sc.c.Mail(from); err != nil {
		return "mail", err
	}
	sc.deadline(ctx, smtpCmdTimeout)
	if err := sc.c.Rcpt(to); err != nil {
		return "rcpt", err
	}
	sc.deadline(ctx, smtpCmdTimeout)
	w, err := sc.c.Data()
	if err != nil {
		return "data", err
	}
	sc.deadline(ctx, smtpDataTimeout)
	if _, err := w.Write(msg); err != nil {
		return "body", err
	}
	if err := w.Close(); err != nil {
		return "body", err
	}
	return "", nil
}

// checkout takes the most recently used idle connection (closing any that sat idle too long), or
// dials a new one. The caller holds a slot, so open connections never exceed maxConns.
func (p *smtpPool) checkout(ctx context.Context) (*smtpConn, bool, error) {
	var stale []*smtpConn
	p.mu.Lock()
	var sc *smtpConn
	for len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if time.Since(c.lastUsed) < smtpIdleTimeout {
			sc = c
			break
		}
		p.open--
		stale = append(stale, c)
	}
	if sc == nil {
		p.open++
	}
	p.mu.Unlock()
	for _, c := range stale {
		c.quit()
	}
	if sc != nil {
		return sc, true, nil
	}
	sc, err := p.connect(ctx)
	if err != nil {
		p.mu.Lock()
		p.open--
		p.mu.Unlock()
		return nil, false, err
	}
	return sc, false, nil
}

func (p *smtpPool) checkin(sc *smtpConn) {
	sc.lastUsed = time.Now()
	_ = sc.nc.SetDeadline(time.Time{})
	p.mu.Lock()
	p.idle = append(p.idle, sc)
	p.mu.Unlock()
}

// discard closes a connection that must not be reused: politely (QUIT) when it is healthy.
func (p *smtpPool) discard(sc *smtpConn, healthy bool) {
	p.mu.Lock()
	p.open--
	p.mu.Unlock()
	if healthy {
		sc.quit()
	} else {
		_ = sc.c.Close()
	}
}

// connect dials, reads the greeting, says EHLO, upgrades with STARTTLS whenever the server offers
// it (implicit TLS on port 465) and authenticates. net/smtp's PlainAuth refuses to send the
// password over a connection that isn't TLS, except to localhost.
func (p *smtpPool) connect(ctx context.Context) (*smtpConn, error) {
	dctx, cancel := context.WithTimeout(ctx, smtpDialTimeout)
	defer cancel()
	nc, err := p.dial(dctx, "tcp", p.key.addr)
	if err != nil {
		return nil, fmt.Errorf("smtp: dial %s: %w", p.key.addr, err)
	}
	sc := &smtpConn{nc: nc}
	sc.deadline(ctx, smtpCmdTimeout)
	stop := context.AfterFunc(ctx, func() { _ = nc.SetDeadline(longAgo) })
	sc, err = p.handshake(ctx, sc)
	if !stop() && err == nil {
		_ = sc.c.Close()
		return nil, fmt.Errorf("smtp: connect: %w", ctx.Err())
	}
	return sc, err
}

// handshake reads the greeting, says EHLO, starts TLS and authenticates.
func (p *smtpPool) handshake(ctx context.Context, sc *smtpConn) (*smtpConn, error) {
	nc := sc.nc
	var conn net.Conn = nc
	if p.key.implicitTLS {
		tc := tls.Client(nc, p.tls)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = nc.Close()
			return nil, fmt.Errorf("smtp: tls: %w", err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, p.host)
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	sc.c = c
	fail := func(err error) (*smtpConn, error) {
		_ = c.Close()
		return nil, err
	}
	helo := p.key.helo
	if helo == "" {
		helo = "localhost"
	}
	if err := c.Hello(helo); err != nil {
		return fail(err)
	}
	if !p.key.implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(p.tls); err != nil {
				return fail(err)
			}
		}
	}
	if p.auth != nil {
		if ok, _ := c.Extension("AUTH"); !ok {
			return fail(errors.New("smtp: server doesn't support AUTH"))
		}
		if err := c.Auth(p.auth); err != nil {
			return fail(err)
		}
	}
	return sc, nil
}

// sweep closes connections idle longer than smtpIdleTimeout and drops pools nobody has used for
// that long. It reports whether any pool remains.
func sweepSMTPPools() bool {
	var stale []*smtpConn
	smtpPools.Lock()
	for k, p := range smtpPools.m {
		p.mu.Lock()
		keep := p.idle[:0]
		for _, c := range p.idle {
			if time.Since(c.lastUsed) >= smtpIdleTimeout {
				p.open--
				stale = append(stale, c)
			} else {
				keep = append(keep, c)
			}
		}
		p.idle = keep
		if p.users == 0 && p.open == 0 && time.Since(p.lastUsed) >= smtpIdleTimeout {
			delete(smtpPools.m, k)
		}
		p.mu.Unlock()
	}
	left := len(smtpPools.m) > 0
	if !left {
		smtpPools.stop, smtpPools.done = nil, nil
	}
	smtpPools.Unlock()
	for _, c := range stale {
		c.quit()
	}
	return left
}

func smtpJanitor(stop, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(smtpJanitorEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if !sweepSMTPPools() {
				return
			}
		}
	}
}

// closeSMTPPools closes every idle connection, forgets every pool and stops the janitor (tests).
func closeSMTPPools() {
	var conns []*smtpConn
	smtpPools.Lock()
	for k, p := range smtpPools.m {
		p.mu.Lock()
		conns = append(conns, p.idle...)
		p.open -= len(p.idle)
		p.idle = nil
		p.mu.Unlock()
		delete(smtpPools.m, k)
	}
	stop, done := smtpPools.stop, smtpPools.done
	smtpPools.stop, smtpPools.done = nil, nil
	smtpPools.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	for _, c := range conns {
		c.quit()
	}
}
