package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// corpusResult analyzes the files of the oracle corpus design name.
func corpusResult(t *testing.T, name string) *sema.Result {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "corpus", name)
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var srcs []sema.Source
	for _, e := range es {
		if !strings.HasSuffix(e.Name(), ".vhd") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, sema.Source{Path: e.Name(), Text: b})
	}
	r := sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	return r
}

// TestSignatureHelpAgreesWithHover: on the overload_ops corpus design
// (#8), at every offset where signature help answers, its first
// signature is the overload hover and go to definition give at the
// call's name, and every call written with an association list or as
// an operator gets signature help (#56).
func TestSignatureHelpAgreesWithHover(t *testing.T) {
	r := corpusResult(t, "overload_ops")
	n := checkSignatureHelp(t, r, "overload_ops.vhd",
		"overload_ops_pkg.vhd", "overload_ops_tb.vhd")
	t.Logf("%d positions checked", n)
}

// checkSignatureHelp checks signature help against hover and
// definition at every offset of the files at paths, and that every
// call gets it. The calls expected come from the call references sema
// recorded and the tokens after them, not from the walk signature help
// makes, so a call that walk misses fails the check. It returns the
// number of offsets checked.
func checkSignatureHelp(t *testing.T, r *sema.Result,
	paths ...string,
) int {
	t.Helper()
	checked := 0
	for _, p := range paths {
		f := mustFile(t, r, p)
		calls, ops := expectedCalls(f)
		if len(calls) == 0 {
			t.Fatalf("%s: no call", p)
		}
		seen := map[cst.Range]bool{}
		for off := 0; off <= len(f.Text); off++ {
			sigs, ok := SignatureHelp(r, p, off)
			if !ok {
				continue
			}
			c, isCall := innermostCall(f, off)
			if m, isMap := innermostMap(f, off); isMap && (!isCall ||
				!within(c.list.Span(), m.list.Span())) {
				continue // a map: no overloads
			}
			if !isCall {
				c, _ = innermostOperator(f, off)
			}
			ref := c.ref
			seen[ref.Span] = true
			at := fmt.Sprintf("%s:%d (%s)", p, off,
				f.Text[ref.Span.Start:ref.Span.End])
			if ref.Decl == nil {
				t.Errorf("%s: the call does not resolve", at)
				continue
			}
			if sigs[0].Decl != ref.Decl {
				t.Errorf("%s: signature help %s, reference %s", at,
					sema.Describe(sigs[0].Decl), sema.Describe(ref.Decl))
			}
			h, ok := HoverAt(r, p, ref.Span.Start)
			if !ok || h.Decl != sigs[0].Decl {
				t.Errorf("%s: hover %v, signature help %s", at, h.Decl,
					sema.Describe(sigs[0].Decl))
			}
			l, _ := LocationOf(sigs[0].Decl)
			if d := Definition(r, p, ref.Span.Start); len(d) != 1 ||
				d[0] != l {
				t.Errorf("%s: definition %s, signature help %s", at,
					locs(d), locs([]Location{l}))
			}
			for i, s := range sigs {
				if i > 0 && s.Decl == sigs[0].Decl {
					t.Errorf("%s: the resolved overload is listed twice",
						at)
				}
			}
			checked++
		}
		for name, open := range calls {
			at := fmt.Sprintf("%s:%d: the call %s", p, name.Start,
				f.Text[name.Start:name.End])
			if !seen[name] {
				t.Errorf("%s gets no signature help", at)
				continue
			}
			// Right after its parenthesis, the call is the innermost.
			sigs, ok := SignatureHelp(r, p, open)
			if ref, _ := callRef(f, name); !ok ||
				sigs[0].Decl != ref.Decl {
				t.Errorf("%s: no signature help of its own after (", at)
			}
		}
		for name := range ops {
			if !seen[name] {
				t.Errorf("%s:%d: the operator %s gets no signature help",
					p, name.Start, f.Text[name.Start:name.End])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no call checked")
	}
	return checked
}

// expectedCalls returns, by the span of their name, the calls of the
// file f written with an association list (a call reference followed
// by a parenthesis), with the offset after that parenthesis, and the
// operator calls outside any call's or map's parentheses (there, the
// call's or map's signature help answers).
func expectedCalls(f *sema.File) (calls map[cst.Range]int,
	ops map[cst.Range]bool,
) {
	var toks []cst.Token
	index := map[int]int{}
	for tk := range f.Root().DescendantTokens() {
		if tk.Missing() || tk.Span().Len() == 0 {
			continue
		}
		index[tk.Span().Start] = len(toks)
		toks = append(toks, tk)
	}
	// closing returns the span inside the parentheses opened at i.
	closing := func(i int) cst.Range {
		depth := 0
		for j := i; j < len(toks); j++ {
			switch toks[j].Kind() {
			case token.LParen:
				depth++
			case token.RParen:
				if depth--; depth == 0 {
					return cst.Range{Start: toks[i].Span().End,
						End: toks[j].Span().Start}
				}
			}
		}
		return cst.Range{Start: toks[i].Span().End, End: len(f.Text)}
	}
	var parens []cst.Range
	for i, tk := range toks {
		if tk.Kind() == token.KwMap && i+1 < len(toks) &&
			toks[i+1].Kind() == token.LParen {
			parens = append(parens, closing(i+1))
		}
	}
	calls, ops = map[cst.Range]int{}, map[cst.Range]bool{}
	var opRefs []sema.Ref
	for _, u := range f.Units {
		for _, ref := range u.Refs {
			if ref.File != f || ref.Kind != sema.RefCall ||
				ref.Decl == nil && ref.Overload == nil {
				continue
			}
			i, ok := index[ref.Span.Start]
			if !ok || toks[i].Span() != ref.Span {
				continue
			}
			if i+1 < len(toks) && toks[i+1].Kind() == token.LParen {
				in := closing(i + 1)
				calls[ref.Span] = in.Start
				parens = append(parens, in)
				continue
			}
			switch toks[i].Parent().Kind() {
			case cst.KindBinaryExpr, cst.KindUnaryExpr:
				if ref.Overload != nil {
					opRefs = append(opRefs, ref)
				}
			}
		}
	}
	for _, ref := range opRefs {
		if !slices.ContainsFunc(parens, func(in cst.Range) bool {
			return within(ref.Span, in)
		}) {
			ops[ref.Span] = true
		}
	}
	return calls, ops
}

func TestSignatureHelpActiveParameter(t *testing.T) {
	r := analyzeOne(t, `entity e is
  generic (W : natural := 8; D : natural := 2);
  port (a : in bit; b : out bit; c : in bit);
end entity;
architecture rtl of e is
  function f(x : integer; y : integer; z : integer) return integer is
  begin
    return x;
  end function;
  signal s, t : bit;
  constant k1 : integer := f(1, 2, 3);
  constant k2 : integer := f(z => 1, x => 2, y => 3);
  constant k3 : integer := f(1, z => 2, y => 3);
begin
  u0 : entity work.e generic map (D => 3) port map (s, t, c => s);
end architecture;
`)
	src := string(mustFile(t, r, "a.vhd").Text)
	at := func(s string, n int) int {
		i := strings.Index(src, s)
		if i < 0 {
			t.Fatalf("no %q", s)
		}
		return i + n
	}
	for _, c := range []struct {
		name  string
		off   int
		label string
		param int
	}{
		{"first positional", at("f(1, 2, 3)", 2), "function f", 0},
		{"after a comma", at("f(1, 2, 3)", 5), "function f", 1},
		{"third positional", at("f(1, 2, 3)", 8), "function f", 2},
		{"named z", at("f(z =>", 2), "function f", 2},
		{"named x", at("x => 2", 1), "function f", 0},
		{"positional then named", at("f(1, z", 2), "function f", 0},
		{"named after positional", at("y => 3);\nbegin", 0),
			"function f", 1},
		{"generic map, named", at("D => 3", 0), "entity e generic", 1},
		{"port map, positional", at("(s, t", 4), "entity e port", 1},
		{"port map, named", at("c => s", 0), "entity e port", 2},
	} {
		sigs, ok := SignatureHelp(r, "a.vhd", c.off)
		if !ok {
			t.Errorf("%s: no signature help", c.name)
			continue
		}
		if !strings.HasPrefix(sigs[0].Label, c.label) ||
			sigs[0].Active != c.param {
			t.Errorf("%s: %q active %d, want %q... active %d", c.name,
				sigs[0].Label, sigs[0].Active, c.label, c.param)
		}
		for i, p := range sigs[0].Params {
			if p.Start < 0 || p.End > len(sigs[0].Label) ||
				p.Start >= p.End {
				t.Errorf("%s: parameter %d at %v in %q", c.name, i, p,
					sigs[0].Label)
			}
		}
	}
	if _, ok := SignatureHelp(r, "a.vhd", at("signal s", 0)); ok {
		t.Error("signature help outside any call")
	}
}

func mustFile(t *testing.T, r *sema.Result, p string) *sema.File {
	t.Helper()
	f, ok := r.File(p)
	if !ok {
		t.Fatalf("no file %s", p)
	}
	return f
}

func TestSignatureLabels(t *testing.T) {
	r := analyzeOne(t, `entity e is
  generic (W : natural := 8);
  port (a : in bit; signal b : out bit_vector(W - 1 downto 0));
end entity;
architecture rtl of e is
  procedure p(constant x : in integer; variable y : inout integer) is
  begin
  end procedure;
begin
  u0 : entity work.e generic map (4) port map (a => open, b => open);
  process
    variable v : integer;
  begin
    p(1, v);
    wait;
  end process;
end architecture;
`)
	src := string(mustFile(t, r, "a.vhd").Text)
	for _, c := range []struct {
		at, want string
		params   []string
	}{
		{"(4)", "entity e generic (W : NATURAL := 8)",
			[]string{"W : NATURAL := 8"}},
		{"(a =>", "entity e port (a : in BIT; signal b : out " +
			"BIT_VECTOR(W - 1 downto 0))",
			[]string{"a : in BIT", "signal b : out BIT_VECTOR(W - 1 " +
				"downto 0)"}},
		{"(1, v)", "procedure p(constant x : in INTEGER; variable y " +
			": inout INTEGER)", []string{"constant x : in INTEGER",
			"variable y : inout INTEGER"}},
	} {
		sigs, ok := SignatureHelp(r, "a.vhd", strings.Index(src, c.at)+1)
		if !ok {
			t.Fatalf("%s: no signature help", c.at)
		}
		s := sigs[0]
		var params []string
		for _, p := range s.Params {
			params = append(params, s.Label[p.Start:p.End])
		}
		if s.Label != c.want || !slices.Equal(params, c.params) {
			t.Errorf("%s: %q %q, want %q %q", c.at, s.Label, params,
				c.want, c.params)
		}
	}
}

// callsEverywhereSrc has calls of f in every place an expression can
// be outside an initial value or a statement (an index constraint of
// a port, a subtype, an array type and an allocator; a range
// constraint; generate and loop ranges; a slice), and package
// instances, declared and design units, whose generic maps are
// positional and named.
const callsEverywhereSrc = `package fp is
  function f(x : integer; y : integer) return integer;
end package;
package body fp is
  function f(x : integer; y : integer) return integer is
  begin
    return x + y;
  end function;
end package body;
package gp is
  generic (A : natural := 1; B : natural := 2; C : natural := 3);
  constant K : natural := A;
end package;
use work.fp.all;
entity e is
  port (p : in bit_vector(f(1, 2) downto 0));
end entity;
architecture rtl of e is
  subtype word is bit_vector(f(3, 4) downto 0);
  type mem is array (0 to f(5, 6)) of word;
  signal s : integer range 0 to f(7, 8);
  signal v : bit_vector(15 downto 0);
  type ptr is access bit_vector;
  package pi is new work.gp generic map (7);
  package pj is new work.gp generic map (8, C => 9);
begin
  g : for i in 0 to f(9, 10) generate
  end generate;
  process
    variable q : ptr;
  begin
    for j in f(11, 12) downto 0 loop
    end loop;
    q := new bit_vector(f(13, 14) downto 0);
    v(f(15, 16) - 24 downto 0) <= (others => '0');
    wait;
  end process;
end architecture;
package pl is new work.gp generic map (5, 6);
`

// TestSignatureHelpEverywhere: a call in a subtype indication, a range
// or an allocator gets the signature help a call in an initial value
// gets, and the generic map of a package instance gets the generic
// list of the package (#56).
func TestSignatureHelpEverywhere(t *testing.T) {
	src := callsEverywhereSrc
	r := analyzeOne(t, src)
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	const fLabel = "function f(x : INTEGER; y : INTEGER) return INTEGER"
	const gpLabel = "package gp generic (A : NATURAL := 1; " +
		"B : NATURAL := 2; C : NATURAL := 3)"
	for _, c := range []struct {
		at     string
		label  string
		active int
	}{
		{"f(1, ", fLabel, 1},
		{"f(3, ", fLabel, 1},
		{"f(5, ", fLabel, 1},
		{"f(7, ", fLabel, 1},
		{"f(9, ", fLabel, 1},
		{"f(11, ", fLabel, 1},
		{"f(13, ", fLabel, 1},
		{"f(15, ", fLabel, 1},
		{"map (", gpLabel, 0},
		{"map (8, ", gpLabel, 2},
		{"map (5, ", gpLabel, 1},
	} {
		off := strings.Index(src, c.at) + len(c.at)
		sigs, ok := SignatureHelp(r, "a.vhd", off)
		if !ok {
			t.Errorf("%q: no signature help", c.at)
			continue
		}
		if sigs[0].Label != c.label || sigs[0].Active != c.active {
			t.Errorf("%q: %q active %d, want %q active %d", c.at,
				sigs[0].Label, sigs[0].Active, c.label, c.active)
		}
	}
	checkSignatureHelp(t, r, "a.vhd")
}

// declaredSrc has a package function, a package-body function, an
// entity function and functions nested in an architecture, a process,
// a block and a generate. Type marks are written in a different case
// from the type's declaration so the signature can be checked against
// hover's spelling (#118).
const declaredSrc = `package Maths is
  type Word is array (0 to 3) of bit;
  type nibble is array (0 to 3) of bit;
  function clip(value : integer; limit : integer) return integer;
  function width(x : Word) return Word;
  function lo(x : NIBBLE) return nibble;
  function lim(x : integer range 0 to 9) return integer;
  type counter_t is protected
    impure function get return integer;
  end protected counter_t;
end package;

package body Maths is
  function clip(value : integer; limit : integer) return integer is
  begin
    return value;
  end function;
  function width(x : Word) return Word is
  begin
    return x;
  end function;
  function lo(x : NIBBLE) return nibble is
  begin
    return x;
  end function;
  function lim(x : integer range 0 to 9) return integer is
  begin
    return x;
  end function;
  function hidden(x : integer) return integer is
    function inner(y : integer) return integer is
    begin
      return y;
    end function;
  begin
    return inner(x);
  end function;
  constant hid : integer := hidden(1);
  type counter_t is protected body
    variable n : integer := 0;
    impure function get return integer is
    begin
      return n;
    end function;
  end protected body counter_t;
end package body;

library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use work.Maths.all;

entity ALU is
  function ident(x : integer) return integer is
  begin
    return x;
  end function;
end entity;

architecture RTL of ALU is
  function pick(sel : std_logic; a, b : unsigned) return unsigned is
  begin
    return a;
  end function;
  function wide(x : unsigned(7 downto 0)) return unsigned is
  begin
    return x;
  end function;
  signal res : unsigned(7 downto 0);
  constant clipped : integer := clip(300, 255);
  constant shaped : Word := width((others => '0'));
  constant low : nibble := lo((others => '0'));
  constant limited : integer := lim(3);
  constant same : integer := ident(1);
begin
  res <= pick('1', res, res);
  res <= wide(res);
  tick : process is
    function local(x : integer) return integer is
    begin
      return x;
    end function;
    variable v : integer := local(1);
  begin
    wait;
  end process;
  process is
    function bare(x : integer) return integer is
    begin
      return x;
    end function;
    variable v : integer := bare(2);
  begin
    wait;
  end process;
  b : block is
    function inside(x : integer) return integer is
    begin
      return x;
    end function;
    constant bk : integer := inside(1);
  begin
  end block;
  g : if true generate
    function gfun(x : integer) return integer is
    begin
      return x;
    end function;
    constant gk : integer := gfun(1);
  begin
  end generate;
end architecture;
`

// TestSignatureHelpDeclaredAt is the signature help a client renders
// (#118): the label, each parameter, the active parameter and the
// origin. The origin is the same text hover and completion use.
// Package functions are "work.maths" (the unit key, not the declared
// spelling Maths). A function written only in the package body is
// "package body work.maths". A local function is "architecture RTL of
// ALU". Type marks use the type's declaration, so integer is INTEGER
// and NIBBLE is nibble.
func TestSignatureHelpDeclaredAt(t *testing.T) {
	src := declaredSrc
	r := analyzeOne(t, src)
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	for _, c := range []struct {
		at, label, origin string
		params            []string
		active            int
	}{
		{"clip(300, ", "function clip(value : INTEGER; limit : " +
			"INTEGER) return INTEGER", "work.maths",
			[]string{"value : INTEGER", "limit : INTEGER"}, 1},
		{"width((others", "function width(x : Word) return Word",
			"work.maths", []string{"x : Word"}, 0},
		{"lo((others", "function lo(x : nibble) return nibble",
			"work.maths", []string{"x : nibble"}, 0},
		{"lim(3", "function lim(x : INTEGER range 0 to 9) return " +
			"INTEGER", "work.maths",
			[]string{"x : INTEGER range 0 to 9"}, 0},
		{"hidden(1", "function hidden(x : INTEGER) return INTEGER",
			"package body work.maths", []string{"x : INTEGER"}, 0},
		{"inner(x", "function inner(y : INTEGER) return INTEGER",
			"function hidden in package body work.maths",
			[]string{"y : INTEGER"}, 0},
		{"ident(1", "function ident(x : INTEGER) return INTEGER",
			"work.alu", []string{"x : INTEGER"}, 0},
		{"pick('1'", "function pick(sel : STD_LOGIC; a : UNSIGNED; " +
			"b : UNSIGNED) return UNSIGNED",
			"architecture RTL of ALU",
			[]string{"sel : STD_LOGIC", "a : UNSIGNED",
				"b : UNSIGNED"}, 0},
		{"wide(res", "function wide(x : UNSIGNED(7 downto 0)) " +
			"return UNSIGNED", "architecture RTL of ALU",
			[]string{"x : UNSIGNED(7 downto 0)"}, 0},
		{"local(1", "function local(x : INTEGER) return INTEGER",
			"process tick in architecture RTL of ALU",
			[]string{"x : INTEGER"}, 0},
		{"bare(2", "function bare(x : INTEGER) return INTEGER",
			"process in architecture RTL of ALU",
			[]string{"x : INTEGER"}, 0},
		{"inside(1", "function inside(x : INTEGER) return INTEGER",
			"block b in architecture RTL of ALU",
			[]string{"x : INTEGER"}, 0},
		{"gfun(1", "function gfun(x : INTEGER) return INTEGER",
			"generate g in architecture RTL of ALU",
			[]string{"x : INTEGER"}, 0},
	} {
		off := strings.Index(src, c.at)
		if off < 0 {
			t.Fatalf("no %q", c.at)
		}
		off += len(c.at)
		sigs, ok := SignatureHelp(r, "a.vhd", off)
		if !ok || len(sigs) != 1 {
			t.Errorf("%s: %d signatures", c.at, len(sigs))
			continue
		}
		s := sigs[0]
		var params []string
		for _, p := range s.Params {
			if p.Start < 0 || p.End > len(s.Label) || p.Start >= p.End {
				t.Errorf("%s: parameter %v in %q", c.at, p, s.Label)
				continue
			}
			params = append(params, s.Label[p.Start:p.End])
		}
		if s.Label != c.label || s.Active != c.active ||
			!slices.Equal(params, c.params) ||
			s.Doc != "from "+c.origin {
			t.Errorf("%s:\n got %q active %d %q\n doc %q\nwant %q "+
				"active %d %q\n doc %q", c.at, s.Label, s.Active,
				params, s.Doc, c.label, c.active, c.params,
				"from "+c.origin)
		}
		name := s.Decl.Text
		h, hok := HoverAt(r, "a.vhd", strings.Index(src, c.at))
		if !hok || !strings.Contains(h.Markdown, "from "+c.origin) {
			t.Errorf("%s: hover %q, want from %s", c.at, h.Markdown,
				c.origin)
		}
		found := false
		for _, it := range Complete(r, "a.vhd", strings.Index(src, c.at)) {
			if it.Label == name &&
				strings.Contains(it.Detail, "from "+c.origin) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: completion has no %s from %s", c.at, name,
				c.origin)
		}
	}
	got := map[sema.UnitKind]string{}
	for _, u := range r.Units {
		if u.File == nil || u.File.Bundled {
			continue
		}
		for _, d := range u.Decls {
			if d.Text == "get" && d.Kind == sema.DeclFunction {
				got[u.Kind] = declaredAt(d)
			}
		}
	}
	if got[sema.UnitPackage] != "protected counter_t in work.maths" ||
		got[sema.UnitPackageBody] != "protected body counter_t in "+
			"package body work.maths" {
		t.Errorf("protected method: %v", got)
	}
}
