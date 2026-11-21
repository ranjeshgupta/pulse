// Package safehttp builds HTTP clients for URLs that users type in (journey webhooks), which must
// not reach the server's own network: loopback, private ranges, link-local (cloud metadata at
// 169.254.169.254), and so on. The check runs on the resolved address at connect time, so DNS
// tricks (a public name that resolves to 10.0.0.5, or rebinding between lookups) don't get through.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is returned when the destination address is not public.
var ErrBlocked = errors.New("destination address is not allowed")

// Allowed reports whether ip is a public unicast address. IPv6 forms that carry an IPv4 address
// (NAT64 64:ff9b::/96 and 64:ff9b:1::/48, 6to4 2002::/16, IPv4-compatible ::a.b.c.d and
// IPv4-mapped ::ffff:a.b.c.d) are judged by the IPv4 address inside.
func Allowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := embeddedIPv4(ip); v4 != nil {
		return allowedOne(v4)
	}
	return allowedOne(ip)
}

func allowedOne(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() || ip.Equal(net.IPv4bcast) {
		return false
	}
	for _, n := range denied {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// denied are the ranges, beyond the ones net.IP classifies, that aren't public unicast.
var denied = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",     // "this network"
		"100.64.0.0/10", // carrier-grade NAT
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
		"240.0.0.0/4",   // reserved
		"fec0::/10",     // site-local (deprecated)
	} {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}()

var (
	_, nat64, _      = net.ParseCIDR("64:ff9b::/96")
	_, nat64local, _ = net.ParseCIDR("64:ff9b:1::/48")
	_, sixToFour, _  = net.ParseCIDR("2002::/16")
)

// embeddedIPv4 returns the IPv4 address an IPv6 address carries, or nil.
func embeddedIPv4(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4 // IPv4, or IPv4-mapped
	}
	ip = ip.To16()
	if ip == nil {
		return nil
	}
	switch {
	case nat64.Contains(ip), nat64local.Contains(ip):
		return net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
	case sixToFour.Contains(ip):
		return net.IPv4(ip[2], ip[3], ip[4], ip[5]).To4()
	}
	// IPv4-compatible ::a.b.c.d (deprecated, but some stacks still route it); :: and ::1 land in
	// 0.0.0.0/8, which is refused too.
	for _, b := range ip[:12] {
		if b != 0 {
			return nil
		}
	}
	return net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
}

// Dialer returns a TCP dialer that refuses non-public destinations (checked on the resolved
// address at connect time), unless allowPrivate is set. For protocols other than HTTP (IMAP).
func Dialer(allowPrivate bool) *net.Dialer {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !Allowed(ip) {
				return fmt.Errorf("%w: %s", ErrBlocked, host)
			}
			return nil
		}
	}
	return dialer
}

// Client returns a client that refuses non-public destinations, unless allowPrivate is set (local
// development, where webhook receivers run on localhost).
func Client(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := Dialer(allowPrivate)
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               nil, // a proxy would hide the real destination from the check
			DialContext:         func(ctx context.Context, n, a string) (net.Conn, error) { return dialer.DialContext(ctx, n, a) },
			TLSHandshakeTimeout: 5 * time.Second,
			MaxIdleConns:        50,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil // each redirect dials again, through the same check
		},
	}
}

// SameOrigin returns a copy of c that follows redirects only within the request's own scheme and
// host. Requests that carry credentials (custom auth headers, a login body with a password) must
// never be re-sent to another origin: Go drops Authorization on such a redirect, but not custom
// headers, and a 307/308 sends the body again.
func SameOrigin(c *http.Client) *http.Client {
	cp := *c
	prev := c.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		first := via[0].URL
		if !strings.EqualFold(req.URL.Scheme, first.Scheme) || !strings.EqualFold(req.URL.Host, first.Host) {
			return fmt.Errorf("refused a redirect from %s to another site (%s)", first.Host, req.URL.Host)
		}
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	}
	return &cp
}
