// Package domaincheck checks a sending domain's DNS for email deliverability (O6): SPF, DKIM,
// DMARC, BIMI and MX, measured against the Gmail and Yahoo bulk-sender rules (2024): SPF and DKIM
// set up, a DMARC record (at least p=none), and for big senders DMARC alignment. Each check says
// what was found and, when it isn't right, what to put in DNS.
package domaincheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

// Resolver is the DNS lookups the checks need (net.Resolver in production).
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

// Status of a check.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
)

// Check is one finding.
type Check struct {
	Key    string `json:"key"` // spf | dkim | dmarc | bimi | mx
	Title  string `json:"title"`
	Status string `json:"status"`
	Found  string `json:"found,omitempty"` // the record(s) seen
	Note   string `json:"note"`            // what it means
	Fix    string `json:"fix,omitempty"`   // what to add or change
}

// Result is a domain's report.
type Result struct {
	Domain string  `json:"domain"`
	Checks []Check `json:"checks"`
	// BulkReady: meets the Gmail / Yahoo rules for sending more than 5,000 emails a day.
	BulkReady bool   `json:"bulk_ready"`
	Summary   string `json:"summary"`
}

// Service is the sending service's DNS needs, by Pulse's SMTP service names (providers.DetectService).
type Service struct {
	SPFInclude string   // e.g. amazonses.com
	Selectors  []string // DKIM selectors to try
}

// Services are the common ones; unknown services check SPF and DKIM by the selectors given.
var Services = map[string]Service{
	"ses":       {SPFInclude: "amazonses.com"},
	"sendgrid":  {SPFInclude: "sendgrid.net", Selectors: []string{"s1", "s2"}},
	"mailgun":   {SPFInclude: "mailgun.org", Selectors: []string{"mx", "smtp", "k1"}},
	"postmark":  {SPFInclude: "spf.mtasv.net", Selectors: []string{"pm"}},
	"mailjet":   {SPFInclude: "spf.mailjet.com", Selectors: []string{"mailjet"}},
	"gmail":     {SPFInclude: "_spf.google.com", Selectors: []string{"google"}},
	"brevo":     {SPFInclude: "spf.brevo.com", Selectors: []string{"mail"}},
	"sparkpost": {SPFInclude: "sparkpostmail.com", Selectors: []string{"scph0124", "scph1123"}},
}

// commonSelectors are tried when the service is unknown.
var commonSelectors = []string{"default", "google", "selector1", "selector2", "s1", "s2", "k1", "mail", "dkim", "smtp", "pm", "mx"}

var domainRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidDomain reports whether d looks like a domain name.
func ValidDomain(d string) bool { return len(d) <= 253 && domainRe.MatchString(d) }

// Run checks domain for a sending service (its key in Services, or "") and extra DKIM selectors.
func Run(ctx context.Context, r Resolver, domain, service string, selectors []string) (Result, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if !ValidDomain(domain) {
		return Result{}, errors.New("enter a domain like example.com")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	svc := Services[service]
	res := Result{Domain: domain}
	spf := checkSPF(ctx, r, domain, svc)
	dkim := checkDKIM(ctx, r, domain, append(append([]string{}, selectors...), svc.Selectors...))
	dmarc := checkDMARC(ctx, r, domain)
	res.Checks = []Check{spf, dkim, dmarc, checkBIMI(ctx, r, domain, dmarc), checkMX(ctx, r, domain)}
	res.BulkReady = spf.Status == OK && dkim.Status == OK && dmarc.Status != Fail
	switch {
	case res.BulkReady:
		res.Summary = "Ready for Gmail and Yahoo, even for large sends."
	case spf.Status == Fail && dkim.Status == Fail:
		res.Summary = "Gmail and Yahoo will reject or spam-folder these emails: set up SPF and DKIM first."
	default:
		res.Summary = "Some fixes needed before large sends (over 5,000 a day to Gmail or Yahoo)."
	}
	return res, nil
}

func txt(ctx context.Context, r Resolver, name string) []string {
	recs, err := r.LookupTXT(ctx, name)
	if err != nil {
		return nil
	}
	return recs
}

func withPrefix(recs []string, prefix string) []string {
	var out []string
	for _, t := range recs {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), prefix) {
			out = append(out, strings.TrimSpace(t))
		}
	}
	return out
}

func checkSPF(ctx context.Context, r Resolver, domain string, svc Service) Check {
	c := Check{Key: "spf", Title: "SPF: which servers may send for the domain"}
	recs := withPrefix(txt(ctx, r, domain), "v=spf1")
	include := svc.SPFInclude
	suggest := "v=spf1 include:" + firstNonEmpty(include, "<your sending service>") + " ~all"
	switch {
	case len(recs) == 0:
		c.Status, c.Note = Fail, "No SPF record."
		c.Fix = "Add a TXT record on " + domain + ": " + suggest
	case len(recs) > 1:
		c.Status, c.Found, c.Note = Fail, strings.Join(recs, " | "), "More than one SPF record: receivers treat that as no SPF at all."
		c.Fix = "Merge them into one TXT record, e.g. " + suggest
	default:
		c.Found = recs[0]
		low := strings.ToLower(recs[0])
		switch {
		case include != "" && !strings.Contains(low, "include:"+include):
			c.Status, c.Note = Warn, "SPF doesn't include your sending service."
			c.Fix = "Add include:" + include + " before the ~all / -all in the record."
		case strings.HasSuffix(low, "+all"):
			c.Status, c.Note, c.Fix = Fail, "+all lets anyone send as you.", "End the record with ~all or -all."
		case !strings.HasSuffix(low, "~all") && !strings.HasSuffix(low, "-all"):
			c.Status, c.Note, c.Fix = Warn, "The record doesn't end with ~all or -all.", "End it with ~all (soft fail) or -all."
		default:
			c.Status, c.Note = OK, "SPF is set up."
		}
	}
	return c
}

