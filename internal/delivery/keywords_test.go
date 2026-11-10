package delivery

import "testing"

func TestKeywords(t *testing.T) {
	k := DefaultKeywords
	for text, want := range map[string]string{
		"STOP": "opt_out", "stop": "opt_out", " Stop. ": "opt_out", "stop all": "opt_out", "Unsubscribe!": "opt_out",
		"start": "opt_in", "UNSTOP": "opt_in",
		"please stop sending me these": "", "stopped": "", "": "", "hello": "",
	} {
		if got, _ := k.Match(text); got != want {
			t.Errorf("Match(%q) = %q, want %q", text, got, want)
		}
	}
	r := Default()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Keywords.OptOut = append(r.Keywords.OptOut, "START")
	if r.Validate() == nil {
		t.Error("a keyword in both lists must be refused")
	}
	r.Keywords = Keywords{OptIn: []string{"START"}}
	if r.Validate() == nil {
		t.Error("removing every opt-out keyword must be refused")
	}
	r.Keywords = Keywords{OptOut: []string{"STOP", "NO MORE"}}
	if r.Validate() == nil {
		t.Error("keywords are single words")
	}
}
