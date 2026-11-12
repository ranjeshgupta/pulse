// Package eventschema describes the properties a workspace's events carry and checks events
// against them.
//
// A schema names an event's properties, their types, which are required, the allowed values of
// enums (with labels), properties that apply only when another one has certain values (signup
// method "social" → social_provider), which hold personal data, and whether a property's value is
// added to a list field of the contact (a purchase adds product_code to "products").
//
// Schemas never change what an event means: in warn mode (the default) problems are counted per
// event and day with a few recent samples; in enforce mode the server API (POST /v1/events)
// refuses events with problems. Browser and app SDK events are never refused. Checking an event
// costs a map lookup when its name has no schema.
package eventschema

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/segment"
)

// Modes.
const (
	ModeOff     = "off"     // the schema documents the event; nothing is checked
	ModeWarn    = "warn"    // problems are counted and sampled; events are kept
	ModeEnforce = "enforce" // the server API refuses events with problems
)

// Property types.
const (
	TypeString = "string"
	TypeNumber = "number"
	TypeBool   = "bool"
	TypeDate   = "date"
	TypeEnum   = "enum"
	TypeList   = "list"
	TypeObject = "object"
)

var types = map[string]bool{TypeString: true, TypeNumber: true, TypeBool: true, TypeDate: true, TypeEnum: true, TypeList: true, TypeObject: true}

const (
	maxProps      = 100
	maxEnumValues = 200
	maxLabel      = 120
	maxDesc       = 500
)

