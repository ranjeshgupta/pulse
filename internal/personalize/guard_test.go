package personalize

import (
	"strings"
	"testing"
	"time"
)

// A workspace's template must not be able to stall a sender: loops, recursion and output are bounded.
func TestRenderIsBounded(t *testing.T) {
	r := New(vars())
	dot := legacy{vars().Contact, map[string]any{"list": []any{"a", "b", "c"}, "s": strings.Repeat("x", 1<<20)}}
	start := time.Now()
	for _, src := range []string{
		`{{range 1000000000000}}{{end}}`,
		`{{$n := 99999999}}{{range $n}}{{end}}`,
		`{{range $i, $e := .Attrs.list}}{{range 5000}}{{end}}{{end}}`,
		`{{define "a"}}{{template "a"}}{{template "a"}}{{end}}{{template "a"}}`,
		`{{define "a"}}{{template "a" .}}{{template "a" .}}{{end}}{{template "a" .}}`,
		`{{range 9000}}{{$.Attrs.s}}{{end}}`,
	} {
		if _, err := r.Text("f", src, dot); err == nil {
			t.Errorf("text %s: want an error", src)
		}
		if _, err := r.HTML("f", src, dot); err == nil {
			t.Errorf("html %s: want an error", src)
		}
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("bounded renders took %s", d)
	}

	// Ordinary loops and calls still work.
	for _, c := range []struct{ src, want string }{
		{`{{range .Attrs.list}}{{.}}{{end}}`, "abc"},
		{`{{range $i, $e := .Attrs.list}}{{$i}}{{end}}`, "012"},
		{`{{range 3}}x{{end}}`, "xxx"},
		{`{{range .Attrs.missing}}x{{else}}none{{end}}`, "none"},
		{`{{define "d"}}[{{.}}]{{end}}{{template "d" "v"}}{{template "d"}}`, "[v][]"},
	} {
		got, err := r.Text("f", c.src, dot)
		if err != nil || got != c.want {
			t.Errorf("text %s = %q, %v; want %q", c.src, got, err, c.want)
		}
	}
	got, err := r.HTML("f", `<ul>{{range .Attrs.list}}<li>{{.}}</li>{{end}}</ul>{{define "d"}}<b>{{.}}</b>{{end}}{{template "d" "<x>"}}`, dot)
	if err != nil || got != `<ul><li>a</li><li>b</li><li>c</li></ul><b>&lt;x&gt;</b>` {
		t.Errorf("html = %q, %v", got, err)
	}
	js, err := r.JSON("b", `{"l":[{{range $i, $e := .Attrs.list}}{{if $i}},{{end}}"{{$e}}"{{end}}]}`, dot)
	if err != nil || js != `{"l":["a","b","c"]}` {
		t.Errorf("json = %q, %v", js, err)
	}
}

func TestExpandBlocksIsBounded(t *testing.T) {
	big := strings.Repeat("x", 200_000)
	src := strings.Repeat("{{block.big}}", 1000)
	out := ExpandBlocks(src, map[string]string{"big": big})
	if len(out) > MaxExpanded+len(src) {
		t.Fatalf("expanded to %d bytes", len(out))
	}
	if got := ExpandBlocks("a{{block.f}}b{{ block.f }}", map[string]string{"f": "<{{block.g}}>"}); got != "a<>b<>" {
		t.Errorf("got %q", got)
	}
}
