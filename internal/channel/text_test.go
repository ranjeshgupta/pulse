package channel

import (
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/model"
)

func TestTextFromHTML(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"links keep their URL", `<p>Hi <b>Asha</b>,</p><p>See <a href="https://shop.test/sale?x=1&amp;y=2">our sale</a> today.</p>`,
			"Hi Asha,\n\nSee our sale (https://shop.test/sale?x=1&y=2) today."},
		{"a link whose text is its URL once", `<a href="https://a.test/">https://a.test/</a>`, "https://a.test/"},
		{"mailto shows the address once", `Write to <a href="mailto:help@a.test">help@a.test</a>`, "Write to help@a.test"},
		{"image links fall back to alt text or the URL", `<a href="https://a.test/x"><img src="b.png" alt="Shop"></a> <a href="https://a.test/y"><img src="c.png"></a>`,
			"Shop (https://a.test/x) https://a.test/y"},
		{"non-web links keep only their text", `<a href="#top">Top</a> <a href="javascript:x()">Go</a>`, "Top Go"},
		{"head, style and comments go", `<html><head><title>T</title><style>p{color:red}</style></head><body><!-- hidden --><p>Body</p></body></html>`, "Body"},
		{"entities decode, whitespace collapses", "<p>Tom &amp; Jerry&nbsp;&nbsp; &lt;3\n\n\n   friends</p>", "Tom & Jerry <3 friends"},
		{"lists, breaks, rules", `<ul><li>One</li><li>Two</li></ul>line<br>next<hr>end`, "- One\n- Two\n\nline\nnext\n\n----------\n\nend"},
		{"tables read across", `<table><tr><td>Plan</td><td>Pro</td></tr><tr><td>Price</td><td>₹499</td></tr></table>`, "Plan Pro\nPrice ₹499"},
		{"escaped markup in a link stays text", `<a href="https://a.test/?q=%3Cb%3E">a &lt;b&gt; tag</a>`, "a <b> tag (https://a.test/?q=%3Cb%3E)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextFromHTML(tc.in); got != tc.want {
				t.Errorf("got\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestEmailTextPart(t *testing.T) {
	c := model.Contact{Attrs: map[string]any{"first_name": "Asha"}}
	// No text: generated from the rendered HTML.
	out, err := Render(model.Template{Channel: Email, Subject: "s", HTML: `<p>Hi {{.Attrs.first_name}}</p>`}, c)
	if err != nil || out.Content.Text != "Hi Asha" || !out.TextGenerated {
		t.Fatalf("generated text = %q (%v), err %v", out.Content.Text, out.TextGenerated, err)
	}
	// Own text: rendered as a text template (not HTML-escaped).
	out, err = Render(model.Template{Channel: Email, Subject: "s", HTML: `<p>x</p>`,
		Content: json.RawMessage(`{"text":"Hi {{.Attrs.first_name}} & co"}`)}, c)
	if err != nil || out.Content.Text != "Hi Asha & co" || out.TextGenerated {
		t.Fatalf("own text = %q (%v), err %v", out.Content.Text, out.TextGenerated, err)
	}
	// Other channels never get a generated text.
	out, _ = Render(model.Template{Channel: Push, Content: json.RawMessage(`{"title":"a","body":"b"}`)}, c)
	if out.Content.Text != "" {
		t.Errorf("push text = %q", out.Content.Text)
	}
}

func TestValidateAttachments(t *testing.T) {
	email := func(content string) model.Template {
		return model.Template{Channel: Email, Subject: "s", HTML: "<p>x</p>", Content: json.RawMessage(content)}
	}
	if err := Validate(email(`{"attachments":[1,2]}`)); err != nil {
		t.Errorf("valid attachments: %v", err)
	}
	for content, want := range map[string]string{
		`{"attachments":[1,1]}`:                     "distinct",
		`{"attachments":[0]}`:                       "distinct",
		`{"attachments":[1,2,3,4,5,6,7,8,9,10,11]}`: "at most 10",
		`{"text":"{{.Broken"}`:                      "text",
	} {
		if err := Validate(email(content)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", content, err, want)
		}
	}
	sms := model.Template{Channel: SMS, Content: json.RawMessage(`{"text":"hi","attachments":[1]}`)}
	if err := Validate(sms); err == nil || !strings.Contains(err.Error(), "only email") {
		t.Errorf("sms with attachments: %v", err)
	}
}
