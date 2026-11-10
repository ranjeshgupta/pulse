// Package channel defines the delivery channels, what a template holds for each, and how a
// template is rendered for one contact. Dispatchers and the API share these definitions.
package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"

	"pulse/internal/model"
)

const (
	Email    = "email"
	SMS      = "sms"
	WhatsApp = "whatsapp"
	RCS      = "rcs"
	Push     = "push"
	Inbox    = "inbox" // the app inbox: messages kept in Pulse, shown by the SDKs
)

// All lists every channel, in the order the UI shows them.
var All = []string{Email, SMS, WhatsApp, RCS, Push, Inbox}

// External lists the channels delivered through a provider (every channel but the inbox, which
// Pulse delivers itself).
var External = []string{Email, SMS, WhatsApp, RCS, Push}

// Topic is the Kafka topic a channel's send requests go to.
func Topic(ch string) string { return "messages." + ch }

// Valid reports whether ch is a known channel.
func Valid(ch string) bool {
	for _, c := range All {
		if c == ch {
			return true
		}
	}
	return false
}

// FallsBackToSMS reports whether a channel supports an SMS fallback template.
func FallsBackToSMS(ch string) bool { return ch == WhatsApp || ch == RCS }

// Content is a template's channel-specific body. Every string may use Go template syntax over
// {{.Contact.Email}}, {{.Attrs.first_name}} and so on.
type Content struct {
	// sms
	Text     string `json:"text,omitempty"`      // also rcs
	SenderID string `json:"sender_id,omitempty"` // sms: alphanumeric sender / DLT header
	// whatsapp: a pre-approved template on the WhatsApp Business account
	TemplateName   string   `json:"template_name,omitempty"`
	Language       string   `json:"language,omitempty"`
	BodyParams     []string `json:"body_params,omitempty"`
	HeaderImageURL string   `json:"header_image_url,omitempty"`
	PreviewText    string   `json:"preview_text,omitempty"` // how the approved template reads, for the UI
	// rcs
	Suggestions []Suggestion `json:"suggestions,omitempty"`
	Card        *Card        `json:"card,omitempty"`
	// push
	Title    string            `json:"title,omitempty"`
	Body     string            `json:"body,omitempty"`
	ImageURL string            `json:"image_url,omitempty"`
	URL      string            `json:"url,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
	// inbox (also uses title, body, image_url, url, data)
	ExpiresDays int    `json:"expires_days,omitempty"` // gone from the inbox after this many days (default 30)
	Category    string `json:"category,omitempty"`     // for filtering in the app, e.g. "offers"
}

type Suggestion struct {
	Type  string `json:"type"` // reply | url | dial
	Text  string `json:"text"`
	Value string `json:"value,omitempty"` // url or phone number
}

type Card struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
}

// ParseContent decodes a template's content JSON.
func ParseContent(raw json.RawMessage) (Content, error) {
	var c Content
	if len(raw) == 0 {
		return c, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("content: %w", err)
	}
	return c, nil
}

// Validate checks that a template has what its channel needs and that every field parses as a template.
func Validate(t model.Template) error {
	if !Valid(t.Channel) {
		return fmt.Errorf("unknown channel %q", t.Channel)
	}
	c, err := ParseContent(t.Content)
	if err != nil {
		return err
	}
	var missing []string
	need := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	switch t.Channel {
	case Email:
		need("subject", t.Subject)
		need("html", t.HTML)
	case SMS:
		need("content.text", c.Text)
		if len(c.Text) > 1600 {
			return errors.New("sms text is longer than 1600 characters")
		}
	case WhatsApp:
		need("content.template_name", c.TemplateName)
	case RCS:
		if c.Text == "" && c.Card == nil {
			missing = append(missing, "content.text or content.card")
		}
		if len(c.Suggestions) > 4 {
			return errors.New("rcs supports at most 4 suggestions")
		}
		for _, s := range c.Suggestions {
			if s.Type != "reply" && s.Type != "url" && s.Type != "dial" {
				return fmt.Errorf("rcs suggestion type %q must be reply, url or dial", s.Type)
			}
			need("suggestion text", s.Text)
		}
	case Push:
		need("content.title", c.Title)
		need("content.body", c.Body)
	case Inbox:
		need("content.title", c.Title)
		need("content.body", c.Body)
		if c.ExpiresDays < 0 || c.ExpiresDays > 365 {
			return errors.New("inbox messages expire after 1 to 365 days")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s template needs %s", t.Channel, strings.Join(missing, ", "))
	}
	// Every field must parse, so authors find mistakes on save rather than at send time.
	_, err = Render(t, model.Contact{Attrs: map[string]any{}})
	return err
}

// Rendered is a template filled in for one contact.
type Rendered struct {
	Subject string
	HTML    string
	Content Content
}

type data struct {
	Contact        model.Contact
	Attrs          map[string]any
	UnsubscribeURL string // email: {{.UnsubscribeURL}} places the link; otherwise a footer is added
	PreferencesURL string // email: {{.PreferencesURL}}, the contact's preference center
}

// Links are a message's personal links, available to its template.
type Links struct {
	Unsubscribe string
	Preferences string
}

// Render fills every field of the template for one contact. HTML is auto-escaped; plain-text fields
// are not (they are never interpreted as markup by the channel).
func Render(t model.Template, contact model.Contact) (Rendered, error) {
	return RenderFor(t, contact, Links{})
}

// RenderFor renders with the message's unsubscribe and preference links available to the template.
func RenderFor(t model.Template, contact model.Contact, links Links) (Rendered, error) {
	d := data{Contact: contact, Attrs: contact.Attrs, UnsubscribeURL: links.Unsubscribe, PreferencesURL: links.Preferences}
	var firstErr error
	text := func(field, s string) string {
		if s == "" || firstErr != nil {
			return s
		}
		tpl, err := texttemplate.New(field).Option("missingkey=zero").Parse(s)
		if err != nil {
			firstErr = fmt.Errorf("%s: %w", field, err)
			return ""
		}
		var b bytes.Buffer
		if err := tpl.Execute(&b, d); err != nil {
			firstErr = fmt.Errorf("%s: %w", field, err)
			return ""
		}
		return strings.ReplaceAll(b.String(), "<no value>", "")
	}

	var out Rendered
	// Subjects become mail headers: strip CR/LF so attribute values cannot inject headers.
	out.Subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(text("subject", t.Subject))
	if t.HTML != "" {
		tpl, err := htmltemplate.New("html").Option("missingkey=zero").Parse(t.HTML)
		if err != nil {
			return out, fmt.Errorf("html: %w", err)
		}
		var b bytes.Buffer
		if err := tpl.Execute(&b, d); err != nil {
			return out, fmt.Errorf("html: %w", err)
		}
		out.HTML = strings.ReplaceAll(b.String(), "&lt;no value&gt;", "")
	}
	c, err := ParseContent(t.Content)
	if err != nil {
		return out, err
	}
	c.Text, c.SenderID = text("text", c.Text), text("sender_id", c.SenderID)
	c.HeaderImageURL = text("header_image_url", c.HeaderImageURL)
	for i, p := range c.BodyParams {
		c.BodyParams[i] = text(fmt.Sprintf("body_params[%d]", i), p)
	}
	for i := range c.Suggestions {
		c.Suggestions[i].Text = text("suggestion text", c.Suggestions[i].Text)
		c.Suggestions[i].Value = text("suggestion value", c.Suggestions[i].Value)
	}
	if c.Card != nil {
		c.Card.Title, c.Card.Description = text("card title", c.Card.Title), text("card description", c.Card.Description)
		c.Card.ImageURL = text("card image", c.Card.ImageURL)
	}
	c.Title, c.Body = text("title", c.Title), text("body", c.Body)
	c.ImageURL, c.URL = text("image_url", c.ImageURL), text("url", c.URL)
	for k, v := range c.Data {
		c.Data[k] = text("data."+k, v)
	}
	out.Content = c
	return out, firstErr
}
