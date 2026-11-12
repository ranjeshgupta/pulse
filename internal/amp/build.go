package amp

// The visual AMP block builder: the app edits a list of blocks (Doc), and the server turns it into
// an AMP for Email document. Text may carry personalisation variables ({{contact.first_name}}):
// it's written HTML-escaped, and the renderer fills the variables per recipient (escaped again for
// where they land). Forms and polls post back to Pulse through "pulse:submit/<block>".

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Doc is the builder's document.
type Doc struct {
	Blocks []Block `json:"blocks"`
	Style  Style   `json:"style"`
}

// Style is the document's look.
type Style struct {
	Background string `json:"background,omitempty"` // page colour
	Text       string `json:"text,omitempty"`
	Accent     string `json:"accent,omitempty"` // buttons, bars
	Font       string `json:"font,omitempty"`   // sans | serif
}

// Block types.
const (
	TypeHeading   = "heading"
	TypeText      = "text"
	TypeImage     = "image"
	TypeButton    = "button"
	TypeCarousel  = "carousel"
	TypeAccordion = "accordion"
	TypeGallery   = "gallery"
	TypePoll      = "poll"
	TypeForm      = "form"
)

// Block is one piece of the email.
type Block struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// heading, text, button label, poll question, form title
	Text string `json:"text,omitempty"`
	// image, button: the link; image: the image
	URL   string  `json:"url,omitempty"`
	Image *Image  `json:"image,omitempty"`
	Items []Image `json:"items,omitempty"` // carousel and gallery images
	// carousel: slide height in px (default 300), autoplay seconds (0 = off)
	Height   int `json:"height,omitempty"`
	Autoplay int `json:"autoplay,omitempty"`
	// accordion sections
	Sections []Section `json:"sections,omitempty"`
	// poll answers; form fields
	Options []string    `json:"options,omitempty"`
	Fields  []FormField `json:"fields,omitempty"`
	// poll: show the results after answering (and to people who already answered)
	ShowResults bool   `json:"show_results,omitempty"`
	SubmitText  string `json:"submit_text,omitempty"`
	SuccessText string `json:"success_text,omitempty"`
}

// Image is one picture with an optional link and caption.
type Image struct {
	Src     string `json:"src"`
	Alt     string `json:"alt,omitempty"`
	Link    string `json:"link,omitempty"`
	Caption string `json:"caption,omitempty"`
	Width   int    `json:"width,omitempty"`  // px; default 600
	Height  int    `json:"height,omitempty"` // px; default 300
}

// Section is one accordion panel.
type Section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// FormField is one input of a form block. SaveTo copies the answer to a contact field (a custom
// field only: see the post-back handler).
type FormField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | email | number | textarea | select | checkbox
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
	SaveTo   string   `json:"save_to,omitempty"`
}

const (
	maxBlocks   = 40
	maxItems    = 10
	maxOptions  = 8
	maxFields   = 10
	maxSections = 10
)

var (
	colorRe    = regexp.MustCompile(`^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`)
	fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

// reservedFields are names a form field can't take: they're the event's own properties.
var reservedFields = map[string]bool{"block": true, "message_id": true, "template_id": true, "journey_id": true,
	"broadcast_id": true, "run": true, "variant": true, "answer": true, "question": true, "form": true}

// ParseDoc decodes a builder document (nil or empty: no document).
func ParseDoc(raw json.RawMessage) (*Doc, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("AMP blocks: %w", err)
	}
	return &d, nil
}

// Find returns the block with this id.
func (d *Doc) Find(id string) (Block, bool) {
	if d == nil {
		return Block{}, false
	}
	for _, b := range d.Blocks {
		if b.ID == id {
			return b, true
		}
	}
	return Block{}, false
}

func httpsOrVar(u string) bool {
	u = strings.TrimSpace(u)
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "{{")
}

func linkOK(u string) bool {
	u = strings.TrimSpace(u)
	return u == "" || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "{{") ||
		strings.HasPrefix(u, "mailto:")
}

