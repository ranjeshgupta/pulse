//go:build e2e

package e2e

// TestAPIRobustness sends bad input to every API route and checks that the server answers with a
// clear 4xx: never a 5xx, never a dropped connection, never a stack trace, and never anything from
// another workspace.
//
// The route list is not hand-kept: it is read at test time from the mux.HandleFunc registrations
// in internal/api/*.go, and the request bodies are built from the Go types each handler decodes
// into (found by walking the handler's source), so new routes and new fields are covered without
// touching this file.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- routes, read from the source ----

type rbRoute struct {
	Method, Path, File string
	Params             []string
	types              []rbType // what the handler decodes the body into
	query              []string // query (and form) parameters it reads
	multipart, form    bool
}

type rbType struct {
	expr ast.Expr
	pkg  string
}

type rbIndex struct {
	funcs map[string][]*ast.FuncDecl // internal/api functions and methods, by name
	types map[string]rbType          // "pkg.Name" for every type in internal/...
}

var rbParamRE = regexp.MustCompile(`\{([a-z_]+)\.{0,3}\}`)

func rbLoadRoutes(t *testing.T) ([]rbRoute, *rbIndex) {
	t.Helper()
	root := "../../internal"
	fset := token.NewFileSet()
	idx := &rbIndex{funcs: map[string][]*ast.FuncDecl{}, types: map[string]rbType{}}
	var apiFiles []*ast.File
	var apiNames []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		pkg := f.Name.Name
		for _, decl := range f.Decls {
			if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.TYPE {
				for _, s := range g.Specs {
					ts := s.(*ast.TypeSpec)
					idx.types[pkg+"."+ts.Name.Name] = rbType{ts.Type, pkg}
				}
			}
		}
		if filepath.Dir(p) == filepath.Join(root, "api") {
			apiFiles = append(apiFiles, f)
			apiNames = append(apiNames, filepath.Base(p))
			for _, decl := range f.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
					idx.funcs[fd.Name.Name] = append(idx.funcs[fd.Name.Name], fd)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading internal/: %v", err)
	}
	var routes []rbRoute
	for i, f := range apiFiles {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pat, _ := strconv.Unquote(lit.Value)
			method, path, ok := strings.Cut(pat, " ")
			if !ok {
				return true
			}
			r := rbRoute{Method: method, Path: path, File: apiNames[i]}
			for _, m := range rbParamRE.FindAllStringSubmatch(path, -1) {
				r.Params = append(r.Params, m[1])
			}
			idx.analyze(&r, call.Args[1])
			routes = append(routes, r)
			return true
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Path+" "+routes[i].Method < routes[j].Path+" "+routes[j].Method
	})
	return routes, idx
}

// fnBody is a function to look into: a declared function or a function literal.
type fnBody struct {
	typ  *ast.FuncType
	body *ast.BlockStmt
}

// analyze walks a route's handler (and the request-taking helpers it calls, three levels deep)
// for the types it decodes request bodies into and the query and form parameters it reads.
func (idx *rbIndex) analyze(r *rbRoute, handler ast.Expr) {
	var queue []fnBody
	depth := map[*ast.BlockStmt]int{}
	add := func(fb fnBody, d int) {
		if _, seen := depth[fb.body]; seen || fb.body == nil {
			return
		}
		depth[fb.body] = d
		queue = append(queue, fb)
	}
	ast.Inspect(handler, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			add(fnBody{x.Type, x.Body}, 0)
			return false
		case *ast.SelectorExpr:
			for _, fd := range idx.funcs[x.Sel.Name] {
				if fd.Recv != nil && !takesHandler(fd.Type) {
					add(fnBody{fd.Type, fd.Body}, 0)
				}
			}
		case *ast.Ident:
			for _, fd := range idx.funcs[x.Name] {
				if fd.Recv == nil && !takesHandler(fd.Type) {
					add(fnBody{fd.Type, fd.Body}, 0)
				}
			}
		}
		return true
	})
	var primary, secondary []rbType
	qs := map[string]bool{}
	for i := 0; i < len(queue); i++ {
		fb := queue[i]
		d := depth[fb.body]
		ast.Inspect(fb.body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, recv := "", ""
			switch f := call.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
				recv = exprString(f.X)
			}
			switch {
			case name == "decode" && len(call.Args) == 3:
				primary = append(primary, idx.varType(fb, call.Args[2])...)
			case name == "Decode" && len(call.Args) == 1 && strings.Contains(recv, "NewDecoder"):
				primary = append(primary, idx.varType(fb, call.Args[0])...)
			case name == "Unmarshal" && recv == "json" && len(call.Args) == 2:
				secondary = append(secondary, idx.varType(fb, call.Args[1])...)
			case name == "Get" && len(call.Args) == 1 && (strings.Contains(recv, "Query()") || slices.Contains([]string{"q", "qs", "query", "v", "vals", "values", "params", "form", "r.Form", "r.PostForm"}, recv)):
				if s, ok := strLit(call.Args[0]); ok {
					qs[s] = true
				}
			case name == "FormValue" || name == "PostFormValue":
				if s, ok := strLit(call.Args[0]); ok {
					qs[s] = true
				}
				r.form = true
			case name == "ParseForm":
				r.form = true
			case name == "FormFile" || name == "ParseMultipartForm" || name == "MultipartReader":
				r.multipart = true
			}
			if d < 3 {
				for _, fd := range idx.funcs[name] {
					if (fd.Recv != nil) == (recv != "") && takesRequest(fd.Type) && !takesHandler(fd.Type) {
						add(fnBody{fd.Type, fd.Body}, d+1)
					}
				}
			}
			return true
		})
	}
	r.types = primary
	if len(r.types) == 0 {
		r.types = secondary
	}
	for q := range qs {
		r.query = append(r.query, q)
	}
	sort.Strings(r.query)
}

func strLit(e ast.Expr) (string, bool) {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, err := strconv.Unquote(lit.Value)
		return s, err == nil
	}
	return "", false
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.CallExpr:
		return exprString(x.Fun) + "()"
	case *ast.StarExpr:
		return exprString(x.X)
	}
	return "?"
}

// takesRequest: the function has a *http.Request or url.Values parameter (helpers that read input).
func takesRequest(ft *ast.FuncType) bool {
	for _, p := range ft.Params.List {
		s := exprString(p.Type)
		if s == "http.Request" || s == "url.Values" {
			return true
		}
	}
	return false
}

// takesHandler: middleware (it wraps a handler); not followed, as it is not where input is read.
func takesHandler(ft *ast.FuncType) bool {
	for _, p := range ft.Params.List {
		if _, ok := p.Type.(*ast.FuncType); ok {
			return true
		}
		if s := exprString(p.Type); strings.HasSuffix(s, "Handler") || strings.HasSuffix(s, "HandlerFunc") {
			return true
		}
	}
	return false
}

