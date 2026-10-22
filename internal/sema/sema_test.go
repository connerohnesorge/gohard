package sema

import (
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// occurrence returns the offset of the nth (from 0) whole-word
// occurrence of word in the file at path of files.
func occurrence(t testing.TB, files map[string]string, path, word string,
	nth int,
) int {
	t.Helper()
	text := strings.ToLower(files[path])
	w := strings.ToLower(word)
	isWord := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
	}
	from := 0
	for {
		i := strings.Index(text[from:], w)
		if i < 0 {
			t.Fatalf("%s: no occurrence %d of %q", path, nth, word)
		}
		i += from
		end := i + len(w)
		if (i == 0 || !isWord(text[i-1])) &&
			(end == len(text) || !isWord(text[end])) {
			if nth == 0 {
				return i
			}
			nth--
		}
		from = i + 1
	}
}

// declOf returns what the nth occurrence of word in path denotes.
func declOf(t testing.TB, r *Result, files map[string]string, path,
	word string, nth int,
) *Decl {
	t.Helper()
	off := occurrence(t, files, path, word, nth)
	if ref, ok := r.RefAt(path, off); ok {
		return ref.Decl
	}
	if d, ok := r.DeclAt(path, off); ok {
		return d
	}
	t.Fatalf("%s: occurrence %d of %q resolves to nothing", path, nth,
		word)
	return nil
}

// at reports where d is declared as path:word.
func where(d *Decl) string {
	if d == nil || d.File == nil {
		return "<none>"
	}
	return d.File.Path + ":" + string(d.File.Text[d.Span.Start:d.Span.End])
}

func noErrors(t testing.TB, r *Result) {
	t.Helper()
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error || d.Class() != diag.Design {
			t.Fatalf("unexpected diagnostic:\n%s", diagText(r))
		}
	}
}

var crossFile = map[string]string{
	"pkg.vhd": `library ieee;
use ieee.std_logic_1164.all;
package defs is
  constant WIDTH : natural := 8;
  type state is (idle, busy);
  subtype word is std_logic_vector(WIDTH - 1 downto 0);
  function inc(x : natural) return natural;
end package defs;
package body defs is
  function inc(x : natural) return natural is
  begin
    return x + 1;
  end function inc;
end package body;
`,
	"top.vhd": `library ieee;
use ieee.std_logic_1164.all;
use work.defs.all;
entity top is
  port (d : in word; q : out word);
end entity top;
architecture rtl of top is
  signal s : state := idle;
  signal n : natural := inc(WIDTH);
begin
  q <= d;
  run: process (s) is
  begin
    if s = idle then
      s <= busy;
    end if;
  end process run;
end architecture rtl;
`,
}

