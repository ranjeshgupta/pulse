// Package personalize fills message templates with standard variables:
//
//	{{contact.first_name}}           any contact field: email, phone, phone_country, list fields...
//	{{event.items.0.name}}           the triggering event's properties (dotted paths, list indexes,
//	                                 items[].name for every item, joined with ", ")
//	{{campaign.name}}                the journey or broadcast: name, id, type, step, channel, template
//	{{links.unsubscribe}}            the message's own links: unsubscribe, preferences
//
// with pipes for defaults and formats:
//
//	{{contact.first_name | default:"there" | title}}
//	{{event.total | money:"INR"}}  {{event.when | date:"2 Jan 2006"}}  {{event.title | truncate:40}}
//
// The new syntax is translated into Go template actions, so templates written with the older
// {{.Attrs.first_name}} syntax keep working unchanged, the two can be mixed, and HTML output is
// escaped by html/template exactly as before.
package personalize

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Namespaces of the standard variables.
const (
	NSContact  = "contact"
	NSEvent    = "event"
	NSCampaign = "campaign"
	NSLinks    = "links"
	// NSMessage is the message being sent (to, text, subject…), for request templates of the
	// custom HTTP gateway only (message templates have it empty).
	NSMessage = "message"
	// NSBlock is a reusable content block ({{block.footer}}): the renderer puts the block's
	// content in before anything else is filled (ExpandBlocks); one it doesn't know is empty.
	NSBlock = "block"
	// NSReco is the products recommended for the contact at send time (package catalog):
	// {{reco.1.name}}, reco.1.url, image, price, sale_price, currency, category, code; reco.count;
	// reco.last_viewed.name … (what they last looked at without buying).
	NSReco = "reco"
	// NSSurvey is a survey's link for the message's contact: {{survey.12}} (package survey).
	NSSurvey = "survey"
)

// blockRe finds {{block.key}} (spaces allowed inside the braces).
var blockRe = regexp.MustCompile(`\{\{\s*block\.([a-z0-9_]{1,64})\s*\}\}`)

// RecoKeys are the fields of a recommended product; maxReco is how many a message can show.
var RecoKeys = []string{"name", "url", "image", "price", "sale_price", "currency", "category", "code"}

const maxReco = 12

var surveyIDRe = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

