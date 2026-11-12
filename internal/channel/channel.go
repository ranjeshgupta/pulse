// Package channel defines the delivery channels, what a template holds for each, and how a
// template is rendered for one contact. Dispatchers and the API share these definitions.
package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"pulse/internal/amp"
	"pulse/internal/model"
	"pulse/internal/personalize"
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

// Content is a template's channel-specific body. Every string may use the standard variables
// ({{contact.first_name | default:"there"}}, {{event.product}}, see package personalize) and the
// older Go template syntax ({{.Contact.Email}}, {{.Attrs.first_name}}).
type Content struct {
	// sms; Text is also rcs, and email's plain-text part (generated from the HTML when empty)
	Text     string `json:"text,omitempty"`
	SenderID string `json:"sender_id,omitempty"` // sms: alphanumeric sender / DLT header
	// sms in India: the DLT template id this text was registered under, and the principal entity
	// id when it differs from the provider's (Gupshup Enterprise sends both with each message).
	DLTTemplateID string `json:"dlt_template_id,omitempty"`
	DLTEntityID   string `json:"dlt_entity_id,omitempty"`
	// sms, rcs: replace http(s) links in the text (and RCS url suggestions) with short tracked
	// links (/t/s/{code}), so clicks are recorded as sms_clicked / rcs_clicked.
	TrackLinks bool `json:"track_links,omitempty"`
	// email: asset ids sent as attachments (checked against the workspace's assets when saved)
	Attachments []int64 `json:"attachments,omitempty"`
	// email: the optional AMP part (text/x-amp-html), sent between the plain-text and HTML parts by
	// providers that can (providers.SupportsAMP). AMPBlocks is the visual AMP builder's document
	// (amp.Doc); when present, the API builds AMP from it on save.
	AMP       string          `json:"amp,omitempty"`
	AMPBlocks json.RawMessage `json:"amp_blocks,omitempty"`
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
	if t.Channel != Email && len(c.Attachments) > 0 {
		return errors.New("only email templates take attachments")
	}
	if t.Channel != Email && (c.AMP != "" || len(c.AMPBlocks) > 0) {
		return errors.New("only email templates have an AMP part")
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
		if len(c.Text) > 200_000 {
			return errors.New("the plain-text part is longer than 200,000 characters")
		}
		if c.AMP != "" {
			if errs := amp.Errors(amp.Validate(c.AMP)); len(errs) > 0 {
				return AMPError{Problems: errs}
			}
		}
		if len(c.Attachments) > MaxAttachments {
			return fmt.Errorf("an email can have at most %d attachments", MaxAttachments)
		}
		seen := map[int64]bool{}
		for _, id := range c.Attachments {
			if id <= 0 || seen[id] {
				return errors.New("attachments must be distinct asset ids")
			}
			seen[id] = true
		}
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
	if t.MissingValues != "" && t.MissingValues != MissingEmpty && t.MissingValues != MissingSkip {
		return errors.New(`missing_values must be "empty" or "skip"`)
	}
	// Every field must parse, so authors find mistakes on save rather than at send time.
	_, err = Render(t, model.Contact{Attrs: map[string]any{}})
	return err
}

// AMPError lists what's wrong with a template's AMP part, with line numbers.
type AMPError struct{ Problems []amp.Problem }

func (e AMPError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for i, p := range e.Problems {
		if i == 10 {
			parts = append(parts, fmt.Sprintf("and %d more", len(e.Problems)-10))
			break
		}
		parts = append(parts, p.String())
	}
	return "AMP part: " + strings.Join(parts, "; ")
}

// PrepareAMP builds the AMP part from the visual builder's blocks, when the template has them
// (the generated code replaces content.amp). Without blocks it leaves the template as it is.
func PrepareAMP(t *model.Template) error {
	if t.Channel != Email || len(t.Content) == 0 {
		return nil
	}
	c, err := ParseContent(t.Content)
	if err != nil || len(c.AMPBlocks) == 0 {
		return err
	}
	d, err := amp.ParseDoc(c.AMPBlocks)
	if err != nil || d == nil {
		return err
	}
	out, err := d.Build()
	if err != nil {
		return fmt.Errorf("AMP blocks: %w", err)
	}
	c.AMP = out
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	t.Content = raw
	return nil
}

// MaxAttachments is how many files one email may attach (their total size is capped in assets).
const MaxAttachments = 10

// Rendered is a template filled in for one contact.
type Rendered struct {
	Subject string
	HTML    string
	Content Content
	// TextGenerated: the email's plain-text part (Content.Text) was generated from the HTML.
	TextGenerated bool
	// Missing lists the standard variables (contact.first_name, event.product…) that had no value
	// and no default, for the template's missing-value policy.
	Missing []string
}

// Missing-value policies (model.Template.MissingValues).
const (
	MissingEmpty = "empty" // render the variable empty (the default)
	MissingSkip  = "skip"  // send nothing to that contact; the outcome records why
)

// data is what the older syntax reads: {{.Attrs.first_name}}, {{.Contact.Email}}, {{.UnsubscribeURL}}.
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

// Vars is the personalisation context of one message besides the contact.
type Vars struct {
	Event    map[string]any // the properties of the event that started the journey
	Campaign personalize.Campaign
	Links    Links
}

// Render fills every field of the template for one contact. HTML is auto-escaped; plain-text fields
// are not (they are never interpreted as markup by the channel).
func Render(t model.Template, contact model.Contact) (Rendered, error) {
	return RenderWith(t, contact, Vars{})
}

// RenderFor renders with the message's unsubscribe and preference links available to the template.
func RenderFor(t model.Template, contact model.Contact, links Links) (Rendered, error) {
	return RenderWith(t, contact, Vars{Links: links})
}

// RenderWith renders with the full personalisation context: {{contact.x}}, {{event.x}},
// {{campaign.x}} and {{links.x}}, and the older {{.Attrs.x}} syntax.
func RenderWith(t model.Template, contact model.Contact, v Vars) (Rendered, error) {
	d := data{Contact: contact, Attrs: contact.Attrs, UnsubscribeURL: v.Links.Unsubscribe, PreferencesURL: v.Links.Preferences}
	links := map[string]string{}
	if v.Links.Unsubscribe != "" {
		links["unsubscribe"] = v.Links.Unsubscribe
	}
	if v.Links.Preferences != "" {
		links["preferences"] = v.Links.Preferences
	}
	camp := v.Campaign
	if camp.Channel == "" {
		camp.Channel = t.Channel
	}
	if camp.Template == "" {
		camp.Template = t.Name
	}
	r := personalize.New(personalize.Vars{Contact: contact, Event: v.Event, Campaign: camp, Links: links})
	var firstErr error
	text := func(field, s string) string {
		if s == "" || firstErr != nil {
			return s
		}
		out, err := r.Text(field, s, d)
		if err != nil {
			firstErr = fmt.Errorf("%s: %w", field, err)
			return ""
		}
		return out
	}

	var out Rendered
	// Subjects become mail headers: strip CR/LF so attribute values cannot inject headers.
	out.Subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(text("subject", t.Subject))
	if t.HTML != "" {
		h, err := r.HTML("html", t.HTML, d)
		if err != nil {
			return out, fmt.Errorf("html: %w", err)
		}
		out.HTML = h
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
	if c.AMP != "" && firstErr == nil {
		// The client's amp-mustache templates ({{message}}) are cut out first: they aren't ours.
		src, restore := amp.Protect(c.AMP)
		h, err := r.HTML("amp", src, d)
		if err != nil {
			return out, fmt.Errorf("amp: %w", err)
		}
		c.AMP = restore(h)
	}
	c.AMPBlocks = nil // the builder's document isn't part of a message
	c.Title, c.Body = text("title", c.Title), text("body", c.Body)
	c.ImageURL, c.URL = text("image_url", c.ImageURL), text("url", c.URL)
	for k, val := range c.Data {
		c.Data[k] = text("data."+k, val)
	}
	if (t.Channel == Email || t.Channel == "") && strings.TrimSpace(c.Text) == "" && out.HTML != "" {
		c.Text, out.TextGenerated = TextFromHTML(out.HTML), true
	}
	out.Content = c
	out.Missing = r.Missing()
	return out, firstErr
}