// TestCrossFile: every name occurrence maps to its declaration's file
// and name span, across files and through use clauses, and every
// declaration lists its references across the workspace.
func TestCrossFile(t *testing.T) {
	r := analyze(t, crossFile, nil)
	noErrors(t, r)
	cases := []struct {
		path, word string
		nth        int
		want       string
	}{
		{"top.vhd", "word", 0, "pkg.vhd:word"},
		{"top.vhd", "state", 0, "pkg.vhd:state"},
		{"top.vhd", "idle", 0, "pkg.vhd:idle"},
		{"top.vhd", "idle", 1, "pkg.vhd:idle"},
		{"top.vhd", "inc", 0, "pkg.vhd:inc"},
		{"top.vhd", "WIDTH", 0, "pkg.vhd:WIDTH"},
		{"top.vhd", "defs", 0, "pkg.vhd:defs"},
		{"top.vhd", "top", 2, "top.vhd:top"}, // of top
		{"top.vhd", "top", 1, "top.vhd:top"}, // end entity top
		{"top.vhd", "run", 1, "top.vhd:run"}, // end process run
		{"top.vhd", "s", 1, "top.vhd:s"},
		{"top.vhd", "d", 1, "top.vhd:d"},
		{"top.vhd", "std_logic_vector", -1, ""},
		{"pkg.vhd", "std_logic_vector", 0,
			"ieee/std_logic_1164.vhdl:STD_LOGIC_VECTOR"},
		{"pkg.vhd", "defs", 1, "pkg.vhd:defs"}, // package body defs
		{"pkg.vhd", "inc", 1, "pkg.vhd:inc"},   // the body's spec
		{"pkg.vhd", "x", 2, "pkg.vhd:x"},       // x + 1: the body's x
	}
	for _, c := range cases {
		if c.nth < 0 {
			continue
		}
		if got := where(declOf(t, r, crossFile, c.path, c.word,
			c.nth)); got != c.want {
			t.Errorf("%s %s #%d -> %s, want %s", c.path, c.word, c.nth,
				got, c.want)
		}
	}
	// References of `idle` across files.
	idle := declOf(t, r, crossFile, "pkg.vhd", "idle", 0)
	var refs []string
	for _, ref := range r.References(idle) {
		l, c := ref.File.Lines.LineCol(ref.Span.Start)
		refs = append(refs, ref.File.Path+":"+itoa(l)+":"+itoa(c))
	}
	want := "top.vhd:8:23 top.vhd:14:12"
	if got := strings.Join(refs, " "); got != want {
		t.Errorf("references of idle: %s, want %s", got, want)
	}
	// The body of inc completes the declaration.
	body, _ := r.DeclAt("pkg.vhd", occurrence(t, crossFile, "pkg.vhd",
		"inc", 1))
	if body == nil || body.Spec == nil || where(body.Spec) != "pkg.vhd:inc" ||
		body.Spec.Span == body.Span {
		t.Errorf("body of inc: spec %v", where(body.Spec))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestUndefined: undeclared names are sema/undefined, at the name.
func TestUndefined(t *testing.T) {
	r := analyze(t, map[string]string{"a.vhd": `entity a is end;
architecture rtl of a is
  signal s : bit;
begin
  s <= nope;
  t <= '1';
end;
`}, nil)
	got := diagText(r)
	for _, w := range []string{
		"a.vhd:5:8: sema/undefined: nope is not declared",
		"a.vhd:6:3: sema/undefined: t is not declared",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
}

// TestHiding: an inner declaration hides an outer homograph; a
// non-overloadable one hides every outer declaration of the name.
func TestHiding(t *testing.T) {
	files := map[string]string{"h.vhd": `package p is
  constant k : integer := 1;
end;
use work.p.all;
entity h is end;
architecture rtl of h is
  constant k : integer := 2;
  signal s : integer := k;
begin
  b: block is
    constant k : boolean := true;
    signal t : boolean := k;
  begin
  end block;
end;
`}
	r := analyze(t, files, nil)
	noErrors(t, r)
	if got := declOf(t, r, files, "h.vhd", "k", 2); got.Span.Start !=
		occurrence(t, files, "h.vhd", "k", 1) {
		t.Errorf("s := k -> %s", where(got))
	}
	if got := declOf(t, r, files, "h.vhd", "k", 4); got.Span.Start !=
		occurrence(t, files, "h.vhd", "k", 3) {
		t.Errorf("t := k -> %s", where(got))
	}
}

// TestUseConflict: LRM 12.4 rule c, two use clauses making two
// non-overloadable declarations of one name potentially visible: the
// name is not visible; overloadable ones are both visible.
func TestUseConflict(t *testing.T) {
	r := analyze(t, map[string]string{"c.vhd": `package p1 is
  constant k : integer := 1;
  function f(x : integer) return integer;
end;
package p2 is
  constant k : integer := 2;
  function f(x : boolean) return integer;
end;
use work.p1.all, work.p2.all;
entity c is end;
architecture rtl of c is
  signal a : integer := k;
  signal b : integer := f(1) + f(true);
begin
end;
`}, nil)
	got := diagText(r)
	want := "c.vhd:12:25: sema/error: k is not visible: use clauses " +
		"make several declarations visible (p1.k, p2.k)\n"
	if !strings.Contains(got, want) {
		t.Fatalf("got\n%s\nwant %s", got, want)
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("only k is an error:\n%s", got)
	}
}

// TestExplicitHidesImplicit: a user "=" for a type hides the implicit
// one in the same region, so a = b is not ambiguous.
func TestExplicitHidesImplicit(t *testing.T) {
	files := map[string]string{"e.vhd": `package p is
  type t is (x, y);
  function "="(l, r : t) return boolean;
end;
use work.p.all;
entity e is end;
architecture rtl of e is
  signal a, b : t;
  signal c : boolean := a = b;
begin
end;
`}
	r := analyze(t, files, nil)
	noErrors(t, r)
	ref, ok := r.RefAt("e.vhd", strings.Index(files["e.vhd"], "a = b")+2)
	if !ok || ref.Decl == nil || ref.Decl.Implicit {
		t.Fatalf("a = b resolves to %+v", ref.Decl)
	}
}

// TestOverloadResult: an ambiguous call is a diagnostic listing the
// candidates, and the reference carries the same candidates with their
// reasons for hover and completion.
func TestOverloadResult(t *testing.T) {
	files := map[string]string{"o.vhd": `package p is
  type a is (x, y);
  type b is (x, z);
  function f(v : a) return integer;
  function f(v : b) return integer;
  function g(v : integer) return integer;
end;
use work.p.all;
entity o is end;
architecture rtl of o is
  signal s : integer := f(x);
  signal t : integer := g(true);
  signal u : integer := f(y);
begin
end;
`}
	r := analyze(t, files, nil)
	got := diagText(r)
	wants := []string{
		`o.vhd:11:25: sema/ambiguous-overload: ambiguous f: f [a return ` +
			`INTEGER] from p; f [b return INTEGER] from p`,
		`o.vhd:12:25: sema/no-overload: no g matches: g [INTEGER ` +
			`return INTEGER] from p argument 1 is BOOLEAN, not INTEGER`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if strings.Count(got, "\n") != 2 {
		t.Errorf("f(y) is not ambiguous:\n%s", got)
	}
	ref, ok := r.RefAt("o.vhd", occurrence(t, files, "o.vhd", "f", 2))
	if !ok || ref.Overload == nil || ref.Overload.Chosen != nil ||
		len(ref.Overload.Candidates) != 2 ||
		!ref.Overload.Ambiguous() {
		t.Fatalf("f(x) overload %+v", ref.Overload)
	}
	ref, _ = r.RefAt("o.vhd", occurrence(t, files, "o.vhd", "g", 1))
	if ref.Overload == nil || ref.Overload.Candidates[0].Fits ||
		ref.Overload.Candidates[0].Reason !=
			"argument 1 is BOOLEAN, not INTEGER" {
		t.Fatalf("g(true) overload %+v", ref.Overload)
	}
	ref, _ = r.RefAt("o.vhd", occurrence(t, files, "o.vhd", "f", 3))
	if ref.Decl == nil || where(ref.Decl) != "o.vhd:f" ||
		ref.Decl.Params[0].Type.Name() != "a" {
		t.Fatalf("f(y) chose %v", where(ref.Decl))
	}
}

// TestOperators: operator overloads come from the use clauses that
// select them (overload_ops in miniature), and an operator reference
// carries the chosen overload and the package it is from.
func TestOperators(t *testing.T) {
	files := map[string]string{"n.vhd": `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity n is end;
architecture rtl of n is
  signal a, b : unsigned(7 downto 0);
  signal c : unsigned(7 downto 0) := a + b;
  signal i : integer := to_integer(a) + 1;
  signal e : boolean := a = b;
begin
end;
`}
	r := analyze(t, files, nil)
	noErrors(t, r)
	plus := strings.Index(files["n.vhd"], "a + b") + 2
	ref, ok := r.RefAt("n.vhd", plus)
	if !ok || ref.Decl == nil {
		t.Fatalf("+ unresolved: %+v", ref)
	}
	d := ref.Decl
	if d.Unit.Name != "numeric_std" || Signature(d) !=
		"[UNRESOLVED_UNSIGNED, UNRESOLVED_UNSIGNED return "+
			"UNRESOLVED_UNSIGNED]" {
		t.Fatalf("+ is %s %s", d.Unit.Label(), Signature(d))
	}
	i := strings.Index(files["n.vhd"], "+ 1")
	ref, _ = r.RefAt("n.vhd", i)
	if ref.Decl == nil || !ref.Decl.Implicit || ref.Decl.Unit.Name !=
		"standard" {
		t.Fatalf("to_integer(a) + 1 uses %v", ref.Decl)
	}
}

// TestLibraries: library mapping by folder, mapped libraries in use
// clauses, unmapped libraries, missing units (what was scanned),
// duplicate units (both files) and import cycles (the path).
func TestLibraries(t *testing.T) {
	cfg := func(c *config.Config) {
		c.Libraries = []config.Library{
			{Name: "mylib", Folders: []config.Folder{{Path: "lib"}}},
			{Name: "deep", Folders: []config.Folder{{Path: "lib/deep"}}},
		}
	}
	files := map[string]string{
		"lib/p.vhd":      "package p is constant k : integer := 1; end;",
		"lib/deep/q.vhd": "package q is constant k : integer := 2; end;",
		"src/a.vhd": `library mylib, deep, nowhere;
use mylib.p.all;
use deep.q;
use mylib.missing.all;
entity a is end;
architecture rtl of a is
  constant x : integer := mylib.p.k + deep.q.k;
begin
end;
`,
		"src/dup1.vhd": "entity twice is end;",
		"src/dup2.vhd": "entity twice is end;",
		"src/c1.vhd":   "use work.c2.all; package c1 is end;",
		"src/c2.vhd":   "use work.c1.all; package c2 is end;",
	}
	r := analyze(t, files, cfg)
	if LibraryOf(func() config.Config {
		c := config.Defaults()
		cfg(&c)
		return c
	}(), "lib/deep/q.vhd") != "deep" {
		t.Fatal("the longest folder wins")
	}
	got := diagText(r)
	for _, w := range []string{
		"src/a.vhd:1:22: library/unmapped: library nowhere is not mapped",
		"src/a.vhd:4:11: library/missing-unit: library mylib has no " +
			"unit missing (scanned lib)",
		"src/dup2.vhd:1:8: library/duplicate-unit: unit twice is " +
			"declared twice in library work: src/dup1.vhd and " +
			"src/dup2.vhd",
		"src/c1.vhd:1:26: library/import-cycle: import cycle: " +
			"work.c1 -> work.c2 -> work.c1",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if strings.Count(got, "\n") != 4 {
		t.Errorf("want exactly 4 diagnostics:\n%s", got)
	}
	lib, ok := r.Library("deep")
	if !ok {
		t.Fatal("no library deep")
	}
	if _, ok := lib.Unit("q"); !ok {
		t.Fatal("deep.q not indexed")
	}
}

// TestViews: the bundled packages per standard (#33): 1993 hides the
// 2008 additions (UNRESOLVED_UNSIGNED, fixed_pkg, std_logic_textio,
// to_string) while UNSIGNED and its operations stay usable.
func TestViews(t *testing.T) {
	src := func(body string) map[string]string {
		return map[string]string{"v.vhd": `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity v is end;
architecture rtl of v is
` + body + `
begin
end;
`}
	}
	std := func(s string) func(*config.Config) {
		return func(c *config.Config) { c.Standard = s }
	}
	ok93 := src(`  signal a, b : unsigned(3 downto 0);
  signal e : boolean := a = b;
  signal c : unsigned(3 downto 0) := a + b;`)
	noErrors(t, analyze(t, ok93, std("1993")))
	r := analyze(t, src(`  signal u : unresolved_unsigned(3 downto 0);
  constant s : string := to_string(5);`), std("1993"))
	got := diagText(r)
	for _, w := range []string{
		"sema/undefined: unresolved_unsigned is not declared",
		"sema/undefined: to_string is not declared",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("1993: missing %q in\n%s", w, got)
		}
	}
	noErrors(t, analyze(t, src(`  signal u : unresolved_unsigned(3 downto 0);
  constant s : string := to_string(5);`), std("2008")))
	r = analyze(t, map[string]string{"t.vhd": `library ieee;
use ieee.std_logic_textio.all;
use ieee.fixed_pkg.all;
entity t is end;
`}, std("1993"))
	got = diagText(r)
	for _, w := range []string{
		"t.vhd:2:10: library/missing-unit: library ieee has no unit " +
			"std_logic_textio (scanned the bundled ieee library for " +
			"VHDL-1993)",
		"t.vhd:3:10: library/missing-unit: library ieee has no unit " +
			"fixed_pkg",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("1993 units: missing %q in\n%s", w, got)
		}
	}
}

// TestStatic: unit-level static evaluation reports out-of-range
// constants with the unit as the instance, and the step limit as a
// warning listed in Incomplete.
func TestStatic(t *testing.T) {
	files := map[string]string{"s.vhd": `package s is
  constant bad : natural := -1;
  constant big : bit_vector(0 to 9999) := (others => '1');
  constant same : boolean := big = big;
  constant good : integer := 2 ** 10 + 16#FF#;
end;
`}
	c := config.Defaults()
	r := Analyze(Input{Config: c, Steps: 5000, Files: []Source{{
		Path: "s.vhd", Text: []byte(files["s.vhd"])}}})
	got := diagText(r)
	for _, w := range []string{
		"s.vhd:2:29: sema/static-error: value -1 is out of range 0 to " +
			"2147483647 in work.s",
		"s.vhd:3:43: sema/eval-limit: constant big could not be " +
			"computed within 1,000,000 evaluation steps",
		"s.vhd:4:30: sema/eval-limit: constant same could not be " +
			"computed within 1,000,000 evaluation steps",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if strings.Join(r.Incomplete, ",") != "work.s" {
		t.Errorf("incomplete %v", r.Incomplete)
	}
	// The same constants with the editor budget evaluate.
	r = analyze(t, files, nil)
	if strings.Contains(diagText(r), "eval-limit") {
		t.Errorf("editor budget:\n%s", diagText(r))
	}
	ev := NewEvaluator(r, 0)
	good := r.Units[0].Region.Lookup("good")[0]
	v, err := ev.Value(good.Value)
	if err != nil || v.I != 1024+255 {
		t.Errorf("good = %v %v", v.Image(), err)
	}
}

// TestOpaque: encrypted IP is opaque (exit 2) unless allow_opaque lists
// it (a hint); units using an unimplemented construct are opaque with
// unsupported/construct; their dependents are unchecked.
func TestOpaque(t *testing.T) {
	enc := "entity core is end;\narchitecture rtl of core is\n" +
		"`protect begin_protected\n`protect data_block\nABCDEF==\n" +
		"`protect end_protected\nbegin end;\n"
	files := map[string]string{
		"core.vhd": enc,
		"pkg.vhd": `package p is
  type t is protected
    procedure inc;
    alias bump is inc [];
  end protected t;
end;
`,
		"user.vhd": "use work.p.all;\nentity user is end;\n",
	}
	std19 := func(c *config.Config) { c.Standard = "2019" }
	r := analyze(t, files, std19)
	got := diagText(r)
	for _, w := range []string{
		"core.vhd:2:14: sema/encrypted-unit: work.core(rtl) is " +
			"encrypted IP and cannot be analyzed",
		"pkg.vhd:4:5: unsupported/construct: method aliases in " +
			"protected types is not supported yet (planned for v1.1)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	exit := diag.ExitCode(r.Diagnostics, false, diag.NoSignal)
	if exit != diag.ExitTool {
		t.Errorf("exit %d, want 2:\n%s", exit, got)
	}
	r = analyze(t, files, func(c *config.Config) {
		std19(c)
		c.AllowOpaque = []string{"work.core"}
	})
	if !strings.Contains(diagText(r), "core.vhd:2:14: sema/opaque-allowed:"+
		" work.core(rtl) is encrypted IP, allowed by allow_opaque") {
		t.Errorf("allow_opaque:\n%s", diagText(r))
	}
	// A dependent of an opaque unit is unchecked.
	r = analyze(t, files, std19)
	got = diagText(r)
	if !strings.Contains(got, "user.vhd:2:8: sema/unchecked-unit: "+
		"work.user was not checked: it depends on opaque unit work.p") {
		t.Errorf("unchecked:\n%s", got)
	}
	// The other message variant, for a construct not planned for v1.1.
	saved := constructs
	constructs = append(constructs[:len(constructs):len(constructs)],
		construct{kind: cst.KindGroupTemplateDecl,
			name: "group templates"})
	defer func() { constructs = saved }()
	r = analyze(t, map[string]string{"g.vhd": `package g is
  group pair is (signal, signal);
end;
`}, nil)
	if !strings.Contains(diagText(r), "g.vhd:2:3: unsupported/construct:"+
		" group templates is not supported yet, please report") {
		t.Errorf("please report:\n%s", diagText(r))
	}
}

// TestStdStandardComments checks the implicit operations sema declares
// for the std.standard types against the declarations the standard's
// own text lists in comments (the reference model, #33 and LRM 5).
func TestStdStandardComments(t *testing.T) {
	b := Bundle()
	var std *Unit
	for _, u := range b.Units {
		if u.Library == "std" && u.Name == "standard" {
			std = u
		}
	}
	listed := standardComments(t, string(std.File.Text))
	have := map[string]bool{}
	for _, d := range std.Region.Decls {
		if d.Implicit && d.Kind.IsSubprogram() {
			have[profileKey(d)] = true
		}
	}
	for _, k := range listed {
		if !have[k] {
			t.Errorf("std.standard lists %s; sema does not declare it", k)
		}
	}
	if len(listed) < 300 {
		t.Fatalf("only %d listed operations parsed", len(listed))
	}
}

// profileKey renders an implicit operation as NAME(T1,T2)R.
func profileKey(d *Decl) string {
	var ps []string
	for _, p := range d.Params {
		ps = append(ps, strings.ToUpper(p.Type.Name()))
	}
	s := strings.ToUpper(strings.Trim(string(d.Name), `"`)) + "(" +
		strings.Join(ps, ",") + ")"
	if d.Type != nil {
		s += strings.ToUpper(d.Type.Name())
	}
	return s
}

// standardComments parses the "-- function ..." lines of
// std.standard into profile keys.
func standardComments(t *testing.T, text string) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		l := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if !strings.HasPrefix(l, "--") {
			continue
		}
		l = strings.TrimSpace(strings.TrimPrefix(l, "--"))
		if !strings.HasPrefix(strings.ToLower(l), "function ") {
			continue
		}
		l = l[len("function "):]
		open := strings.Index(l, "(")
		ret := strings.LastIndex(strings.ToLower(l), "return")
		if open < 0 || ret < 0 {
			continue
		}
		name := strings.ToUpper(strings.Trim(strings.TrimSpace(l[:open]),
			`" `))
		name = strings.TrimSpace(strings.TrimSuffix(name, `"`))
		params := l[open+1 : strings.LastIndex(l[:ret], ")")]
		var types []string
		for group := range strings.SplitSeq(params, ";") {
			names, typ, ok := strings.Cut(group, ":")
			if !ok {
				continue
			}
			typ = strings.ToUpper(strings.TrimSpace(typ))
			typ = strings.TrimSpace(strings.TrimPrefix(typ, "IN "))
			n := strings.Count(names, ",") + 1
			for range n {
				types = append(types, typ)
			}
		}
		r := strings.ToUpper(strings.TrimSpace(strings.TrimSuffix(
			strings.TrimSpace(l[ret+len("return"):]), ";")))
		out = append(out, name+"("+strings.Join(types, ",")+")"+r)
	}
	return out
}

// TestStandards: every standard analyzes the bundle-using design
// without errors (the views differ only in what they hide).
func TestStandards(t *testing.T) {
	for _, s := range token.Standards {
		r := analyze(t, crossFile, func(c *config.Config) {
			c.Standard = s.String()
		})
		noErrors(t, r)
	}
}
