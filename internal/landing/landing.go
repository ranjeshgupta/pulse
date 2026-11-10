// Package landing validates and renders landing pages.
//
// A page is the workspace's own HTML (from the drag-and-drop builder or written by hand). Editors
// are trusted with their markup, but nothing they write runs as script: <script> elements and
// inline event handlers are removed when the page is rendered, and the page is served under a CSP
// whose script-src is a per-response nonce carried only by the two scripts Pulse adds (the web SDK
// and its landing-page bootstrap, both from Pulse's own origin).
//
// Forms are embedded with {form:KEY} (or an element with data-pulse-form="KEY"); the web SDK
// renders them, so a submission merges the visitor's page views into the contact.
package landing

import (
	"unicode"

	"errors"
	"fmt"
	xhtml "golang.org/x/net/html"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pulse/internal/model"
	"pulse/internal/schedule"
)

const (
	Draft     = "draft"
	Published = "published"

	MaxHTML = 2 << 20
)

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,78}[a-z0-9])?$`)

// Validate checks a page before it is saved.
func Validate(p model.LandingPage) error {
	var errs []error
	if !slugRe.MatchString(p.Slug) {
		errs = append(errs, errors.New("the address must be 1-80 lowercase letters, digits and dashes (e.g. spring-sale)"))
	}
	if strings.TrimSpace(p.Title) == "" {
		errs = append(errs, errors.New("a title is required"))
	} else if len(p.Title) > 200 {
		errs = append(errs, errors.New("the title is longer than 200 characters"))
	}
	if len(p.MetaDescription) > 320 {
		errs = append(errs, errors.New("the meta description is longer than 320 characters"))
	}
	if strings.TrimSpace(p.HTML) == "" {
		errs = append(errs, errors.New("the page has no content"))
	} else if len(p.HTML) > MaxHTML {
		errs = append(errs, errors.New("the page is larger than 2 MB"))
	}
	if p.Status != Draft && p.Status != Published {
		errs = append(errs, errors.New("status must be draft or published"))
	}
	if err := (schedule.Window{Start: p.StartAt, End: p.EndAt}).Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Live reports whether a page is served now: published and inside its schedule.
func Live(p model.LandingPage, now time.Time) bool {
	return p.Status == Published && (schedule.Window{Start: p.StartAt, End: p.EndAt}).Active(now)
}

var (
	formToken = regexp.MustCompile(`\{form:([A-Za-z0-9_-]{1,40})\}`)
	formAttr  = regexp.MustCompile(`(?i)\bdata-pulse-form\s*=\s*["']([A-Za-z0-9_-]{1,40})["']`)
)

// FormKeys lists the form keys a page embeds, in order of first use.
func FormKeys(doc string) []string {
	var keys []string
	seen := map[string]bool{}
	for _, re := range []*regexp.Regexp{formToken, formAttr} {
		for _, m := range re.FindAllStringSubmatch(doc, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				keys = append(keys, m[1])
			}
		}
	}
	return keys
}

// Sanitize removes what may not run or redirect: scripts (HTML and SVG), event handlers,
// javascript:/vbscript:/data: URLs, srcdoc, <base>, <meta http-equiv>, <object> and <embed>. It
// parses the page with the same HTML5 algorithm browsers use and serializes the tree, so there is
// no parser differential to slip markup past (the page's CSP blocks scripts anyway).
func Sanitize(doc string) string {
	root, err := xhtml.Parse(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	clean(root)
	var b strings.Builder
	if err := xhtml.Render(&b, root); err != nil {
		return ""
	}
	return b.String()
}

var dropElems = map[string]bool{"script": true, "base": true, "object": true, "embed": true, "applet": true, "frameset": true}

var urlAttrs = map[string]bool{"href": true, "src": true, "action": true, "formaction": true, "xlink:href": true,
	"poster": true, "data": true, "background": true, "cite": true, "ping": true}

func clean(n *xhtml.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == xhtml.ElementNode {
			name := strings.ToLower(c.Data)
			if dropElems[name] || name == "meta" && hasAttr(c, "http-equiv") {
				n.RemoveChild(c)
				c = next
				continue
			}
			attrs := c.Attr[:0]
			for _, a := range c.Attr {
				k := strings.ToLower(a.Key)
				if a.Namespace != "" {
					k = strings.ToLower(a.Namespace) + ":" + k
				}
				switch {
				case strings.HasPrefix(k, "on"), k == "srcdoc":
					continue
				case urlAttrs[k] && unsafeURL(a.Val):
					a.Val = "#"
				}
				attrs = append(attrs, a)
			}
			c.Attr = attrs
		}
		clean(c)
		c = next
	}
}

