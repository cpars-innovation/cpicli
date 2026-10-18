// Package schemagen writes the JSON schemas of the CLI results (schemas/).
//
// It type-checks internal/cmd and finds, for every command, the values its
// RunE passes to output.SetResult (directly or through functions of the
// package it calls). Map literals with constant keys become objects with
// those properties. The command tree then links each command's RunE to its
// function by file and line.
package schemagen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const (
	cmdPkg    = "github.com/cpars-innovation/cpicli/internal/cmd"
	outputPkg = "github.com/cpars-innovation/cpicli/internal/output"
	idBase    = "https://github.com/cpars-innovation/cpicli/schemas/"
)

// Schema is a JSON schema as written.
type Schema = map[string]any

// Files are the generated schema files by name.
type Files map[string][]byte

// Generate returns the schema files for the command tree root (built from
// the package in dir, the module root) at the given schemaVersion.
func Generate(moduleDir string, root *cobra.Command, schemaVersion int) (Files, error) {
	a, err := analyze(moduleDir)
	if err != nil {
		return nil, err
	}
	g := &gen{defs: Schema{}, seen: map[string]bool{}}
	files := Files{}
	type entry struct {
		Command string `json:"command"`
		File    string `json:"file"`
		Typed   bool   `json:"typed"`
	}
	var index []entry
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.RunE == nil || c.Hidden || c == root {
			return
		}
		path := strings.TrimPrefix(c.CommandPath(), root.Name()+" ")
		pc := reflect.ValueOf(c.RunE).Pointer()
		file, line := runtime.FuncForPC(pc).FileLine(pc)
		fn := a.funcs[pos{filepath.Base(file), line}]
		g.defs, g.seen = Schema{}, map[string]bool{}
		var variants []Schema
		seen := map[string]bool{}
		typed := true
		if fn != nil {
			for _, r := range a.results(fn) {
				s := g.expr(a, r)
				key := mustJSON(s)
				if seen[key] {
					continue
				}
				seen[key] = true
				if len(s) == 0 {
					typed = false
				}
				variants = append(variants, s)
			}
		}
		var result Schema
		switch len(variants) {
		case 0:
			result = Schema{"type": "null", "description": "The command has no result."}
		case 1:
			result = variants[0]
		default:
			sort.Slice(variants, func(i, j int) bool { return mustJSON(variants[i]) < mustJSON(variants[j]) })
			anyOf := []any{}
			for _, v := range variants {
				anyOf = append(anyOf, v)
			}
			result = Schema{"anyOf": anyOf}
		}
		name := strings.ReplaceAll(path, " ", "_") + ".json"
		doc := Schema{
			"$schema":         "https://json-schema.org/draft/2020-12/schema",
			"$id":             idBase + name,
			"title":           "cpictl " + path + " result",
			"description":     "The result field of the --output json envelope (envelope.json) of cpictl " + path + ".",
			"x-schemaVersion": schemaVersion,
		}
		for k, v := range result {
			doc[k] = v
		}
		if len(g.defs) > 0 {
			doc["$defs"] = g.defs
		}
		files[name] = append(mustIndent(doc), '\n')
		index = append(index, entry{Command: path, File: name, Typed: typed})
	}
	walk(root)
	sort.Slice(index, func(i, j int) bool { return index[i].Command < index[j].Command })
	files["index.json"] = append(mustIndent(Schema{"schemaVersion": schemaVersion, "commands": index}), '\n')

	g.defs, g.seen = Schema{}, map[string]bool{}
	env := g.named(a, a.lookup(outputPkg, "Envelope"))
	envDoc := Schema{
		"$schema":         "https://json-schema.org/draft/2020-12/schema",
		"$id":             idBase + "envelope.json",
		"title":           "cpictl --output json envelope",
		"description":     "Every command writes one envelope to stdout with --output json; result is described per command (index.json).",
		"x-schemaVersion": schemaVersion,
		"$ref":            env["$ref"],
		"$defs":           g.defs,
	}
	files["envelope.json"] = append(mustIndent(envDoc), '\n')
	return files, nil
}

