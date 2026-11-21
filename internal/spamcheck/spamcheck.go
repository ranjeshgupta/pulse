// Package spamcheck scores an email's content the way spam filters look at it (O6): rules in the
// spirit of SpamAssassin's content tests, without sending anything anywhere. Each problem found
// adds points and says how to fix it; under 3 points is good, 3 to 6 risky, over 6 likely spam.
// It can't see sender reputation or authentication: the domain check (package domaincheck) covers
// SPF / DKIM / DMARC.
package spamcheck

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Issue is one problem found.
type Issue struct {
	Rule   string  `json:"rule"`
	Points float64 `json:"points"`
	Title  string  `json:"title"`
	Fix    string  `json:"fix"`
}

// Result is a message's score.
type Result struct {
	Score  float64 `json:"score"`
	Rating string  `json:"rating"` // good | risky | spam
	Issues []Issue `json:"issues"`
	// Preview: what an inbox list shows (Gmail-like): the subject and the preheader (the first text).
	Preheader string `json:"preheader"`
	HTMLBytes int    `json:"html_bytes"`
	Links     int    `json:"links"`
	Images    int    `json:"images"`
}

// Email is what's checked.
type Email struct {
	Subject string
	HTML    string
	Text    string
}

var spamPhrases = []string{
	"100% free", "act now", "apply now", "as seen on", "buy direct", "call now", "cash bonus", "click below", "click here",
	"congratulations", "dear friend", "double your", "earn extra cash", "eliminate debt", "extra income", "free gift",
	"free money", "get paid", "guaranteed", "increase sales", "limited time", "lowest price", "make money", "miracle",
	"no catch", "no cost", "no credit check", "no obligation", "once in a lifetime", "risk-free", "risk free", "special promotion",
	"this isn't spam", "urgent", "while supplies last", "winner", "you have been selected", "you're a winner", "100% satisfied",
	"cheap", "lottery", "viagra", "casino", "crypto giveaway", "work from home",
}

var shorteners = []string{"bit.ly", "tinyurl.com", "goo.gl", "t.co", "ow.ly", "is.gd", "buff.ly", "cutt.ly", "rebrand.ly", "shorturl.at"}

var (
	tagRe     = regexp.MustCompile(`(?s)<[^>]+>`)
	styleRe   = regexp.MustCompile(`(?is)<(style|script|head)[^>]*>.*?</(style|script|head)>`)
	imgRe     = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	altRe     = regexp.MustCompile(`(?is)\balt\s*=\s*["'][^"']+["']`)
	linkRe    = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
	hiddenRe  = regexp.MustCompile(`(?i)(display\s*:\s*none|font-size\s*:\s*[01]px|visibility\s*:\s*hidden|color\s*:\s*#?fff(fff)?\b[^>]*background[^>]*#?fff)`)
	formRe    = regexp.MustCompile(`(?i)<(form|script|iframe|object|embed)\b`)
	spaceRe   = regexp.MustCompile(`\s+`)
	moneyRe   = regexp.MustCompile(`[$₹€£]{2,}|\$\d+[,\d]*\s*(million|billion)`)
	domainRe  = regexp.MustCompile(`(?i)\b([a-z0-9-]+\.)+(com|in|net|org|io|co|app|shop)\b`)
	urlTextRe = regexp.MustCompile(`(?i)https?://`)
)

