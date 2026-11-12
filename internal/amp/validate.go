// Package amp handles the AMP part of an email (text/x-amp-html): a pragmatic validator for AMP for
// Email documents, the visual block builder's generator, the per-recipient post-back links forms
// and polls use, and a static preview. Full validation is Google's job (the AMP validator and
// Gmail's sender registration); this package catches the mistakes that make clients drop the part.
package amp

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// MaxBytes is the largest AMP part email clients render (Gmail: 200 KB).
const MaxBytes = 200 * 1024

// maxCSS is the largest <style amp-custom> AMP for Email allows.
const maxCSS = 75_000

// Problem is one validation finding. Line is 1-based (0: the whole document).
type Problem struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
	// Warning problems don't stop a save (unused scripts, a missing preheader…).
	Warning bool `json:"warning,omitempty"`
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	}
	return p.Message
}

// Components are the AMP for Email extensions Pulse accepts, with the script version each needs.
var Components = map[string]string{
	"amp-accordion": "0.1", "amp-anim": "0.1", "amp-autocomplete": "0.1", "amp-bind": "0.1",
	"amp-carousel": "0.2", "amp-date-picker": "0.1", "amp-fit-text": "0.1", "amp-form": "0.1",
	"amp-image-lightbox": "0.1", "amp-inputmask": "0.1", "amp-lightbox": "0.1", "amp-list": "0.1",
	"amp-selector": "0.1", "amp-sidebar": "0.1", "amp-timeago": "0.1", "amp-mustache": "0.2",
}

// builtIn elements need no script; amp-state comes with amp-bind.
var builtIn = map[string]string{"amp-img": "", "amp-layout": "", "amp-state": "amp-bind"}

// disallowed are HTML elements AMP for Email refuses.
var disallowed = map[string]string{
	"img": "use <amp-img> with width and height", "iframe": "embedded frames aren't allowed", "frame": "frames aren't allowed",
	"frameset": "frames aren't allowed", "object": "plugins aren't allowed", "embed": "plugins aren't allowed",
	"applet": "plugins aren't allowed", "video": "video isn't allowed in AMP email", "audio": "audio isn't allowed in AMP email",
	"base": "<base> isn't allowed", "link": "external stylesheets aren't allowed: put CSS in <style amp-custom>",
	"param": "plugins aren't allowed", "picture": "use <amp-img>", "svg": "inline SVG isn't allowed in AMP email",
	"canvas": "<canvas> needs JavaScript, which isn't allowed", "dialog": "<dialog> isn't allowed; use <amp-lightbox>",
}

var (
	componentScript = regexp.MustCompile(`^https://cdn\.ampproject\.org/v0/(amp-[a-z-]+)-(\d+\.\d+)\.js$`)
	blockIDRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	cssImportant    = regexp.MustCompile(`(?i)!\s*important`)
	cssBad          = regexp.MustCompile(`(?i)@import|expression\s*\(|behavior\s*:|-moz-binding|javascript:`)
)

// Placeholders: a form posts to, and a list reads from, Pulse. Pulse replaces them per recipient
// with signed links when the email is sent (see Fill).
const (
	SubmitPrefix  = "pulse:submit/"
	ResultsPrefix = "pulse:results/"
)

// ValidBlockID reports whether id can name a form or poll block.
func ValidBlockID(id string) bool { return blockIDRe.MatchString(id) }

type validator struct {
	probs                                          []Problem
	line                                           int
	scripts                                        map[string]int // declared component -> line
	used                                           map[string]int // used component -> first line
	stack                                          []string
	sawHTML                                        bool
	sawCharset, sawRuntime, sawBoiler, sawViewport bool
	inHead                                         bool
	headKids                                       int
	styleKind                                      string // amp-custom | boilerplate | ""
	cssBytes                                       int
	customCSS                                      int
	blocks                                         map[string]int
}

func (v *validator) add(line int, format string, a ...any) {
	v.probs = append(v.probs, Problem{Line: line, Message: fmt.Sprintf(format, a...)})
}

func (v *validator) warn(line int, format string, a ...any) {
	v.probs = append(v.probs, Problem{Line: line, Message: fmt.Sprintf(format, a...), Warning: true})
}

