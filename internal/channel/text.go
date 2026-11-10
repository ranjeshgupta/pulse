package channel

import (
	"html"
	"regexp"
	"strings"
)

// The plain-text part of an email. A template may write its own (content.text); otherwise it is
// generated from the rendered HTML, so every email goes out as multipart/alternative.

var (
	reDropBlocks = regexp.MustCompile(`(?is)<(head|style|script|title|noscript)\b[^>]*>.*?</(head|style|script|title|noscript)\s*>`)
	reComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reAnchor     = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a\s*>`)
	reHref       = regexp.MustCompile(`(?is)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	reImgAlt     = regexp.MustCompile(`(?is)<img\b[^>]*?\balt\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>`)
	reListItem   = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	reBreak      = regexp.MustCompile(`(?i)<br\s*/?>`)
	reHR         = regexp.MustCompile(`(?i)<hr\b[^>]*>`)
	reParagraph  = regexp.MustCompile(`(?i)</?(p|h[1-6]|table|ul|ol|blockquote|pre|dl)\b[^>]*>`)
	reSoftBlock  = regexp.MustCompile(`(?i)</?(div|section|article|header|footer|center|dt|dd|tbody|thead|tfoot)\b[^>]*>|</tr\s*>`)
	reCell       = regexp.MustCompile(`(?i)</t[dh]\s*>`)
	reTag        = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpaces     = regexp.MustCompile(`[ \t\f\v\x{00a0}\x{200b}\x{200c}\x{200d}\x{feff}]+`)
	reBreakRun   = regexp.MustCompile(`[ \t\f\v\x{00a0}\x01\x02\x03]*[\x01\x02\x03][ \t\f\v\x{00a0}\x01\x02\x03]*`)
	reCtrlEntity = regexp.MustCompile(`(?i)&#(?:0*[1-3]|x0*[1-3]);`)
)

// TextFromHTML turns an email's HTML into readable plain text: links become "text (url)", list
// items get a dash, blocks and line breaks become new lines, entities are decoded and whitespace is
// collapsed.
func TextFromHTML(doc string) string {
	// Line breaks in the source are just spaces in HTML; tags decide where lines break.
	// The line-break markers used below can't come from the document, raw or as entities.
	s := strings.NewReplacer("\r", " ", "\n", " ", "\x01", "", "\x02", "", "\x03", "").Replace(doc)
	s = reCtrlEntity.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")
	s = reDropBlocks.ReplaceAllString(s, "")
	s = reImgAlt.ReplaceAllStringFunc(s, func(m string) string {
		p := reImgAlt.FindStringSubmatch(m)
		return " " + p[1] + p[2] + " "
	})
	s = reAnchor.ReplaceAllStringFunc(s, func(m string) string {
		p := reAnchor.FindStringSubmatch(m)
		inner := collapse(html.UnescapeString(reTag.ReplaceAllString(p[2], " ")))
		h := reHref.FindStringSubmatch(p[1])
		if h == nil {
			return inner
		}
		href := strings.TrimSpace(html.UnescapeString(h[1] + h[2] + h[3]))
		low := strings.ToLower(href)
		if !(strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "mailto:") || strings.HasPrefix(low, "tel:")) {
			return inner
		}
		// Escape again so the URL survives the entity decoding of the whole text below.
		esc := html.EscapeString(href)
		bare := href[strings.IndexByte(href, ':')+1:] // the address of a mailto: or tel: link
		switch {
		case inner == "":
			return " " + esc + " "
		case strings.EqualFold(inner, href) || strings.EqualFold(inner, bare):
			return html.EscapeString(inner)
		}
		return html.EscapeString(inner) + " (" + esc + ")"
	})
	// Markers for line breaks, resolved once all tags are gone: a run of them becomes one new line,
	// or a blank line when it includes a paragraph edge or two <br>s.
	const soft, para, br = "\x01", "\x02", "\x03"
	s = reListItem.ReplaceAllString(s, soft+"- ")
	s = reBreak.ReplaceAllString(s, br)
	s = reHR.ReplaceAllString(s, para+"----------"+para)
	s = reParagraph.ReplaceAllString(s, para)
	s = reSoftBlock.ReplaceAllString(s, soft)
	s = reCell.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reBreakRun.ReplaceAllStringFunc(s, func(run string) string {
		if strings.Contains(run, para) || strings.Count(run, br) > 1 {
			return "\n\n"
		}
		return "\n"
	})
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = collapse(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func collapse(s string) string {
	return strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
}