// Check validates the document (before building), with messages for marketers.
func (d *Doc) Check() error {
	if len(d.Blocks) == 0 {
		return errors.New("add at least one block")
	}
	if len(d.Blocks) > maxBlocks {
		return fmt.Errorf("at most %d blocks", maxBlocks)
	}
	for _, c := range []string{d.Style.Background, d.Style.Text, d.Style.Accent} {
		if c != "" && !colorRe.MatchString(c) {
			return fmt.Errorf("colour %q must look like #1a2b3c", c)
		}
	}
	ids := map[string]bool{}
	var errs []error
	for i, b := range d.Blocks {
		n := fmt.Sprintf("block %d (%s)", i+1, b.Type)
		if !ValidBlockID(b.ID) {
			errs = append(errs, fmt.Errorf("%s: its name must be lowercase letters, digits, - or _", n))
		} else if ids[b.ID] {
			errs = append(errs, fmt.Errorf("%s: the name %q is used twice", n, b.ID))
		}
		ids[b.ID] = true
		imgs := func(list []Image, min int) {
			if len(list) < min || len(list) > maxItems {
				errs = append(errs, fmt.Errorf("%s: add %d to %d images", n, min, maxItems))
			}
			for _, im := range list {
				if !httpsOrVar(im.Src) {
					errs = append(errs, fmt.Errorf("%s: images must be https links", n))
					return
				}
				if !linkOK(im.Link) {
					errs = append(errs, fmt.Errorf("%s: links must be web addresses", n))
					return
				}
			}
		}
		switch b.Type {
		case TypeHeading, TypeText:
			if strings.TrimSpace(b.Text) == "" {
				errs = append(errs, fmt.Errorf("%s: write some text", n))
			}
		case TypeImage:
			if b.Image == nil {
				errs = append(errs, fmt.Errorf("%s: choose an image", n))
			} else {
				imgs([]Image{*b.Image}, 1)
			}
		case TypeButton:
			if strings.TrimSpace(b.Text) == "" || strings.TrimSpace(b.URL) == "" || !linkOK(b.URL) {
				errs = append(errs, fmt.Errorf("%s: a button needs a label and a web address", n))
			}
		case TypeCarousel:
			imgs(b.Items, 2)
			if b.Autoplay < 0 || b.Autoplay > 30 {
				errs = append(errs, fmt.Errorf("%s: autoplay is 0 (off) to 30 seconds", n))
			}
		case TypeGallery:
			imgs(b.Items, 2)
		case TypeAccordion:
			if len(b.Sections) == 0 || len(b.Sections) > maxSections {
				errs = append(errs, fmt.Errorf("%s: add 1 to %d sections", n, maxSections))
			}
			for _, s := range b.Sections {
				if strings.TrimSpace(s.Title) == "" {
					errs = append(errs, fmt.Errorf("%s: every section needs a title", n))
					break
				}
			}
		case TypePoll:
			if strings.TrimSpace(b.Text) == "" {
				errs = append(errs, fmt.Errorf("%s: write the question", n))
			}
			if len(b.Options) < 2 || len(b.Options) > maxOptions {
				errs = append(errs, fmt.Errorf("%s: a poll has 2 to %d answers", n, maxOptions))
			}
			seen := map[string]bool{}
			for _, o := range b.Options {
				o = strings.TrimSpace(o)
				if o == "" || len(o) > 100 || seen[strings.ToLower(o)] {
					errs = append(errs, fmt.Errorf("%s: answers must be different and 1 to 100 characters", n))
					break
				}
				seen[strings.ToLower(o)] = true
			}
		case TypeForm:
			if len(b.Fields) == 0 || len(b.Fields) > maxFields {
				errs = append(errs, fmt.Errorf("%s: a form has 1 to %d fields", n, maxFields))
			}
			keys := map[string]bool{}
			for _, f := range b.Fields {
				switch {
				case !fieldKeyRe.MatchString(f.Key) || reservedFields[f.Key]:
					errs = append(errs, fmt.Errorf("%s: field name %q must be lowercase letters, digits and _ (and not %s)", n, f.Key, "block, answer, form…"))
				case keys[f.Key]:
					errs = append(errs, fmt.Errorf("%s: field %q is used twice", n, f.Key))
				case strings.TrimSpace(f.Label) == "":
					errs = append(errs, fmt.Errorf("%s: field %q needs a label", n, f.Key))
				}
				keys[f.Key] = true
				switch f.Type {
				case "text", "email", "number", "textarea", "checkbox":
				case "select":
					if len(f.Options) == 0 || len(f.Options) > 20 {
						errs = append(errs, fmt.Errorf("%s: field %q needs 1 to 20 choices", n, f.Key))
					}
				default:
					errs = append(errs, fmt.Errorf("%s: field %q has an unknown type %q", n, f.Key, f.Type))
				}
			}
		default:
			errs = append(errs, fmt.Errorf("%s: unknown block type", n))
		}
	}
	return errors.Join(errs...)
}

// SaveTargets lists the contact fields form blocks copy answers to.
func (d *Doc) SaveTargets() []string {
	var out []string
	if d == nil {
		return out
	}
	for _, b := range d.Blocks {
		for _, f := range b.Fields {
			if f.SaveTo != "" {
				out = append(out, f.SaveTo)
			}
		}
	}
	return out
}

