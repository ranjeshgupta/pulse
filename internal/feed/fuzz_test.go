package feed

import (
	"strings"
	"testing"
	"time"
)

// FuzzParse feeds arbitrary documents to Parse and renders what it accepts: nothing may panic,
// at most MaxParsed items are read, and rendered links are only ever http(s).
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		`<?xml version="1.0"?><rss version="2.0"><channel><title>Blog</title><item><title>A &amp; B</title><link>https://x.y/a</link><description>&lt;p&gt;Hi&lt;/p&gt;</description><pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate><enclosure url="https://x.y/i.png" type="image/png"/></item></channel></rss>`,
		`<feed xmlns="http://www.w3.org/2005/Atom"><title>T</title><entry><title>E</title><link rel="alternate" href="javascript:alert(1)"/><summary>s</summary><updated>2026-01-02T03:04:05Z</updated></entry></feed>`,
		`<rss><channel><item><dc:date xmlns:dc="http://purl.org/dc/elements/1.1/">2026-01-02</dc:date></item></channel></rss>`,
		`<rss>`, ``, `<!DOCTYPE x [<!ENTITY a "b">]><rss/>`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fd, err := Parse(b)
		if err != nil {
			return
		}
		if len(fd.Items) > MaxParsed {
			t.Fatalf("%d items parsed", len(fd.Items))
		}
		items := Since(fd, time.Time{}, time.Now(), true)
		if len(items) > MaxItems {
			t.Fatalf("Since kept %d items", len(items))
		}
		Newest(items)
		h, _ := Render(items)
		if strings.Contains(strings.ToLower(h), `href="javascript`) {
			t.Fatalf("rendered a javascript link: %s", h)
		}
	})
}
