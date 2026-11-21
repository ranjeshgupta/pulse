// Package feed reads RSS 2.0 and Atom feeds for RSS-to-email campaigns (O6): each run of a
// campaign with a feed sends the items published since the last run, rendered as {{block.feed}}
// (HTML for the email, a plain list for its text part); a run with nothing new sends nothing.
package feed

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Item is one feed entry.
type Item struct {
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Summary   string    `json:"summary"`
	Published time.Time `json:"published"`
	Image     string    `json:"image,omitempty"`
}

// Feed is a parsed feed.
type Feed struct {
	Title string `json:"title"`
	Items []Item `json:"items"`
}

// MaxItems caps what one email lists; MaxParsed caps the items read from one feed.
const (
	MaxItems  = 10
	MaxParsed = 500
)

const maxBytes = 2 << 20

type rss struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
			Date        string `xml:"http://purl.org/dc/elements/1.1/ date"`
			Enclosure   struct {
				URL  string `xml:"url,attr"`
				Type string `xml:"type,attr"`
			} `xml:"enclosure"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atom struct {
	Title   string `xml:"title"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
	} `xml:"entry"`
}

var dateLayouts = []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02"}

// zoneHours are the zone names feeds use that Go reads with a zero offset (unknown abbreviations).
var zoneHours = map[string]int{"EST": -5, "EDT": -4, "CST": -6, "CDT": -5, "MST": -7, "MDT": -6, "PST": -8, "PDT": -7,
	"GMT": 0, "UTC": 0, "Z": 0}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			if name, off := t.Zone(); off == 0 {
				if h, ok := zoneHours[name]; ok {
					t = t.Add(-time.Duration(h) * time.Hour)
				}
			}
			return t.UTC()
		}
	}
	return time.Time{}
}

var tagRe = regexp.MustCompile(`(?s)<[^>]+>`)

// plain turns an item's HTML summary into short text.
func plain(s string, n int) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, " "))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// Parse reads an RSS 2.0 or Atom document.
func Parse(b []byte) (Feed, error) {
	var f Feed
	var r rss
	if err := xml.Unmarshal(b, &r); err == nil && len(r.Channel.Items) > 0 {
		f.Title = strings.TrimSpace(r.Channel.Title)
		for _, it := range r.Channel.Items[:min(len(r.Channel.Items), MaxParsed)] {
			d := parseDate(it.PubDate)
			if d.IsZero() {
				d = parseDate(it.Date)
			}
			img := ""
			if strings.HasPrefix(it.Enclosure.Type, "image/") {
				img = it.Enclosure.URL
			}
			f.Items = append(f.Items, Item{Title: plain(it.Title, 200), Link: strings.TrimSpace(it.Link), Summary: plain(it.Description, 300), Published: d, Image: img})
		}
		return f, nil
	}
	var a atom
	if err := xml.Unmarshal(b, &a); err == nil && len(a.Entries) > 0 {
		f.Title = strings.TrimSpace(a.Title)
		for _, e := range a.Entries[:min(len(a.Entries), MaxParsed)] {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			d := parseDate(e.Published)
			if d.IsZero() {
				d = parseDate(e.Updated)
			}
			sum := e.Summary
			if sum == "" {
				sum = e.Content
			}
			f.Items = append(f.Items, Item{Title: plain(e.Title, 200), Link: strings.TrimSpace(link), Summary: plain(sum, 300), Published: d})
		}
		return f, nil
	}
	return f, errors.New("not an RSS or Atom feed with items")
}

// Fetch downloads and parses a feed with client (a safehttp client: public addresses only).
func Fetch(ctx context.Context, client *http.Client, url string) (Feed, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return Feed{}, errors.New("the feed address must start with https://")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Feed{}, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")
	req.Header.Set("User-Agent", "Pulse-RSS/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return Feed{}, fmt.Errorf("couldn't fetch the feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Feed{}, fmt.Errorf("the feed answered HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return Feed{}, err
	}
	return Parse(b)
}

// Since keeps the items published after t and no later than until (newest first, at most
// MaxItems); items dated later (scheduled posts) wait for a later run. Items without a date count
// only when first is set (the campaign's first feed run).
func Since(f Feed, t, until time.Time, first bool) []Item {
	var out []Item
	for _, it := range f.Items {
		if it.Published.IsZero() {
			if first {
				out = append(out, it)
			}
		} else if it.Published.After(t) && !it.Published.After(until) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) }) // newest first
	if len(out) > MaxItems {
		out = out[:MaxItems]
	}
	return out
}

// Render makes the email's HTML list and the plain-text list of items. Feed text never becomes
// template code: "{" is &#123; in the HTML, and "{{" is split by a zero-width space in the text.
func Render(items []Item) (htmlOut, text string) {
	var h, t strings.Builder
	for _, it := range items {
		link := it.Link
		if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
			link = ""
		}
		h.WriteString(`<div style="margin:0 0 18px">`)
		if it.Image != "" && strings.HasPrefix(it.Image, "https://") {
			fmt.Fprintf(&h, `<img src="%s" alt="" style="max-width:100%%;border-radius:6px;margin:0 0 6px">`, esc(it.Image))
		}
		if link != "" {
			fmt.Fprintf(&h, `<h3 style="margin:0 0 4px"><a href="%s">%s</a></h3>`, esc(link), esc(it.Title))
		} else {
			fmt.Fprintf(&h, `<h3 style="margin:0 0 4px">%s</h3>`, esc(it.Title))
		}
		if it.Summary != "" {
			fmt.Fprintf(&h, `<p style="margin:0">%s</p>`, esc(it.Summary))
		}
		h.WriteString(`</div>`)
		fmt.Fprintf(&t, "- %s", plainText(it.Title))
		if link != "" {
			fmt.Fprintf(&t, " %s", plainText(link))
		}
		t.WriteString("\n")
	}
	return h.String(), strings.TrimSpace(t.String())
}

// esc escapes feed text for HTML, braces too ({{block.feed}} goes into the template before it is
// parsed).
func esc(s string) string { return strings.ReplaceAll(html.EscapeString(s), "{", "&#123;") }

// plainText breaks every "{{" in feed text for the plain-text part.
func plainText(s string) string {
	for strings.Contains(s, "{{") {
		s = strings.ReplaceAll(s, "{{", "{\u200b{")
	}
	return s
}

// Newest is the latest publication time among items (zero without dates).
func Newest(items []Item) time.Time {
	var n time.Time
	for _, it := range items {
		if it.Published.After(n) {
			n = it.Published
		}
	}
	return n
}
