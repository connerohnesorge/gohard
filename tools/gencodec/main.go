// Command gencodec writes internal/sema/codec_gen.go, the field-by-field
// code of the prebuilt-bundle codec (#33; the format is described in
// internal/sema/codec.go).
//
// It type-checks internal/sema from source (a stale codec_gen.go may
// not compile; its errors are ignored) and walks every type reachable
// from sema.Result. For each struct type it writes an encoder and a
// decoder of its fields, and for each pointer-to-struct type a table
// that keeps the pointers' identity. A few types are written by hand
// in codec.go: *cst.Green, cst.Node, cst.Token (unexported fields of
// another package), *span.Index (rebuilt from the file's text) and
// *parser.Result (its source bytes). Any other struct of another
// package must have exported fields only. A field tagged `codec:"-"`
// is neither written nor read.
//
//	go run ./tools/gencodec -root .
//
// The output is a pure function of the type definitions, so the
// generated-file sync check (#30) fails when a sema type changes and
// codec_gen.go is not regenerated. codecSchema is the hash of the
// generated code: a prebuilt blob of another schema is never decoded.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"go/format"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	modPath  = "github.com/connerohnesorge/gohard/v2/"
	semaPath = modPath + "internal/sema"
	outPath  = "internal/sema/codec_gen.go"
)

// special are the types codec.go writes by hand, by type string, with
// the encoder and decoder calls (%s is the value).
var special = map[string][2]string{
	modPath + "internal/cst.Node":        {"e.node(%s)", "d.node()"},
	modPath + "internal/cst.Token":       {"e.token(%s)", "d.token()"},
	"*" + modPath + "internal/cst.Green": {"e.green(%s)", "d.green()"},
	"*" + modPath + "internal/parser.Result": {"e.parseResult(%s)",
		"d.parseResult()"},
}

// partial are the structs of other packages whose exported fields are
// generated and whose unexported ones codec.go handles.
var partial = map[string]bool{modPath + "internal/parser.Result": true}

const spanIndex = "*" + modPath + "internal/span.Index"

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	src, err := generate(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gencodec:", err)
		os.Exit(1)
	}
	name := filepath.Join(*root, filepath.FromSlash(outPath))
	old, _ := os.ReadFile(name)
	if bytes.Equal(old, src) {
		return
	}
	if err := os.WriteFile(name, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gencodec:", err)
		os.Exit(1)
	}
}

func generate(root string) ([]byte, error) {
	cfg := &packages.Config{Dir: root, Mode: packages.NeedName |
		packages.NeedTypes | packages.NeedSyntax | packages.NeedImports,
		Tests: false}
	pkgs, err := packages.Load(cfg, "./internal/sema")
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 || pkgs[0].Types == nil {
		return nil, fmt.Errorf("cannot load %s", semaPath)
	}
	obj := pkgs[0].Types.Scope().Lookup("Result")
	if obj == nil {
		return nil, fmt.Errorf("sema.Result not found")
	}
	g := &gen{sema: pkgs[0].Types, done: map[string]bool{},
		imports: map[string]string{}}
	if err := g.walk(obj.Type()); err != nil {
		return nil, err
	}
	return g.file()
}

type gen struct {
	sema    *types.Package
	done    map[string]bool
	order   []*types.Named // structs, in walk order
	ptrs    []*types.Named // pointed-to structs with a table
	imports map[string]string
	vars    int
	err     error
}

