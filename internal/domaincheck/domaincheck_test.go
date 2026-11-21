package domaincheck

import (
	"context"
	"errors"
	"net"
	"testing"
)

type fake map[string][]string

func (f fake) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := f[name]; ok {
		return v, nil
	}
	return nil, errors.New("no such host")
}
func (f fake) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if _, ok := f["mx:"+name]; ok {
		return []*net.MX{{Host: "mx.example.com."}}, nil
	}
	return nil, errors.New("no such host")
}

func status(r Result, key string) string {
	for _, c := range r.Checks {
		if c.Key == key {
			return c.Status
		}
	}
	return ""
}

func TestDomainCheck(t *testing.T) {
	good := fake{
		"acme.test":               {"v=spf1 include:amazonses.com ~all", "google-site-verification=x"},
		"s1._domainkey.acme.test": {"v=DKIM1; k=rsa; p=MIGf"},
		"_dmarc.acme.test":        {"v=DMARC1; p=quarantine; rua=mailto:d@acme.test"},
		"default._bimi.acme.test": {"v=BIMI1; l=https://acme.test/logo.svg"},
		"mx:acme.test":            nil,
	}
	r, err := Run(context.Background(), good, "Acme.test.", "ses", []string{"s1"})
	if err != nil || !r.BulkReady || status(r, "spf") != OK || status(r, "dkim") != OK || status(r, "dmarc") != OK || status(r, "bimi") != OK || status(r, "mx") != OK {
		t.Fatalf("good domain: %+v %v", r, err)
	}
	bad := fake{"bad.test": {"v=spf1 include:a ~all", "v=spf1 include:b ~all"}}
	r, _ = Run(context.Background(), bad, "bad.test", "", nil)
	if r.BulkReady || status(r, "spf") != Fail || status(r, "dkim") != Fail || status(r, "dmarc") != Fail || status(r, "mx") != Warn {
		t.Errorf("bad domain: %+v", r)
	}
	mon := fake{"m.test": {"v=spf1 include:sendgrid.net -all"}, "s2._domainkey.m.test": {"k=rsa; p=abc"}, "_dmarc.m.test": {"v=DMARC1;p=none"}}
	r, _ = Run(context.Background(), mon, "m.test", "sendgrid", nil)
	if !r.BulkReady || status(r, "dmarc") != Warn || status(r, "bimi") != Warn {
		t.Errorf("monitoring DMARC: %+v", r)
	}
	if _, err := Run(context.Background(), good, "not a domain", "", nil); err == nil {
		t.Error("bad domain name passed")
	}
	wrongInclude := fake{"w.test": {"v=spf1 include:mailgun.org ~all"}}
	r, _ = Run(context.Background(), wrongInclude, "w.test", "ses", nil)
	if status(r, "spf") != Warn {
		t.Errorf("SPF without the service's include: %+v", r.Checks[0])
	}
}