func or(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func px(v, def int) int {
	if v <= 0 || v > 2000 {
		return def
	}
	return v
}

// esc escapes text for HTML; personalisation tags survive (their quotes become &#34;, which the
// renderer decodes inside a tag).
func esc(s string) string { return html.EscapeString(s) }

// para turns plain text with blank lines into paragraphs and line breaks.
func para(s string) string {
	var b strings.Builder
	for _, p := range strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n"), "\n\n") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		b.WriteString("<p>" + strings.ReplaceAll(esc(strings.TrimSpace(p)), "\n", "<br>") + "</p>")
	}
	return b.String()
}

// Build generates the AMP document. The result passes Validate (tests check every block type).
func (d *Doc) Build() (string, error) {
	if err := d.Check(); err != nil {
		return "", err
	}
	bg, fg, accent := or(d.Style.Background, "#f4f6fa"), or(d.Style.Text, "#152238"), or(d.Style.Accent, "#2747d6")
	font := "Arial,Helvetica,sans-serif"
	if d.Style.Font == "serif" {
		font = "Georgia,'Times New Roman',serif"
	}
	need := map[string]bool{}
	var body strings.Builder
	for _, b := range d.Blocks {
		body.WriteString("\n")
		switch b.Type {
		case TypeHeading:
			body.WriteString(`<h1>` + esc(b.Text) + `</h1>`)
		case TypeText:
			body.WriteString(`<div class="text">` + para(b.Text) + `</div>`)
		case TypeImage:
			body.WriteString(image(*b.Image, "responsive"))
		case TypeButton:
			body.WriteString(`<p class="center"><a class="btn" href="` + esc(b.URL) + `">` + esc(b.Text) + `</a></p>`)
		case TypeCarousel, TypeGallery:
			need["amp-carousel"] = true
			h := px(b.Height, 300)
			if b.Type == TypeCarousel {
				auto := ""
				if b.Autoplay > 0 {
					auto = fmt.Sprintf(` autoplay delay="%d" loop`, b.Autoplay*1000)
				}
				body.WriteString(fmt.Sprintf(`<amp-carousel id="%s" class="carousel" width="600" height="%d" layout="responsive" type="slides"%s>`, esc(b.ID), h, auto))
				for _, im := range b.Items {
					im.Width, im.Height = 600, h
					body.WriteString(`<div class="slide">` + image(im, "responsive") + `</div>`)
				}
			} else {
				body.WriteString(fmt.Sprintf(`<amp-carousel id="%s" class="gallery" height="%d" layout="fixed-height" type="carousel">`, esc(b.ID), h))
				for _, im := range b.Items {
					w := px(im.Width, h*4/3)
					im.Width, im.Height = w, h
					body.WriteString(image(im, "fixed"))
				}
			}
			body.WriteString(`</amp-carousel>`)
		case TypeAccordion:
			need["amp-accordion"] = true
			body.WriteString(`<amp-accordion id="` + esc(b.ID) + `" class="accordion" expand-single-section>`)
			for _, s := range b.Sections {
				body.WriteString(`<section><h4>` + esc(s.Title) + `</h4><div class="panel">` + para(s.Body) + `</div></section>`)
			}
			body.WriteString(`</amp-accordion>`)
		case TypePoll:
			need["amp-form"], need["amp-mustache"] = true, true
			body.WriteString(poll(b))
		case TypeForm:
			need["amp-form"], need["amp-mustache"] = true, true
			body.WriteString(form(b))
		}
	}
	var head strings.Builder
	head.WriteString(`<!doctype html>
<html ⚡4email data-css-strict>
<head>
<meta charset="utf-8">
<script async src="https://cdn.ampproject.org/v0.js"></script>`)
	for _, c := range []string{"amp-accordion", "amp-carousel", "amp-form", "amp-mustache"} {
		if need[c] {
			head.WriteString(fmt.Sprintf("\n<script async %s=\"%s\" src=\"https://cdn.ampproject.org/v0/%s-%s.js\"></script>", scriptAttr(c), c, c, Components[c]))
		}
	}
	head.WriteString(`
<style amp4email-boilerplate>body{visibility:hidden}</style>
<style amp-custom>
body{margin:0;background:` + bg + `;color:` + fg + `;font-family:` + font + `;font-size:16px;line-height:1.5}
.wrap{max-width:600px;margin:0 auto;padding:24px 20px;background:#ffffff}
h1{font-size:26px;line-height:1.25;margin:0 0 14px}
p{margin:0 0 12px}.center{text-align:center}
.btn{display:inline-block;background:` + accent + `;color:#ffffff;padding:12px 22px;border-radius:6px;text-decoration:none;font-weight:bold}
.caption{font-size:13px;color:#5b6474;margin:6px 0 0}
.gallery amp-img{margin-right:8px}
.accordion section{border-bottom:1px solid #e4e8ee}
.accordion h4{background:#ffffff;border:0;padding:12px 4px;font-size:16px;margin:0}
.accordion .panel{padding:0 4px 12px}
.poll,.form{border:1px solid #e4e8ee;border-radius:8px;padding:16px;margin:16px 0}
.poll label{display:block;padding:8px 10px;border:1px solid #c3cbd6;border-radius:6px;margin:0 0 8px}
.form label{display:block;margin:0 0 12px;font-weight:bold}
.form input,.form select,.form textarea{display:block;width:100%;box-sizing:border-box;padding:9px;border:1px solid #c3cbd6;border-radius:6px;font-size:15px;font-weight:normal;margin-top:4px}
.form .check{font-weight:normal}.form .check input{display:inline;width:auto;margin-right:6px}
button{background:` + accent + `;color:#ffffff;border:0;border-radius:6px;padding:11px 20px;font-size:15px;font-weight:bold}
.bar{background:#e4e8ee;border-radius:4px;height:8px;margin:2px 0 10px}
.bar div{background:` + accent + `;height:8px;border-radius:4px}
.note{font-size:14px;color:#5b6474}.error{color:#b42318}
</style>
</head>
<body>
<div class="wrap">`)
	return head.String() + body.String() + "\n</div>\n</body>\n</html>\n", nil
}

