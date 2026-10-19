package sema

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// overloadOps reads the overload_ops corpus design (#8), with the
// architecture's use clause for ow_sat replaced by use when set.
func overloadOps(t *testing.T, use string) (map[string]string, *Result) {
	t.Helper()
	dir := "../../testdata/corpus/overload_ops"
	files := map[string]string{}
	for _, p := range []string{"overload_ops_pkg.vhd", "overload_ops.vhd",
		"overload_ops_tb.vhd"} {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		files[p] = string(b)
	}
	if use != "" {
		const sat = "use work.ow_sat.all;"
		if !strings.Contains(files["overload_ops.vhd"], sat) {
			t.Fatal("overload_ops.vhd has no use of ow_sat")
		}
		files["overload_ops.vhd"] = strings.Replace(
			files["overload_ops.vhd"], sat, use, 1)
	}
	return files, analyze(t, files, nil)
}

// chosen returns "package signature" of the overload chosen at the
// operator or name occurrence at the start of the nth (from 0)
// occurrence of text in path.
func chosen(t *testing.T, r *Result, files map[string]string, path,
	text string, nth int,
) string {
	t.Helper()
	src := files[path]
	off := -1
	for i := 0; i <= nth; i++ {
		j := strings.Index(src[off+1:], text)
		if j < 0 {
			t.Fatalf("%s: no occurrence %d of %q", path, nth, text)
		}
		off += 1 + j
	}
	ref, ok := r.RefAt(path, off)
	if !ok || ref.Decl == nil {
		t.Fatalf("%s: %q chose nothing: %+v", path, text, ref.Overload)
	}
	d := ref.Decl.Denotes()
	if ref.Overload == nil || ref.Overload.Chosen != ref.Decl {
		t.Errorf("%s: %q: the overload result does not carry the "+
			"chosen declaration", path, text)
	}
	return string(d.Unit.Name) + " " + Signature(d)
}

// TestOverloadOps: the overload_ops corpus design (#8) resolves the
// overload its use clauses select, by parameter types, by expanded
// name and by result type, and every reference carries the chosen
// declaration and its package for hover.
func TestOverloadOps(t *testing.T) {
	files, r := overloadOps(t, "")
	noErrors(t, r)
	if s := diagText(r); s != "" {
		t.Fatalf("diagnostics:\n%s", s)
	}
	const f = "overload_ops.vhd"
	wants := []struct {
		text string
		nth  int
		want string
	}{
		{"+ b;", 0, "ow_sat [word_t, word_t return word_t]"},
		{"+ 100", 0, "ow_sat [word_t, INTEGER return word_t]"},
		{`"+"(a, b)`, 0, "ow_wrap [word_t, word_t return word_t]"},
		{"-a;", 0, "ow_sat [word_t return word_t]"},
		{"-a;", 1, "ow_sat [word_t return INTEGER]"},
	}
	for _, w := range wants {
		if got := chosen(t, r, files, f, w.text, w.nth); got != w.want {
			t.Errorf("%q (%d) chose %s, want %s", w.text, w.nth, got,
				w.want)
		}
	}
	// The testbench's w(...) and record "=" resolve too.
	if got := chosen(t, r, files, "overload_ops_tb.vhd", "w(200)", 0); got !=
		"ow_types [INTEGER return word_t]" {
		t.Errorf("w(200) chose %s", got)
	}
}