// --- static analysis ---

type pos struct {
	file string
	line int
}

type fnNode struct {
	sets   []ast.Expr   // SetResult arguments
	calls  []callSite   // package functions called
	lits   []*fnNode    // nested function literals (may run as part of this one)
	params []*types.Var // parameters of a declared function
}

type callSite struct {
	fn   *types.Func
	args []ast.Expr
}

type analysis struct {
	info   *types.Info
	pkg    *types.Package
	funcs  map[pos]*fnNode
	byFunc map[*types.Func]*fnNode
	all    map[string]*types.Package
}

func (a *analysis) lookup(pkgPath, name string) types.Type {
	p := a.all[pkgPath]
	if p == nil {
		panic("schemagen: package not loaded: " + pkgPath)
	}
	return p.Scope().Lookup(name).Type()
}

// results are the SetResult arguments reachable from fn. A parameter of a
// called function that is passed on to SetResult is replaced by the
// caller's argument (emitContent(cmd, res, ...)).
func (a *analysis) results(fn *fnNode) []ast.Expr {
	var out []ast.Expr
	visited := map[*fnNode]bool{}
	var visit func(n *fnNode, args []ast.Expr)
	visit = func(n *fnNode, args []ast.Expr) {
		if n == nil {
			return
		}
		if args == nil {
			if visited[n] {
				return
			}
			visited[n] = true
		}
		for _, e := range n.sets {
			if id, ok := e.(*ast.Ident); ok && args != nil {
				if i := paramIndex(a, n, id); i >= 0 && i < len(args) {
					out = append(out, args[i])
					continue
				}
			}
			out = append(out, e)
		}
		for _, l := range n.lits {
			visit(l, nil)
		}
		for _, c := range n.calls {
			callee := a.byFunc[c.fn]
			if callee != nil && passesParam(a, callee) {
				visit(callee, c.args)
			} else {
				visit(callee, nil)
			}
		}
	}
	visit(fn, nil)
	return out
}

// passesParam: the function passes one of its parameters to SetResult.
func passesParam(a *analysis, n *fnNode) bool {
	for _, e := range n.sets {
		if id, ok := e.(*ast.Ident); ok && paramIndex(a, n, id) >= 0 {
			return true
		}
	}
	return false
}

// paramIndex is the index of the parameter id names (-1: none).
func paramIndex(a *analysis, n *fnNode, id *ast.Ident) int {
	v, ok := a.info.Uses[id].(*types.Var)
	if !ok {
		return -1
	}
	return slices.Index(n.params, v)
}

