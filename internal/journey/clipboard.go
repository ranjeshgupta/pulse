package journey

// Copy and paste of journey steps. The app copies a selection of steps to the clipboard as JSON
// (Clipboard); pasting sends it to the server first, which checks it (CheckClipboard) and drops
// what can't come along: links to steps that weren't copied, and references to things that don't
// exist in this workspace (a template of another workspace). The app then gives the steps new ids
// and inserts them.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
)

// ClipboardKind marks Pulse's journey-steps clipboard JSON.
const ClipboardKind = "journey-steps"

// MaxClipboardSteps is the most steps one paste takes.
const MaxClipboardSteps = 100

// Clipboard is a copied selection of steps: Entry is the first step (the one the paste point leads
// to); edges to steps outside the selection are empty.
type Clipboard struct {
	Pulse   string          `json:"pulse"`
	Version int             `json:"version"`
	Entry   string          `json:"entry"`
	Nodes   map[string]Node `json:"nodes"`
	// From is the journey the steps were copied from (informational).
	From int64 `json:"from,omitempty"`
}

// ParseClipboard decodes clipboard JSON strictly (unknown fields are refused).
func ParseClipboard(b []byte) (Clipboard, error) {
	var c Clipboard
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Clipboard{}, fmt.Errorf("this isn't a copy of journey steps: %w", err)
	}
	return c, nil
}

// withEdges maps a step's outgoing edges.
func (n Node) withEdges(f func(string) string) Node {
	if n.Type == NodeSplit {
		vs := slices.Clone(n.Variants)
		for i := range vs {
			vs[i].Next = f(vs[i].Next)
		}
		n.Variants = vs
		return n
	}
	switch len(n.edges()) {
	case 1:
		n.Next = f(n.Next)
	case 2:
		n.Yes, n.No = f(n.Yes), f(n.No)
	}
	return n
}

// Ref is something a step refers to by id.
type Ref struct {
	Kind string // template, segment, form, asset, page, journey, focus, company, integration, user
	ID   int64
}

// refs lists a step's references, with a function that removes one.
func (n *Node) refs() []struct {
	Ref
	clear func()
} {
	type rc = struct {
		Ref
		clear func()
	}
	var out []rc
	one := func(kind string, p *int64) {
		if *p > 0 {
			out = append(out, rc{Ref{kind, *p}, func() { *p = 0 }})
		}
	}
	many := func(kind string, p *[]int64) {
		for _, id := range *p {
			out = append(out, rc{Ref{kind, id}, func() {
				*p = slices.DeleteFunc(slices.Clone(*p), func(x int64) bool { return x == id })
			}})
		}
	}
	one("template", &n.TemplateID)
	one("template", &n.FallbackTemplateID)
	for i := range n.Channels {
		one("template", &n.Channels[i].TemplateID)
		one("template", &n.Channels[i].FallbackTemplateID)
	}
	if n.Type == NodeCondition {
		one("segment", &n.SegmentID)
	}
	one("segment", &n.ListID)
	many("segment", &n.AddLists)
	many("segment", &n.RemoveLists)
	one("form", &n.FormID)
	one("asset", &n.AssetID)
	one("page", &n.PageID)
	many("journey", &n.JourneyIDs)
	many("journey", &n.AddJourneys)
	many("journey", &n.RemoveJourneys)
	one("focus", &n.FocusID)
	one("company", &n.CompanyID)
	one("integration", &n.IntegrationID)
	many("user", &n.UserIDs)
	if n.OwnerID != nil {
		one("user", n.OwnerID)
	}
	return out
}

// Refs lists the references of every step of the clipboard (deduplicated).
func (c Clipboard) Refs() []Ref {
	seen := map[Ref]bool{}
	var out []Ref
	for _, id := range c.order() {
		n := c.Nodes[id]
		for _, r := range n.refs() {
			if !seen[r.Ref] {
				seen[r.Ref] = true
				out = append(out, r.Ref)
			}
		}
	}
	return out
}

func (c Clipboard) order() []string {
	ids := make([]string, 0, len(c.Nodes))
	for id := range c.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

const clipEnd = "__end"

// CheckClipboard validates a clipboard and returns it cleaned, with a note for everything dropped:
// edges and jumps to steps that weren't copied, message decisions watching a send step that wasn't
// copied, and references exists says don't exist in this workspace. Steps whose settings are
// invalid make it fail, as saving the journey would.
func CheckClipboard(c Clipboard, exists func(Ref) bool) (Clipboard, []string, error) {
	if c.Pulse != ClipboardKind || c.Version != 1 {
		return c, nil, errors.New("this isn't a copy of journey steps (or it comes from a newer Pulse)")
	}
	if len(c.Nodes) == 0 || len(c.Nodes) > MaxClipboardSteps {
		return c, nil, fmt.Errorf("paste 1 to %d steps at a time", MaxClipboardSteps)
	}
	if _, ok := c.Nodes[c.Entry]; !ok {
		return c, nil, errors.New("the copied steps have no first step")
	}
	var notes []string
	in := func(id string) string {
		if _, ok := c.Nodes[id]; ok {
			return id
		}
		return ""
	}
	out := Clipboard{Pulse: c.Pulse, Version: c.Version, Entry: c.Entry, From: c.From, Nodes: map[string]Node{}}
	for _, id := range c.order() {
		if id == clipEnd || id == "" {
			return c, nil, fmt.Errorf("invalid step id %q", id)
		}
		n := c.Nodes[id].withEdges(in)
		if n.Type == NodeJump && n.Target != "" && in(n.Target) == "" {
			n.Target = ""
			notes = append(notes, fmt.Sprintf("step %q: choose where it jumps to (its target wasn't copied)", id))
		}
		if n.Type == NodeMessageDecision && n.Step != "" && in(n.Step) == "" {
			n.Step = ""
			notes = append(notes, fmt.Sprintf("step %q: choose which message it watches (that send step wasn't copied)", id))
		}
		out.Nodes[id] = n
	}
	// The steps' own settings are checked before references are dropped (a step left without its
	// template is fine to paste: it is chosen again).
	if err := out.validate(); err != nil {
		return c, nil, err
	}
	for _, id := range out.order() {
		n := out.Nodes[id]
		n.Channels = slices.Clone(n.Channels)
		n.AddLists, n.RemoveLists = slices.Clone(n.AddLists), slices.Clone(n.RemoveLists)
		for _, r := range n.refs() {
			if exists != nil && !exists(r.Ref) {
				r.clear()
				notes = append(notes, fmt.Sprintf("step %q: %s %d doesn't exist in this workspace; choose it again", id, r.Kind, r.ID))
			}
		}
		out.Nodes[id] = n
	}
	return out, notes, nil
}

// validate checks the steps' own settings as a journey save would: in a copy where every free edge
// ends the path, and steps whose link was dropped above are stood in for.
func (c Clipboard) validate() error {
	g := Graph{Trigger: Trigger{Type: TriggerEvent, Event: "clipboard_check"}, Entry: c.Entry,
		Nodes: map[string]Node{clipEnd: {Type: NodeExit}}}
	end := func(id string) string {
		if id == "" {
			return clipEnd
		}
		return id
	}
	for id, n := range c.Nodes {
		n = n.withEdges(end)
		switch {
		case n.Type == NodeJump && n.Target == "":
			n.Target = clipEnd
		case n.Type == NodeMessageDecision && n.Step == "":
			n = Node{Type: NodeWait, Seconds: 60, Next: n.Yes}
		}
		g.Nodes[id] = n
	}
	return g.Validate()
}
