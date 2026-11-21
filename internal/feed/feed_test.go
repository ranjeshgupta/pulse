package feed

import (
	"strings"
	"testing"
	"time"
)

const rssDoc = `<?xml version="1.0"?><rss version="2.0"><channel><title>Acme Blog</title>
<item><title>Older &amp; wiser</title><link>https://acme.test/old</link><description>&lt;p&gt;Old post&lt;/p&gt;</description><pubDate>Mon, 02 Nov 2026 10:00:00 +0530</pubDate></item>
<item><title>Diwali picks</title><link>https://acme.test/diwali</link><description>Our &lt;b&gt;favourite&lt;/b&gt; gifts</description><pubDate>Fri, 20 Nov 2026 09:00:00 +0000</pubDate>
<enclosure url="https://acme.test/d.jpg" type="image/jpeg"/></item></channel></rss>`

const atomDoc = `<feed xmlns="http://www.w3.org/2005/Atom"><title>Acme</title><entry><title>Hello</title>
<link rel="alternate" href="https://acme.test/hello"/><summary>Hi there</summary><published>2026-11-19T08:00:00Z</published></entry></feed>`

func TestParseAndSince(t *testing.T) {
	f, err := Parse([]byte(rssDoc))
	if err != nil || f.Title != "Acme Blog" || len(f.Items) != 2 || f.Items[0].Title != "Older & wiser" || f.Items[0].Summary != "Old post" {
		t.Fatalf("rss: %+v %v", f, err)
	}
	items := Since(f, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC))
	if len(items) != 1 || items[0].Title != "Diwali picks" || items[0].Image == "" {
		t.Errorf("since: %+v", items)
	}
	if all := Since(f, time.Time{}); len(all) != 2 || all[0].Title != "Diwali picks" {
		t.Errorf("first run, newest first: %+v", all)
	}
	h, txt := Render(items)
	if !strings.Contains(h, `<a href="https://acme.test/diwali">Diwali picks</a>`) || !strings.Contains(txt, "- Diwali picks https://acme.test/diwali") {
		t.Errorf("render: %s / %s", h, txt)
	}
	a, err := Parse([]byte(atomDoc))
	if err != nil || len(a.Items) != 1 || a.Items[0].Link != "https://acme.test/hello" || a.Items[0].Published.IsZero() {
		t.Errorf("atom: %+v %v", a, err)
	}
	if _, err := Parse([]byte("<html></html>")); err == nil {
		t.Error("html parsed as a feed")
	}
	big := "<rss><channel>" + strings.Repeat("<item><title>x</title><pubDate>Mon, 02 Nov 2026 10:00:00 +0530</pubDate></item>", 20000) + "</channel></rss>"
	if f, err := Parse([]byte(big)); err != nil || len(f.Items) != MaxParsed || len(Since(f, time.Time{})) != MaxItems {
		t.Errorf("big feed: %d items, %v", len(f.Items), err)
	}
	if h, _ := Render([]Item{{Title: "<script>x</script>", Link: "javascript:alert(1)"}}); strings.Contains(h, "<script>") || strings.Contains(h, "javascript:") {
		t.Errorf("unsafe render: %s", h)
	}
}