// walk records every struct type reachable from t.
func (g *gen) walk(t types.Type) error {
	key := t.String()
	if g.done[key] {
		return nil
	}
	g.done[key] = true
	if _, ok := special[key]; ok || key == spanIndex {
		if key == "*"+modPath+"internal/parser.Result" {
			return g.walk(t.(*types.Pointer).Elem())
		}
		return nil
	}
	switch u := t.(type) {
	case *types.Named:
		st, ok := u.Underlying().(*types.Struct)
		if !ok {
			return g.walk(u.Underlying())
		}
		g.order = append(g.order, u)
		foreign := u.Obj().Pkg() != g.sema
		for i, f := range fields(st) {
			if skipped(st, i) {
				continue
			}
			if foreign && !f.Exported() {
				if partial[key] {
					continue
				}
				return fmt.Errorf("%s.%s: unexported field of another "+
					"package; write the type by hand in codec.go", key,
					f.Name())
			}
			if err := g.walk(f.Type()); err != nil {
				return fmt.Errorf("%s.%s: %w", key, f.Name(), err)
			}
		}
		return nil
	case *types.Pointer:
		n, ok := u.Elem().(*types.Named)
		if !ok {
			return fmt.Errorf("pointer to %s", u.Elem())
		}
		if _, ok := n.Underlying().(*types.Struct); !ok {
			return fmt.Errorf("pointer to %s", n)
		}
		g.ptrs = append(g.ptrs, n)
		return g.walk(n)
	case *types.Slice:
		return g.walk(u.Elem())
	case *types.Map:
		if err := g.walk(u.Key()); err != nil {
			return err
		}
		return g.walk(u.Elem())
	case *types.Basic:
		switch u.Info() & (types.IsInteger | types.IsFloat |
			types.IsBoolean | types.IsString) {
		case 0:
			return fmt.Errorf("unsupported basic type %s", u)
		}
		if u.Kind() == types.Float32 || u.Kind() == types.Uintptr {
			return fmt.Errorf("unsupported basic type %s", u)
		}
		return nil
	}
	return fmt.Errorf("unsupported type %s", t)
}

// fields returns the fields of st with their indices.
func fields(st *types.Struct) func(func(int, *types.Var) bool) {
	return func(yield func(int, *types.Var) bool) {
		for i := range st.NumFields() {
			if !yield(i, st.Field(i)) {
				return
			}
		}
	}
}

// skipped reports whether field i of st is tagged `codec:"-"`: state
// that is never stored (a result's memory of the analysis it reused,
// #53), so the codec neither writes nor reads it.
func skipped(st *types.Struct, i int) bool {
	return reflect.StructTag(st.Tag(i)).Get("codec") == "-"
}

// id is the Go identifier part of a named type: Decl, eval_Type.
func (g *gen) id(n *types.Named) string {
	if n.Obj().Pkg() == g.sema {
		return n.Obj().Name()
	}
	return n.Obj().Pkg().Name() + "_" + n.Obj().Name()
}

func (g *gen) qual(p *types.Package) string {
	if p == g.sema {
		return ""
	}
	g.imports[p.Path()] = p.Name()
	return p.Name()
}

func (g *gen) typ(t types.Type) string { return types.TypeString(t, g.qual) }

func (g *gen) v(prefix string) string {
	g.vars++
	return fmt.Sprintf("%s%d", prefix, g.vars)
}

// enc returns statements writing the value x of type t. x is
// addressable.
func (g *gen) enc(t types.Type, x string) string {
	if s, ok := special[t.String()]; ok {
		return fmt.Sprintf(s[0], x) + "\n"
	}
	switch u := t.(type) {
	case *types.Pointer:
		return fmt.Sprintf("e.p_%s(%s)\n", g.id(u.Elem().(*types.Named)), x)
	case *types.Named:
		if _, ok := u.Underlying().(*types.Struct); ok {
			return fmt.Sprintf("e.s_%s(&%s)\n", g.id(u), x)
		}
		return g.enc(u.Underlying(), x)
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return fmt.Sprintf("e.bool(bool(%s))\n", x)
		case u.Info()&types.IsString != 0:
			return fmt.Sprintf("e.str(string(%s))\n", x)
		case u.Info()&types.IsFloat != 0:
			return fmt.Sprintf("e.float(float64(%s))\n", x)
		case u.Info()&types.IsUnsigned != 0:
			return fmt.Sprintf("e.uint(uint64(%s))\n", x)
		}
		return fmt.Sprintf("e.int(int64(%s))\n", x)
	case *types.Slice:
		if b, ok := u.Elem().(*types.Basic); ok && b.Kind() == types.Byte {
			return fmt.Sprintf("e.bytes(%s)\n", x)
		}
		i := g.v("i")
		return fmt.Sprintf("if %s == nil {\ne.uint(0)\n} else {\n"+
			"e.uint(uint64(len(%s)) + 1)\nfor %s := range %s {\n%s}\n}\n",
			x, x, i, x, g.enc(u.Elem(), x+"["+i+"]"))
	case *types.Map:
		return g.encMap(u, x)
	}
	g.err = fmt.Errorf("enc: unsupported %s", t)
	return ""
}

