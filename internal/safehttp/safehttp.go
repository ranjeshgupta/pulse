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

// Allowed reports whether ip is a public unicast address.
func Allowed(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() ||
		cgnat.Contains(ip) || ip.Equal(net.IPv4bcast))
}

var _, cgnat, _ = net.ParseCIDR("100.64.0.0/10")

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