// varType finds the declared type of the variable a decode call writes into.
func (idx *rbIndex) varType(fb fnBody, arg ast.Expr) []rbType {
	if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
		arg = u.X
	}
	if cl, ok := arg.(*ast.CompositeLit); ok {
		return []rbType{{cl.Type, "api"}}
	}
	id, ok := arg.(*ast.Ident)
	if !ok {
		return nil
	}
	var found ast.Expr
	for _, p := range fb.typ.Params.List {
		for _, n := range p.Names {
			if n.Name == id.Name {
				found = p.Type
			}
		}
	}
	ast.Inspect(fb.body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.ValueSpec:
			for i, n := range x.Names {
				if n.Name != id.Name {
					continue
				}
				if x.Type != nil {
					found = x.Type
				} else if i < len(x.Values) {
					found = litType(x.Values[i])
				}
			}
		case *ast.AssignStmt:
			if x.Tok != token.DEFINE {
				return true
			}
			for i, l := range x.Lhs {
				if li, ok := l.(*ast.Ident); ok && li.Name == id.Name && i < len(x.Rhs) {
					found = litType(x.Rhs[i])
				}
			}
		}
		return true
	})
	if found == nil {
		return nil
	}
	return []rbType{{found, "api"}}
}

func litType(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case *ast.CompositeLit:
		return x.Type
	case *ast.UnaryExpr:
		return litType(x.X)
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "new" && len(x.Args) == 1 {
			return x.Args[0]
		}
	}
	return nil
}

// ---- body generation from Go types ----

const rbInvalidUTF8 = "~INVALID-UTF8~"

var rbWeird = "😀 ‮RTL‬ عربى \u0000NUL '\"<script>x</script> OR 1=1;-- " + rbInvalidUTF8

type rbGen struct {
	idx     *rbIndex
	mode    string // weird, zero, huge, neg, wrong, null, ghost, xref
	strSize int
	xref    func(key string) any // B's id for an id field (mode xref)
}

func (g *rbGen) body(types []rbType) []byte {
	obj := map[string]any{}
	for _, ty := range types {
		v := g.value(ty.expr, ty.pkg, 0, "")
		if m, ok := v.(map[string]any); ok {
			for k, x := range m {
				obj[k] = x
			}
		} else if len(types) == 1 {
			b, _ := json.Marshal(v)
			return rbFixUTF8(b)
		}
	}
	b, _ := json.Marshal(obj)
	return rbFixUTF8(b)
}

func rbFixUTF8(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte(rbInvalidUTF8), []byte("\xff\xfe\xc3"))
}

func (g *rbGen) value(e ast.Expr, pkg string, depth int, key string) any {
	if depth > 7 {
		return nil
	}
	switch x := e.(type) {
	case *ast.ParenExpr:
		return g.value(x.X, pkg, depth, key)
	case *ast.StarExpr:
		return g.value(x.X, pkg, depth, key)
	case *ast.Ident:
		switch x.Name {
		case "string":
			return g.str(key)
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "byte", "rune", "uintptr":
			return g.int(key)
		case "float32", "float64":
			return g.float()
		case "bool":
			return g.boolean()
		case "any":
			return g.anyv()
		case "error":
			return nil
		}
		if ty, ok := g.idx.types[pkg+"."+x.Name]; ok {
			return g.value(ty.expr, ty.pkg, depth+1, key)
		}
		return g.anyv()
	case *ast.SelectorExpr:
		p := exprString(x.X)
		switch p + "." + x.Sel.Name {
		case "time.Time":
			return g.time()
		case "time.Duration":
			return g.int(key)
		case "json.RawMessage":
			// the documents behind the raw fields, so their own fields get odd values too
			if ty, ok := g.idx.types[rbRawTypes[key]]; ok && depth < 3 {
				return g.value(ty.expr, ty.pkg, depth+1, key)
			}
			return g.anyv()
		}
		if ty, ok := g.idx.types[p+"."+x.Sel.Name]; ok {
			return g.value(ty.expr, ty.pkg, depth+1, key)
		}
		return g.anyv()
	case *ast.ArrayType:
		if id, ok := x.Elt.(*ast.Ident); ok && id.Name == "byte" {
			return g.str(key)
		}
		switch g.mode {
		case "zero":
			return []any{}
		case "null":
			return nil
		case "wrong":
			return map[string]any{"x": 1}
		case "huge":
			small := *g
			small.mode = "weird"
			el := small.value(x.Elt, pkg, depth+1, key)
			out := make([]any, 1000)
			for i := range out {
				out[i] = el
			}
			return out
		}
		return []any{g.value(x.Elt, pkg, depth+1, strings.TrimSuffix(key, "s"))}
	case *ast.MapType:
		switch g.mode {
		case "zero":
			return map[string]any{}
		case "null":
			return nil
		case "wrong":
			return []any{1}
		}
		k := rbWeird
		if kid, ok := x.Key.(*ast.Ident); ok && kid.Name != "string" {
			k = "-1"
		}
		if g.mode == "huge" {
			small := *g
			small.mode = "weird"
			el := small.value(x.Value, pkg, depth+1, "")
			out := map[string]any{}
			for i := range 1000 {
				out[fmt.Sprint(k, i)] = el
			}
			return out
		}
		return map[string]any{k: g.value(x.Value, pkg, depth+1, "")}
	case *ast.StructType:
		if depth > 0 && g.mode == "wrong" {
			return []any{1, 2, 3}
		}
		if depth > 0 && g.mode == "null" {
			return nil
		}
		out := map[string]any{}
		for _, f := range x.Fields.List {
			name := ""
			if f.Tag != nil {
				tag, _ := strconv.Unquote(f.Tag.Value)
				name = strings.Split(reflectTag(tag, "json"), ",")[0]
				if name == "-" {
					continue
				}
			}
			if len(f.Names) == 0 { // embedded
				if name == "" {
					if m, ok := g.value(f.Type, pkg, depth+1, "").(map[string]any); ok {
						for k, v := range m {
							out[k] = v
						}
					}
					continue
				}
				out[name] = g.value(f.Type, pkg, depth+1, name)
				continue
			}
			for _, n := range f.Names {
				if !n.IsExported() {
					continue
				}
				k := name
				if k == "" {
					k = n.Name
				}
				out[k] = g.value(f.Type, pkg, depth+1, k)
			}
		}
		return out
	case *ast.InterfaceType:
		return g.anyv()
	}
	return nil
}

func reflectTag(tag, key string) string {
	for tag != "" {
		i := strings.Index(tag, `:"`)
		if i < 0 {
			return ""
		}
		name := strings.TrimSpace(tag[:i])
		rest := tag[i+2:]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return ""
		}
		if name == key {
			return rest[:j]
		}
		tag = rest[j+1:]
	}
	return ""
}

func rbIsIDKey(key string) bool {
	return key == "id" || key == "ids" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_ids")
}