// Validate checks an AMP for Email document as written (personalisation tags may be in it) and
// returns its problems, errors first by line. An empty document has none (no AMP part).
func Validate(src string) []Problem {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	v := &validator{scripts: map[string]int{}, used: map[string]int{}, blocks: map[string]int{}, line: 1}
	if len(src) > MaxBytes {
		v.add(0, "the AMP part is %d KB; email clients show at most 200 KB", (len(src)+1023)/1024)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(src)), "<!doctype html>") {
		v.add(1, "the document must start with <!doctype html>")
	}
	z := html.NewTokenizer(strings.NewReader(src))
	z.SetMaxBuf(0)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				v.add(v.line, "can't read the document: %v", z.Err())
			}
			break
		}
		raw := z.Raw()
		start := v.line
		tok := z.Token()
		v.token(tt, tok, start)
		v.line += bytes.Count(raw, []byte("\n"))
	}
	v.finish()
	sort.SliceStable(v.probs, func(i, j int) bool {
		if v.probs[i].Warning != v.probs[j].Warning {
			return !v.probs[i].Warning
		}
		return v.probs[i].Line < v.probs[j].Line
	})
	return v.probs
}

// Errors returns only the problems that stop a save.
func Errors(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if !p.Warning {
			out = append(out, p)
		}
	}
	return out
}

func attrs(t html.Token) map[string]string {
	m := make(map[string]string, len(t.Attr))
	for _, a := range t.Attr {
		m[strings.ToLower(a.Key)] = a.Val
	}
	return m
}

