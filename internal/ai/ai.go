// Package ai is AI help for marketers (O6): subject line ideas, rewriting a message's words in
// another tone or length, translating a template into a language, and a segment from a sentence.
// It calls Claude (Anthropic's Messages API) with a JSON schema for every answer, and checks the
// answer before anyone sees it: rewrites and translations must keep every {{variable}} and
// {{block}} exactly, and a segment rule must pass the same validation as one built by hand.
// It is on when the server has an API key (PULSE_AI_API_KEY); nothing is sent otherwise.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Model is the Claude model used.
const Model = "claude-opus-5-5"

// ErrRefused: the model declined the request.
var ErrRefused = errors.New("the AI declined this request; try different wording")

// ErrAnswerTooLong: the answer hit the output limit (it would be cut off).
var ErrAnswerTooLong = errors.New("the answer was too long for AI help; try a shorter text")

// Input limits: a rewrite's text and a translation's fields (as JSON), in bytes.
const (
	MaxRewrite   = 60_000
	MaxTranslate = 120_000
)

// TooLongError: the input is over a limit (it is refused, never cut).
type TooLongError struct{ Size, Limit int }

func (e *TooLongError) Error() string {
	return fmt.Sprintf("too long for AI help: %d KB, the limit is %d KB", (e.Size+999)/1000, e.Limit/1000)
}

// Completer returns JSON for a prompt, matching schema (the Claude client, or a fake in tests).
type Completer interface {
	JSON(ctx context.Context, system, user string, schema map[string]any) ([]byte, error)
}

// Claude completes through the Anthropic API.
type Claude struct{ api anthropic.Client }

// NewClaude makes a client with the server's API key.
func NewClaude(apiKey string) *Claude {
	return &Claude{api: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithMaxRetries(2))}
}

func (c *Claude) JSON(ctx context.Context, system, user string, schema map[string]any) ([]byte, error) {
	resp, err := c.api.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     Model,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user))},
		// A policy refusal is re-served by a fallback model in the same call.
		Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
		Betas:        []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		OutputConfig: anthropic.BetaOutputConfigParam{Format: anthropic.BetaJSONOutputFormatParam{Schema: schema}},
	})
	if err != nil {
		return nil, err
	}
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return nil, ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return nil, ErrAnswerTooLong
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	if b.Len() == 0 {
		return nil, fmt.Errorf("the AI returned no answer (stop reason %s)", resp.StopReason)
	}
	return []byte(b.String()), nil
}

// Helper runs the tasks.
type Helper struct{ C Completer }

const baseSystem = `You help marketers write messages in a customer engagement platform.
Placeholders such as {{contact.first_name | default:"there"}}, {{event.product}}, {{block.footer}},
{{reco.1.name}} and {{survey.12}} are filled per recipient when the message is sent: keep every one
exactly as written (same text, same count), never invent new ones, and never translate them.
Write plainly, without hype the brand didn't ask for. Answer only with the JSON asked for.`

var tagRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// Tags lists the placeholders in s, sorted (repeats kept).
func Tags(s string) []string {
	out := tagRe.FindAllString(s, -1)
	for i, t := range out { // {{ a }} and {{a}} are the same placeholder
		out[i] = "{{" + strings.Join(strings.Fields(t[2:len(t)-2]), " ") + "}}"
	}
	sort.Strings(out)
	return out
}

