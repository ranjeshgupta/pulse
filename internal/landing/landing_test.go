package landing

import (
	xhtml "golang.org/x/net/html"
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

func page() model.LandingPage {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	return model.LandingPage{ID: 12, Slug: "spring-sale", Title: "Spring sale", HTML: "<h1>Sale</h1>", Status: Published, StartAt: start}
}

func TestValidateAndLive(t *testing.T) {
	if err := Validate(page()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		edit func(*model.LandingPage)
		want string
	}{
		{func(p *model.LandingPage) { p.Slug = "Spring Sale" }, "lowercase"},
		{func(p *model.LandingPage) { p.Slug = "-x" }, "lowercase"},
		{func(p *model.LandingPage) { p.Slug = "../admin" }, "lowercase"},
		{func(p *model.LandingPage) { p.Title = " " }, "title"},
		{func(p *model.LandingPage) { p.HTML = "" }, "no content"},
		{func(p *model.LandingPage) { p.Status = "live" }, "draft or published"},
		{func(p *model.LandingPage) { p.StartAt = time.Time{} }, "start"},
		{func(p *model.LandingPage) { e := p.StartAt.Add(-time.Hour); p.EndAt = &e }, "after the start"},
	} {
		p := page()
		tc.edit(&p)
		if err := Validate(p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want %q", err, tc.want)
		}
	}

	p := page()
	if Live(p, p.StartAt.Add(-time.Second)) || !Live(p, p.StartAt) {
		t.Error("a page goes live at its start")
	}
	end := p.StartAt.Add(24 * time.Hour)
	p.EndAt = &end
	if Live(p, end) || !Live(p, end.Add(-time.Second)) {
		t.Error("a page stops at its end")
	}
	p.Status = Draft
	if Live(p, p.StartAt.Add(time.Hour)) {
		t.Error("a draft is not live")
	}
}

func TestSanitize(t *testing.T) {
	in := `<p onclick="steal()" class="x">Hi</p><script>alert(1)</script><SCRIPT src="//evil.test/x.js"></SCRIPT>` +
		`<a href="javascript:alert(1)">x</a><img src=x onerror=alert(1)><base href="https://evil.test/">` +
		`<meta http-equiv="refresh" content="0;url=https://evil.test"><script>unclosed`
	out := Sanitize(in)
	for _, bad := range []string{"<script", "<SCRIPT", "onclick", "onerror", "javascript:", "<base", "http-equiv"} {
		if strings.Contains(out, bad) {
			t.Errorf("sanitized page still has %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, `<p class="x">Hi</p>`) || !strings.Contains(out, `<a href="#">x</a>`) {
		t.Errorf("sanitized markup = %s", out)
	}
	// Tricks that defeat regex sanitizers.
	tricky := `<scr<script>x</script>ipt>alert(1)</script><svg><script>alert(1)</script></svg>` +
		`<a href=" java&#x09;script:alert(1)">a</a><a href="JaVaScRiPt:alert(1)">b</a><iframe srcdoc="<script>alert(1)</script>"></iframe>` +
		`<a href="data:text/html,<script>alert(1)</script>">c</a><img src="data:image/png;base64,AAAA"><form><button formaction="javascript:x">d</button></form>` +
		`<object data="x.swf"></object><embed src="x.swf">`
	out = Sanitize(tricky)
	// Check what a browser would build, not the text: "<scr<script>" is one odd element named
	// "scr<script", which is harmless.
	root, _ := xhtml.Parse(strings.NewReader(out))
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && (n.Data == "script" || n.Data == "object" || n.Data == "embed") {
			t.Errorf("tricky input kept a <%s> element:\n%s", n.Data, out)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	for _, bad := range []string{"javascript:", "srcdoc", "data:text"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Errorf("tricky input kept %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, `data:image/png`) {
		t.Errorf("inline images should stay: %s", out)
	}
	if Sanitize(out) != out {
		t.Error("sanitizing twice must change nothing")
	}
}

func TestFormKeys(t *testing.T) {
	got := FormKeys(`<div>{form:fAbc123}</div><section data-pulse-form="fXyz"></section>{form:fAbc123}{form:bad key}`)
	if strings.Join(got, ",") != "fAbc123,fXyz" {
		t.Errorf("keys = %v", got)
	}
}

func TestRenderLive(t *testing.T) {
	p := page()
	p.MetaDescription = `Save "big" <now>`
	p.HTML = `<h1>Sale</h1>{form:fAbc}<script>x()</script>`
	doc := Render(p, Page{SiteKey: "sk_1", Nonce: "N0nce"})
	for _, want := range []string{
		"<!doctype html>", "<title>Spring sale</title>", `<meta name="description" content="Save &#34;big&#34; &lt;now&gt;"/>`,
		`<div data-pulse-form="fAbc"></div>`,
		`<script nonce="N0nce" src="/sdk/pulse.js"></script>`,
		`<script nonce="N0nce" src="/sdk/lp.js" data-site-key="sk_1" data-page-id="12"></script></body>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("page lacks %s:\n%s", want, doc)
		}
	}
	if strings.Count(doc, "<script") != 2 {
		t.Errorf("only the two SDK scripts may remain:\n%s", doc)
	}
	// Form tokens only become elements in text, never inside attributes or raw-text elements.
	p.HTML = `<p title="{form:fAbc}">x</p><textarea>{form:fAbc}</textarea><xmp><title></title><script>alert(1)</script></xmp>`
	doc = Render(p, Page{SiteKey: "sk_1", Nonce: "N0nce"})
	if strings.Contains(doc, `title="<div`) || strings.Contains(doc, "<textarea><div") || strings.Contains(doc, "alert(1)") ||
		strings.Count(doc, "<script") != 2 {
		t.Errorf("tokens in attributes or raw text, or raw-text tricks:\n%s", doc)
	}
	csp := CSP("N0nce")
	if !strings.Contains(csp, "script-src 'nonce-N0nce';") || !strings.Contains(csp, "frame-ancestors *") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("csp = %s", csp)
	}

	// A full document keeps its own head and body, with the page's title and description instead of its own.
	p.HTML = `<!DOCTYPE html><html><head><title>Old</title><meta name="description" content="old"><style>h1{color:red}</style></head><body class="b"><h1>Hi</h1></body></html>`
	doc = Render(p, Page{SiteKey: "sk_1", Nonce: "n"})
	if strings.Contains(doc, "Old") || strings.Contains(doc, `content="old"`) || !strings.Contains(doc, "<style>h1{color:red}</style>") ||
		!strings.Contains(doc, `<body class="b">`) || !strings.HasSuffix(doc, `data-page-id="12"></script></body></html>`) ||
		strings.Count(doc, "<title>") != 1 {
		t.Errorf("full document:\n%s", doc)
	}
}

func TestRenderPreview(t *testing.T) {
	p := page()
	p.HTML = `<h1>Sale</h1>{form:fAbc}<div data-pulse-form="fGone"></div>`
	forms := map[string]model.Form{"fAbc": {PublicKey: "fAbc", Status: "draft", Fields: []model.FormField{
		{Key: "email", Label: "Your <email>", Type: "email", Required: true}, {Key: "city", Label: "City", Type: "select", Options: []string{"Pune", "Delhi"}},
		{Key: "src", Type: "hidden"}},
		Settings: model.FormSettings{SubmitText: "Get it"}}}
	doc := Render(p, Page{Preview: true, Forms: forms})
	for _, want := range []string{"Your &lt;email&gt; *", "Pune<span aria-hidden=\"true\">▾</span>", "Get it", "This form is a draft", "Form fGone</strong>: not found", `http-equiv="Content-Security-Policy"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("preview lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "<script") {
		t.Errorf("preview has scripts:\n%s", doc)
	}
}

func TestRenderPreviewMultiPage(t *testing.T) {
	p := page()
	p.HTML = `{form:fPages}`
	forms := map[string]model.Form{"fPages": {PublicKey: "fPages", Status: "active", Fields: []model.FormField{
		{Key: "email", Label: "Email", Type: "email", Required: true}, {Key: "cv", Label: "Your CV", Type: "file", Required: true},
		{Type: "page_break", Label: "More"}, {Key: "city", Label: "City <b>", Type: "text"}}}}
	doc := Render(p, Page{Preview: true, Forms: forms})
	for _, want := range []string{"Page 1 of 2", "Your CV *", "Choose a file", ">Next<"} {
		if !strings.Contains(doc, want) {
			t.Errorf("preview lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "City") || strings.Contains(doc, "<script") {
		t.Errorf("preview shows later pages or scripts:\n%s", doc)
	}
}