func checkDKIM(ctx context.Context, r Resolver, domain string, selectors []string) Check {
	c := Check{Key: "dkim", Title: "DKIM: messages signed for the domain"}
	tried := map[string]bool{}
	for _, sel := range append(selectors, commonSelectors...) {
		sel = strings.TrimSpace(strings.ToLower(sel))
		if sel == "" || tried[sel] {
			continue
		}
		tried[sel] = true
		name := sel + "._domainkey." + domain
		for _, t := range txt(ctx, r, name) {
			if strings.Contains(strings.ToLower(t), "p=") && (strings.Contains(strings.ToLower(t), "v=dkim1") || strings.Contains(t, "k=rsa")) {
				c.Status, c.Found = OK, name
				c.Note = "Found a DKIM key at " + name + "."
				if strings.Contains(strings.ReplaceAll(strings.ToLower(t), " ", ""), "p=;") {
					c.Status, c.Note = Fail, "The DKIM key at "+name+" is empty (revoked)."
				}
				return c
			}
		}
	}
	c.Status = Fail
	c.Note = "No DKIM key found at the usual selectors."
	c.Fix = "Turn on DKIM (domain authentication) in your sending service and add the records it gives you. If you already did, enter its selector to check it."
	return c
}

var dmarcPolicy = regexp.MustCompile(`(?i)(?:^|;)\s*p\s*=\s*(none|quarantine|reject)`)

func checkDMARC(ctx context.Context, r Resolver, domain string) Check {
	c := Check{Key: "dmarc", Title: "DMARC: what receivers do with mail that fails SPF and DKIM"}
	recs := withPrefix(txt(ctx, r, "_dmarc."+domain), "v=dmarc1")
	suggest := "v=DMARC1; p=none; rua=mailto:dmarc@" + domain
	switch {
	case len(recs) == 0:
		c.Status, c.Note = Fail, "No DMARC record. Gmail and Yahoo require one for bulk senders."
		c.Fix = "Add a TXT record on _dmarc." + domain + ": " + suggest + " (start with p=none, move to quarantine when reports look clean)"
	case len(recs) > 1:
		c.Status, c.Found, c.Note, c.Fix = Fail, strings.Join(recs, " | "), "More than one DMARC record.", "Keep one TXT record on _dmarc."+domain+"."
	default:
		c.Found = recs[0]
		m := dmarcPolicy.FindStringSubmatch(recs[0])
		switch {
		case m == nil:
			c.Status, c.Note, c.Fix = Fail, "The DMARC record has no policy (p=).", "Use: "+suggest
		case strings.EqualFold(m[1], "none"):
			c.Status, c.Note = Warn, "DMARC is in monitoring mode (p=none): enough for Gmail and Yahoo, but spoofed mail still gets through."
			c.Fix = "When its reports show your own mail passing, change to p=quarantine."
		default:
			c.Status, c.Note = OK, fmt.Sprintf("DMARC enforces p=%s.", strings.ToLower(m[1]))
		}
	}
	return c
}

func checkBIMI(ctx context.Context, r Resolver, domain string, dmarc Check) Check {
	c := Check{Key: "bimi", Title: "BIMI: your logo next to your emails (optional)"}
	recs := withPrefix(txt(ctx, r, "default._bimi."+domain), "v=bimi1")
	if len(recs) == 0 {
		c.Status, c.Note = Warn, "No BIMI record. Optional: shows your logo in Gmail, Yahoo and Apple Mail."
		c.Fix = "Needs DMARC at p=quarantine or reject, an SVG logo and usually a mark certificate (VMC); then a TXT record on default._bimi." + domain + "."
		return c
	}
	c.Found = recs[0]
	if dmarc.Status != OK {
		c.Status, c.Note = Warn, "BIMI is set, but logos only show when DMARC enforces quarantine or reject."
		return c
	}
	c.Status, c.Note = OK, "BIMI is set up."
	return c
}

func checkMX(ctx context.Context, r Resolver, domain string) Check {
	c := Check{Key: "mx", Title: "MX: the domain can receive mail (replies, bounces)"}
	mx, err := r.LookupMX(ctx, domain)
	if err != nil || len(mx) == 0 {
		c.Status, c.Note = Warn, "No MX record: replies and some bounces to this domain are lost, and some receivers distrust domains without one."
		c.Fix = "Point MX at the mailbox service you use for this domain."
		return c
	}
	hosts := make([]string, 0, len(mx))
	for _, m := range mx {
		hosts = append(hosts, strings.TrimSuffix(m.Host, "."))
	}
	c.Status, c.Found, c.Note = OK, strings.Join(hosts, ", "), "The domain receives mail."
	return c
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