// SameTags reports whether b keeps exactly a's placeholders.
func SameTags(a, b string) bool {
	x, y := Tags(a), Tags(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func strList(n int) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": n}
}

func object(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	sort.Strings(req)
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

// SubjectLines suggests subject lines (or push titles) for a message's content.
func (h Helper) SubjectLines(ctx context.Context, content, current string, n int) ([]string, error) {
	n = max(1, min(n, 10))
	user := fmt.Sprintf("Suggest %d different subject lines for this email, each under 60 characters, varied in angle (benefit, curiosity, urgency, personal). You may use {{contact.first_name | default:\"there\"}} in at most one.\n\nCurrent subject: %s\n\nEmail content:\n%s", n, current, clip(content, 20000))
	raw, err := h.C.JSON(ctx, baseSystem, user, object(map[string]any{"subjects": strList(n)}))
	if err != nil {
		return nil, err
	}
	var out struct{ Subjects []string }
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	var keep []string
	for _, s := range out.Subjects {
		if s = strings.TrimSpace(s); s != "" && len(s) <= 200 {
			keep = append(keep, s)
		}
	}
	if len(keep) == 0 {
		return nil, errors.New("the AI didn't suggest anything usable; try again")
	}
	return keep, nil
}

// Styles of rewriting.
var Styles = map[string]string{
	"shorter":    "Make it about half as long, keeping the key message and call to action.",
	"friendlier": "Make it warmer and more conversational.",
	"urgent":     "Add honest urgency (no false deadlines or claims).",
	"formal":     "Make it more formal and professional.",
	"fix":        "Fix spelling, grammar and punctuation only; change nothing else.",
	"simpler":    "Use simpler words and shorter sentences.",
}

// Rewrite rewrites a message's words in a style; HTML markup is kept. The placeholders must come
// back exactly.
func (h Helper) Rewrite(ctx context.Context, text, style, channel string) (string, error) {
	how, ok := Styles[style]
	if !ok {
		return "", fmt.Errorf("style: one of %s", strings.Join(keys(Styles), ", "))
	}
	if len(text) > MaxRewrite {
		return "", &TooLongError{Size: len(text), Limit: MaxRewrite}
	}
	user := fmt.Sprintf("Rewrite this %s message. %s If it is HTML, keep the markup, links and images and change only the words.\n\n%s", channel, how, text)
	raw, err := h.C.JSON(ctx, baseSystem, user, object(map[string]any{"text": map[string]any{"type": "string"}}))
	if err != nil {
		return "", err
	}
	var out struct{ Text string }
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Text) == "" {
		return "", errors.New("the AI returned nothing; try again")
	}
	if !SameTags(text, out.Text) {
		return "", errors.New("the AI changed the {{…}} placeholders, so its version was thrown away; try again")
	}
	return out.Text, nil
}

// Translate translates a template's fields (subject, html, text, title, body…) into language.
func (h Helper) Translate(ctx context.Context, fields map[string]string, language string) (map[string]string, error) {
	keep := map[string]string{}
	for k, v := range fields {
		if strings.TrimSpace(v) != "" {
			keep[k] = v
		}
	}
	if len(keep) == 0 {
		return nil, errors.New("there's nothing to translate")
	}
	in, _ := json.Marshal(keep)
	if len(in) > MaxTranslate {
		return nil, &TooLongError{Size: len(in), Limit: MaxTranslate}
	}
	props := map[string]any{}
	for k := range keep {
		props[k] = map[string]any{"type": "string"}
	}
	user := fmt.Sprintf("Translate each field of this message into the language with code %q, as a native marketer would write it (not word for word). Keep HTML markup, links, numbers, brand and product names. Return the same fields.\n\n%s", language, in)
	raw, err := h.C.JSON(ctx, baseSystem, user, object(props))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	for k, v := range keep {
		if strings.TrimSpace(out[k]) == "" {
			return nil, fmt.Errorf("the AI left %s out; try again", k)
		}
		if !SameTags(v, out[k]) {
			return nil, fmt.Errorf("the AI changed the {{…}} placeholders in %s, so its translation was thrown away; try again", k)
		}
	}
	return out, nil
}

// SegmentRule turns a sentence into a segment rule over the workspace's attribute and event names
// (only names are sent, never contacts' data). The caller validates the rule.
func (h Helper) SegmentRule(ctx context.Context, sentence string, attrs, events []string) (json.RawMessage, string, error) {
	user := fmt.Sprintf(`Turn this audience description into a segment rule.

Description: %s

Rule format (JSON): a condition is one of
- {"attr": "<contact attribute>", "op": "<op>", "value": <value>} with op eq or ne (text, number or true/false),
  contains, not_contains, starts_with or ends_with (text), gt, gte, lt or lte (number), in or not_in (a list),
  exists or not_exists (no value)
- {"event": "<event name>", "op": "gte|gt|eq|lte|lt", "count": <n>, "days": <1-365>}: how many times they did it
  in the last days (did it: op gte, count 1; never did it: op eq, count 0); add "where": [{"prop": "<property>",
  "op": "<op>", "value": <value>}] to count only events whose properties match
- {"tag": "<tag>", "op": "has|not_has"}
and conditions combine with {"all": [...]}, {"any": [...]} or {"none": [...]}.
Use only these contact attributes: %s
and only these events: %s
If the description needs something that isn't there, use the closest match and say so in the note.
Return the rule as a JSON string in "rule", and in "note" one sentence for the marketer on what it selects.`,
		clip(sentence, 2000), strings.Join(firstN(attrs, 300), ", "), strings.Join(firstN(events, 300), ", "))
	raw, err := h.C.JSON(ctx, baseSystem, user, object(map[string]any{"rule": map[string]any{"type": "string"}, "note": map[string]any{"type": "string"}}))
	if err != nil {
		return nil, "", err
	}
	var out struct{ Rule, Note string }
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	if !json.Valid([]byte(out.Rule)) {
		return nil, "", errors.New("the AI's rule wasn't valid JSON; try again")
	}
	return json.RawMessage(out.Rule), out.Note, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func firstN(v []string, n int) []string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