// Text strips HTML to the words a reader sees.
func Text(html string) string {
	s := styleRe.ReplaceAllString(html, " ")
	s = tagRe.ReplaceAllString(s, " ")
	for _, r := range [][2]string{{"&nbsp;", " "}, {"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"}, {"&#39;", "'"}, {"&quot;", `"`}} {
		s = strings.ReplaceAll(s, r[0], r[1])
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// Check scores an email.
func Check(e Email) Result {
	var r Result
	add := func(rule string, pts float64, title, fix string) {
		r.Issues = append(r.Issues, Issue{Rule: rule, Points: pts, Title: title, Fix: fix})
		r.Score += pts
	}
	subj := strings.TrimSpace(e.Subject)
	body := Text(e.HTML)
	if body == "" {
		body = strings.TrimSpace(e.Text)
	}
	r.HTMLBytes = len(e.HTML)
	r.Preheader = body
	if len([]rune(r.Preheader)) > 110 {
		r.Preheader = string([]rune(r.Preheader)[:110]) + "…"
	}

	// Subject.
	switch {
	case subj == "":
		add("subject_missing", 2.5, "No subject", "Write a short subject that says what's inside.")
	case len([]rune(subj)) > 80:
		add("subject_long", 0.5, "Long subject (cut off in most inboxes)", "Keep it under about 50 characters.")
	}
	if letters, upper := caseCount(subj); letters >= 8 && float64(upper)/float64(letters) > 0.7 {
		add("subject_caps", 2.0, "Subject mostly in CAPITALS", "Use normal capitalisation; shouting looks like spam.")
	}
	if strings.Count(subj, "!") >= 2 || strings.Contains(subj, "!!") {
		add("subject_bangs", 1.5, "Several exclamation marks in the subject", "One at most, or none.")
	}
	if strings.HasPrefix(strings.ToUpper(subj), "RE:") || strings.HasPrefix(strings.ToUpper(subj), "FWD:") {
		add("subject_fake_reply", 2.0, "Subject pretends to be a reply or forward", "Remove RE: / FWD: unless it really is one.")
	}
	low := strings.ToLower(subj + " " + body)
	var hits []string
	for _, p := range spamPhrases {
		if strings.Contains(low, p) {
			hits = append(hits, p)
		}
	}
	if len(hits) > 0 {
		add("spam_phrases", min(3.0, 0.6*float64(len(hits))), "Phrases spam filters watch for: "+quoteList(hits, 6),
			"Say it in your own words: what the offer is, for whom, until when.")
	}
	if moneyRe.MatchString(subj + " " + body) {
		add("money", 1.0, "Money symbols repeated or big sums", "Write prices plainly, once.")
	}

	// Body.
	if e.HTML != "" {
		imgs := imgRe.FindAllString(e.HTML, -1)
		r.Images = len(imgs)
		words := len(strings.Fields(body))
		if r.Images > 0 && words < 50 {
			add("image_heavy", 2.5, fmt.Sprintf("Mostly images (%d image(s), %d words)", r.Images, words),
				"Add real text: filters can't read images, and many people have images off.")
		}
		missingAlt := 0
		for _, im := range imgs {
			if !altRe.MatchString(im) {
				missingAlt++
			}
		}
		if missingAlt > 0 {
			add("img_alt", 0.3, fmt.Sprintf("%d image(s) without alt text", missingAlt), "Describe each image in its alt text.")
		}
		if strings.TrimSpace(e.Text) == "" {
			add("no_text_part", 0.5, "No plain-text version", "Pulse writes one from the HTML when you leave it empty; a hand-written one reads better.")
		}
		if r.HTMLBytes > 102*1024 {
			add("gmail_clip", 1.0, fmt.Sprintf("HTML is %d KB: Gmail cuts emails over 102 KB", r.HTMLBytes/1024),
				"Trim the HTML (inline styles, big tables); Gmail hides the rest behind \"View entire message\", including the unsubscribe link.")
		}
		if hiddenRe.MatchString(e.HTML) {
			add("hidden_text", 2.0, "Hidden text (display:none, tiny or white-on-white text)", "Remove hidden text: filters treat it as a trick. A hidden preheader span is fine if it's short.")
		}
		if formRe.MatchString(e.HTML) {
			add("active_content", 1.5, "Forms, scripts or embedded frames", "Email clients block them; link to a page instead.")
		}
		links := linkRe.FindAllStringSubmatch(e.HTML, -1)
		r.Links = len(links)
		if r.Links > 25 {
			add("many_links", 1.0, fmt.Sprintf("%d links", r.Links), "Keep to the links that matter.")
		}
		for _, l := range links {
			href, text := l[1], Text(l[2])
			u, err := url.Parse(href)
			if err != nil {
				continue
			}
			host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
			for _, sh := range shorteners {
				if host == sh {
					add("shortener", 2.0, "Link shortener ("+sh+")", "Link to the real address: spammers hide behind shorteners.")
					break
				}
			}
			if m := domainRe.FindString(text); m != "" && host != "" && !strings.Contains(host, strings.ToLower(strings.TrimPrefix(m, "www."))) &&
				!strings.Contains(href, "{{") {
				add("link_mismatch", 2.5, "A link's text shows "+m+" but it goes to "+host, "Make the text match where the link goes (Pulse's click tracking is fine).")
				break
			}
			if u.Scheme == "http" {
				add("http_link", 0.3, "Link without https ("+host+")", "Use https:// links.")
				break
			}
		}
		if words > 0 && urlTextRe.MatchString(body) && strings.Count(strings.ToLower(body), "http") > 5 {
			add("raw_urls", 0.5, "Lots of bare web addresses in the text", "Use linked words instead.")
		}
		if letters, upper := caseCount(body); letters > 200 && float64(upper)/float64(letters) > 0.3 {
			add("body_caps", 1.0, "A lot of text in CAPITALS", "Use normal capitalisation.")
		}
	}

	switch {
	case r.Score < 3:
		r.Rating = "good"
	case r.Score <= 6:
		r.Rating = "risky"
	default:
		r.Rating = "spam"
	}
	r.Score = float64(int(r.Score*10+0.5)) / 10
	if r.Issues == nil {
		r.Issues = []Issue{}
	}
	return r
}

func caseCount(s string) (letters, upper int) {
	for _, c := range s {
		if unicode.IsLetter(c) {
			letters++
			if unicode.IsUpper(c) {
				upper++
			}
		}
	}
	return
}

func quoteList(xs []string, n int) string {
	if len(xs) > n {
		xs = append(xs[:n:n], fmt.Sprintf("and %d more", len(xs)-n))
	}
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = "“" + x + "”"
	}
	return strings.Join(q, ", ")
}