func analyze(moduleDir string) (*analysis, error) {
	cmd := exec.Command("go", "list", "-export", "-deps", "-json=ImportPath,Export,Dir,GoFiles", "./internal/cmd")
	cmd.Dir = moduleDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	exports := map[string]string{}
	var dir string
	var files []string
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p struct {
			ImportPath, Export, Dir string
			GoFiles                 []string
		}
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		exports[p.ImportPath] = p.Export
		if p.ImportPath == cmdPkg {
			dir, files = p.Dir, p.GoFiles
		}
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, f := range files {
		af, err := parser.ParseFile(fset, filepath.Join(dir, f), nil, 0)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, af)
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok || f == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	})
	a := &analysis{
		info:   &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}},
		funcs:  map[pos]*fnNode{},
		byFunc: map[*types.Func]*fnNode{},
		all:    map[string]*types.Package{},
	}
	conf := types.Config{Importer: imp}
	a.pkg, err = conf.Check(cmdPkg, fset, parsed, a.info)
	if err != nil {
		return nil, err
	}
	var collect func(p *types.Package)
	collect = func(p *types.Package) {
		if a.all[p.Path()] != nil {
			return
		}
		a.all[p.Path()] = p
		for _, i := range p.Imports() {
			collect(i)
		}
	}
	collect(a.pkg)

	for _, f := range parsed {
		var stack []*fnNode
		push := func(n ast.Node) *fnNode {
			fn := &fnNode{}
			p := fset.Position(n.Pos())
			a.funcs[pos{filepath.Base(p.Filename), p.Line}] = fn
			if len(stack) > 0 {
				stack[len(stack)-1].lits = append(stack[len(stack)-1].lits, fn)
			}
			return fn
		}
		var inspect func(n ast.Node) bool
		inspect = func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Body == nil {
					return false
				}
				fn := push(n)
				if obj, ok := a.info.Defs[n.Name].(*types.Func); ok {
					a.byFunc[obj] = fn
					sig := obj.Type().(*types.Signature)
					for i := 0; i < sig.Params().Len(); i++ {
						fn.params = append(fn.params, sig.Params().At(i))
					}
				}
				stack = append(stack, fn)
				ast.Inspect(n.Body, inspect)
				stack = stack[:len(stack)-1]
				return false
			case *ast.FuncLit:
				fn := push(n)
				stack = append(stack, fn)
				ast.Inspect(n.Body, inspect)
				stack = stack[:len(stack)-1]
				return false
			case *ast.CallExpr:
				if len(stack) == 0 {
					return true
				}
				cur := stack[len(stack)-1]
				var id *ast.Ident
				switch fun := n.Fun.(type) {
				case *ast.Ident:
					id = fun
				case *ast.SelectorExpr:
					id = fun.Sel
				}
				if id == nil {
					return true
				}
				obj, ok := a.info.Uses[id].(*types.Func)
				if !ok || obj.Pkg() == nil {
					return true
				}
				switch {
				case obj.Pkg().Path() == outputPkg && obj.Name() == "SetResult" && len(n.Args) == 2:
					cur.sets = append(cur.sets, n.Args[1])
				case obj.Pkg() == a.pkg:
					cur.calls = append(cur.calls, callSite{fn: obj, args: n.Args})
				}
			}
			return true
		}
		ast.Inspect(f, inspect)
	}
	return a, nil
}

// --- schemas from types ---

type gen struct {
	defs Schema
	seen map[string]bool
}

// expr is the schema of a SetResult argument: a map literal with constant
// keys is an object with those properties.
func (g *gen) expr(a *analysis, e ast.Expr) Schema {
	if p, ok := e.(*ast.ParenExpr); ok {
		return g.expr(a, p.X)
	}
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		return g.expr(a, u.X)
	}
	if cl, ok := e.(*ast.CompositeLit); ok {
		if m, ok := a.info.TypeOf(cl).Underlying().(*types.Map); ok && isString(m.Key()) {
			props := Schema{}
			var required []string
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					return g.typ(a, a.info.TypeOf(cl))
				}
				tv := a.info.Types[kv.Key]
				if tv.Value == nil || tv.Value.Kind() != constant.String {
					return g.typ(a, a.info.TypeOf(cl))
				}
				k := constant.StringVal(tv.Value)
				props[k] = g.expr(a, kv.Value)
				required = append(required, k)
			}
			sort.Strings(required)
			s := Schema{"type": "object", "properties": props}
			if len(required) > 0 {
				s["required"] = required
			}
			return s
		}
	}
	return g.typ(a, a.info.TypeOf(e))
}

func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

func (g *gen) typ(a *analysis, t types.Type) Schema {
	if t == nil {
		return Schema{}
	}
	if p, ok := t.(*types.Pointer); ok {
		return g.typ(a, p.Elem())
	}
	if n, ok := t.(*types.Named); ok {
		obj := n.Obj()
		if obj.Pkg() != nil {
			switch obj.Pkg().Path() + "." + obj.Name() {
			case "time.Time":
				return Schema{"type": "string", "format": "date-time"}
			case "time.Duration":
				return Schema{"type": "integer", "description": "nanoseconds"}
			case "encoding/json.RawMessage":
				return Schema{}
			}
		}
		if marshaler(n) {
			return Schema{"description": "custom JSON encoding (" + obj.Name() + ")"}
		}
		if _, ok := n.Underlying().(*types.Struct); ok {
			return g.named(a, n)
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return Schema{"type": "boolean"}
		case u.Info()&types.IsInteger != 0:
			return Schema{"type": "integer"}
		case u.Info()&types.IsFloat != 0:
			return Schema{"type": "number"}
		case u.Info()&types.IsString != 0:
			return Schema{"type": "string"}
		}
		return Schema{}
	case *types.Slice:
		if b, ok := u.Elem().Underlying().(*types.Basic); ok && b.Kind() == types.Byte {
			return Schema{"type": "string", "contentEncoding": "base64"}
		}
		return Schema{"type": "array", "items": g.typ(a, u.Elem())}
	case *types.Array:
		return Schema{"type": "array", "items": g.typ(a, u.Elem())}
	case *types.Map:
		return Schema{"type": "object", "additionalProperties": g.typ(a, u.Elem())}
	case *types.Struct:
		return g.structSchema(a, u)
	case *types.Interface:
		return Schema{}
	}
	return Schema{}
}