// Schema is one event's description.
type Schema struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	Mode        string    `json:"mode"`
	Props       []Prop    `json:"props"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Prop is one property. Key names it in the app and in depends_on; Path is where it is in the
// event's props (default Key): a dotted path like product.code, or items[].code for a property
// of every item of a list.
type Prop struct {
	Key         string       `json:"key"`
	Path        string       `json:"path,omitempty"`
	Label       string       `json:"label,omitempty"`
	Description string       `json:"description,omitempty"`
	Type        string       `json:"type"`
	Required    bool         `json:"required,omitempty"`
	Values      []EnumValue  `json:"values,omitempty"`     // enum: the allowed values
	DependsOn   *Dependency  `json:"depends_on,omitempty"` // applies only when another property has one of some values
	Personal    bool         `json:"personal,omitempty"`   // personal data: never shown in samples or copied into messages
	AddToList   string       `json:"add_to_list,omitempty"`
	parsed      segment.Path `json:"-"`
}

// EnumValue is an allowed value of an enum property and its label in the app.
type EnumValue struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Dependency makes a property apply only when the property Prop (a key of the same schema) has
// one of Values: required only then, and a problem when it is sent otherwise.
type Dependency struct {
	Prop   string   `json:"prop"`
	Values []string `json:"values"`
}

// PathOf returns where the property is in an event's props.
func (p Prop) PathOf() string {
	if p.Path != "" {
		return p.Path
	}
	return p.Key
}

// Normalize checks a schema and fills defaults. The name is the event's name.
func (s *Schema) Normalize() error {
	if !event.ValidName(s.Name) {
		return fmt.Errorf("invalid event name %q", s.Name)
	}
	// AMP answers are recorded by Pulse too, but their properties are the marketer's (poll answers,
	// form fields), so they may have a schema for segments and journeys.
	if event.PlatformOnly(s.Name) && s.Name != event.AMPFormSubmitted && s.Name != event.AMPPollAnswered {
		return fmt.Errorf("%s is recorded by Pulse itself and can't have a schema", s.Name)
	}
	switch s.Mode {
	case "":
		s.Mode = ModeWarn
	case ModeOff, ModeWarn, ModeEnforce:
	default:
		return errors.New(`mode must be "off", "warn" or "enforce"`)
	}
	s.Label = strings.TrimSpace(s.Label)
	if len(s.Label) > maxLabel || len(s.Description) > maxDesc {
		return errors.New("label or description too long")
	}
	if len(s.Props) > maxProps {
		return fmt.Errorf("at most %d properties", maxProps)
	}
	if s.Props == nil {
		s.Props = []Prop{}
	}
	keys := map[string]int{}
	paths := map[string]bool{}
	for i := range s.Props {
		p := &s.Props[i]
		if !attr.ValidKey(p.Key) {
			return fmt.Errorf("property %d: the key needs letters, digits and _, starting with a letter", i+1)
		}
		if _, dup := keys[p.Key]; dup {
			return fmt.Errorf("property %q is listed twice", p.Key)
		}
		keys[p.Key] = i
		if p.Path == p.Key {
			p.Path = ""
		}
		parsed, ok := segment.ParsePath(p.PathOf())
		if !ok {
			return fmt.Errorf("property %q: the path must be a name, a dotted path like product.code, or items[].code", p.Key)
		}
		if paths[p.PathOf()] {
			return fmt.Errorf("property %q: another property already reads %s", p.Key, p.PathOf())
		}
		paths[p.PathOf()] = true
		p.parsed = parsed
		if !types[p.Type] {
			return fmt.Errorf("property %q: type must be string, number, bool, date, enum, list or object", p.Key)
		}
		p.Label = strings.TrimSpace(p.Label)
		if len(p.Label) > maxLabel || len(p.Description) > maxDesc {
			return fmt.Errorf("property %q: label or description too long", p.Key)
		}
		if p.Type == TypeEnum {
			if len(p.Values) == 0 || len(p.Values) > maxEnumValues {
				return fmt.Errorf("property %q: an enum needs 1-%d values", p.Key, maxEnumValues)
			}
			seen := map[string]bool{}
			for j := range p.Values {
				v := &p.Values[j]
				v.Value, v.Label = strings.TrimSpace(v.Value), strings.TrimSpace(v.Label)
				if v.Value == "" || len(v.Value) > 200 || len(v.Label) > maxLabel || seen[v.Value] {
					return fmt.Errorf("property %q: enum values must be distinct, non-empty and short", p.Key)
				}
				seen[v.Value] = true
			}
		} else if len(p.Values) > 0 {
			return fmt.Errorf("property %q: only enums have values", p.Key)
		}
		if p.AddToList != "" {
			if !ListTargetAllowed(p.AddToList) {
				return fmt.Errorf("property %q: %q can't be a list field", p.Key, p.AddToList)
			}
			if p.Type == TypeObject || p.Type == TypeList {
				return fmt.Errorf("property %q: only single values (text, numbers, enums) can be added to a list field", p.Key)
			}
		}
	}
	for i := range s.Props {
		p := &s.Props[i]
		d := p.DependsOn
		if d == nil {
			continue
		}
		j, ok := keys[d.Prop]
		if !ok || d.Prop == p.Key {
			return fmt.Errorf("property %q depends on %q, which isn't another property of this event", p.Key, d.Prop)
		}
		if s.Props[j].DependsOn != nil {
			return fmt.Errorf("property %q depends on %q, which depends on another property itself; use one level", p.Key, d.Prop)
		}
		if s.Props[j].parsed.List || p.parsed.List {
			return fmt.Errorf("property %q: dependencies can't use items[] paths", p.Key)
		}
		if len(d.Values) == 0 || len(d.Values) > maxEnumValues {
			return fmt.Errorf("property %q: list the values of %q it applies to", p.Key, d.Prop)
		}
	}
	return nil
}

// Violation is one problem with one event.
type Violation struct {
	Prop    string `json:"prop"`
	Code    string `json:"code"` // missing | type | enum | not_applicable
	Message string `json:"message"`
}

// Check returns the event's problems against the schema (nil when it fits). It runs on every
// checked event, so it allocates only when something is wrong.
func (s *Schema) Check(props map[string]any) []Violation {
	var out []Violation
	for i := range s.Props {
		p := &s.Props[i]
		applies := true
		if d := p.DependsOn; d != nil {
			v, ok := segment.Value(props, s.byKey(d.Prop).PathOf())
			applies = ok && contains(d.Values, attr.String(v))
		}
		vals := p.values(props)
		if !applies {
			for _, v := range vals {
				if v.present && v.v != nil {
					out = append(out, Violation{p.Key, "not_applicable", fmt.Sprintf("%s is only sent when %s is %s", p.Key, p.DependsOn.Prop, strings.Join(p.DependsOn.Values, " or "))})
					break
				}
			}
			continue
		}
		for _, v := range vals {
			if !v.present || v.v == nil {
				if p.Required {
					out = append(out, Violation{p.Key, "missing", p.Key + " is required"})
					break
				}
				continue
			}
			if code, msg := p.checkValue(v.v); code != "" {
				out = append(out, Violation{p.Key, code, msg})
				break
			}
		}
	}
	return out
}

func (s *Schema) byKey(k string) *Prop {
	for i := range s.Props {
		if s.Props[i].Key == k {
			return &s.Props[i]
		}
	}
	return &Prop{Key: k}
}

type value struct {
	v       any
	present bool
}

// values reads the property: one value, or one per item of the list its path goes through (none
// when the list is missing or empty: a required item property then reports the list missing).
func (p *Prop) values(props map[string]any) []value {
	if !p.parsed.List {
		v, ok := segment.Value(props, p.PathOf())
		return []value{{v, ok}}
	}
	list, ok := segment.Value(props, strings.Join(p.parsed.Keys, "."))
	items, _ := list.([]any)
	if !ok || len(items) == 0 {
		return []value{{nil, false}}
	}
	out := make([]value, len(items))
	for i, it := range items {
		m, isObj := it.(map[string]any)
		if len(p.parsed.InItems) == 0 {
			out[i] = value{it, true}
			continue
		}
		if !isObj {
			out[i] = value{nil, false}
			continue
		}
		v, ok := segment.Value(m, strings.Join(p.parsed.InItems, "."))
		out[i] = value{v, ok}
	}
	return out
}

func (p *Prop) checkValue(v any) (code, msg string) {
	switch p.Type {
	case TypeString:
		if _, ok := v.(string); !ok {
			return "type", p.Key + " should be text"
		}
	case TypeNumber:
		if _, ok := v.(float64); !ok {
			return "type", p.Key + " should be a number"
		}
	case TypeBool:
		if _, ok := v.(bool); !ok {
			return "type", p.Key + " should be true or false"
		}
	case TypeDate:
		s, _ := v.(string)
		if _, ok := segment.DatePart(s); !ok {
			return "type", p.Key + " should be a date like 2026-11-30 or an RFC 3339 time"
		}
	case TypeList:
		if _, ok := v.([]any); !ok {
			return "type", p.Key + " should be a list"
		}
	case TypeObject:
		if _, ok := v.(map[string]any); !ok {
			return "type", p.Key + " should be an object"
		}
	case TypeEnum:
		s := attr.String(v)
		for _, e := range p.Values {
			if e.Value == s {
				return "", ""
			}
		}
		return "enum", fmt.Sprintf("%s should be one of %s", p.Key, p.enumList())
	}
	return "", ""
}

func (p *Prop) enumList() string {
	vals := make([]string, 0, len(p.Values))
	for _, e := range p.Values {
		vals = append(vals, e.Value)
	}
	if len(vals) > 8 {
		vals = append(vals[:8], "…")
	}
	return strings.Join(vals, ", ")
}

// ListTargetAllowed reports whether an event schema may add values to the contact field key: a
// custom field only. Pulse's own fields (tags, points, consent, bounces, stage…) and identity
// fields change only through their own events, never through a schema's list adds.
func ListTargetAllowed(key string) bool {
	if !attr.ValidKey(key) || event.EventOnlyAttr(key) || consent.IsReserved(key) {
		return false
	}
	switch key {
	case event.AttrTags, event.AttrPoints, event.AttrFirstSeen, event.AttrLastSeen, event.AttrLocale,
		"email", "phone", "name", "first_name", "last_name", "external_id", "anonymous_id", "user_id", "timezone":
		return false
	}
	return true
}

// ListAdds returns, per contact list field, the values this event adds (the schema's add_to_list
// properties with a value: text, numbers and enums as their canonical strings).
func (s *Schema) ListAdds(props map[string]any) map[string][]string {
	var out map[string][]string
	for i := range s.Props {
		p := &s.Props[i]
		if p.AddToList == "" {
			continue
		}
		for _, v := range p.values(props) {
			if !v.present || v.v == nil {
				continue
			}
			switch v.v.(type) {
			case string, float64, bool:
			default:
				continue
			}
			sv := attr.String(v.v)
			if sv == "" || len(sv) > 200 {
				continue
			}
			if out == nil {
				out = map[string][]string{}
			}
			if !contains(out[p.AddToList], sv) {
				out[p.AddToList] = append(out[p.AddToList], sv)
			}
		}
	}
	return out
}

// PersonalPaths lists the paths of properties marked personal.
func (s *Schema) PersonalPaths() []string {
	var out []string
	for _, p := range s.Props {
		if p.Personal {
			out = append(out, p.PathOf())
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
