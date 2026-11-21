package personalize

import (
	"runtime"
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
		// Strings built in variables and never written out.
		`{{$x := print 1}}{{range 40}}{{$x = print $x $x}}{{end}}`,
		`{{$x := print 1}}{{range 40}}{{$x = printf "%s%s" $x $x}}{{end}}`,
		`{{$x := print 1}}{{range 40}}{{$x = println $x $x}}{{end}}`,
		`{{$x := print 1}}{{range 60}}{{$x = html $x $x "<>"}}{{end}}`,
		`{{$x := print 1}}{{range 60}}{{$x = js $x $x "<>"}}{{end}}`,
		`{{$x := print 1}}{{range 60}}{{$x = urlquery $x $x "&"}}{{end}}`,
		`{{range 9000}}{{$y := html $.Attrs.s}}{{end}}`,
		`{{range 9000}}{{$y := slice (slice $.Attrs.s 0) 1}}{{end}}`,
		`{{range 9000}}{{$y := pulseUpper $.Attrs.s}}{{end}}`,
		`{{$x := printf "%999999999d" 1}}`,
		`{{$x := printf "%.999999999f" 1.0}}`,
		`{{$x := printf "%*d" 999999999 1}}`,
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

	// The doubling payload fails fast and in little memory, with a clear error.
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := r.Text("f", `{{$x := print 1}}{{range 40}}{{$x = print $x $x}}{{end}}`, dot)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "builds more than") {
		t.Errorf("doubling: err = %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 4*maxWork {
		t.Errorf("doubling allocated %d MB", grew>>20)
	}

	// Ordinary loops and calls still work.
	for _, c := range []struct{ src, want string }{
		{`{{range .Attrs.list}}{{.}}{{end}}`, "abc"},
		{`{{range $i, $e := .Attrs.list}}{{$i}}{{end}}`, "012"},
		{`{{range 3}}x{{end}}`, "xxx"},
		{`{{range .Attrs.missing}}x{{else}}none{{end}}`, "none"},
		{`{{define "d"}}[{{.}}]{{end}}{{template "d" "v"}}{{template "d"}}`, "[v][]"},
		{`{{print "a" 1}}|{{printf "%03d-%s" 7 "x"}}|{{println "z"}}`, "a1|007-x|z\n"},
		{`{{html "<b>"}} {{js "'"}} {{urlquery "a b"}} {{slice "hello" 1 3}} {{index (slice .Attrs.list 1) 0}}`, `&lt;b&gt; \' a+b el b`},
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
