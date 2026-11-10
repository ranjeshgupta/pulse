package journey

import (
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"sync"
	"time"
)

// ValidEmail is the "has valid email address" condition: the address parses, has a domain with a
// dot, isn't a known disposable domain (when asked), and the domain accepts mail.
func ValidEmail(ctx context.Context, mx MXChecker, email string, rejectDisposable bool) bool {
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email {
		return false
	}
	at := strings.LastIndexByte(email, '@')
	domain := strings.ToLower(email[at+1:])
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	if rejectDisposable && Disposable(domain) {
		return false
	}
	return mx.AcceptsMail(ctx, domain)
}

// disposable is a short list of well-known throwaway-mail domains.
var disposable = map[string]bool{
	"mailinator.com": true, "guerrillamail.com": true, "guerrillamail.net": true, "10minutemail.com": true,
	"tempmail.com": true, "temp-mail.org": true, "yopmail.com": true, "trashmail.com": true, "sharklasers.com": true,
	"getnada.com": true, "dispostable.com": true, "maildrop.cc": true, "throwawaymail.com": true, "fakeinbox.com": true,
	"mailnesia.com": true, "mintemail.com": true, "mohmal.com": true, "emailondeck.com": true, "tempail.com": true,
}

// Disposable reports whether domain is a known disposable-mail domain.
func Disposable(domain string) bool { return disposable[strings.ToLower(domain)] }

// DNSChecker looks up a domain's MX records (or, without any, an A/AAAA record: RFC 5321 implicit
// MX) with a timeout, caching answers. A null MX ("." — RFC 7505) means the domain takes no mail.
// When DNS itself fails (timeout, server error) the domain gets the benefit of the doubt, and that
// answer is cached only briefly.
type DNSChecker struct {
	Resolver *net.Resolver
	Timeout  time.Duration // per lookup; default 3 s
	TTL      time.Duration // cache; default 6 h

	mu    sync.Mutex
	cache map[string]mxEntry
}

type mxEntry struct {
	ok  bool
	exp time.Time
}

func (d *DNSChecker) AcceptsMail(ctx context.Context, domain string) bool {
	now := time.Now()
	d.mu.Lock()
	if e, ok := d.cache[domain]; ok && now.Before(e.exp) {
		d.mu.Unlock()
		return e.ok
	}
	d.mu.Unlock()
	timeout, ttl := d.Timeout, d.TTL
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	if ttl == 0 {
		ttl = 6 * time.Hour
	}
	r := d.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ok, sure := lookupMail(ctx, r, domain)
	if !sure {
		ttl = time.Minute
	}
	d.mu.Lock()
	if d.cache == nil || len(d.cache) > 100_000 {
		d.cache = map[string]mxEntry{}
	}
	d.cache[domain] = mxEntry{ok: ok, exp: now.Add(ttl)}
	d.mu.Unlock()
	return ok
}

// lookupMail answers whether domain accepts mail; sure is false when DNS failed rather than answered.
func lookupMail(ctx context.Context, r *net.Resolver, domain string) (ok, sure bool) {
	mxs, err := r.LookupMX(ctx, domain)
	if err == nil && len(mxs) > 0 {
		for _, m := range mxs {
			if m.Host != "." && m.Host != "" {
				return true, true
			}
		}
		return false, true // null MX
	}
	if err != nil && !notFound(err) {
		return true, false
	}
	addrs, err := r.LookupHost(ctx, domain)
	if err != nil {
		if notFound(err) {
			return false, true
		}
		return true, false
	}
	return len(addrs) > 0, true
}

func notFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}
