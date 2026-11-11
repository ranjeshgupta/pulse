// Package forms validates form definitions and turns a submission into profile changes.
//
// A form may span several pages (page_break fields split them; each page is checked before the
// next is shown, and everything again on submit) and may take file uploads (file fields: up to
// MaxFileFields, each with its allowed types and a size limit; the type is checked against the
// file's content, see assets.DetectUpload). Uploaded files are kept with the workspace's assets
// but are private: only signed-in users with contacts access can download them.
//
// A submission becomes events for one contact, in order: identify (the mapped attributes),
// resubscribed (channels the form opts in to), list_joined, tag_added and form_submitted. They go
// through the normal pipeline, so a submission updates segments and starts journeys within seconds.
package forms

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"pulse/internal/assets"
	"pulse/internal/attr"
	"pulse/internal/channel"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/segment"
)

const (
	MaxFields   = 30
	maxValueLen = 2000
	// MaxPages is the most pages a form may have; MaxFileFields the most file fields.
	MaxPages      = 10
	MaxFileFields = 5
	// DefaultFileMB and MaxFileMB bound a file field's size limit.
	DefaultFileMB = 5
	MaxFileMB     = 10

	TypeFile      = "file"
	TypePageBreak = "page_break"

	// Submitted is the event every submission records; props.form_id says which form.
	Submitted = "form_submitted"
	// ConsentField is the checkbox shown when a form has consent text.
	ConsentField = "_consent"
)