func (g *gen) encMap(m *types.Map, x string) string {
	k, v := g.v("k"), g.v("v")
	body := fmt.Sprintf("%s := %s[%s]\n%s%s", v, x, k,
		g.enc(m.Key(), k), g.enc(m.Elem(), v))
	head := fmt.Sprintf("if %s == nil {\ne.uint(0)\n} else {\n"+
		"e.uint(uint64(len(%s)) + 1)\n", x, x)
	switch kt := m.Key().(type) {
	case *types.Pointer:
		n := g.id(kt.Elem().(*types.Named))
		c := g.v("n")
		l := g.v("l")
		return fmt.Sprintf("e.later = append(e.later, func() {\n%s"+
			"%s := 0\n%s := len(e.t.p_%s.list)\n"+
			"for _, %s := range e.t.p_%s.list {\n"+
			"if _, ok := %s[%s]; !ok {\ncontinue\n}\n%s++\n%s}\n"+
			"for _, %s := range extraKeys(e, &e.t.p_%s, %s, %s, %s) {\n"+
			"%s++\n%s}\n"+
			"if %s != len(%s) {\npanic(\"sema: codec: map key "+
			"unreachable\")\n}\n}\n})\n", head, c, l, n, k, n, x, k, c,
			body, k, n, x, l, c, c, body, c, x)
	case *types.Named:
		if st, ok := kt.Underlying().(*types.Struct); ok {
			var cmps []string
			for f := range st.Fields() {
				if _, ok := f.Type().Underlying().(*types.Basic); !ok {
					g.err = fmt.Errorf("map key %s", kt)
				}
				cmps = append(cmps, fmt.Sprintf("cmp.Compare(a.%s, b.%s)",
					f.Name(), f.Name()))
			}
			g.imports["cmp"] = "cmp"
			g.imports["maps"] = "maps"
			g.imports["slices"] = "slices"
			ks := g.v("ks")
			return fmt.Sprintf("%s%s := slices.Collect(maps.Keys(%s))\n"+
				"slices.SortFunc(%s, func(a, b %s) int {\n"+
				"return cmp.Or(%s)\n})\nfor _, %s := range %s {\n%s}\n}\n",
				head, ks, x, ks, g.typ(kt), strings.Join(cmps, ", "), k,
				ks, body)
		}
	}
	if _, ok := m.Key().Underlying().(*types.Basic); !ok {
		g.err = fmt.Errorf("map key %s", m.Key())
	}
	g.imports["maps"] = "maps"
	g.imports["slices"] = "slices"
	return fmt.Sprintf("%sfor _, %s := range slices.Sorted(maps.Keys(%s)) "+
		"{\n%s}\n}\n", head, k, x, body)
}

// dec returns statements decoding a value of type t into x.
func (g *gen) dec(t types.Type, x string) string {
	if s, ok := special[t.String()]; ok {
		return x + " = " + s[1] + "\n"
	}
	switch u := t.(type) {
	case *types.Pointer:
		return fmt.Sprintf("%s = d.p_%s()\n", x,
			g.id(u.Elem().(*types.Named)))
	case *types.Named:
		if _, ok := u.Underlying().(*types.Struct); ok {
			return fmt.Sprintf("d.s_%s(&%s)\n", g.id(u), x)
		}
		return g.decBasic(u, u.Underlying(), x)
	case *types.Basic:
		return g.decBasic(u, u, x)
	case *types.Slice:
		if b, ok := u.Elem().(*types.Basic); ok && b.Kind() == types.Byte {
			return fmt.Sprintf("%s = d.bytes()\n", x)
		}
		n, i := g.v("n"), g.v("i")
		return fmt.Sprintf("if %s, ok := d.count(); ok {\n"+
			"%s = make(%s, %s)\nfor %s := range %s {\n%s}\n}\n", n, x,
			g.typ(u), n, i, x, g.dec(u.Elem(), x+"["+i+"]"))
	case *types.Map:
		n, k, v := g.v("n"), g.v("k"), g.v("v")
		s := fmt.Sprintf("if %s, ok := d.count(); ok {\n"+
			"%s = make(%s, %s)\nfor range %s {\nvar %s %s\nvar %s %s\n"+
			"%s%s%s[%s] = %s\n}\n}\n", n, x, g.typ(u), n, n, k,
			g.typ(u.Key()), v, g.typ(u.Elem()), g.dec(u.Key(), k),
			g.dec(u.Elem(), v), x, k, v)
		if _, ok := u.Key().(*types.Pointer); ok {
			return "d.later = append(d.later, func() {\n" + s + "})\n"
		}
		return s
	}
	g.err = fmt.Errorf("dec: unsupported %s", t)
	return ""
}

