package safehttp

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllowed(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.16.0.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false, "fe80::1": false, "fd00::1": false,
		"0.1.2.3": false, "192.0.0.8": false, "198.18.0.1": false, "198.19.255.1": false, "240.0.0.1": false,
		"255.255.255.255": false, "fec0::1": false, "::": false,
		// IPv6 forms carrying an IPv4 address are judged by that address.
		"64:ff9b::7f00:1": false, "64:ff9b::a9fe:a9fe": false, "64:ff9b::808:808": true,
		"64:ff9b:1::a00:1": false, "2002:7f00:1::": false, "2002:a9fe:a9fe::1": false, "2002:808:808::1": true,
		"::127.0.0.1": false, "::10.0.0.1": false, "::ffff:127.0.0.1": false, "::ffff:169.254.169.254": false,
		"::8.8.8.8": true, "::ffff:8.8.8.8": true,
	} {
		if got := Allowed(net.ParseIP(ip)); got != want {
			t.Errorf("Allowed(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestClientBlocksLocalTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	if _, err := Client(2*time.Second, false).Get(srv.URL); !errors.Is(err, ErrBlocked) {
		t.Errorf("request to %s: err = %v, want ErrBlocked", srv.URL, err)
	}
	resp, err := Client(2*time.Second, true).Get(srv.URL)
	if err != nil || resp.StatusCode != 204 {
		t.Errorf("allowPrivate: %v %v", resp, err)
	}
}
