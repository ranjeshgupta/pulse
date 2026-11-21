package personalize

import (
	"fmt"
	"net/url"
	"reflect"
	texttemplate "text/template"
)

// Output is capped by limitedBuffer, but a template can also build strings that are never written:
// {{$x := print 1}}{{range 40}}{{$x = print $x $x}}{{end}} doubles a variable forty times. So every
// function that returns text is charged for the bytes it makes against a per-render budget, and
// the Go built-ins that build text (print, printf, println, html, js, urlquery, slice) are replaced
// by versions that check the size before they allocate.
const maxWork = 4 * maxOutput

var errTooMuchWork = fmt.Errorf("the template builds more than %d MB of text", maxWork>>20)

// maxFmtWidth bounds printf widths and precisions (%99999999d would allocate the padding).
const maxFmtWidth = 1000

// charge adds n bytes to the render's budget.
func (r *Renderer) charge(n int) error {
	if n < 0 || r.work+n > maxWork {
		r.work = maxWork + 1
		return errTooMuchWork
	}
	r.work += n
	return nil
}

// argSize estimates the printed size of fmt arguments without printing them: text exactly, other
// values a small allowance (they come from the message data, which is bounded).
func argSize(args []any) int {
	n := 0
	for _, a := range args {
		switch x := a.(type) {
		case string:
			n += len(x)
		case []byte:
			n += len(x)
		case JSONRaw:
			n += len(x)
		case fmt.Stringer, error:
			n += 64
		default:
			n += 32
		}
		n++ // separators
	}
	return n
}

// checkFormat refuses printf formats whose width or precision would allocate a lot (or come from
// an argument), and returns an allowance for the verbs' padding.
func checkFormat(format string) (int, error) {
	extra := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		for i < len(format) && (format[i] == '+' || format[i] == '-' || format[i] == '#' || format[i] == ' ' || format[i] == '0') {
			i++
		}
		for part := 0; part < 2; part++ {
			if i < len(format) && format[i] == '[' { // explicit argument index
				for i < len(format) && format[i] != ']' {
					i++
				}
				i++
			}
			if i < len(format) && format[i] == '*' {
				return 0, fmt.Errorf("printf widths from arguments (%%*) aren't supported in templates")
			}
			w := 0
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				w = w*10 + int(format[i]-'0')
				if w > maxFmtWidth {
					return 0, fmt.Errorf("printf widths and precisions are limited to %d", maxFmtWidth)
				}
				i++
			}
			extra += w
			if part == 0 && i < len(format) && format[i] == '.' {
				i++
				continue
			}
			break
		}
	}
	return extra, nil
}

// boundedFuncs replaces the text-building Go built-ins with versions charged to the budget.
func (r *Renderer) boundedFuncs(f map[string]any) {
	f["print"] = func(args ...any) (string, error) {
		if err := r.charge(argSize(args)); err != nil {
			return "", err
		}
		return fmt.Sprint(args...), nil
	}
	f["println"] = func(args ...any) (string, error) {
		if err := r.charge(argSize(args) + 1); err != nil {
			return "", err
		}
		return fmt.Sprintln(args...), nil
	}
	f["printf"] = func(format string, args ...any) (string, error) {
		pad, err := checkFormat(format)
		if err != nil {
			return "", err
		}
		// %q and %x can double or sextuple text, so text arguments count six times.
		if err := r.charge(len(format) + pad + 6*argSize(args)); err != nil {
			return "", err
		}
		return fmt.Sprintf(format, args...), nil
	}
	escaper := func(factor int, esc func(...any) string) func(...any) (string, error) {
		return func(args ...any) (string, error) {
			if err := r.charge(factor * argSize(args)); err != nil {
				return "", err
			}
			return esc(args...), nil
		}
	}
	f["html"] = escaper(6, texttemplate.HTMLEscaper)
	f["js"] = escaper(6, texttemplate.JSEscaper)
	f["urlquery"] = escaper(3, func(args ...any) string { return url.QueryEscape(evalArgs(args)) })
	f["slice"] = r.slice
}

// evalArgs is how text/template's urlquery prints its arguments.
func evalArgs(args []any) string {
	if len(args) == 1 {
		if s, ok := args[0].(string); ok {
			return s
		}
	}
	return fmt.Sprint(args...)
}

// slice is text/template's slice (a substring or sub-list, which shares memory), charged for its
// length so a loop of slices still counts.
func (r *Renderer) slice(item any, idx ...int) (any, error) {
	v := reflect.ValueOf(item)
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil, fmt.Errorf("slice of untyped nil")
	}
	if len(idx) > 3 {
		return nil, fmt.Errorf("too many slice indexes: %d", len(idx))
	}
	var cap int
	switch v.Kind() {
	case reflect.String:
		if len(idx) == 3 {
			return nil, fmt.Errorf("cannot 3-index slice a string")
		}
		cap = v.Len()
	case reflect.Array, reflect.Slice:
		cap = v.Cap()
	default:
		return nil, fmt.Errorf("can't slice item of type %s", v.Type())
	}
	ix := [3]int{0, v.Len(), 0}
	for i, x := range idx {
		if x < 0 || x > cap {
			return nil, fmt.Errorf("index out of range: %d", x)
		}
		ix[i] = x
	}
	if ix[0] > ix[1] {
		return nil, fmt.Errorf("invalid slice index: %d > %d", ix[0], ix[1])
	}
	var out reflect.Value
	if len(idx) < 3 {
		out = v.Slice(ix[0], ix[1])
	} else {
		if ix[1] > ix[2] {
			return nil, fmt.Errorf("invalid slice index: %d > %d", ix[1], ix[2])
		}
		out = v.Slice3(ix[0], ix[1], ix[2])
	}
	if err := r.charge(out.Len()); err != nil {
		return nil, err
	}
	return out.Interface(), nil
}

var errorType = reflect.TypeFor[error]()

// metered wraps a template function so the text it returns is charged to the budget. Functions
// that can return an error report the overrun that way; others panic, which text/template turns
// into an execution error.
func (r *Renderer) metered(fn any) any {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return fn
	}
	t := v.Type()
	return reflect.MakeFunc(t, func(in []reflect.Value) []reflect.Value {
		var out []reflect.Value
		if t.IsVariadic() {
			out = v.CallSlice(in)
		} else {
			out = v.Call(in)
		}
		n := 0
		for _, o := range out {
			n += resultSize(o)
		}
		if err := r.charge(n); err != nil {
			if len(out) == 2 && t.Out(1) == errorType {
				out[0] = reflect.Zero(t.Out(0))
				out[1] = reflect.ValueOf(&err).Elem()
				return out
			}
			panic(err)
		}
		return out
	}).Interface()
}

func resultSize(v reflect.Value) int {
	for v.IsValid() && v.Kind() == reflect.Interface {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return 0
	}
	switch v.Kind() {
	case reflect.String:
		return v.Len()
	case reflect.Slice, reflect.Array, reflect.Map:
		return v.Len() * 16
	}
	return 0
}