// TestOverloadOpsWrap: with use work.ow_wrap.all instead, a + b runs
// ow_wrap's body, and a + 100 and -a have no overload.
func TestOverloadOpsWrap(t *testing.T) {
	files, r := overloadOps(t, "use work.ow_wrap.all;")
	if got := chosen(t, r, files, "overload_ops.vhd", "+ b;", 0); got !=
		"ow_wrap [word_t, word_t return word_t]" {
		t.Errorf("a + b chose %s", got)
	}
	got := diagText(r)
	for _, w := range []string{
		"overload_ops.vhd:23:15: sema/no-overload: no + matches: " +
			`"+" [word_t, word_t return word_t] from ow_wrap argument 2 ` +
			"is universal_integer, not word_t",
		"overload_ops.vhd:27:12: sema/no-overload: no - matches",
		"overload_ops.vhd:28:12: sema/no-overload: no - matches",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if n := strings.Count(got, "\n"); n != 3 {
		t.Errorf("%d diagnostics, want 3:\n%s", n, got)
	}
}

// TestOverloadOpsBoth: with both packages used, their "+" for word_t
// are homographs that hide each other (LRM 12.4), so a + b is an error
// and never a silent pick; a + 100 still has its one overload.
func TestOverloadOpsBoth(t *testing.T) {
	files, r := overloadOps(t,
		"use work.ow_sat.all;\nuse work.ow_wrap.all;")
	got := diagText(r)
	const w = `overload_ops.vhd:23:15: sema/ambiguous-overload: ` +
		`ambiguous +: "+" [word_t, word_t return word_t] from ow_sat; ` +
		`"+" [word_t, word_t return word_t] from ow_wrap`
	if !strings.Contains(got, w) || strings.Count(got, "\n") != 1 {
		t.Errorf("a + b with both packages: want %s, got\n%s", w, got)
	}
	if got := chosen(t, r, files, "overload_ops.vhd", "+ 100", 0); got !=
		"ow_sat [word_t, INTEGER return word_t]" {
		t.Errorf("a + 100 chose %s", got)
	}
}

// checkOverloads checks the editor-mode overload result of every
// reference of r: the candidates are in relevance order, a candidate
// fits exactly when it has no reason, the chosen declaration is a
// fitting candidate and the reference's declaration, and a reference
// with more than one fitting candidate and no choice always has an
// ambiguous-overload diagnostic. In files without errors (clean
// designs) every overloaded name resolves.
func checkOverloads(t *testing.T, name string, r *Result) int {
	t.Helper()
	return checkOverloadsIn(t, name, r, true)
}

func checkOverloadsIn(t *testing.T, name string, r *Result,
	bundle bool,
) int {
	t.Helper()
	errFiles := map[string]bool{}
	ambiguous := map[string]bool{}
	for _, d := range r.Diagnostics {
		if d.Severity == 1 {
			errFiles[d.Path] = true
		}
		if d.Code == "sema/ambiguous-overload" {
			ambiguous[d.Path+":"+strconv.Itoa(d.Line)+":"+
				strconv.Itoa(d.Col)] = true
		}
	}
	n := 0
	for _, u := range r.AllUnits() {
		if u.File.Bundled && !bundle {
			continue
		}
		for _, ref := range u.Refs {
			o := ref.Overload
			if o == nil {
				continue
			}
			n++
			at := func() string {
				l, c := ref.File.Lines.LineCol(ref.Span.Start)
				return ref.File.Path + ":" + strconv.Itoa(l) + ":" +
					strconv.Itoa(c)
			}
			if !slices.EqualFunc(o.Candidates, sortedCandidates(o),
				func(a, b Candidate) bool { return a.Decl == b.Decl }) {
				t.Errorf("%s: %s: candidates out of order", name, at())
			}
			chosenFits := o.Chosen == nil
			for _, c := range o.Candidates {
				if c.Fits != (c.Reason == "") {
					t.Errorf("%s: %s: candidate %s fits=%v reason %q",
						name, at(), describe(c.Decl), c.Fits, c.Reason)
				}
				if c.Fits && c.Decl == o.Chosen {
					chosenFits = true
				}
			}
			if !chosenFits {
				t.Errorf("%s: %s: chose %s, which does not fit", name,
					at(), describe(o.Chosen))
			}
			if ref.Decl != o.Chosen {
				t.Errorf("%s: %s: ref denotes another declaration",
					name, at())
			}
			if errFiles[ref.File.Path] || ref.File.Bundled &&
				len(r.Diagnostics) > 0 {
				continue
			}
			if o.Chosen == nil {
				t.Errorf("%s: %s: %s does not resolve in a clean file "+
					"(%d candidates)", name, at(), o.Name,
					len(o.Candidates))
			}
			if o.Ambiguous() && !ambiguous[at()] {
				t.Errorf("%s: %s: silent ambiguity", name, at())
			}
		}
	}
	return n
}

// TestOverloadInvariants checks every overload result of the bundled
// std/ieee packages, the oracle corpus designs (#8) and NEORV32.
func TestOverloadInvariants(t *testing.T) {
	designs, run := loadCorpus(t)
	total := 0
	for i, d := range designs {
		r := run(filepath.Join("../../testdata/corpus", d.Name), d)
		// The bundle is shared: check it once.
		total += checkOverloadsIn(t, d.Name, r, i == 0)
	}
	if srcs := neorv32Sources(t, "../../testdata/neorv32"); len(srcs) > 0 {
		c := config.Defaults()
		c.Libraries = []config.Library{{Name: "neorv32",
			Folders: []config.Folder{{Path: "."}}}}
		total += checkOverloads(t, "neorv32", Analyze(Input{Files: srcs,
			Config: c}))
	}
	if total < 10000 {
		t.Errorf("only %d overload results checked", total)
	}
}

// TestAmbiguousAlwaysReported: an ambiguous call, operator or literal
// is a diagnostic listing every fitting candidate with its package,
// and the reference carries the same candidates (for hover and
// completion), never a silent pick.
func TestAmbiguousAlwaysReported(t *testing.T) {
	files := map[string]string{"a.vhd": `package p1 is
  type t is (a, b);
  function f(x : integer) return t;
  procedure q(x : integer);
end;
package p2 is
  type u is (a, c);
  function f(x : integer) return u;
  procedure q(x : integer);
end;
use work.p1.all;
use work.p2.all;
entity e is end;
architecture rtl of e is
  signal s : boolean := f(1) = f(2);
  signal v : boolean := a = a;
begin
  process
  begin
    q(1);
    wait;
  end process;
end;
`}
	r := analyze(t, files, nil)
	got := diagText(r)
	for _, w := range []string{
		`a.vhd:15:30: sema/ambiguous-overload: ambiguous =: "=" [t, t ` +
			`return BOOLEAN] from p1; "=" [u, u return BOOLEAN] from p2`,
		`a.vhd:16:27: sema/ambiguous-overload: ambiguous =: "=" [t, t ` +
			`return BOOLEAN] from p1; "=" [u, u return BOOLEAN] from p2`,
		`a.vhd:20:5: sema/ambiguous-overload: ambiguous q: q [INTEGER] ` +
			`from p1; q [INTEGER] from p2`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if n := strings.Count(got, "\n"); n != 3 {
		t.Errorf("%d diagnostics, want 3:\n%s", n, got)
	}
	checkOverloads(t, "a.vhd", r)
	ref, ok := r.RefAt("a.vhd", occurrence(t, files, "a.vhd", "q", 2))
	if !ok || ref.Overload == nil || !ref.Overload.Ambiguous() ||
		len(ref.Overload.Candidates) != 2 {
		t.Fatalf("q(1): %+v", ref.Overload)
	}
}

// TestNearestFirst: the candidates of a failed call are ordered by how
// close each came, so the diagnostic and hover lead with the overload
// the author most likely meant.
func TestNearestFirst(t *testing.T) {
	files := map[string]string{"n.vhd": `package p is
  function g(a : integer) return integer;
  function g(a, b : integer) return integer;
  function g(a : integer; b : boolean) return integer;
end;
use work.p.all;
entity e is end;
architecture rtl of e is
  signal s : integer := g(1, 'x');
begin
end;
`}
	r := analyze(t, files, nil)
	ref, _ := r.RefAt("n.vhd", occurrence(t, files, "n.vhd", "g", 3))
	o := ref.Overload
	if o == nil || len(o.Candidates) != 3 {
		t.Fatalf("g(1, 'x'): %+v", o)
	}
	var order []string
	for _, c := range o.Candidates {
		order = append(order, Signature(c.Decl)+" "+strconv.Itoa(c.Near))
	}
	want := []string{"[INTEGER, INTEGER return INTEGER] 1",
		"[INTEGER, BOOLEAN return INTEGER] 1",
		"[INTEGER return INTEGER] 0"}
	if !slices.Equal(order, want) {
		t.Errorf("order %q, want %q", order, want)
	}
}

// TestIndexedCall: f(i) indexes the result of a parameterless function
// returning an array, with no overload diagnostic, and the reference
// records f as the chosen overload.
func TestIndexedCall(t *testing.T) {
	files := map[string]string{"i.vhd": `package p is
  type arr is array (0 to 3) of integer;
  function f return arr;
end;
use work.p.all;
entity e is end;
architecture rtl of e is
  signal s : integer := f(2);
  signal v : arr := f;
begin
end;
`}
	r := analyze(t, files, nil)
	if got := diagText(r); got != "" {
		t.Fatalf("diagnostics:\n%s", got)
	}
	checkOverloads(t, "i.vhd", r)
	if d := declOf(t, r, files, "i.vhd", "f", 1); d == nil ||
		d.Kind != DeclFunction {
		t.Fatalf("f(2) denotes %v", d)
	}
}

// TestContextResolution: LRM 12.5 resolves a complete context: the
// result type an outer call or target requires picks an inner
// overload, named associations and default values select by formal,
// and literals take the type of the formal they are matched to.
func TestContextResolution(t *testing.T) {
	files := map[string]string{"c.vhd": `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
package q is
  function g(x : integer) return integer;
  function g(x : integer) return boolean;
  function f(x : integer) return integer;
  function h(x : boolean) return integer;
  procedure pr(a : integer; b : boolean := false);
  procedure pr(a : real);
end;
use work.q.all;
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity c is end;
architecture rtl of c is
  constant k1 : integer := f(g(1));
  constant k2 : integer := h(g(1));
  constant k3 : boolean := g(2);
  constant k4 : integer := to_integer(unsigned'("0101"));
  constant k5 : std_logic_vector(4 downto 0) := "0101" & '1';
  constant k6 : bit_vector(1 downto 0) := '1' & '0';
  constant k7 : boolean := g(3) and true;
begin
  process
  begin
    pr(b => true, a => 1);
    pr(1);
    pr(1.0);
    wait;
  end process;
end;
`}
	r := analyze(t, files, nil)
	if got := diagText(r); got != "" {
		t.Fatalf("diagnostics:\n%s", got)
	}
	checkOverloads(t, "c.vhd", r)
	sig := func(word string, nth int) string {
		d := declOf(t, r, files, "c.vhd", word, nth)
		return Signature(d)
	}
	for _, c := range []struct {
		word string
		nth  int
		want string
	}{
		{"g", 2, "[INTEGER return INTEGER]"}, // f(g(1))
		{"g", 3, "[INTEGER return BOOLEAN]"}, // h(g(1))
		{"g", 4, "[INTEGER return BOOLEAN]"}, // k3
		{"g", 5, "[INTEGER return BOOLEAN]"}, // g(3) and true
		{"pr", 2, "[INTEGER, BOOLEAN]"},      // named
		{"pr", 3, "[INTEGER, BOOLEAN]"},      // default b
		{"pr", 4, "[REAL]"},                  // by literal type
	} {
		if got := sig(c.word, c.nth); got != c.want {
			t.Errorf("%s #%d: %s, want %s", c.word, c.nth, got, c.want)
		}
	}
	// The formals of the named association refer to pr's parameters.
	b := declOf(t, r, files, "c.vhd", "b", 1)
	if b.Kind != DeclParameter || b.Type.Name() != "BOOLEAN" {
		t.Errorf("formal b denotes %v", where(b))
	}
}