func hasAttr(n *xhtml.Node, key string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

// unsafeURL reports whether a URL would run code: javascript:, vbscript: or data: (other than
// images), ignoring the whitespace and control characters browsers ignore.
func unsafeURL(v string) bool {
	var b strings.Builder
	for _, r := range v {
		if r > ' ' && r != 0x7f {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	u := b.String()
	return strings.HasPrefix(u, "javascript:") || strings.HasPrefix(u, "vbscript:") ||
		strings.HasPrefix(u, "data:") && !strings.HasPrefix(u, "data:image/") || strings.HasPrefix(u, "data:image/svg")
}

// Page is what Render needs besides the page itself.
type Page struct {
	SiteKey string // the workspace's web SDK key
	Nonce   string // the response's CSP script nonce
	// Preview renders without scripts; forms are drawn as static, disabled previews from Forms.
	Preview bool
	Forms   map[string]model.Form
}

// Render produces the full HTML document for a page.
func Render(p model.LandingPage, o Page) string {
	body := Sanitize(p.HTML)
	body = formToken.ReplaceAllStringFunc(body, func(m string) string {
		key := formToken.FindStringSubmatch(m)[1]
		return `<div data-pulse-form="` + key + `"></div>`
	})
	if o.Preview {
		body = previewForms(body, o.Forms)
	}
	head := `<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(p.Title) + `</title>`
	if p.MetaDescription != "" {
		head += `<meta name="description" content="` + html.EscapeString(p.MetaDescription) + `">`
	}
	if o.Preview {
		// The app shows previews in a sandboxed srcdoc iframe, where response headers don't apply.
		head += `<meta http-equiv="Content-Security-Policy" content="` + html.EscapeString(previewMetaCSP) + `"><meta name="robots" content="noindex">`
	}
	scripts := ""
	if !o.Preview {
		n := html.EscapeString(o.Nonce)
		scripts = `<script nonce="` + n + `" src="/sdk/pulse.js"></script>` +
			`<script nonce="` + n + `" src="/sdk/lp.js" data-site-key="` + html.EscapeString(o.SiteKey) +
			`" data-page-id="` + strconv.FormatInt(p.ID, 10) + `"></script>`
	}
	return wrap(body, head, scripts)
}

var (
	reHTMLOpen = regexp.MustCompile(`(?i)<html\b[^>]*>`)
	reHeadOpen = regexp.MustCompile(`(?i)<head\b[^>]*>`)
	reBodyOpen = regexp.MustCompile(`(?i)<body\b[^>]*>`)
	reTitle    = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title\s*>`)
	reMetaDesc = regexp.MustCompile(`(?i)<meta\b[^>]*name\s*=\s*["']?(description|viewport)["']?[^>]*>`)
	reCharset  = regexp.MustCompile(`(?i)<meta\b[^>]*charset[^>]*>`)
)

// wrap puts a fragment into a document, or merges head and scripts into a full document (the
// page's own title, description, viewport and charset give way to the page settings).
func wrap(body, head, scripts string) string {
	if !reBodyOpen.MatchString(body) {
		return "<!doctype html><html lang=\"en\"><head>" + head + "</head><body>" + body + scripts + "</body></html>"
	}
	doc := reTitle.ReplaceAllString(body, "")
	doc = reMetaDesc.ReplaceAllString(doc, "")
	doc = reCharset.ReplaceAllString(doc, "")
	if loc := reHeadOpen.FindStringIndex(doc); loc != nil {
		doc = doc[:loc[1]] + head + doc[loc[1]:]
	} else {
		loc := reBodyOpen.FindStringIndex(doc)
		doc = doc[:loc[0]] + "<head>" + head + "</head>" + doc[loc[0]:]
	}
	if i := strings.LastIndex(strings.ToLower(doc), "</body>"); i >= 0 {
		doc = doc[:i] + scripts + doc[i:]
	} else {
		doc += scripts
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(doc)), "<!doctype") {
		if !reHTMLOpen.MatchString(doc) {
			doc = "<html lang=\"en\">" + doc + "</html>"
		}
		doc = "<!doctype html>" + doc
	}
	return doc
}

var formDiv = regexp.MustCompile(`(?is)<(div|section|span)\b([^>]*?)\bdata-pulse-form\s*=\s*["']([A-Za-z0-9_-]{1,40})["']([^>]*)>\s*</(div|section|span)>`)

// previewForms draws each embedded form as a disabled, static copy (the live page renders it with
// the SDK). Unknown or draft forms show a notice instead.
func previewForms(doc string, forms map[string]model.Form) string {
	return formDiv.ReplaceAllStringFunc(doc, func(m string) string {
		key := formDiv.FindStringSubmatch(m)[3]
		f, ok := forms[key]
		box := `<div style="border:1px dashed #c3cbd6;border-radius:8px;padding:16px;margin:12px 0;font:15px/1.5 system-ui,sans-serif;color:#152238">`
		if !ok {
			return box + `<strong>Form ` + html.EscapeString(key) + `</strong>: not found in this workspace.</div>`
		}
		var b strings.Builder
		b.WriteString(box)
		if f.Status != "active" {
			b.WriteString(`<p style="margin:0 0 8px;color:#9a3412">This form is a draft: visitors won't see it until it's active.</p>`)
		}
		if f.Settings.Description != "" {
			b.WriteString(`<p style="margin:0 0 12px;color:#5b6474">` + html.EscapeString(f.Settings.Description) + `</p>`)
		}
		for _, fd := range f.Fields {
			if fd.Type == "hidden" {
				continue
			}
			label := html.EscapeString(fd.Label)
			if fd.Required {
				label += " *"
			}
			if fd.Type == "checkbox" {
				fmt.Fprintf(&b, `<label style="display:flex;gap:8px;margin:0 0 12px"><input type="checkbox" disabled> %s</label>`, label)
				continue
			}
			fmt.Fprintf(&b, `<label style="display:block;margin:0 0 12px;font-weight:600;font-size:14px">%s<span style="display:block;margin-top:6px;height:38px;border:1px solid #c3cbd6;border-radius:6px;background:#fff"></span></label>`, label)
		}
		if f.Settings.ConsentText != "" {
			fmt.Fprintf(&b, `<label style="display:flex;gap:8px;margin:0 0 12px"><input type="checkbox" disabled> %s</label>`, html.EscapeString(f.Settings.ConsentText))
		}
		submit := f.Settings.SubmitText
		if submit == "" {
			submit = "Submit"
		}
		fmt.Fprintf(&b, `<span style="display:block;text-align:center;background:#2747d6;color:#fff;border-radius:6px;padding:10px;font-weight:600">%s</span></div>`, html.EscapeString(submit))
		return b.String()
	})
}

// CSP is the Content-Security-Policy for a live page: only the nonced Pulse scripts run; images,
// styles, fonts, media and frames may come from the page's own choice of https sources; the SDK
// talks to Pulse's origin only; any site may frame the page.
func CSP(nonce string) string {
	return "default-src 'none'; script-src 'nonce-" + nonce + "'; style-src 'self' 'unsafe-inline' https:; img-src 'self' https: data:; " +
		"font-src 'self' https: data:; media-src 'self' https:; frame-src 'self' https:; connect-src 'self'; form-action 'self'; " +
		"base-uri 'none'; object-src 'none'; frame-ancestors *"
}

// PreviewCSP runs nothing at all and isolates the document (it is shown inside the app).
const PreviewCSP = "default-src 'none'; style-src 'unsafe-inline' https: http:; img-src https: http: data:; font-src https: http: data:; " +
	"media-src https: http:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox"

// previewMetaCSP is the part of PreviewCSP a <meta> element can carry.
const previewMetaCSP = "default-src 'none'; style-src 'unsafe-inline' https: http:; img-src https: http: data:; " +
	"font-src https: http: data:; media-src https: http:; base-uri 'none'; form-action 'none'"