func has(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func (v *validator) token(tt html.TokenType, t html.Token, line int) {
	switch tt {
	case html.TextToken:
		if v.styleKind == "amp-custom" {
			v.css(t.Data, line)
		} else if len(v.stack) > 0 && v.stack[len(v.stack)-1] == "script" && strings.TrimSpace(t.Data) != "" {
			// JSON inside amp-state is data, not code.
			if len(v.stack) < 2 || v.stack[len(v.stack)-2] != "amp-state" {
				v.add(line, "custom JavaScript isn't allowed in AMP email")
			}
		}
	case html.StartTagToken, html.SelfClosingTagToken:
		v.start(t, line)
		if tt == html.StartTagToken && !voidElements[t.Data] {
			v.stack = append(v.stack, t.Data)
		}
	case html.EndTagToken:
		if t.Data == "style" {
			v.styleKind = ""
		}
		if t.Data == "head" {
			v.inHead = false
		}
		for i := len(v.stack) - 1; i >= 0; i-- {
			if v.stack[i] == t.Data {
				v.stack = v.stack[:i]
				break
			}
		}
	}
}

var voidElements = map[string]bool{"area": true, "br": true, "col": true, "hr": true, "img": true, "input": true,
	"meta": true, "source": true, "track": true, "wbr": true, "link": true, "base": true, "embed": true, "param": true}

func (v *validator) start(t html.Token, line int) {
	a := attrs(t)
	name := t.Data
	if v.inHead {
		v.headKids++
	}
	switch name {
	case "html":
		v.sawHTML = true
		if !has(a, "⚡4email") && !has(a, "amp4email") {
			v.add(line, `<html> needs the ⚡4email attribute: <html ⚡4email data-css-strict>`)
		}
	case "head":
		v.inHead = true
	case "meta":
		switch {
		case has(a, "charset"):
			v.sawCharset = true
			if !strings.EqualFold(a["charset"], "utf-8") {
				v.add(line, `the charset must be utf-8`)
			}
			if v.headKids != 1 {
				v.add(line, `<meta charset="utf-8"> must be the first element in <head>`)
			}
		case strings.EqualFold(a["name"], "viewport"):
			v.sawViewport = true
		default:
			if has(a, "http-equiv") {
				v.add(line, "<meta http-equiv> isn't allowed")
			}
		}
	case "script":
		v.script(a, line)
	case "style":
		switch {
		case has(a, "amp4email-boilerplate"):
			v.sawBoiler = true
			v.styleKind = "boilerplate"
		case has(a, "amp-custom"):
			v.customCSS++
			if v.customCSS > 1 {
				v.add(line, "only one <style amp-custom> is allowed")
			}
			if !v.inHead {
				v.add(line, "<style amp-custom> must be in <head>")
			}
			v.styleKind = "amp-custom"
		default:
			v.add(line, "styles go in one <style amp-custom> in <head>")
		}
	case "form":
		v.form(a, line)
	case "input":
		switch strings.ToLower(a["type"]) {
		case "file", "password", "image", "button":
			v.add(line, `<input type="%s"> isn't allowed in AMP email`, a["type"])
		}
	case "template":
		if a["type"] != "amp-mustache" {
			v.add(line, `<template> must be type="amp-mustache"`)
		} else {
			v.use("amp-mustache", line)
		}
	case "a":
		if href, ok := a["href"]; ok && !safeHref(href) {
			v.add(line, "links must be http(s), mailto: or tel: (found %q)", trunc(href))
		}
	}
	if why, bad := disallowed[name]; bad {
		v.add(line, "<%s>: %s", name, why)
	}
	if strings.HasPrefix(name, "amp-") {
		v.component(name, a, line)
	}
	for k, val := range a {
		switch {
		case strings.HasPrefix(k, "on") && k != "on":
			v.add(line, "<%s %s>: event handler attributes (custom JavaScript) aren't allowed; use on=\"…\" actions", name, k)
		case k == "style":
			if cssImportant.MatchString(val) {
				v.add(line, "<%s>: !important isn't allowed in AMP email", name)
			}
			v.cssBytes += len(val)
		case k == "class" || k == "id":
			if strings.Contains(val, "i-amphtml-") || strings.Contains(val, "-amp-") {
				v.add(line, "<%s>: names starting with i-amphtml- are reserved", name)
			}
		case strings.HasPrefix(k, "[") && strings.HasSuffix(k, "]"):
			v.use("amp-bind", line)
		}
		if strings.Contains(strings.ToLower(strings.TrimSpace(val)), "javascript:") {
			v.add(line, "<%s %s>: javascript: URLs aren't allowed", name, k)
		}
	}
}

func safeHref(h string) bool {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" || strings.HasPrefix(h, "#") || strings.HasPrefix(h, "{{") {
		return true
	}
	for _, p := range []string{"https://", "http://", "mailto:", "tel:"} {
		if strings.HasPrefix(h, p) {
			return true
		}
	}
	return false
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

func (v *validator) script(a map[string]string, line int) {
	src := a["src"]
	switch {
	case strings.EqualFold(a["type"], "application/json"):
		// amp-state data; checked by the body rule (must sit inside <amp-state>)
	case src == "https://cdn.ampproject.org/v0.js":
		v.sawRuntime = true
		if !has(a, "async") {
			v.add(line, "the AMP runtime script needs async")
		}
	case has(a, "custom-element") || has(a, "custom-template"):
		ce := a["custom-element"]
		if ce == "" {
			ce = a["custom-template"]
		}
		want, ok := Components[ce]
		if !ok {
			v.add(line, "%s isn't supported in AMP email", ce)
			return
		}
		m := componentScript.FindStringSubmatch(src)
		if m == nil || m[1] != ce {
			v.add(line, "%s must load from https://cdn.ampproject.org/v0/%s-%s.js", ce, ce, want)
			return
		}
		if !has(a, "async") {
			v.add(line, "the %s script needs async", ce)
		}
		v.scripts[ce] = line
	default:
		v.add(line, "custom JavaScript isn't allowed in AMP email: only the AMP runtime and AMP component scripts")
	}
}

func (v *validator) use(c string, line int) {
	if _, ok := v.used[c]; !ok {
		v.used[c] = line
	}
}

func (v *validator) component(name string, a map[string]string, line int) {
	if dep, ok := builtIn[name]; ok {
		if dep != "" {
			v.use(dep, line)
		}
	} else if _, ok := Components[name]; ok {
		v.use(name, line)
	} else {
		v.add(line, "<%s> isn't supported in AMP email", name)
		return
	}
	layout := a["layout"]
	sized := has(a, "width") && has(a, "height") || layout == "fill" || layout == "nodisplay" || layout == "container" ||
		layout == "flex-item" || (layout == "fixed-height" && has(a, "height")) || (layout == "responsive" && has(a, "width") && has(a, "height"))
	switch name {
	case "amp-img", "amp-anim":
		if !sized {
			v.add(line, "<%s> needs width and height (or a layout)", name)
		}
		if src := a["src"]; src != "" && !strings.HasPrefix(src, "https://") && !strings.HasPrefix(src, "{{") {
			v.add(line, "<%s> images must be https URLs", name)
		}
	case "amp-carousel":
		if !sized {
			v.add(line, "<amp-carousel> needs width and height (or layout=\"fixed-height\" with a height)")
		}
		if t := a["type"]; t != "" && t != "slides" && t != "carousel" {
			v.add(line, `<amp-carousel> type must be "slides" or "carousel"`)
		}
	case "amp-list":
		src := a["src"]
		switch {
		case strings.HasPrefix(src, ResultsPrefix):
			if !ValidBlockID(strings.TrimPrefix(src, ResultsPrefix)) {
				v.add(line, "<amp-list>: %q isn't a valid block name", strings.TrimPrefix(src, ResultsPrefix))
			}
		case strings.HasPrefix(src, "https://"), strings.HasPrefix(src, "amp-state:"):
		default:
			v.add(line, `<amp-list> src must be an https URL or "pulse:results/<block>"`)
		}
		v.use("amp-mustache", line) // lists render through a template
	}
}

func (v *validator) form(a map[string]string, line int) {
	v.use("amp-form", line)
	if has(a, "action") {
		v.add(line, `<form> must use action-xhr, not action`)
	}
	act := a["action-xhr"]
	switch {
	case act == "":
		v.add(line, `<form> needs action-xhr="pulse:submit/<block>" to post back to Pulse`)
	case strings.HasPrefix(act, SubmitPrefix):
		id := strings.TrimPrefix(act, SubmitPrefix)
		if !ValidBlockID(id) {
			v.add(line, "<form>: %q isn't a valid block name (lowercase letters, digits, - and _)", id)
		} else if prev, dup := v.blocks[id]; dup {
			v.add(line, "<form>: block %q is already used on line %d", id, prev)
		} else {
			v.blocks[id] = line
		}
	default:
		v.add(line, `<form> must post back to Pulse: action-xhr="pulse:submit/<block>" (found %q)`, trunc(act))
	}
	if m := strings.ToLower(a["method"]); m != "post" {
		v.add(line, `<form> must use method="post"`)
	}
}

func (v *validator) css(s string, line int) {
	v.cssBytes += len(s)
	for i, l := range strings.Split(s, "\n") {
		if cssImportant.MatchString(l) {
			v.add(line+i, "!important isn't allowed in AMP email CSS")
		}
		if cssBad.MatchString(l) {
			v.add(line+i, "this CSS isn't allowed in AMP email (@import, expression, behavior, javascript:)")
		}
	}
}

func (v *validator) finish() {
	if !v.sawHTML {
		v.add(1, "<html ⚡4email> is missing")
	}
	if !v.sawCharset {
		v.add(0, `<meta charset="utf-8"> is missing from <head>`)
	}
	if !v.sawRuntime {
		v.add(0, `the AMP runtime is missing: <script async src="https://cdn.ampproject.org/v0.js"></script>`)
	}
	if !v.sawBoiler {
		v.add(0, `the AMP boilerplate is missing: <style amp4email-boilerplate>body{visibility:hidden}</style>`)
	}
	if v.cssBytes > maxCSS {
		v.add(0, "CSS is %d bytes; AMP email allows %d (stylesheet and style attributes together)", v.cssBytes, maxCSS)
	}
	used := make([]string, 0, len(v.used))
	for c := range v.used {
		used = append(used, c)
	}
	sort.Strings(used)
	for _, c := range used {
		if _, ok := v.scripts[c]; !ok {
			v.add(v.used[c], `<%s> needs its script in <head>: <script async %s="%s" src="https://cdn.ampproject.org/v0/%s-%s.js"></script>`,
				c, scriptAttr(c), c, c, Components[c])
		}
	}
	for c, l := range v.scripts {
		if _, ok := v.used[c]; !ok && c != "amp-bind" {
			v.warn(l, "the %s script is loaded but not used", c)
		}
	}
}

func scriptAttr(c string) string {
	if c == "amp-mustache" {
		return "custom-template"
	}
	return "custom-element"
}

// Blocks lists the form and poll block names a document posts to, in order.
func Blocks(src string) []string {
	var out []string
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			t := z.Token()
			if t.Data == "form" {
				if act := attrs(t)["action-xhr"]; strings.HasPrefix(act, SubmitPrefix) {
					if id := strings.TrimPrefix(act, SubmitPrefix); ValidBlockID(id) && !slices.Contains(out, id) {
						out = append(out, id)
					}
				}
			}
		}
	}
}
