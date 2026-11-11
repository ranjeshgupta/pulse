package journey

import (
	"encoding/json"
	"strings"
	"testing"
)

func clip(t *testing.T, s string) Clipboard {
	t.Helper()
	c, err := ParseClipboard([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCheckClipboard(t *testing.T) {
	// A wait, an email, a decision watching the email, a jump to a step outside the selection, and a
	// send_message whose WhatsApp template is from elsewhere and whose next step wasn't copied.
	c := clip(t, `{"pulse":"journey-steps","version":1,"entry":"w1","from":3,"nodes":{
		"w1":{"type":"wait","seconds":3600,"next":"e1"},
		"e1":{"type":"send_email","template_id":7,"next":"d1","deadline":{"at":"21:00"}},
		"d1":{"type":"message_decision","step":"e1","outcome":"opened","seconds":86400,"yes":"j1","no":"m1"},
		"j1":{"type":"jump","target":"far"},
		"m1":{"type":"send_message","channels":[{"channel":"whatsapp","template_id":99,"fallback_template_id":7},{"channel":"email","template_id":7}],"next":"x9"}
	}}`)
	exists := func(r Ref) bool { return !(r.Kind == "template" && r.ID == 99) }
	out, notes, err := CheckClipboard(c, exists)
	if err != nil {
		t.Fatal(err)
	}
	if out.Nodes["m1"].Next != "" || out.Nodes["w1"].Next != "e1" || out.Nodes["e1"].Next != "d1" {
		t.Errorf("edges: m1.next=%q w1.next=%q", out.Nodes["m1"].Next, out.Nodes["w1"].Next)
	}
	if out.Nodes["j1"].Target != "" || out.Nodes["d1"].Step != "e1" || out.Nodes["d1"].Yes != "j1" {
		t.Errorf("jump %q, decision %+v", out.Nodes["j1"].Target, out.Nodes["d1"])
	}
	if out.Nodes["m1"].Channels[0].TemplateID != 0 || out.Nodes["m1"].Channels[0].FallbackTemplateID != 7 || out.Nodes["m1"].Channels[1].TemplateID != 7 {
		t.Errorf("channels %+v", out.Nodes["m1"].Channels)
	}
	if c.Nodes["m1"].Channels[0].TemplateID != 99 {
		t.Error("the input clipboard was changed")
	}
	if out.Nodes["e1"].Deadline == nil || out.Nodes["e1"].Deadline.At != "21:00" {
		t.Error("the deadline was lost")
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{`"j1": choose where it jumps`, `"m1": template 99 doesn't exist`} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes %q lack %q", joined, want)
		}
	}
	if len(notes) != 2 {
		t.Errorf("notes: %v", notes)
	}
	refs := c.Refs()
	if len(refs) != 2 { // template 7 and template 99, once each
		t.Errorf("refs %v", refs)
	}
	// The cleaned clipboard round-trips.
	b, _ := json.Marshal(out)
	if _, err := ParseClipboard(b); err != nil {
		t.Error(err)
	}
}

func TestCheckClipboardDecisionWithoutItsStep(t *testing.T) {
	c := clip(t, `{"pulse":"journey-steps","version":1,"entry":"d1","nodes":{
		"d1":{"type":"message_decision","step":"e5","outcome":"clicked","seconds":3600,"yes":"","no":""}}}`)
	out, notes, err := CheckClipboard(c, nil)
	if err != nil || out.Nodes["d1"].Step != "" || len(notes) != 1 {
		t.Fatalf("%+v %v %v", out.Nodes["d1"], notes, err)
	}
}

func TestCheckClipboardRefuses(t *testing.T) {
	for name, s := range map[string]string{
		"not ours":     `{"pulse":"something","version":1,"entry":"a","nodes":{"a":{"type":"exit"}}}`,
		"newer":        `{"pulse":"journey-steps","version":2,"entry":"a","nodes":{"a":{"type":"exit"}}}`,
		"no entry":     `{"pulse":"journey-steps","version":1,"entry":"b","nodes":{"a":{"type":"exit"}}}`,
		"empty":        `{"pulse":"journey-steps","version":1,"entry":"a","nodes":{}}`,
		"unknown type": `{"pulse":"journey-steps","version":1,"entry":"a","nodes":{"a":{"type":"teleport"}}}`,
		"bad settings": `{"pulse":"journey-steps","version":1,"entry":"a","nodes":{"a":{"type":"wait","seconds":0}}}`,
		"bad deadline": `{"pulse":"journey-steps","version":1,"entry":"a","nodes":{"a":{"type":"send_sms","template_id":1,"deadline":{"at":"9pm"}}}}`,
		"reserved id":  `{"pulse":"journey-steps","version":1,"entry":"__end","nodes":{"__end":{"type":"exit"}}}`,
	} {
		c, err := ParseClipboard([]byte(s))
		if err != nil {
			continue // refused while decoding is fine too
		}
		if _, _, err := CheckClipboard(c, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseClipboard([]byte(`{"pulse":"journey-steps","version":1,"entry":"a","nodes":{"a":{"type":"exit","evil":1}}}`)); err == nil {
		t.Error("unknown fields should be refused")
	}
	big := `{"pulse":"journey-steps","version":1,"entry":"n0","nodes":{`
	for i := range MaxClipboardSteps + 1 {
		if i > 0 {
			big += ","
		}
		big += `"n` + strings.Repeat("x", i) + `":{"type":"exit"}`
	}
	big += `}}`
	if c, err := ParseClipboard([]byte(big)); err == nil {
		if _, _, err := CheckClipboard(c, nil); err == nil {
			t.Error("too many steps accepted")
		}
	}
}