func marshaler(n *types.Named) bool {
	for _, t := range []types.Type{n, types.NewPointer(n)} {
		ms := types.NewMethodSet(t)
		for i := 0; i < ms.Len(); i++ {
			if ms.At(i).Obj().Name() == "MarshalJSON" {
				return true
			}
		}
	}
	return false
}

// named returns a $ref to the named struct type, defined once in $defs.
func (g *gen) named(a *analysis, t types.Type) Schema {
	n := t.(*types.Named)
	obj := n.Obj()
	name := obj.Name()
	if obj.Pkg() != nil {
		name = filepath.Base(obj.Pkg().Path()) + "." + obj.Name()
	}
	ref := Schema{"$ref": "#/$defs/" + name}
	if g.seen[name] {
		return ref
	}
	g.seen[name] = true
	g.defs[name] = g.structSchema(a, n.Underlying().(*types.Struct))
	return ref
}

func (g *gen) structSchema(a *analysis, st *types.Struct) Schema {
	props := Schema{}
	required := map[string]bool{}
	g.fields(a, st, props, required, map[*types.Struct]bool{})
	s := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		var req []string
		for k := range required {
			req = append(req, k)
		}
		sort.Strings(req)
		s["required"] = req
	}
	return s
}

// fields adds the JSON fields of st to props. Fields of embedded structs
// are inlined, as encoding/json does; a field of the outer struct wins.
func (g *gen) fields(a *analysis, st *types.Struct, props Schema, required map[string]bool, visiting map[*types.Struct]bool) {
	if visiting[st] {
		return
	}
	visiting[st] = true
	var embedded []*types.Struct
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" && opts == "" {
			continue
		}
		if f.Embedded() && name == "" {
			t := f.Type()
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			if est, ok := t.Underlying().(*types.Struct); ok {
				embedded = append(embedded, est)
				continue
			}
		}
		if !f.Exported() {
			continue
		}
		if name == "" {
			name = f.Name()
		}
		optList := strings.Split(opts, ",")
		omit := slices.Contains(optList, "omitempty") || slices.Contains(optList, "omitzero")
		s := g.typ(a, f.Type())
		if !omit && nullable(f.Type()) && len(s) > 0 {
			s = Schema{"anyOf": []any{s, Schema{"type": "null"}}}
		}
		if slices.Contains(optList, "string") {
			s = Schema{"type": "string"}
		}
		props[name] = s
		if !omit {
			required[name] = true
		}
	}
	for _, est := range embedded {
		inner, innerReq := Schema{}, map[string]bool{}
		g.fields(a, est, inner, innerReq, visiting)
		for k, v := range inner {
			if _, taken := props[k]; !taken {
				props[k] = v
				if innerReq[k] {
					required[k] = true
				}
			}
		}
	}
}

// nullable: the zero value marshals as null.
func nullable(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Interface:
		if b, ok := t.Underlying().(*types.Slice); ok {
			if e, ok := b.Elem().Underlying().(*types.Basic); ok && e.Kind() == types.Byte {
				return false // []byte: "" (base64) — nil is null too, but rare
			}
		}
		return true
	}
	return false
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func mustIndent(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
