package amp

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzValidate runs arbitrary AMP documents through the validator, the preview, Protect/restore and
// Fill: nothing may panic, and Protect's restore gives the document back.
func FuzzValidate(f *testing.F) {
	f.Add(minimal)
	if out, err := fullDoc().Build(); err == nil {
		f.Add(out)
	}
	f.Add(`<template type="amp-mustache"><p>{{message}}</p></template><form action-xhr="pulse:submit/a"></form>`)
	f.Fuzz(func(t *testing.T, src string) {
		Errors(Validate(src))
		Blocks(src)
		PreviewHTML(src)
		Fill(src, func(kind, block string) string { return "https://x.example/" + kind + "/" + block })
		p, restore := Protect(src)
		if !strings.Contains(src, "PULSEAMPTPL") && restore(p) != src {
			t.Fatalf("Protect/restore changed %q into %q", src, restore(p))
		}
	})
}

// FuzzBuild decodes arbitrary builder documents: a document Build accepts must produce AMP that
// validates.
func FuzzBuild(f *testing.F) {
	if b, err := json.Marshal(fullDoc()); err == nil {
		f.Add(string(b))
	}
	f.Add(`{"blocks":[{"id":"a","type":"text","text":"<b>x</b> {{contact.first_name}}"}]}`)
	f.Add(`{"style":{"accent":"#abc"},"blocks":[{"id":"p","type":"poll","text":"Q","options":["a","b"]}]}`)
	f.Fuzz(func(t *testing.T, raw string) {
		d, err := ParseDoc(json.RawMessage(raw))
		if err != nil || d == nil {
			return
		}
		d.SaveTargets()
		out, err := d.Build()
		if err != nil {
			return
		}
		if ps := Errors(Validate(out)); len(ps) > 0 {
			t.Fatalf("built AMP has problems %v\nfrom %s\n%s", ps, raw, out)
		}
	})
}
