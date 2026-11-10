// Package forms validates form definitions and turns a submission into profile changes.
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
	"strconv"
	"strings"

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

	// Submitted is the event every submission records; props.form_id says which form.
	Submitted = "form_submitted"
	// ConsentField is the checkbox shown when a form has consent text.
	ConsentField = "_consent"
)

var (
	types = map[string]bool{"text": true, "email": true, "tel": true, "number": true, "textarea": true,
		"select": true, "checkbox": true, "hidden": true}
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
	for i, fd := range f.Fields {
		where := fmt.Sprintf("field %d", i+1)
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
func Visible(f model.Form, known map[string]bool) []model.FormField {
	out := make([]model.FormField, 0, len(f.Fields))
	for _, fd := range f.Fields {
		if fd.Progress && known[fd.Key] && fd.Key != "email" && fd.Key != "phone" {
			continue // email and phone always show: they identify the submitter
		}
		out = append(out, fd)
	}
	return out
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