var (
	types = map[string]bool{"text": true, "email": true, "tel": true, "number": true, "textarea": true,
		"select": true, "checkbox": true, "hidden": true, TypeFile: true, TypePageBreak: true}
	e164   = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	colour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Validate checks a form definition. listOK reports whether an id is a static list of the workspace.
func Validate(f model.Form, listOK func(int64) bool) error {
	var errs []error
	if strings.TrimSpace(f.Name) == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if len(f.Fields) == 0 || len(f.Fields) > MaxFields {
		errs = append(errs, fmt.Errorf("a form needs 1 to %d fields", MaxFields))
	}
	seen := map[string]bool{}
	identifies := false
	pages, files := 1, 0
	for i, fd := range f.Fields {
		where := fmt.Sprintf("field %d", i+1)
		if fd.Type == TypePageBreak {
			pages++
			switch {
			case i == 0 || i == len(f.Fields)-1:
				errs = append(errs, fmt.Errorf("%s: a page break goes between fields, not first or last", where))
			case f.Fields[i-1].Type == TypePageBreak:
				errs = append(errs, fmt.Errorf("%s: two page breaks in a row make an empty page", where))
			}
			if len(fd.Label) > 200 {
				errs = append(errs, fmt.Errorf("%s: the page title is too long", where))
			}
			continue
		}
		if fd.Type == TypeFile {
			files++
			if fd.Key == "email" || fd.Key == "phone" {
				errs = append(errs, fmt.Errorf("%s: %s can't be a file field", where, fd.Key))
			}
			if fd.MaxMB < 0 || fd.MaxMB > MaxFileMB {
				errs = append(errs, fmt.Errorf("%s: the size limit must be 1 to %d MB", where, MaxFileMB))
			}
			for _, a := range fd.Accept {
				if !slices.Contains(assets.UploadExtensions(), strings.ToLower(a)) {
					errs = append(errs, fmt.Errorf("%s: %q can't be uploaded (allowed: %s)", where, a, strings.Join(assets.UploadExtensions(), " ")))
				}
			}
		}
		switch {
		case !attr.ValidKey(fd.Key):
			errs = append(errs, fmt.Errorf("%s: %q can't be an attribute name (letters, digits and _ only)", where, fd.Key))
		case consent.IsReserved(fd.Key):
			errs = append(errs, fmt.Errorf("%s: %q is managed by Pulse; forms can't set it", where, fd.Key))
		case seen[fd.Key]:
			errs = append(errs, fmt.Errorf("%s: %q is used twice", where, fd.Key))
		}
		seen[fd.Key] = true
		if !types[fd.Type] {
			errs = append(errs, fmt.Errorf("%s: unknown type %q", where, fd.Type))
		}
		if fd.Type != "hidden" && strings.TrimSpace(fd.Label) == "" {
			errs = append(errs, fmt.Errorf("%s: add a label", where))
		}
		if fd.Type == "select" && len(fd.Options) == 0 {
			errs = append(errs, fmt.Errorf("%s: a dropdown needs options", where))
		}
		if fd.Key == "email" && fd.Type != "email" || fd.Key == "phone" && fd.Type != "tel" {
			errs = append(errs, fmt.Errorf("%s: the %s attribute needs a %s field", where, fd.Key, map[string]string{"email": "email", "phone": "phone (tel)"}[fd.Key]))
		}
		identifies = identifies || fd.Key == "email" || fd.Key == "phone"
	}
	if !identifies {
		errs = append(errs, errors.New("add an email or phone field (key email or phone): it says who the submitter is"))
	}
	if pages > MaxPages {
		errs = append(errs, fmt.Errorf("a form can have at most %d pages", MaxPages))
	}
	if files > MaxFileFields {
		errs = append(errs, fmt.Errorf("a form can have at most %d file fields", MaxFileFields))
	}
	st := f.Settings
	if st.RedirectURL != "" && !strings.HasPrefix(st.RedirectURL, "https://") && !strings.HasPrefix(st.RedirectURL, "http://") {
		errs = append(errs, errors.New("the redirect must be an http(s) URL"))
	}
	for _, id := range st.ListIDs {
		if !listOK(id) {
			errs = append(errs, fmt.Errorf("list %d doesn't exist", id))
		}
	}
	for _, t := range st.Tags {
		if !segment.ValidTag(t) {
			errs = append(errs, fmt.Errorf("invalid tag %q", t))
		}
	}
	for _, ch := range st.Resubscribe {
		if !channel.Valid(ch) {
			errs = append(errs, fmt.Errorf("unknown channel %q", ch))
		}
	}
	if len(st.Resubscribe) > 0 && strings.TrimSpace(st.ConsentText) == "" {
		errs = append(errs, errors.New("opting people in needs consent text they agree to"))
	}
	if st.Theme != "" && !colour.MatchString(st.Theme) {
		errs = append(errs, errors.New("the accent color must look like #2747d6"))
	}
	return errors.Join(errs...)
}

// Visible returns the fields to show. With progressive profiling, a field is skipped when the
// visitor's profile already has a value for it (known is nil for unknown visitors).
// Page breaks stay, except where skipping fields would leave a page empty.
func Visible(f model.Form, known map[string]bool) []model.FormField {
	out := make([]model.FormField, 0, len(f.Fields))
	for _, fd := range f.Fields {
		if fd.Progress && known[fd.Key] && fd.Key != "email" && fd.Key != "phone" && fd.Type != TypePageBreak {
			continue // email and phone always show: they identify the submitter
		}
		if fd.Type == TypePageBreak {
			switch {
			case len(out) == 0:
				continue
			case out[len(out)-1].Type == TypePageBreak:
				out[len(out)-1] = fd // the page between them is empty now: drop it
				continue
			case !shown(out, len(out)):
				continue // the page before it has only hidden fields: they join the next page
			}
		}
		out = append(out, fd)
	}
	for len(out) > 0 && out[len(out)-1].Type == TypePageBreak {
		out = out[:len(out)-1]
	}
	return out
}

// shown reports whether the page ending at out[:end] has a field the visitor sees (not hidden).
func shown(out []model.FormField, end int) bool {
	for i := end - 1; i >= 0 && out[i].Type != TypePageBreak; i-- {
		if out[i].Type != "hidden" {
			return true
		}
	}
	return false
}

// Page is one page of a form: its title (the page break's label; the first page has none) and fields.
type Page struct {
	Title  string
	Fields []model.FormField
}

// Pages splits fields at their page breaks. A form without breaks is one page.
func Pages(fields []model.FormField) []Page {
	pages := []Page{{}}
	for _, fd := range fields {
		if fd.Type == TypePageBreak {
			pages = append(pages, Page{Title: fd.Label})
			continue
		}
		pages[len(pages)-1].Fields = append(pages[len(pages)-1].Fields, fd)
	}
	return pages
}

// MaxBytes is a file field's size limit in bytes.
func MaxBytes(fd model.FormField) int64 {
	mb := fd.MaxMB
	if mb <= 0 {
		mb = DefaultFileMB
	}
	return int64(mb) << 20
}

// Upload is a file sent for a file field.
type Upload struct {
	Filename string
	Data     []byte
}

// File is an accepted upload: its safe name and the content type found from its content.
type File struct {
	Key         string
	Filename    string
	ContentType string
	Data        []byte
}

// ParseFiles checks the uploads of the visible file fields: required, size, and type (from the
// file's content, among the field's allowed extensions).
func ParseFiles(visible []model.FormField, uploads map[string]Upload) ([]File, []FieldError) {
	var out []File
	var errs []FieldError
	for _, fd := range visible {
		if fd.Type != TypeFile {
			continue
		}
		u, ok := uploads[fd.Key]
		if !ok || len(u.Data) == 0 {
			if fd.Required {
				errs = append(errs, FieldError{Field: fd.Key, Message: "Choose a file."})
			}
			continue
		}
		if int64(len(u.Data)) > MaxBytes(fd) {
			errs = append(errs, FieldError{Field: fd.Key, Message: fmt.Sprintf("The file is too big: %d MB at most.", MaxBytes(fd)>>20)})
			continue
		}
		name := assets.SafeFilename(u.Filename)
		ct, err := assets.DetectUpload(name, u.Data, fd.Accept)
		if err != nil {
			msg := err.Error()
			errs = append(errs, FieldError{Field: fd.Key, Message: strings.ToUpper(msg[:1]) + msg[1:] + "."})
			continue
		}
		out = append(out, File{Key: fd.Key, Filename: name, ContentType: ct, Data: u.Data})
	}
	return out, errs
}

// Known lists which of a contact's attributes have a value.
func Known(c model.Contact) map[string]bool {
	k := map[string]bool{}
	for key, v := range c.Attrs {
		if attr.String(v) != "" {
			k[key] = true
		}
	}
	if c.Email != "" {
		k["email"] = true
	}
	return k
}

// FieldError is a problem with one submitted value.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Submission is a validated submission: the attributes to set and how to identify the contact.
type Submission struct {
	Attrs   map[string]any
	Email   string
	Phone   string
	Values  map[string]any // everything submitted, for the form_submitted event
	Consent bool
}

// Parse validates submitted values against the visible fields. Hidden fields take their default
// when nothing is sent.
func Parse(f model.Form, visible []model.FormField, values map[string]string) (Submission, []FieldError) {
	sub := Submission{Attrs: map[string]any{}, Values: map[string]any{}}
	var errs []FieldError
	bad := func(key, msg string) { errs = append(errs, FieldError{Field: key, Message: msg}) }
	for _, fd := range visible {
		if fd.Type == TypeFile || fd.Type == TypePageBreak {
			continue // files: ParseFiles; page breaks have no value
		}
		raw := strings.TrimSpace(values[fd.Key])
		if fd.Type == "hidden" && raw == "" {
			raw = fd.Default
		}
		if len(raw) > maxValueLen {
			bad(fd.Key, "This is too long.")
			continue
		}
		if fd.Type == "checkbox" {
			on := raw == "on" || raw == "true" || raw == "1" || raw == "yes"
			if fd.Required && !on {
				bad(fd.Key, "Please tick this box.")
				continue
			}
			sub.Attrs[fd.Key], sub.Values[fd.Key] = on, on
			continue
		}
		if raw == "" {
			if fd.Required {
				bad(fd.Key, "This is required.")
			}
			continue
		}
		var v any = raw
		switch fd.Type {
		case "email":
			a, err := mail.ParseAddress(raw)
			if err != nil || a.Address != raw || !strings.Contains(raw, ".") {
				bad(fd.Key, "Enter a valid email address.")
				continue
			}
			v = strings.ToLower(raw)
		case "tel":
			p := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(raw)
			if !e164.MatchString(p) {
				bad(fd.Key, "Enter the number with its country code, like +91 98123 45678.")
				continue
			}
			v = p
		case "number":
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				bad(fd.Key, "Enter a number.")
				continue
			}
			v = n
		case "select":
			ok := false
			for _, o := range fd.Options {
				ok = ok || o == raw
			}
			if !ok {
				bad(fd.Key, "Choose one of the options.")
				continue
			}
		}
		sub.Values[fd.Key] = v
		switch fd.Key {
		case "email":
			sub.Email = v.(string)
		case "phone":
			sub.Phone = v.(string)
			sub.Attrs[fd.Key] = v
		default:
			sub.Attrs[fd.Key] = v
		}
	}
	if strings.TrimSpace(f.Settings.ConsentText) != "" {
		c := values[ConsentField]
		sub.Consent = c == "on" || c == "true" || c == "1" || c == "yes"
		if !sub.Consent {
			bad(ConsentField, "Please agree to continue.")
		}
	}
	if len(errs) == 0 && sub.Email == "" && sub.Phone == "" {
		bad("email", "Enter your email address or phone number.")
	}
	return sub, errs
}

