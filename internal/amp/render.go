package amp

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

var (
	mustacheRe    = regexp.MustCompile(`(?is)<template\b[^>]*\btype\s*=\s*["']?amp-mustache["']?[^>]*>.*?</template>`)
	placeholderRe = regexp.MustCompile(`"pulse:(submit|results)/([a-z0-9][a-z0-9_-]{0,39})"`)
	markerRe      = regexp.MustCompile(`PULSEAMPTPL(\d+)X`)
)

// Protect cuts the amp-mustache templates out of a document before personalisation (their
// {{name}} tags are the email client's, not Pulse's) and returns a function that puts them back.
// Personalisation variables don't work inside those templates.
func Protect(src string) (string, func(string) string) {
	var saved []string
	out := mustacheRe.ReplaceAllStringFunc(src, func(m string) string {
		saved = append(saved, m)
		return "PULSEAMPTPL" + strconv.Itoa(len(saved)-1) + "X"
	})
	return out, func(s string) string {
		return markerRe.ReplaceAllStringFunc(s, func(m string) string {
			i, err := strconv.Atoi(markerRe.FindStringSubmatch(m)[1])
			if err != nil || i >= len(saved) {
				return ""
			}
			return saved[i]
		})
	}
}

// Fill replaces the post-back placeholders ("pulse:submit/<block>", "pulse:results/<block>") with
// the links url returns (a signed, per-recipient link; "" leaves the placeholder out).
func Fill(doc string, url func(kind, block string) string) string {
	return placeholderRe.ReplaceAllStringFunc(doc, func(m string) string {
		p := placeholderRe.FindStringSubmatch(m)
		return `"` + html.EscapeString(url(p[1], p[2])) + `"`
	})
}

// PreviewHTML turns an AMP document into plain HTML that looks roughly like it in an email client
// that shows AMP, for a script-free preview in the app: images become <img>, carousels scroll
// sideways, accordion sections become <details>, forms can't be sent, and the boilerplate that
// hides the page until the AMP runtime loads is dropped.
func PreviewHTML(doc string) string {
	z := xhtml.NewTokenizer(strings.NewReader(doc))
	var b strings.Builder
	skip := 0          // inside an element left out (scripts, templates, form messages)
	skipTag := ""      // ...its tag
	inAccordion := 0   // depth of amp-accordion
	var stack []string // replaced end tags, innermost last
	pendingSummary := false
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		t := z.Token()
		if skip > 0 {
			switch {
			case (tt == xhtml.StartTagToken) && t.Data == skipTag:
				skip++
			case tt == xhtml.EndTagToken && t.Data == skipTag:
				skip--
			}
			continue
		}
		switch tt {
		case xhtml.TextToken:
			b.WriteString(html.EscapeString(t.Data))
		case xhtml.DoctypeToken:
			b.WriteString("<!doctype html>")
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			a := attrs(t)
			if t.Data == "script" || t.Data == "template" || (t.Data == "style" && has(a, "amp4email-boilerplate")) ||
				has(a, "submit-success") || has(a, "submit-error") || has(a, "submitting") || t.Data == "amp-state" {
				if tt == xhtml.StartTagToken {
					skip, skipTag = 1, t.Data
				}
				continue
			}
			open, end := previewTag(t, a, inAccordion > 0, &pendingSummary)
			if t.Data == "amp-accordion" {
				inAccordion++
			}
			b.WriteString(open)
			if tt == xhtml.StartTagToken && !voidElements[t.Data] {
				stack = append(stack, end)
			}
		case xhtml.EndTagToken:
			if t.Data == "amp-accordion" {
				inAccordion--
			}
			if len(stack) > 0 {
				b.WriteString(stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
		}
	}
	return b.String()
}

// previewTag returns the HTML start and end tags standing in for t.
func previewTag(t xhtml.Token, a map[string]string, inAccordion bool, pendingSummary *bool) (string, string) {
	keep := func(name string, extra string, allowed ...string) string {
		var s strings.Builder
		s.WriteString("<" + name)
		for _, k := range allowed {
			if v, ok := a[k]; ok {
				if (k == "href" || k == "src") && !(strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "mailto:")) {
					continue
				}
				s.WriteString(" " + k + `="` + html.EscapeString(v) + `"`)
			}
		}
		s.WriteString(extra + ">")
		return s.String()
	}
	common := []string{"class", "id", "style", "align", "width", "height", "colspan", "rowspan", "border", "cellpadding", "cellspacing", "bgcolor"}
	switch t.Data {
	case "html":
		return "<html>", "</html>"
	case "amp-img", "amp-anim":
		w, h := a["width"], a["height"]
		st := "max-width:100%;height:auto;display:block"
		if a["layout"] == "fixed" {
			st = "display:inline-block;vertical-align:top;margin-right:8px"
		}
		return `<img src="` + html.EscapeString(safeSrc(a["src"])) + `" alt="` + html.EscapeString(a["alt"]) + `" width="` +
			html.EscapeString(w) + `" height="` + html.EscapeString(h) + `" style="` + st + `">`, ""
	case "amp-carousel":
		h := a["height"]
		st := "display:flex;overflow-x:auto;gap:8px;scroll-snap-type:x mandatory"
		if a["type"] == "slides" {
			st += ";max-height:" + html.EscapeString(h) + "px"
		}
		return keep("div", ` style="`+st+`"`, "class", "id"), "</div>"
	case "amp-accordion":
		return keep("div", "", "class", "id"), "</div>"
	case "section":
		if inAccordion {
			*pendingSummary = true
			return "<details>", "</details>"
		}
	case "h1", "h2", "h3", "h4", "h5", "h6", "header":
		if *pendingSummary {
			*pendingSummary = false
			return `<summary style="cursor:pointer;font-weight:bold;padding:10px 4px">`, "</summary>"
		}
	case "amp-list":
		return `<div class="note" style="color:#5b6474;font-size:14px">(This list loads live in the email.)`, "</div>"
	case "form":
		return keep("form", ` onsubmit="return false"`, "class", "id"), "</form>"
	case "button":
		return keep("button", ` type="button"`, "class", "style"), "</button>"
	case "a":
		return keep("a", ` target="_blank" rel="noopener"`, append(common, "href")...), "</a>"
	case "style":
		return "<style>", "</style>"
	case "meta":
		if has(a, "charset") {
			return `<meta charset="utf-8">`, ""
		}
		return "", ""
	case "input":
		return keep("input", " disabled", "type", "name", "value", "placeholder", "checked", "class", "style"), ""
	case "select", "textarea", "option", "label":
		return keep(t.Data, "", "name", "value", "rows", "class", "style"), "</" + t.Data + ">"
	}
	if strings.HasPrefix(t.Data, "amp-") {
		return keep("div", "", "class", "id"), "</div>"
	}
	if voidElements[t.Data] {
		return keep(t.Data, "", common...), ""
	}
	return keep(t.Data, "", common...), "</" + t.Data + ">"
}

func safeSrc(s string) string {
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return s
	}
	return ""
}