func (g *rbGen) str(key string) any {
	switch g.mode {
	case "zero":
		return ""
	case "huge":
		return strings.Repeat("Ω", g.strSize/2)
	case "neg":
		return "-1"
	case "wrong":
		return 12345
	case "null":
		return nil
	case "ghost":
		return "does-not-exist"
	case "xref":
		if rbIsIDKey(key) || strings.HasSuffix(key, "key") {
			if v := g.xref(key); v != nil {
				return fmt.Sprint(v)
			}
		}
		return "x"
	}
	return rbWeird
}

func (g *rbGen) int(key string) any {
	switch g.mode {
	case "zero":
		return 0
	case "huge":
		return int64(math.MaxInt64)
	case "neg":
		return int64(math.MinInt64)
	case "wrong":
		return "not a number"
	case "null":
		return nil
	case "ghost":
		return 987654321
	case "xref":
		if rbIsIDKey(key) {
			if v := g.xref(key); v != nil {
				return v
			}
		}
		return 1
	}
	return -1
}

func (g *rbGen) float() any {
	switch g.mode {
	case "zero":
		return 0
	case "huge":
		return 1.7e308
	case "neg":
		return -1.7e308
	case "wrong":
		return "not a number"
	case "null":
		return nil
	case "ghost", "xref":
		return 987654321
	}
	return -0.000001
}

func (g *rbGen) boolean() any {
	switch g.mode {
	case "zero":
		return false
	case "wrong":
		return "yes"
	case "null":
		return nil
	}
	return true
}

func (g *rbGen) time() any {
	switch g.mode {
	case "huge":
		return "9999-12-31T23:59:59Z"
	case "neg":
		return "0001-01-01T00:00:00Z"
	case "wrong":
		return 12
	case "null":
		return nil
	case "ghost", "xref":
		return time.Now().UTC().Format(time.RFC3339)
	}
	return "0001-01-01T00:00:00Z"
}

func (g *rbGen) anyv() any {
	switch g.mode {
	case "zero":
		return map[string]any{}
	case "huge":
		return strings.Repeat("Ω", g.strSize/2)
	case "neg":
		return -1
	case "wrong":
		return []any{}
	case "null", "xref":
		return nil
	case "ghost":
		return "does-not-exist"
	}
	return map[string]any{rbWeird: []any{nil, map[string]any{"\u0000": -1, "all": "x"}}}
}

// rbRawTypes are the types the json.RawMessage fields named so are parsed into later.
var rbRawTypes = map[string]string{"graph": "journey.Graph", "rule": "segment.Rule"}

// rbFallback is used for handlers whose decode target couldn't be found in the source.
var rbFallback = func() ast.Expr {
	e, err := parser.ParseExpr("struct{ Name string `json:\"name\"`; ID int64 `json:\"id\"`; IDs []int64 `json:\"ids\"`; Email string `json:\"email\"`; ExternalID string `json:\"external_id\"`; Status string `json:\"status\"`; Rule any `json:\"rule\"`; Graph any `json:\"graph\"`; HTML string `json:\"html\"`; Text string `json:\"text\"`; URL string `json:\"url\"`; Events []string `json:\"events\"`; Value any `json:\"value\"`; Key string `json:\"key\"`; Type string `json:\"type\"`; Channel string `json:\"channel\"`; Code string `json:\"code\"`; Password string `json:\"password\"`; Limit int `json:\"limit\"`; From time.Time `json:\"from\"`; To time.Time `json:\"to\"` }")
	if err != nil {
		panic(err)
	}
	return e
}()

// ---- workspaces and fixtures ----

type rbWS struct {
	c                     *http.Client
	key, site, slug, name string
	userID                string
}

func rbClient(jar bool) *http.Client {
	c := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 64, MaxConnsPerHost: 64},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if jar {
		c.Jar, _ = cookiejar.New(nil)
	}
	return c
}

func rbSignup(t *testing.T, name string) *rbWS {
	t.Helper()
	c := rbClient(true)
	run := fmt.Sprint(time.Now().UnixNano())
	out := jsonCall(t, c, "POST", "/v1/auth/signup", map[string]any{"workspace": name, "email": "o-" + run + "@example.com", "password": "owner password 1"}, 201)
	ws := out["workspace"].(map[string]any)
	w := &rbWS{c: c, key: out["api_key"].(string), site: ws["site_key"].(string), slug: ws["slug"].(string), name: name}
	me := jsonCall(t, c, "GET", "/v1/auth/me", nil, 200)
	if u, ok := me["user"].(map[string]any); ok {
		w.userID = rbIDString(u["id"])
	}
	return w
}

type rbFixtures struct {
	id       map[string]string         // path segment before {id} ("segments", "providers"…) -> id
	body     map[string]map[string]any // the body each was created with
	contact  string
	formKey  string
	assetKey string
	lpSlug   string
}

func rbIDString(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	return ""
}

func rbFindID(m map[string]any) string {
	if id := rbIDString(m["id"]); id != "" {
		return id
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			if id := rbIDString(sub["id"]); id != "" {
				return id
			}
		}
	}
	return ""
}