func (g *gen) decBasic(t types.Type, b types.Type, x string) string {
	u := b.(*types.Basic)
	call := "d.int()"
	switch {
	case u.Info()&types.IsBoolean != 0:
		call = "d.bool()"
	case u.Info()&types.IsString != 0:
		call = "d.str()"
	case u.Info()&types.IsFloat != 0:
		call = "d.float()"
	case u.Info()&types.IsUnsigned != 0:
		call = "d.uint()"
	}
	return fmt.Sprintf("%s = %s(%s)\n", x, g.typ(t), call)
}

func (g *gen) file() ([]byte, error) {
	var body bytes.Buffer
	w := func(f string, a ...any) { fmt.Fprintf(&body, f, a...) }
	ptrs := slices.Clone(g.ptrs)
	w("// encTables and decTables hold one table per pointer type.\n")
	w("type encTables struct {\n")
	for _, p := range ptrs {
		w("p_%s encTable[%s]\n", g.id(p), g.typ(p))
	}
	w("}\n\ntype decTables struct {\n")
	for _, p := range ptrs {
		w("p_%s decTable[%s]\n", g.id(p), g.typ(p))
	}
	w("}\n\n")
	for _, p := range ptrs {
		id := g.id(p)
		w("func (e *encoder) p_%s(p *%s) { e.t.p_%s.ref(e, p, e.s_%s) }"+
			"\n\n", id, g.typ(p), id, id)
		w("func (d *decoder) p_%s() *%s { return d.t.p_%s.ref(d, "+
			"d.s_%s) }\n\n", id, g.typ(p), id, id)
	}
	for _, n := range g.order {
		id := g.id(n)
		st := n.Underlying().(*types.Struct)
		var enc, dec strings.Builder
		for i, f := range fields(st) {
			if skipped(st, i) || n.Obj().Pkg() != g.sema && !f.Exported() {
				continue
			}
			x := "v." + f.Name()
			if f.Type().String() == spanIndex {
				enc.WriteString("e.lines(" + x + ", v.Text)\n")
				g.imports[modPath+"internal/span"] = "span"
				dec.WriteString("if d.bool() {\n" + x +
					" = span.NewIndex(v.Text)\n}\n")
				continue
			}
			enc.WriteString(g.enc(f.Type(), x))
			dec.WriteString(g.dec(f.Type(), x))
		}
		w("func (e *encoder) s_%s(v *%s) {\n%s}\n\n", id, g.typ(n),
			enc.String())
		w("func (d *decoder) s_%s(v *%s) {\n%s}\n\n", id, g.typ(n),
			dec.String())
	}
	if g.err != nil {
		return nil, g.err
	}
	sum := sha256.Sum256(body.Bytes())
	var out bytes.Buffer
	out.WriteString("// Code generated by tools/gencodec; DO NOT EDIT.\n\n" +
		"package sema\n\nimport (\n")
	paths := make([]string, 0, len(g.imports))
	for p := range g.imports {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		fmt.Fprintf(&out, "%q\n", p)
	}
	fmt.Fprintf(&out, ")\n\n// codecSchema is the hash of the code "+
		"below.\nconst codecSchema = %q\n\n",
		hex.EncodeToString(sum[:]))
	out.Write(body.Bytes())
	return format.Source(out.Bytes())
}
