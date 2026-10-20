package stdlib

import (
	"context"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

// TestManifest: the manifest names exactly the embedded VHDL files, each
// under its library folder, declaring (or holding the body of) the unit
// it claims, and in analysis order: every std/ieee/work unit a file
// names is declared by an earlier file (or is the file's own unit).
func TestManifest(t *testing.T) {
	embedded, err := fs.Glob(sources, "*/*.vhdl")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, f := range manifest {
		listed = append(listed, f.Path)
	}
	if !slices.Equal(slices.Sorted(slices.Values(listed)), embedded) {
		t.Fatalf("manifest %v\nembedded %v", listed, embedded)
	}
	units := map[string]bool{}
	for _, f := range manifest {
		units[f.Library+"."+f.Unit] = true
	}
	declared := map[string]bool{}
	for _, f := range manifest {
		if path.Dir(f.Path) != f.Library {
			t.Errorf("%s: library %s", f.Path, f.Library)
		}
		src, _ := Source(f.Path)
		root := parser.Parse(src, parser.Options{
			Standard: token.Std2019}).Tree()
		name, body := unitOf(root)
		if name != f.Unit || body != f.Body {
			t.Errorf("%s: unit %s body %v, manifest %s %v", f.Path, name,
				body, f.Unit, f.Body)
		}
		if f.Body != declared[f.Library+"."+f.Unit] {
			t.Errorf("%s: body before its declaration, or a second "+
				"declaration", f.Path)
		}
		for _, ref := range unitRefs(root, f.Library) {
			if units[ref] && !declared[ref] &&
				ref != f.Library+"."+f.Unit {
				t.Errorf("%s names %s before it is declared", f.Path,
					ref)
			}
		}
		declared[f.Library+"."+f.Unit] = true
	}
}

// unitOf returns the lower-case name of the one library unit in root and
// whether it is a package body.
func unitOf(root cst.Node) (string, bool) {
	for n := range root.Descendants() {
		switch n.Kind() {
		case cst.KindPackageDecl:
			p, _ := cst.As[cst.PackageDecl](n)
			return strings.ToLower(p.Name().Text()), false
		case cst.KindPackageInstantiation:
			p, _ := cst.As[cst.PackageInstantiation](n)
			return strings.ToLower(p.Name().Text()), false
		case cst.KindPackageBody:
			p, _ := cst.As[cst.PackageBody](n)
			return strings.ToLower(p.Name().Text()), true
		}
	}
	return "", false
}

// unitRefs returns "lib.unit" for every selected name whose first two
// segments are a library (std, ieee or work, meaning lib) and a name.
func unitRefs(root cst.Node, lib string) []string {
	var out []string
	for n := range root.Descendants() {
		if n.Kind() != cst.KindSelectedName {
			continue
		}
		var ids []string
		for t := range n.DescendantTokens() {
			if t.Kind() == token.Identifier {
				ids = append(ids, strings.ToLower(t.Text()))
			}
		}
		if len(ids) < 2 {
			continue
		}
		l := ids[0]
		if l == "work" {
			l = lib
		}
		out = append(out, l+"."+ids[1])
	}
	return out
}

// TestParse: every bundled file parses with no problem at all under
// VHDL-2008 and VHDL-2019 (the PR parser-regression run also checks
// this, #33).
func TestParse(t *testing.T) {
	for _, f := range manifest {
		src, _ := Source(f.Path)
		for _, std := range []token.Standard{token.Std2008,
			token.Std2019} {
			r := parser.Parse(src, parser.Options{Standard: std})
			if ds := r.Diagnostics(f.Path); len(ds) > 0 || r.TooLarge {
				t.Errorf("%s under %s: %v", f.Path, std, ds)
			}
		}
	}
}

func TestVerifyClean(t *testing.T) {
	if ds := Verify(context.Background()); len(ds) > 0 {
		t.Fatalf("Verify: %v", ds)
	}
	var paths []string
	for _, s := range Sums() {
		paths = append(paths, s.Path)
	}
	if !slices.Equal(paths, hashed()) {
		t.Fatalf("SHA256SUMS lists %v, want %v", paths, hashed())
	}
}

// TestVerifyTamper: tampering any one bundled file reports exactly that
// file as a tool failure, and tampering never changes the bytes other
// callers see.
func TestVerifyTamper(t *testing.T) {
	for _, p := range hashed() {
		before, _ := sources.ReadFile(p)
		ds := Verify(WithTamper(context.Background(), p))
		want := []diag.Diagnostic{diag.New(diag.StdlibIntegrity, p)}
		if !slices.EqualFunc(ds, want, sameDiag) {
			t.Errorf("tamper %s: %v", p, ds)
		}
		if ds[0].Class() != diag.Tool {
			t.Errorf("tamper %s: class %v, want tool", p, ds[0].Class())
		}
		after, _ := sources.ReadFile(p)
		if string(before) != string(after) {
			t.Fatalf("tamper %s changed the embedded bytes", p)
		}
	}
	two := WithTamper(WithTamper(context.Background(),
		"std/standard.vhdl"), "LICENSE")
	if ds := Verify(two); len(ds) != 2 || ds[0].Message > ds[1].Message {
		t.Errorf("two tampered files: %v", ds)
	}
}

func sameDiag(a, b diag.Diagnostic) bool { return diag.Compare(a, b) == 0 }

// TestVerifyCases covers the manifest failures Verify cannot reach
// through the embedded files: a missing hash line, a wrong hash, an
// unreadable file, and malformed SHA256SUMS lines.
func TestVerifyCases(t *testing.T) {
	read := func(p string) ([]byte, bool) {
		b, err := sources.ReadFile(p)
		return b, err == nil
	}
	sums := Sums()
	if ds := verify(sums, read); len(ds) != 0 {
		t.Fatalf("clean: %v", ds)
	}
	drop := slices.Clone(sums[1:])
	if ds := verify(drop, read); len(ds) != 1 ||
		!strings.Contains(ds[0].Message, sums[0].Path) {
		t.Errorf("missing hash: %v", ds)
	}
	wrong := slices.Clone(sums)
	wrong[2].Hex = strings.Repeat("0", 64)
	if ds := verify(wrong, read); len(ds) != 1 ||
		!strings.Contains(ds[0].Message, sums[2].Path) {
		t.Errorf("wrong hash: %v", ds)
	}
	gone := func(p string) ([]byte, bool) {
		if p == sums[3].Path {
			return nil, false
		}
		return read(p)
	}
	if ds := verify(sums, gone); len(ds) != 1 {
		t.Errorf("unreadable: %v", ds)
	}
	got := parseSums(strings.Repeat("a", 64) + "  ok/x.vhdl\n" +
		"short  a.vhdl\n" + strings.Repeat("b", 64) + "  ../up\n" +
		strings.Repeat("c", 64) + " one-space\n")
	if len(got) != 1 || got[0].Path != "ok/x.vhdl" {
		t.Errorf("parseSums: %v", got)
	}
}

// TestViews: for every standard, the view shows exactly the
// declarations tagged at or before it (#33 Done when), views grow with
// the standard, and no declaration is older than its package.
func TestViews(t *testing.T) {
	all := Decls()
	if len(all) == 0 {
		t.Fatal("no declarations")
	}
	pkgFirst := map[string]token.Standard{}
	for _, d := range all {
		if d.Kind == "package" {
			pkgFirst[d.Library+"."+d.Unit] = d.First
		}
	}
	for _, d := range all {
		if p, ok := pkgFirst[d.Library+"."+d.Unit]; !ok || d.First < p {
			t.Errorf("%s: first %s, package first %s", d.Key(),
				d.First, p)
		}
		if d.First == token.Std2002 {
			t.Errorf("%s: no std/ieee declaration is new in 2002",
				d.Key())
		}
	}
	var prev []Decl
	for _, std := range token.Standards {
		v := ViewOf(std)
		var want []Decl
		for _, d := range all {
			if d.First <= std {
				want = append(want, d)
			}
		}
		got := v.Decls()
		if !slices.EqualFunc(got, want, sameDecl) {
			t.Errorf("view %s: %d declarations, want %d", std,
				len(got), len(want))
		}
		for _, d := range prev {
			if !v.Visible(d) {
				t.Errorf("view %s hides %s, visible in an older view",
					std, d.Key())
			}
		}
		prev = got
	}
	if !slices.EqualFunc(ViewOf(token.Std1993).Decls(),
		ViewOf(token.Std2002).Decls(), sameDecl) {
		t.Error("the 1993 and 2002 views differ")
	}
	if n := len(ViewOf(token.Std2019).Decls()); n != len(all) {
		t.Errorf("view 2019 shows %d of %d", n, len(all))
	}
}

func sameDecl(a, b Decl) bool { return a == b }

// TestViewExamples pins well-known history (README.md).
func TestViewExamples(t *testing.T) {
	byKey := map[string]Decl{}
	for _, d := range Decls() {
		byKey[d.Key()] = d
	}
	cases := []struct {
		key   string
		first token.Standard
	}{
		{"std.standard type BIT", token.Std1993},
		{"std.standard subtype DELAY_LENGTH", token.Std1993},
		{"std.standard type INTEGER_VECTOR", token.Std2008},
		{"std.standard type DIRECTION", token.Std2019},
		{"std.textio procedure WRITE [LINE, REAL, STRING]",
			token.Std2008},
		{"std.textio type LINE_VECTOR", token.Std2019},
		{"std.env package ENV", token.Std2008},
		{"std.env procedure STOP []", token.Std2008},
		{"std.env function GETENV [STRING return STRING]",
			token.Std2019},
		{"std.reflection package REFLECTION", token.Std2019},
		{"ieee.std_logic_1164 subtype STD_LOGIC_VECTOR", token.Std1993},
		{"ieee.std_logic_1164 function \"and\" [STD_ULOGIC_VECTOR " +
			"return STD_ULOGIC]", token.Std2008},
		{"ieee.numeric_std type UNRESOLVED_UNSIGNED", token.Std2008},
		{"ieee.numeric_std subtype UNSIGNED", token.Std1993},
		{"ieee.numeric_std function \"+\" [UNRESOLVED_UNSIGNED, " +
			"UNRESOLVED_UNSIGNED return UNRESOLVED_UNSIGNED]",
			token.Std1993},
		{"ieee.numeric_std function \"?=\" [UNRESOLVED_UNSIGNED, " +
			"UNRESOLVED_UNSIGNED return STD_ULOGIC]", token.Std2008},
		{"ieee.numeric_std function TO_HSTRING [UNRESOLVED_UNSIGNED " +
			"return STRING]", token.Std2008},
		{"ieee.math_real constant MATH_PI", token.Std1993},
		{"ieee.fixed_pkg package FIXED_PKG", token.Std2008},
		{"ieee.float_generic_pkg package FLOAT_GENERIC_PKG",
			token.Std2008},
	}
	for _, c := range cases {
		d, ok := byKey[c.key]
		if !ok {
			t.Errorf("no declaration %q", c.key)
			continue
		}
		if d.First != c.first {
			t.Errorf("%s: first %s, want %s", c.key, d.First, c.first)
		}
	}
	if ViewOf(token.Std2002).HasUnit("ieee", "fixed_pkg") ||
		!ViewOf(token.Std2008).HasUnit("ieee", "fixed_pkg") ||
		ViewOf(token.Std2008).HasUnit("std", "reflection") ||
		!ViewOf(token.Std2019).HasUnit("std", "reflection") ||
		ViewOf(token.Std2019).HasUnit("ieee", "no_such_pkg") {
		t.Error("HasUnit")
	}
}

// TestDeclSpans: every declaration's span is its name in its file, so
// go-to-definition lands on it.
func TestDeclSpans(t *testing.T) {
	for _, d := range Decls() {
		src, ok := Source(d.Path)
		if !ok || d.Span.Start < 0 || d.Span.End > len(src) ||
			d.Span.Start >= d.Span.End {
			t.Fatalf("%s: span %v", d.Key(), d.Span)
		}
		name := string(src[d.Span.Start:d.Span.End])
		if !strings.EqualFold(name, d.Name) {
			t.Errorf("%s: span text %q", d.Key(), name)
		}
	}
}

// TestGHDL2008Set: the 2008 view of ieee and std has exactly GHDL's
// VHDL-2008 declaration set (#33 Done when), up to the reviewed
// differences: GHDL's empty std_logic_textio and its missing textio
// WRITE with a format string.
func TestGHDL2008Set(t *testing.T) {
	ref := readRef(t, token.Std2008)
	view := map[string]bool{}
	for _, d := range ViewOf(token.Std2008).Decls() {
		k := MatchKey(d)
		view[k] = true
		if !ref[k] && !ghdlLacks(d) {
			t.Errorf("view 2008 has %q; GHDL 2008 does not", k)
		}
	}
	for k := range ref {
		if !view[k] && !refMissing[k] {
			t.Errorf("GHDL 2008 has %q; view 2008 does not", k)
		}
	}
}

func ghdlLacks(d Decl) bool {
	return d.Library == "ieee" && d.Unit == "std_logic_textio" &&
		d.Kind == "alias" ||
		d.Key() == "std.textio procedure WRITE [LINE, REAL, STRING]"
}

// TestDeterministic: Decls is a pure function of the embedded bytes.
func TestDeterministic(t *testing.T) {
	a, b := untagged(), untagged()
	if !slices.EqualFunc(a, b, sameDecl) {
		t.Fatal("two extractions differ")
	}
	if !slices.EqualFunc(Decls(), Decls(), sameDecl) {
		t.Fatal("Decls differs between calls")
	}
}

func TestLookup(t *testing.T) {
	f, ok := Lookup("ieee/numeric_std.vhdl")
	if !ok || f.Unit != "numeric_std" || f.Library != "ieee" || f.Body {
		t.Errorf("Lookup: %+v %v", f, ok)
	}
	if _, ok := Lookup("ieee/nope.vhdl"); ok {
		t.Error("Lookup of a missing file")
	}
	for _, p := range []string{"", "/LICENSE", "../x", "a/../LICENSE",
		"nope"} {
		if _, ok := Source(p); ok {
			t.Errorf("Source(%q) succeeded", p)
		}
	}
	if b, ok := Source("LICENSE"); !ok ||
		!strings.Contains(string(b), "Apache License") {
		t.Error("LICENSE")
	}
	if !IsLibrary("IEEE") || !IsLibrary("std") || IsLibrary("work") {
		t.Error("IsLibrary")
	}
	if len(Prebuilt()) == 0 {
		t.Error("Prebuilt is empty: run go generate ./internal/sema")
	}
	d := InternalError("ieee", "x")
	if d.Code != diag.StdlibInternal || d.Class() != diag.Tool {
		t.Errorf("InternalError: %+v", d)
	}
}

// TestParseTags covers malformed firststd.tsv lines.
func TestParseTags(t *testing.T) {
	tags, bad := parseTags("# c\n\n2008\tghdl-08\tk\n" +
		"2007\tx\tk\n2008\t\tk\n2008\tx\n" + "abc\tx\tk\n")
	if len(tags) != 1 || tags[0].Key != "k" ||
		!slices.Equal(bad, []int{4, 5, 6, 7}) {
		t.Errorf("parseTags: %v %v", tags, bad)
	}
}

// TestInternalErrorHook: the stdlib/internal-error test hook reports
// one tool failure per injected file, sorted, naming the library and
// the file, and nothing without the hook.
func TestInternalErrorHook(t *testing.T) {
	if ds := InternalErrors(context.Background()); len(ds) != 0 {
		t.Fatalf("without the hook: %v", ds)
	}
	var none context.Context
	if ds := InternalErrors(none); len(ds) != 0 {
		t.Fatalf("nil context: %v", ds)
	}
	ctx := WithInternalError(WithInternalError(context.Background(),
		"std/textio.vhdl"), "ieee/numeric_std.vhdl")
	ds := InternalErrors(ctx)
	if len(ds) != 2 {
		t.Fatalf("two injected: %v", ds)
	}
	for _, d := range ds {
		if d.Code != diag.StdlibInternal || d.Class() != diag.Tool {
			t.Errorf("%+v: want a stdlib/internal-error tool failure", d)
		}
	}
	if !strings.Contains(ds[0].Message, "ieee/numeric_std.vhdl") ||
		!strings.Contains(ds[0].Message, "bundled ieee") ||
		!strings.Contains(ds[1].Message, "bundled std") {
		t.Errorf("messages: %q, %q", ds[0].Message, ds[1].Message)
	}
}