func rbFixturesFor(t *testing.T, w *rbWS, mark string) *rbFixtures {
	t.Helper()
	f := &rbFixtures{id: map[string]string{}, body: map[string]map[string]any{}}
	future := time.Now().Add(240 * time.Hour).UTC().Format(time.RFC3339)
	hdr := map[string]string{"X-Pulse-CSRF": "1"}
	create := func(res, path string, body map[string]any) string {
		b, _ := json.Marshal(body)
		code, raw, err := rbDo(w.c, "POST", path, hdr, b, "application/json")
		if err != nil || code/100 != 2 {
			t.Logf("fixture %s: %d %s %v", res, code, rbTrunc(raw, 300), err)
			return ""
		}
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		id := rbFindID(m)
		if id == "" {
			t.Logf("fixture %s: no id in %s", res, rbTrunc(raw, 300))
			return ""
		}
		f.id[res], f.body[res] = id, body
		return id
	}
	num := func(s string) any {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return s
		}
		return n
	}
	seg := create("segments", "/v1/segments", map[string]any{"name": "seg " + mark, "rule": map[string]any{"attr": "city", "op": "eq", "value": mark}})
	create("lists", "/v1/lists", map[string]any{"name": "list " + mark})
	tpl := create("templates", "/v1/templates", map[string]any{"name": "tpl " + mark, "channel": "sms", "content": map[string]any{"text": "Hi " + mark}})
	create("journeys", "/v1/journeys", map[string]any{"name": "journey " + mark, "graph": map[string]any{
		"trigger": map[string]any{"type": "event", "event": "rb_" + mark}, "entry": "done",
		"nodes": map[string]any{"done": map[string]any{"type": "exit"}}}})
	create("broadcasts", "/v1/broadcasts", map[string]any{"name": "bc " + mark, "channel": "sms", "template_id": num(tpl), "segment_id": num(seg),
		"repeat": "none", "start_at": future})
	create("providers", "/v1/channels/providers", map[string]any{"channel": "sms", "provider": "sandbox_sms", "name": "prov " + mark})
	create("cohorts", "/v1/cohorts", map[string]any{"name": "cohort " + mark, "spec": map[string]any{"by": "event", "event": "signup", "period": "day", "periods": 3, "return": map[string]any{"event": "open_app"}}})
	create("companies", "/v1/companies", map[string]any{"name": "co " + mark, "domain": mark + ".test"})
	create("content-blocks", "/v1/content-blocks", map[string]any{"key": "blk" + strings.ToLower(mark[len(mark)-6:]), "name": "blk " + mark, "html": "<p>" + mark + "</p>", "text": mark})
	create("dwc", "/v1/dwc", map[string]any{"slot": "hero" + mark[len(mark)-4:], "name": "dwc " + mark, "status": "draft", "default_html": "<p>" + mark + "</p>"})
	create("focus-items", "/v1/focus-items", map[string]any{"name": "focus " + mark, "kind": "bar", "status": "draft", "content": map[string]any{"title": mark},
		"targeting": map[string]any{"trigger": "load", "frequency": "always"}})
	form := create("forms", "/v1/forms", map[string]any{"name": "form " + mark, "status": "active",
		"fields": []any{map[string]any{"key": "email", "label": "Email", "type": "email", "required": true}}})
	if form != "" {
		_, raw, _ := rbDo(w.c, "GET", "/v1/forms/"+form, nil, nil, "")
		var g struct {
			Form struct {
				PublicKey string `json:"public_key"`
			} `json:"form"`
		}
		_ = json.Unmarshal(raw, &g)
		f.formKey = g.Form.PublicKey
	}
	create("funnels", "/v1/funnels", map[string]any{"name": "funnel " + mark, "steps": []any{map[string]any{"event": "a"}, map[string]any{"event": "b"}}, "window_days": 1})
	create("inapp", "/v1/inapp", map[string]any{"name": "inapp " + mark, "layout": "modal", "status": "draft", "content": map[string]any{"title": mark},
		"targeting": map[string]any{"trigger_event": "app_open", "frequency": "once"}})
	f.lpSlug = "lp-" + strings.ToLower(mark)
	create("landing-pages", "/v1/landing-pages", map[string]any{"slug": f.lpSlug, "title": "lp " + mark, "html": "<p>" + mark + "</p>", "status": "published"})
	create("reports", "/v1/reports", map[string]any{"name": "report " + mark, "spec": map[string]any{"source": "events", "metric": map[string]any{"type": "count"},
		"group_by": map[string]any{"type": "prop", "key": "sku"}, "events": []string{"purchase"}, "chart": "bars", "range_days": 7}})
	create("roles", "/v1/roles", map[string]any{"name": "role " + mark, "permissions": map[string]any{"reports": "view"}})
	create("stages", "/v1/stages", map[string]any{"name": "stage " + mark})
	create("surveys", "/v1/surveys", map[string]any{"name": "survey " + mark, "status": "draft", "questions": []any{map[string]any{"id": "nps", "type": "nps", "text": mark}}})
	create("test-contacts", "/v1/test-contacts", map[string]any{"name": "qa " + mark, "email": "qa-" + strings.ToLower(mark) + "@example.com"})
	create("users", "/v1/users", map[string]any{"email": "u-" + strings.ToLower(mark) + "@example.com", "name": "user " + mark, "role": "viewer", "password": "robust password 1"})
	create("webhooks", "/v1/settings/webhooks", map[string]any{"name": "hook " + mark, "url": "https://example.com/hook", "events": []any{"purchase"}, "active": false})
	create("bounce-mailboxes", "/v1/settings/bounce-mailboxes", map[string]any{"enabled": false, "host": "imap.example.com", "port": 993, "tls": "tls", "username": mark, "password": "p"})
	create("integrations", "/v1/integrations", map[string]any{"kind": "hubspot", "name": "int " + mark, "config": map[string]any{"base_url": "https://api.hubapi.com"},
		"secrets": map[string]any{"access_token": "pat-" + mark}})
	if bc := f.id["broadcasts"]; bc != "" {
		create("approvals", "/v1/approvals", map[string]any{"kind": "broadcast", "campaign_id": num(bc), "note": mark})
	}
	// a hook subscription (API key) and an asset (multipart)
	{
		b, _ := json.Marshal(map[string]any{"target_url": "https://example.com/" + mark, "event": "purchase"})
		code, raw, _ := rbDo(rbClient(false), "POST", "/v1/hooks/subscriptions", map[string]string{"Authorization": "Bearer " + w.key}, b, "application/json")
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		if id := rbFindID(m); code/100 == 2 && id != "" {
			f.id["subscriptions"] = id
		}
	}
	{
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "doc-"+mark+".pdf")
		_, _ = fw.Write(e2ePDF)
		_ = mw.Close()
		code, raw, _ := rbDo(w.c, "POST", "/v1/assets", hdr, buf.Bytes(), mw.FormDataContentType())
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		if id := rbFindID(m); code/100 == 2 && id != "" {
			f.id["assets"] = id
			if u, _ := m["url"].(string); strings.Contains(u, "/a/") {
				f.assetKey = u[strings.LastIndex(u, "/a/")+3:]
			}
		} else {
			t.Logf("fixture assets: %d %s", code, rbTrunc(raw, 300))
		}
	}
	f.contact = "c-" + strings.ToLower(mark)
	b, _ := json.Marshal(map[string]any{"external_id": f.contact, "name": "identify", "email": f.contact + "@example.com", "attrs": map[string]any{"note": mark}})
	if code, raw, _ := rbDo(w.c, "POST", "/v1/events", hdr, b, "application/json"); code != 202 {
		t.Logf("fixture contact: %d %s", code, rbTrunc(raw, 300))
	}
	return f
}

// ---- requests ----

type rbProbe struct {
	route  string // "METHOD /pattern"
	what   string // the input, for the report
	phase  int
	client *http.Client
	method string
	path   string
	hdr    map[string]string
	body   []byte
	ctype  string
	// expectations beyond the global ones
	authOnly bool // must be refused (sent without credentials to a signed-in route)
	foreign  bool // addresses another workspace's object
	xref     bool // its body's ids are another workspace's
}

type rbResult struct {
	*rbProbe
	code int
	body []byte
	err  error
	dur  time.Duration
}

func rbDo(c *http.Client, method, path string, hdr map[string]string, body []byte, ctype string) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, apiURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, raw, err
}