// Events are the events a submission records for externalID. anonymousID (a web SDK visitor) is
// merged into the contact.
func Events(f model.Form, sub Submission, externalID, anonymousID string, meta map[string]any) []event.Event {
	id := event.NewID()
	identify := event.Event{ID: id + ":id", ExternalID: externalID, Name: event.Identify, Email: sub.Email, Attrs: sub.Attrs}
	if anonymousID != "" && anonymousID != externalID {
		identify.Props = map[string]any{"anonymous_id": anonymousID}
	}
	evs := []event.Event{identify}
	if sub.Consent {
		for _, ch := range f.Settings.Resubscribe {
			evs = append(evs, event.Event{ID: id + ":rs:" + ch, ExternalID: externalID, Name: consent.Resubscribed,
				Props: map[string]any{"channel": ch, "source": "form", "form_id": f.ID}})
		}
	}
	for _, l := range f.Settings.ListIDs {
		evs = append(evs, event.Event{ID: id + ":l:" + strconv.FormatInt(l, 10), ExternalID: externalID, Name: event.ListJoined,
			Props: map[string]any{"list_id": l, "source": "form", "form_id": f.ID}})
	}
	if len(f.Settings.Tags) > 0 {
		tags := make([]any, len(f.Settings.Tags))
		for i, t := range f.Settings.Tags {
			tags[i] = t
		}
		evs = append(evs, event.Event{ID: id + ":t", ExternalID: externalID, Name: event.TagAdded, Props: map[string]any{"tags": tags, "source": "form"}})
	}
	props := map[string]any{"form_id": f.ID, "form_name": f.Name, "fields": sub.Values}
	for k, v := range meta {
		props[k] = v
	}
	return append(evs, event.Event{ID: id, ExternalID: externalID, Name: Submitted, Props: props})
}