func image(im Image, layout string) string {
	w, h := px(im.Width, 600), px(im.Height, 300)
	tag := fmt.Sprintf(`<amp-img src="%s" alt="%s" width="%d" height="%d" layout="%s"></amp-img>`, esc(im.Src), esc(im.Alt), w, h, layout)
	if im.Link != "" {
		tag = `<a href="` + esc(im.Link) + `">` + tag + `</a>`
	}
	if im.Caption != "" && layout == "responsive" {
		tag += `<p class="caption">` + esc(im.Caption) + `</p>`
	}
	return tag
}

// resultsTemplate shows each answer's share (the post-back's and the results list's "items").
const resultsTemplate = `{{#items}}<p>{{label}} — {{percent}}% ({{count}})</p><div class="bar"><div style="width:{{percent}}%"></div></div>{{/items}}`

func poll(b Block) string {
	var s strings.Builder
	s.WriteString(`<div class="poll"><form id="` + esc(b.ID) + `" method="post" action-xhr="` + SubmitPrefix + b.ID + `">`)
	s.WriteString(`<p><strong>` + esc(b.Text) + `</strong></p>`)
	for _, o := range b.Options {
		s.WriteString(`<label><input type="radio" name="answer" value="` + esc(strings.TrimSpace(o)) + `" required> ` + esc(strings.TrimSpace(o)) + `</label>`)
	}
	s.WriteString(`<button type="submit">` + esc(or(b.SubmitText, "Vote")) + `</button>`)
	s.WriteString(`<div submit-success><template type="amp-mustache"><p><strong>{{message}}</strong></p>`)
	if b.ShowResults {
		s.WriteString(resultsTemplate)
	}
	s.WriteString(`</template></div>`)
	s.WriteString(`<div submit-error><template type="amp-mustache"><p class="error">{{message}}</p></template></div>`)
	s.WriteString(`</form></div>`)
	return s.String()
}

func form(b Block) string {
	var s strings.Builder
	s.WriteString(`<div class="form"><form id="` + esc(b.ID) + `" method="post" action-xhr="` + SubmitPrefix + b.ID + `">`)
	if strings.TrimSpace(b.Text) != "" {
		s.WriteString(`<p><strong>` + esc(b.Text) + `</strong></p>`)
	}
	for _, f := range b.Fields {
		req := ""
		if f.Required {
			req = " required"
		}
		name := esc(f.Key)
		switch f.Type {
		case "textarea":
			s.WriteString(`<label>` + esc(f.Label) + `<textarea name="` + name + `" rows="3"` + req + `></textarea></label>`)
		case "select":
			s.WriteString(`<label>` + esc(f.Label) + `<select name="` + name + `"` + req + `><option value="">Choose…</option>`)
			for _, o := range f.Options {
				s.WriteString(`<option>` + esc(o) + `</option>`)
			}
			s.WriteString(`</select></label>`)
		case "checkbox":
			s.WriteString(`<label class="check"><input type="checkbox" name="` + name + `" value="yes"` + req + `>` + esc(f.Label) + `</label>`)
		default:
			s.WriteString(`<label>` + esc(f.Label) + `<input type="` + f.Type + `" name="` + name + `"` + req + `></label>`)
		}
	}
	s.WriteString(`<button type="submit">` + esc(or(b.SubmitText, "Send")) + `</button>`)
	s.WriteString(`<div submit-success><template type="amp-mustache"><p><strong>{{message}}</strong></p></template></div>`)
	s.WriteString(`<div submit-error><template type="amp-mustache"><p class="error">{{message}}</p></template></div>`)
	s.WriteString(`</form></div>`)
	return s.String()
}