func rbTrunc(b []byte, n int) string {
	s := strings.ToValidUTF8(string(b), "?")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func rbFire(probes []*rbProbe, workers int) []rbResult {
	out := make([]rbResult, len(probes))
	var wg sync.WaitGroup
	next := make(chan int)
	for range workers {
		wg.Go(func() {
			for i := range next {
				p := probes[i]
				start := time.Now()
				code, body, err := rbDo(p.client, p.method, p.path, p.hdr, p.body, p.ctype)
				out[i] = rbResult{p, code, body, err, time.Since(start)}
			}
		})
	}
	for i := range probes {
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

// ---- the test ----

var (
	rbPublicPrefixes = []string{"/p/", "/s/", "/f/", "/a/", "/t/", "/u/", "/v1/web/", "/v1/amp/", "/v1/replies/", "/v1/bounces/",
		"/v1/webhooks/", "/healthz", "/v1/auth/logout", "/v1/auth/signup", "/v1/auth/login", "/v1/auth/sso", "/v1/auth/two-factor/"}
	// Routes that would end the test's own session or lock it out; sent last, one at a time.
	rbLate = []string{"PUT /v1/settings/two-factor", "PUT /v1/settings/sso", "DELETE /v1/settings/sso", "PUT /v1/users/{id}/role",
		"POST /v1/account/two-factor/setup", "POST /v1/account/two-factor/enable", "POST /v1/account/two-factor/disable",
		"POST /v1/account/two-factor/recovery-codes", "PUT /v1/settings/identity", "POST /v1/settings/identity/rotate",
		"POST /v1/settings/api-key", "POST /v1/account/sessions/revoke", "POST /v1/auth/logout"}
	rbBadParams = []string{"0", "-1", "999999999", "9223372036854775808", "abc", "1.5", "%00", "%F0%9F%98%80%E2%80%AE%D8%B9", "%27%20OR%201%3D1--",
		strings.Repeat("a", 5000)}
	rbGenericQuery = []string{"from", "to", "bucket", "tz", "limit", "offset", "days", "page", "q", "cursor", "sort", "order", "status", "id", "segment_id"}
	rbStackRE      = regexp.MustCompile(`goroutine \d+|runtime error|panic:|\.go:\d+`)
)

func rbPublic(path string) bool {
	for _, p := range rbPublicPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func TestAPIRobustness(t *testing.T) {
	routes, idx := rbLoadRoutes(t)
	if len(routes) < 300 {
		t.Fatalf("found only %d routes in internal/api; is the route parser out of date?", len(routes))
	}
	if f := os.Getenv("PULSE_ROBUST_ONLY"); f != "" { // debugging: a regexp over "METHOD /path"
		re := regexp.MustCompile(f)
		routes = slices.DeleteFunc(routes, func(r rbRoute) bool { return !re.MatchString(r.Method + " " + r.Path) })
	}
	typed := 0
	for _, r := range routes {
		if len(r.types) > 0 {
			typed++
		}
	}
	t.Logf("%d routes; %d with a decoded body type found in the source", len(routes), typed)

	run := fmt.Sprint(time.Now().UnixNano())
	short := run[len(run)-8:]
	a := rbSignup(t, "Robust A "+run)
	b := rbSignup(t, "Robust B "+run)
	markA, markB := "rbA"+short, "zzXWSb"+short
	fa := rbFixturesFor(t, a, markA)
	fb := rbFixturesFor(t, b, markB)
	t.Logf("fixtures: A %d, B %d", len(fa.id), len(fb.id))
	time.Sleep(2 * time.Second) // let the contacts land

	sessionHdr := map[string]string{"X-Pulse-CSRF": "1"}
	keyHdr := map[string]string{"Authorization": "Bearer " + a.key}
	siteHdr := map[string]string{"X-Pulse-Site-Key": a.site}
	bare := rbClient(false)

	resOf := func(path string, i int) string { // the path segment before the i-th parameter
		segs := strings.Split(path, "/")
		n := -1
		for j, s := range segs {
			if strings.HasPrefix(s, "{") {
				n++
				if n == i && j > 0 {
					switch segs[j-1] {
					case "s":
						return "surveys"
					case "shopify":
						return "integrations"
					}
					return segs[j-1]
				}
			}
		}
		return ""
	}
	valid := func(w *rbWS, f *rbFixtures, path string, i int) string {
		name := ""
		if ps := rbParamRE.FindAllStringSubmatch(path, -1); i < len(ps) {
			name = ps[i][1]
		}
		switch name {
		case "id":
			if v := f.id[resOf(path, i)]; v != "" {
				return v
			}
			return "1"
		case "external_id":
			return f.contact
		case "key":
			switch {
			case strings.HasPrefix(path, "/a/"):
				return f.assetKey
			case strings.HasPrefix(path, "/f/"), strings.HasPrefix(path, "/v1/web/forms/"):
				return f.formKey
			case strings.HasPrefix(path, "/v1/attributes/"):
				return "note"
			}
			return "x"
		case "name":
			return "purchase"
		case "code":
			return "sku-1"
		case "workspace":
			return w.slug
		case "slug":
			return f.lpSlug
		case "action":
			if strings.HasPrefix(path, "/v1/approvals/") {
				return "approve"
			}
			return "pause"
		}
		return "1"
	}
	fill := func(path string, val func(i int) string) string {
		i := -1
		return rbParamRE.ReplaceAllStringFunc(path, func(string) string { i++; return val(i) })
	}

	var probes []*rbProbe
	addP := func(p *rbProbe) { probes = append(probes, p) }
	late := func(r rbRoute) bool { return slices.Contains(rbLate, r.Method+" "+r.Path) }

	for _, r := range routes {
		rk := r.Method + " " + r.Path
		phase := 1
		if r.Method == "DELETE" {
			phase = 2
		}
		if late(r) {
			phase = 3 + slices.Index(rbLate, rk)
		}
		client, hdr := a.c, sessionHdr
		public := rbPublic(r.Path)
		switch {
		case r.Path == "/v1/tenants":
			client, hdr = bare, map[string]string{"Authorization": "Bearer " + adminToken}
		case strings.HasPrefix(r.Path, "/v1/hooks/"):
			client, hdr = bare, keyHdr
		case public:
			client, hdr = bare, siteHdr
		}
		vpath := fill(r.Path, func(i int) string { return valid(a, fa, r.Path, i) })
		hasBody := r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS"
		emptyBody := []byte(nil)
		if hasBody {
			emptyBody = []byte("{}")
		}
		p := func(what, path string, body []byte, ctype string) {
			addP(&rbProbe{route: rk, what: what, phase: phase, client: client, method: r.Method, path: path, hdr: hdr, body: body, ctype: ctype})
		}

		// bodies
		p("no body", vpath, nil, "")
		if hasBody {
			for what, body := range map[string]string{
				"{}": "{}", "malformed JSON": `{"name": "x", `, "[]": "[]", "null": "null", `"string"`: `"x"`, "number": "12",
				"deeply nested": strings.Repeat("[", 20000), "invalid UTF-8": "{\"name\": \"\xff\xfe\", \"email\": \"\xc3\"}",
				"trailing garbage": `{} {"x": 1}`, "unknown fields": `{"zzz": [1, {"a": null}], "__proto__": {}}`,
			} {
				p(what, vpath, []byte(body), "application/json")
			}
			types := r.types
			if len(types) == 0 {
				types = []rbType{{rbFallback, "api"}}
			}
			for _, mode := range []string{"weird", "zero", "huge", "neg", "wrong", "null", "ghost", "xref"} {
				g := &rbGen{idx: idx, mode: mode, strSize: 1 << 20, xref: func(key string) any { return rbXref(fb, key, r.Path) }}
				body := g.body(types)
				for len(body) > 3<<20 && g.strSize > 256 {
					g.strSize /= 8
					body = g.body(types)
				}
				what := "typed " + mode
				if mode == "xref" {
					what = "typed xref (ids from another workspace)"
				}
				p(what, vpath, body, "application/json")
				if mode == "xref" {
					probes[len(probes)-1].xref = true
				}
			}
			p("1 MB string", vpath, []byte(`{"name": "`+strings.Repeat("x", 1<<20)+`"}`), "application/json")
			p("over the body limit", vpath, bytes.Repeat([]byte(" "), 5<<20), "application/json")
		}
		if r.multipart && hasBody {
			for what, mk := range map[string]func(*multipart.Writer){
				"empty multipart": func(*multipart.Writer) {},
				"odd file name": func(mw *multipart.Writer) {
					for _, q := range r.query {
						_ = mw.WriteField(q, rbWeird)
					}
					fw, _ := mw.CreateFormFile("file", "../../😀\x00‮exe.pdf")
					_, _ = fw.Write([]byte("email,name\n\x00@x,😀\n,,,,\n\"unterminated"))
				},
				"no file part": func(mw *multipart.Writer) { _ = mw.WriteField("name", rbWeird) },
				"empty file": func(mw *multipart.Writer) {
					_, _ = mw.CreateFormFile("file", "")
				},
			} {
				var buf bytes.Buffer
				mw := multipart.NewWriter(&buf)
				mk(mw)
				_ = mw.Close()
				p(what, vpath, buf.Bytes(), mw.FormDataContentType())
			}
			p("multipart with bad boundary", vpath, []byte("--x\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\nzz"), "multipart/form-data; boundary=y")
		}
		if r.form && hasBody {
			v := url.Values{}
			for _, q := range append(slices.Clone(r.query), "email", "name") {
				v.Set(q, rbWeird)
			}
			p("form with odd values", vpath, []byte(v.Encode()), "application/x-www-form-urlencoded")
			p("empty form", vpath, []byte(""), "application/x-www-form-urlencoded")
			p("broken form encoding", vpath, []byte("a=%zz&&=&email=%"), "application/x-www-form-urlencoded")
		}

		// query strings
		qnames := append(slices.Clone(r.query), rbGenericQuery...)
		for what, val := range map[string]string{"-1": "-1", "1e9": "1000000000", "1e9 (float)": "1e9", "overflow": "99999999999999999999",
			"foo": "foo", "unicode/NUL": "😀‮\x00'\"<", "empty": ""} {
			v := url.Values{}
			for _, q := range qnames {
				v.Set(q, val)
			}
			p("query all "+what, vpath+"?"+v.Encode(), emptyBody, "application/json")
		}
		p("query from>to, bucket=foo, tz=Not/AZone, limit=-1", vpath+"?from=2030-01-01&to=2000-01-01&bucket=foo&tz=Not/AZone&limit=-1&offset=-1&days=-1&page=-1", emptyBody, "application/json")
		p("query from>to RFC3339, limit=1e9", vpath+"?from=2030-01-01T00:00:00Z&to=2000-01-01T00:00:00Z&limit=1000000000&bucket=hour&tz=UTC", emptyBody, "application/json")
		p("query far range", vpath+"?from=0001-01-01&to=9999-12-31&bucket=minute&tz=Pacific/Kiritimati", emptyBody, "application/json")
		p("query repeated and odd encoding", vpath+"?limit=1&limit=2&from=%zz&to=&tz=%2e%2e%2f%2e%2e%2fetc", emptyBody, "application/json")

		// path parameters
		for i := range r.Params {
			for _, bad := range rbBadParams {
				bp := fill(r.Path, func(j int) string {
					if j == i {
						return bad
					}
					return valid(a, fa, r.Path, j)
				})
				what := "path {" + r.Params[i] + "}=" + bad
				if len(bad) > 40 {
					what = "path {" + r.Params[i] + "}=<5000 chars>"
				}
				p(what, bp, emptyBody, "application/json")
			}
		}

		if public || r.Path == "/v1/tenants" {
			continue
		}
		// the same route with the API key, and with no credentials at all
		for what, body := range map[string][]byte{"API key, no body": nil, "API key, {}": emptyBody} {
			addP(&rbProbe{route: rk, what: what, phase: phase, client: bare, method: r.Method, path: vpath, hdr: keyHdr, body: body, ctype: "application/json"})
		}
		if hasBody {
			types := r.types
			if len(types) == 0 {
				types = []rbType{{rbFallback, "api"}}
			}
			g := &rbGen{idx: idx, mode: "weird", strSize: 1024}
			addP(&rbProbe{route: rk, what: "API key, typed weird", phase: phase, client: bare, method: r.Method, path: vpath, hdr: keyHdr, body: g.body(types), ctype: "application/json"})
		}
		addP(&rbProbe{route: rk, what: "no credentials", phase: 1, client: bare, method: r.Method, path: vpath, body: emptyBody, ctype: "application/json", authOnly: true,
			hdr: map[string]string{"X-Pulse-CSRF": "1"}})
		addP(&rbProbe{route: rk, what: "bad API key", phase: 1, client: bare, method: r.Method, path: vpath, body: emptyBody, ctype: "application/json", authOnly: true,
			hdr: map[string]string{"Authorization": "Bearer pk_" + strings.Repeat("0", 40)}})

		// another workspace's objects, addressed by id (phase 0: before anything is changed)
		for i, name := range r.Params {
			var bv string
			switch name {
			case "id":
				bv = fb.id[resOf(r.Path, i)]
			case "external_id":
				bv = fb.contact
			}
			if bv == "" {
				continue
			}
			bp := fill(r.Path, func(j int) string {
				if j == i {
					return bv
				}
				return valid(a, fa, r.Path, j)
			})
			body := emptyBody
			if hasBody {
				if src := fb.body[resOf(r.Path, i)]; src != nil && r.Method == "PUT" {
					m := map[string]any{}
					for k, v := range src {
						m[k] = v
					}
					m["name"] = "HACKED by " + markA
					body, _ = json.Marshal(m)
					body = bytes.ReplaceAll(body, []byte(markB), []byte("copied")) // A's own audit log records this body
				}
			}
			addP(&rbProbe{route: rk, what: "another workspace's {" + name + "}=" + bv, phase: 0, client: a.c, method: r.Method, path: bp, hdr: sessionHdr,
				body: body, ctype: "application/json", foreign: true})
			addP(&rbProbe{route: rk, what: "API key, another workspace's {" + name + "}=" + bv, phase: 0, client: bare, method: r.Method, path: bp, hdr: keyHdr,
				body: body, ctype: "application/json", foreign: true})
		}
	}
	probes = append(probes, rbXrefProbes(a, fa, fb, bare, keyHdr)...)

	// B's objects as B sees them, to compare after A has tried everything
	snap := func() map[string]string {
		out := map[string]string{}
		for _, l := range []string{"/v1/segments", "/v1/lists", "/v1/templates", "/v1/journeys", "/v1/broadcasts", "/v1/channels/providers", "/v1/cohorts",
			"/v1/companies", "/v1/content-blocks", "/v1/dwc", "/v1/focus-items", "/v1/forms", "/v1/funnels", "/v1/inapp", "/v1/landing-pages", "/v1/reports",
			"/v1/roles", "/v1/stages", "/v1/surveys", "/v1/test-contacts", "/v1/users", "/v1/settings/webhooks", "/v1/settings/bounce-mailboxes",
			"/v1/integrations", "/v1/assets", "/v1/approvals", "/v1/contacts/" + fb.contact} {
			code, raw, err := rbDo(b.c, "GET", l, nil, nil, "")
			out[l] = fmt.Sprintf("%d marks=%d hacked=%v err=%v", code, strings.Count(string(raw), markB), strings.Contains(string(raw), "HACKED"), err)
		}
		return out
	}
	before := snap()

	// fire, phase by phase
	byPhase := map[int][]*rbProbe{}
	maxPhase := 0
	for _, p := range probes {
		byPhase[p.phase] = append(byPhase[p.phase], p)
		maxPhase = max(maxPhase, p.phase)
	}
	start := time.Now()
	var results []rbResult
	for ph := 0; ph <= maxPhase; ph++ {
		workers := 12
		if ph >= 3 {
			workers = 1
		}
		if len(byPhase[ph]) == 0 {
			continue
		}
		t0 := time.Now()
		results = append(results, rbFire(byPhase[ph], workers)...)
		if ph <= 2 {
			t.Logf("phase %d: %d requests in %v", ph, len(byPhase[ph]), time.Since(t0).Round(time.Millisecond))
		}
	}
	t.Logf("%d requests to %d routes in %v", len(results), len(routes), time.Since(start).Round(time.Millisecond))

	after := snap()
	for l, v := range before {
		if after[l] != v {
			t.Errorf("CROSS-WORKSPACE: workspace B's %s changed while workspace A was probing it: before %s, after %s", l, v, after[l])
		}
	}

	// analyse
	type finding struct {
		kind, route, example string
		n                    int
	}
	findings := map[string]*finding{}
	note := func(kind string, r rbResult, detail string) {
		k := kind + "|" + r.route + "|" + strconv.Itoa(r.code)
		if f, ok := findings[k]; ok {
			f.n++
			return
		}
		findings[k] = &finding{kind: kind, route: r.route, n: 1,
			example: fmt.Sprintf("%s %s [%s] -> %d %s", r.method, rbTrunc([]byte(r.path), 120), r.what, r.code, detail)}
	}
	slow := slices.Clone(results)
	for _, r := range results {
		body := rbTrunc(r.body, 240)
		switch {
		case r.err != nil:
			note("DROPPED", r, r.err.Error())
			continue
		case r.code >= 500:
			if r.code == 500 || !strings.Contains(string(r.body), `"error"`) || strings.Contains(string(r.body), "internal error") {
				note("5XX", r, body)
			} else {
				note("5XX-with-message", r, body)
			}
		}
		if rbStackRE.Match(r.body) {
			note("STACK-TRACE", r, body)
		}
		if strings.Contains(string(r.body), markB) || (r.foreign && strings.Contains(string(r.body), "zzXWSb")) {
			note("CROSS-WORKSPACE-READ", r, body)
		}
		if r.foreign && r.code/100 == 2 && r.method != "GET" {
			note("CROSS-WORKSPACE-WRITE?", r, body)
		}
		if r.xref && r.code/100 == 2 {
			note("XREF-ACCEPTED?", r, rbTrunc(r.rbProbe.body, 160)+" -> "+body)
		}
		if r.authOnly && r.code < 400 {
			note("AUTH-BYPASS", r, body)
		}
		if r.code >= 400 && r.code < 500 && len(bytes.TrimSpace(r.body)) == 0 && r.method != "HEAD" {
			note("4XX-without-message", r, "")
		}
		if bytes.Contains(r.body, []byte("SQLSTATE")) || bytes.Contains(bytes.ToLower(r.body), []byte("clickhouse")) {
			note("DB-ERROR-IN-BODY", r, body)
		}
	}
	sort.Slice(slow, func(i, j int) bool { return slow[i].dur > slow[j].dur })
	for _, r := range slow[:min(8, len(slow))] {
		t.Logf("slow: %v %s %s [%s] -> %d", r.dur.Round(time.Millisecond), r.method, rbTrunc([]byte(r.path), 80), r.what, r.code)
	}
	keys := make([]string, 0, len(findings))
	for k := range findings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	failing := map[string]bool{"DROPPED": true, "5XX": true, "STACK-TRACE": true, "CROSS-WORKSPACE-READ": true, "AUTH-BYPASS": true,
		"CROSS-WORKSPACE-WRITE?": true, "4XX-without-message": true, "DB-ERROR-IN-BODY": true}
	nFail := 0
	for _, k := range keys {
		f := findings[k]
		if failing[f.kind] {
			nFail++
			t.Errorf("%s %s (%d requests), e.g. %s", f.kind, f.route, f.n, f.example)
		} else {
			t.Logf("note: %s %s (%d requests), e.g. %s", f.kind, f.route, f.n, f.example)
		}
	}
	codes := map[int]int{}
	for _, r := range results {
		codes[r.code]++
	}
	t.Logf("status codes: %v; %d failing groups", codes, nFail)
}

// rbXref finds workspace B's id for an id field of a request body.
func rbXref(fb *rbFixtures, key, path string) any {
	k := strings.TrimSuffix(strings.TrimSuffix(key, "_ids"), "_id")
	res := map[string]string{"segment": "segments", "template": "templates", "list": "lists", "journey": "journeys", "broadcast": "broadcasts",
		"campaign": "broadcasts", "form": "forms", "survey": "surveys", "company": "companies", "stage": "stages", "role": "roles", "user": "users",
		"owner": "users", "provider": "providers", "integration": "integrations", "funnel": "funnels", "test_contact": "test-contacts",
		"block": "content-blocks", "asset": "assets", "page": "landing-pages", "landing_page": "landing-pages", "report": "reports", "cohort": "cohorts",
		"mailbox": "bounce-mailboxes", "webhook": "webhooks", "inapp": "inapp", "exclude_segment": "segments", "suppress_segment": "segments"}[k]
	if key == "id" || key == "ids" {
		if parts := strings.Split(strings.TrimPrefix(path, "/v1/"), "/"); len(parts) > 0 {
			res = parts[0]
		}
	}
	switch {
	case key == "external_id":
		return fb.contact
	case strings.HasSuffix(key, "key") && strings.Contains(key, "form"):
		return fb.formKey
	}
	if res == "" {
		res = "segments"
	}
	if v := fb.id[res]; v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n
		}
		return v
	}
	return nil
}

// rbXrefProbes: workspace A creates and changes its own objects so that they point at workspace B's.
// Each must be refused.
func rbXrefProbes(a *rbWS, fa, fb *rbFixtures, bare *http.Client, keyHdr map[string]string) []*rbProbe {
	n := func(f *rbFixtures, res string) any {
		v, err := strconv.ParseInt(f.id[res], 10, 64)
		if err != nil {
			return 987654321
		}
		return v
	}
	future := time.Now().Add(240 * time.Hour).UTC().Format(time.RFC3339)
	exit := map[string]any{"done": map[string]any{"type": "exit"}}
	cases := []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/v1/broadcasts", map[string]any{"name": "x", "channel": "sms", "template_id": n(fb, "templates"), "segment_id": n(fa, "segments"), "repeat": "none", "start_at": future}},
		{"POST", "/v1/broadcasts", map[string]any{"name": "x", "channel": "sms", "template_id": n(fa, "templates"), "segment_id": n(fb, "segments"), "repeat": "none", "start_at": future}},
		{"PUT", "/v1/broadcasts/" + fa.id["broadcasts"], map[string]any{"name": "x", "channel": "sms", "template_id": n(fb, "templates"), "segment_id": n(fa, "segments"), "repeat": "none", "start_at": future}},
		{"POST", "/v1/journeys", map[string]any{"name": "x", "graph": map[string]any{"trigger": map[string]any{"type": "segment_entered", "segment_id": n(fb, "segments")}, "entry": "done", "nodes": exit}}},
		{"POST", "/v1/journeys", map[string]any{"name": "x", "graph": map[string]any{"trigger": map[string]any{"type": "event", "event": "x"}, "entry": "send",
			"nodes": map[string]any{"send": map[string]any{"type": "send_sms", "template_id": n(fb, "templates"), "next": "done"}, "done": map[string]any{"type": "exit"}}}}},
		{"POST", "/v1/journeys", map[string]any{"name": "x", "graph": map[string]any{"trigger": map[string]any{"type": "form_submitted", "form_id": n(fb, "forms")}, "entry": "done", "nodes": exit}}},
		{"POST", "/v1/segments", map[string]any{"name": "x", "rule": map[string]any{"segment": n(fb, "lists"), "op": "in"}}},
		{"POST", "/v1/segments", map[string]any{"name": "x", "rule": map[string]any{"segment": n(fb, "segments"), "op": "in"}}},
		{"POST", "/v1/segments/preview", map[string]any{"rule": map[string]any{"segment": n(fb, "segments"), "op": "in"}}},
		{"POST", "/v1/channels/test", map[string]any{"template_id": n(fb, "templates"), "external_id": fa.contact}},
		{"POST", "/v1/channels/test", map[string]any{"template_id": n(fa, "templates"), "test_contact_id": n(fb, "test-contacts")}},
		{"POST", "/v1/approvals", map[string]any{"kind": "broadcast", "campaign_id": n(fb, "broadcasts")}},
		{"POST", "/v1/dwc", map[string]any{"slot": "xref", "name": "x", "status": "draft", "default_html": "<p/>", "variants": []any{map[string]any{"segment_id": n(fb, "segments"), "html": "<p/>"}}}},
		{"POST", "/v1/inapp", map[string]any{"name": "x", "layout": "modal", "status": "draft", "content": map[string]any{"title": "x"},
			"targeting": map[string]any{"trigger_event": "app_open", "frequency": "once", "segment_ids": []any{n(fb, "segments")}}}},
		{"POST", "/v1/focus-items", map[string]any{"name": "x", "kind": "bar", "status": "draft", "content": map[string]any{"title": "x"},
			"targeting": map[string]any{"trigger": "load", "frequency": "always", "exclude_segment_ids": []any{n(fb, "segments")}}}},
		{"PUT", "/v1/contacts/" + fa.contact + "/company", map[string]any{"company_id": n(fb, "companies")}},
		{"PUT", "/v1/contacts/" + fa.contact + "/owner", map[string]any{"owner_id": n(fb, "users"), "user_id": n(fb, "users")}},
		{"PUT", "/v1/contacts/" + fa.contact + "/stage", map[string]any{"stage_id": n(fb, "stages")}},
		{"POST", "/v1/contacts/view-segment", map[string]any{"segment_id": n(fb, "segments"), "name": "x"}},
		{"POST", "/v1/lists/" + fa.id["lists"] + "/members", map[string]any{"external_ids": []any{fb.contact}, "segment_id": n(fb, "segments")}},
		{"POST", "/v1/hooks/subscriptions", map[string]any{"target_url": "https://example.com/x", "event": "segment_entered", "segment_id": n(fb, "segments")}},
		{"POST", "/v1/landing-pages", map[string]any{"slug": "xref-page", "title": "x", "html": "<p>{form:" + fb.formKey + "}</p>", "status": "draft", "form_id": n(fb, "forms")}},
		{"PUT", "/v1/stages/order", map[string]any{"ids": []any{n(fb, "stages")}}},
		{"PUT", "/v1/focus-items/order", map[string]any{"ids": []any{n(fb, "focus-items")}}},
		{"POST", "/v1/dwc/overlap", map[string]any{"segment_ids": []any{n(fb, "segments")}}},
		{"POST", "/v1/focus-items/overlap", map[string]any{"segment_ids": []any{n(fb, "segments")}}},
		{"POST", "/v1/segments/estimate", map[string]any{"segment_ids": []any{n(fb, "segments")}, "segment_id": n(fb, "segments")}},
		{"POST", "/v1/integrations/" + fa.id["integrations"] + "/audiences", map[string]any{"segment_id": n(fb, "segments"), "name": "x"}},
	}
	var out []*rbProbe
	for _, c := range cases {
		b, _ := json.Marshal(c.body)
		client, hdr := a.c, map[string]string{"X-Pulse-CSRF": "1"}
		if strings.HasPrefix(c.path, "/v1/hooks/") {
			client, hdr = bare, keyHdr
		}
		out = append(out, &rbProbe{route: "XREF " + c.method + " " + c.path, what: "points at another workspace's object: " + rbTrunc(b, 200), phase: 0,
			client: client, method: c.method, path: c.path, hdr: hdr, body: b, ctype: "application/json", foreign: true})
	}
	return out
}