// SurveyIDs lists the surveys a template field links to ({{survey.ID}}).
func SurveyIDs(src string) []string {
	var out []string
	for _, m := range surveyRefRe.FindAllStringSubmatch(src, -1) {
		if !contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

var surveyRefRe = regexp.MustCompile(`survey\.([1-9][0-9]{0,17})`)

var recoIndexRe = regexp.MustCompile(`^([1-9]|1[0-2])$`)

// UsesReco reports whether a template field asks for recommendations.
func UsesReco(src string) bool {
	return strings.Contains(src, "reco.") || strings.Contains(src, "block.recommendations")
}

// BlockKeyRe is a content block's key.
var BlockKeyRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// ExpandBlocks puts each {{block.key}}'s content in (one level: blocks inside blocks stay as
// written and render empty). Unknown keys are left for the renderer, which renders them empty.
func ExpandBlocks(src string, blocks map[string]string) string {
	if len(blocks) == 0 || !strings.Contains(src, "block.") {
		return src
	}
	return blockRe.ReplaceAllStringFunc(src, func(m string) string {
		key := blockRe.FindStringSubmatch(m)[1]
		if b, ok := blocks[key]; ok {
			return blockRe.ReplaceAllString(b, "")
		}
		return m
	})
}

// BlockKeys lists the blocks a template uses.
func BlockKeys(src string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range blockRe.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// MessageKeys are the message.* variables of gateway request templates.
var MessageKeys = []string{"id", "channel", "to", "from", "platform", "subject", "html", "text", "title", "body",
	"image_url", "url", "sender_id", "dlt_template_id", "dlt_entity_id", "template_name", "language", "params",
	"header_image_url", "suggestions", "card", "data", "ref", "amp", "provider_id"}

// CampaignKeys are the campaign.* variables.
var CampaignKeys = []string{"name", "id", "type", "step", "channel", "template"}

// LinkKeys are the links.* variables (empty on channels without them, e.g. SMS).
var LinkKeys = []string{"unsubscribe", "preferences"}

// argMode says whether a pipe takes an argument.
type argMode int

const (
	argNone argMode = iota
	argOptional
	argRequired
)

type pipeDef struct {
	fn   string // the Go template function
	arg  argMode
	help string
}

// pipes are the formats a variable can go through, by name.
var pipes = map[string]pipeDef{
	"default":   {"pulseDefault", argRequired, `a value for contacts without one: default:"there"`},
	"upper":     {"pulseUpper", argNone, "UPPER CASE"},
	"lower":     {"pulseLower", argNone, "lower case"},
	"title":     {"pulseTitle", argNone, "Title Case"},
	"date":      {"pulseDate", argOptional, `a date in a Go layout, in the contact's time zone: date:"2 Jan 2006"`},
	"money":     {"pulseMoney", argOptional, `an amount with its currency (INR when none): money:"INR"`},
	"number":    {"pulseNumber", argOptional, "a number with thousands separators; number:2 keeps 2 decimals"},
	"truncate":  {"pulseTruncate", argRequired, "at most N characters, ending with …: truncate:40"},
	"urlencode": {"pulseURLEncode", argNone, "safe to put in a link's query string"},
	"json":      {"pulseJSON", argNone, "a JSON value (webhook bodies): \"Asha\", 12, [..]"},
}

// PipeNames lists the pipes, sorted.
func PipeNames() []string {
	out := make([]string, 0, len(pipes))
	for n := range pipes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Pipe is one format applied to a variable.
type Pipe struct {
	Name string `json:"name"`
	Arg  string `json:"arg,omitempty"`
}

// Ref is a standard variable used in a template.
type Ref struct {
	Var        string `json:"var"` // e.g. contact.first_name
	Pipes      []Pipe `json:"pipes,omitempty"`
	HasDefault bool   `json:"has_default,omitempty"`
}

var (
	segRe   = regexp.MustCompile(`^[A-Za-z0-9_]+(\[\])?$`)
	identRe = regexp.MustCompile(`^[a-z_]+$`)
)

const maxPathSegments = 6

// IsStandard reports whether an action's text (between the braces, trimmed) uses the standard
// syntax: it starts with contact., event., campaign. or links.
func IsStandard(action string) bool {
	for _, ns := range []string{NSContact, NSEvent, NSCampaign, NSLinks, NSMessage, NSBlock, NSReco, NSSurvey} {
		if strings.HasPrefix(action, ns+".") {
			return true
		}
	}
	return false
}

// Translate rewrites the standard-variable actions of a template into Go template actions and
// returns the variables it found. Other actions ({{.Attrs.x}}, {{if}}, …) are kept as written.
// unescape decodes HTML character references inside standard actions first (an HTML editor may
// write default:"x" as default:&quot;x&quot;).
func Translate(src string, unescape bool) (string, []Ref, error) {
	if !strings.Contains(src, "{{") {
		return src, nil, nil
	}
	var b strings.Builder
	var refs []Ref
	i := 0
	for {
		start := strings.Index(src[i:], "{{")
		if start < 0 {
			b.WriteString(src[i:])
			break
		}
		start += i
		end := actionEnd(src, start+2)
		if end < 0 {
			if IsStandard(strings.TrimSpace(strings.TrimLeft(src[start+2:], "- "))) {
				return "", refs, fmt.Errorf("%s: a quote is not closed or }} is missing", src[start:min(len(src), start+60)])
			}
			b.WriteString(src[i:]) // unterminated: the template parser reports it
			break
		}
		b.WriteString(src[i:start])
		inner := src[start+2 : end]
		lt, rt := "", ""
		body := inner
		if strings.HasPrefix(body, "-") && len(body) > 1 && (body[1] == ' ' || body[1] == '\t' || body[1] == '\n') {
			lt, body = "- ", body[2:]
		}
		if strings.HasSuffix(body, "-") && len(body) > 1 {
			if c := body[len(body)-2]; c == ' ' || c == '\t' || c == '\n' {
				rt, body = " -", body[:len(body)-2]
			}
		}
		trimmed := strings.TrimSpace(body)
		if unescape && strings.Contains(trimmed, "&") {
			trimmed = strings.TrimSpace(html.UnescapeString(trimmed))
		}
		if !IsStandard(trimmed) {
			b.WriteString(src[start : end+2])
			i = end + 2
			continue
		}
		ref, err := parseAction(trimmed)
		if err != nil {
			return "", refs, fmt.Errorf("{{%s}}: %w", trimmed, err)
		}
		refs = append(refs, ref)
		b.WriteString("{{" + lt + goAction(ref) + rt + "}}")
		i = end + 2
	}
	return b.String(), refs, nil
}

// Refs lists the standard variables a template uses (errors are ignored; Translate reports them).
func Refs(src string) []Ref {
	var out []Ref
	i := 0
	for {
		start := strings.Index(src[i:], "{{")
		if start < 0 {
			return out
		}
		start += i
		end := actionEnd(src, start+2)
		if end < 0 {
			return out
		}
		body := strings.Trim(strings.TrimSpace(src[start+2:end]), "- \t\n")
		body = html.UnescapeString(body)
		if IsStandard(body) {
			if r, err := parseAction(body); err == nil {
				out = append(out, r)
			}
		}
		i = end + 2
	}
}

// actionEnd finds the "}}" closing an action whose text starts at from, skipping quoted strings.
func actionEnd(s string, from int) int {
	var quote byte
	for j := from; j < len(s); j++ {
		c := s[j]
		switch {
		case quote != 0:
			if c == '\\' && quote != '`' {
				j++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '`' || c == '\'':
			quote = c
		case c == '}' && j+1 < len(s) && s[j+1] == '}':
			return j
		}
	}
	return -1
}

// parseAction parses "ns.path | pipe:arg | pipe".
func parseAction(s string) (Ref, error) {
	parts, err := splitPipes(s)
	if err != nil {
		return Ref{}, err
	}
	ref := Ref{Var: strings.TrimSpace(parts[0])}
	if err := checkVar(ref.Var); err != nil {
		return Ref{}, err
	}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		name, rawArg, hasArg := strings.Cut(p, ":")
		name = strings.TrimSpace(name)
		def, ok := pipes[name]
		if !identRe.MatchString(name) || !ok {
			return Ref{}, fmt.Errorf("unknown pipe %q (use %s)", name, strings.Join(PipeNames(), ", "))
		}
		var arg string
		if hasArg {
			if def.arg == argNone {
				return Ref{}, fmt.Errorf("%s takes no value", name)
			}
			if arg, err = parseArg(strings.TrimSpace(rawArg)); err != nil {
				return Ref{}, fmt.Errorf("%s: %w", name, err)
			}
		} else if def.arg == argRequired {
			return Ref{}, fmt.Errorf("%s needs a value, e.g. %s", name, def.help[strings.LastIndex(def.help, ":")+1:])
		}
		if name == "truncate" {
			if n, err := strconv.Atoi(arg); err != nil || n < 1 || n > 10000 {
				return Ref{}, fmt.Errorf("truncate needs a number of characters, e.g. truncate:40")
			}
		}
		if name == "number" && hasArg {
			if n, err := strconv.Atoi(arg); err != nil || n < 0 || n > 6 {
				return Ref{}, fmt.Errorf("number takes 0 to 6 decimals, e.g. number:2")
			}
		}
		if name == "default" {
			ref.HasDefault = true
		}
		ref.Pipes = append(ref.Pipes, Pipe{Name: name, Arg: arg})
	}
	return ref, nil
}

// splitPipes splits on | outside quotes.
func splitPipes(s string) ([]string, error) {
	var out []string
	var quote byte
	last := 0
	for j := 0; j < len(s); j++ {
		c := s[j]
		switch {
		case quote != 0:
			if c == '\\' {
				j++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '|':
			out = append(out, s[last:j])
			last = j + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("a quote is not closed")
	}
	return append(out, s[last:]), nil
}

// parseArg reads a pipe's value: "quoted", 'quoted' or a bare word.
func parseArg(a string) (string, error) {
	if a == "" {
		return "", fmt.Errorf("the value is empty")
	}
	switch a[0] {
	case '"':
		v, err := strconv.Unquote(a)
		if err != nil {
			return "", fmt.Errorf("bad quoted value %s", a)
		}
		return v, nil
	case '\'':
		if len(a) < 2 || a[len(a)-1] != '\'' {
			return "", fmt.Errorf("bad quoted value %s", a)
		}
		return a[1 : len(a)-1], nil
	}
	if strings.ContainsAny(a, " \t\n\"'") {
		return "", fmt.Errorf("put the value in quotes: %q", a)
	}
	return a, nil
}

// checkVar validates a variable name: a namespace and a path.
func checkVar(v string) error {
	ns, path, _ := strings.Cut(v, ".")
	segs := strings.Split(path, ".")
	if path == "" || len(segs) > maxPathSegments {
		return fmt.Errorf("name a %s field after the dot", ns)
	}
	lists := 0
	for _, s := range segs {
		if !segRe.MatchString(s) {
			return fmt.Errorf("%q isn't a field name (letters, digits and _)", s)
		}
		if strings.HasSuffix(s, "[]") {
			lists++
		}
	}
	if lists > 1 {
		return fmt.Errorf("a path goes through one list ([]) at most")
	}
	switch ns {
	case NSCampaign:
		if len(segs) != 1 || !contains(CampaignKeys, path) {
			return fmt.Errorf("campaign has %s", strings.Join(CampaignKeys, ", "))
		}
	case NSLinks:
		if len(segs) != 1 || !contains(LinkKeys, path) {
			return fmt.Errorf("links has %s", strings.Join(LinkKeys, ", "))
		}
	case NSMessage:
		if !contains(MessageKeys, segs[0]) {
			return fmt.Errorf("message has %s", strings.Join(MessageKeys, ", "))
		}
	case NSSurvey:
		if len(segs) != 1 || !surveyIDRe.MatchString(path) {
			return fmt.Errorf("a survey link is {{survey.ID}}, the survey's number")
		}
	case NSReco:
		if !(segs[0] == "count" && len(segs) == 1) && !(len(segs) == 2 && (segs[0] == "last_viewed" || recoIndexRe.MatchString(segs[0])) && contains(RecoKeys, segs[1])) {
			return fmt.Errorf("reco is reco.1.name … reco.%d.name (%s), reco.last_viewed.name or reco.count", maxReco, strings.Join(RecoKeys, ", "))
		}
	case NSBlock:
		if len(segs) != 1 || !BlockKeyRe.MatchString(path) {
			return fmt.Errorf("a content block is {{block.key}} with a lower-case key")
		}
	}
	return nil
}

// goAction is the Go template pipeline for a standard variable.
func goAction(r Ref) string {
	fn := "pulseVar"
	if r.HasDefault {
		fn = "pulseVarOpt"
	}
	var b strings.Builder
	b.WriteString(fn + " " + strconv.Quote(r.Var))
	for _, p := range r.Pipes {
		def := pipes[p.Name]
		b.WriteString(" | " + def.fn)
		if def.arg != argNone {
			b.WriteString(" " + strconv.Quote(p.Arg))
		}
	}
	b.WriteString(" | pulseOut")
	return b.String()
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
