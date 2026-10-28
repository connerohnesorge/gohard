package sema

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
)

// TestPSLNamesAndBuiltins: a named sequence or property is a PSL
// operand by its name (IEEE 1850 6.1.1.1, 6.2.4), not a value error;
// prev, rose, fell, stable, onehot, onehot0 and isunknown are built-in
// functions inside PSL code, typed by their argument (IEEE 1850
// 5.2.3): onehot or isunknown of a scalar, or rose of an integer, is
// an error, and outside PSL the names are ordinary identifiers.
func TestPSLNamesAndBuiltins(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity e is end;
architecture a of e is
  signal clk, x, y : bit;
  signal v : bit_vector(3 downto 0);
  signal i : integer;
  default clock is clk'event;
  sequence s1 is {x; y};
  property p1 is always x;
begin
  c1: cover {s1};
  c2: cover s1 report "covered";
  a1: assert always i = prev(i) + 1;
  a2: assert always x = not prev(x);
  a3: assert always rose(x) -> stable(v);
  a4: assert always onehot(v) and onehot0(v);
  a5: assert always fell(x) or ended(s1);
  a6: assert always onehot(x); --E
  a7: assert always rose(i) -> y; --E
  a8: assert always onehot(i); --E
  a9: assert always prev(nope) = x; --E
  a10: assert always isunknown(v) -> y;
  a11: assert always isunknown(x) -> y; --E
  b1: process
    variable prev : integer := 0; -- an identifier outside PSL
  begin
    prev := prev + 1;
    wait;
  end process;
end;
`}})
}

// TestPSLBooleanOperands (#111): an HDL expression or a built-in call
// that is a SERE item, the operand of a property or the condition of
// abort, async_abort or sync_abort is a PSL Boolean (BOOLEAN, BIT or
// STD_ULOGIC), as NVC 1.23.0 requires ("expression must be a PSL
// Boolean"); prev of an INTEGER or a vector is not one. An actual of a
// sequence or property instance may be a value.
func TestPSLBooleanOperands(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
library ieee; use ieee.std_logic_1164.all;
entity e is end;
architecture a of e is
  signal clk, x : std_logic;
  signal b : boolean;
  signal i : integer;
  signal c : character;
  signal v : std_logic_vector(3 downto 0);
  default clock is rising_edge(clk);
  sequence s2(const n) is {x[*n]};
  property p2(boolean y) is always y;
begin
  a1: assert always prev(i); --E
  a2: assert always prev(v); --E
  a3: assert always (prev(i)); --E
  a4: assert always {x; prev(i)}; --E
  a5: assert always (i); --E
  a6: assert always c; --E
  a7: cover {x; v}; --E
  a8: assert always x -> prev(v); --E
  a9: assert always ((x -> b) abort prev(i)); --E
  a10: assert always ((x -> b) async_abort prev(i)); --E
  a11: assert always ((x -> b) sync_abort prev(v)); --E
  a12: assert always (x -> b) abort i; --E
  a13: assert always (x -> b) abort (prev(i)); --E
  b1: assert always prev(x);
  b2: assert always (prev(b) -> b);
  b3: assert always prev(i) = 1 -> x;
  b4: cover {x; prev(x, 2); b};
  b5: cover s2(3);
  b6: assert p2(x);
  b7: assert always rose(x) -> stable(v);
  b8: assert always ((x -> b) abort prev(x));
  b9: assert always (x -> b) sync_abort b;
  b10: assert always ((x -> b) async_abort prev(i) = 1);
end;
`}})
}

// TestPSLBuiltinCalls (#111): a PSL built-in call names its function
// (PSLBuiltin) and keeps its typed actuals for the simulator's PSL
// runner, inside an HDL expression and as a PSL operand of its own,
// whose typed call is recorded like a Boolean's.
func TestPSLBuiltinCalls(t *testing.T) {
	src := `library ieee; use ieee.std_logic_1164.all;
entity e is end;
architecture a of e is
  signal clk, x, y : std_logic;
  default clock is rising_edge(clk);
begin
  a1: assert always (prev(x, 2) = '1' -> y);
  a2: assert always rose(x) -> stable(y);
end;
`
	r := analyze(t, map[string]string{"e.vhd": src},
		func(c *config.Config) { c.Standard = "2008" })
	if len(r.Diagnostics) > 0 {
		t.Fatal(diagText(r))
	}
	var got []string
	for _, f := range r.Files {
		for n := range f.Root().Descendants() {
			if n.Kind() != cst.KindPSLBoolean &&
				n.Kind() != cst.KindPSLBuiltinCall {
				continue
			}
			e := f.PSLExpr(n)
			var walk func(e *Expr)
			walk = func(e *Expr) {
				if e == nil {
					return
				}
				if name := PSLBuiltin(e); name != "" {
					var args []string
					for _, a := range e.Args {
						args = append(args, a.Node.Text()+":"+a.Type.Name())
					}
					got = append(got, n.Kind().String()+" "+name+"("+
						strings.Join(args, ", ")+") "+e.Type.Name())
				}
				for _, a := range e.Args {
					walk(a)
				}
			}
			walk(e)
		}
	}
	want := []string{
		"PSLBoolean prev(x:STD_LOGIC, 2:universal_integer) STD_LOGIC",
		"PSLBuiltinCall rose(x:STD_LOGIC) BOOLEAN",
		"PSLBuiltinCall stable(y:STD_LOGIC) BOOLEAN",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("built-in calls:\n%s", strings.Join(got, "\n"))
	}
}

// TestPSLSequenceContext: cover and restrict take a sequence, and a
// SERE holds sequences and booleans, so a property instance there is
// an error, while a property directive takes either (IEEE 1850 6.1.1,
// 7.1.4, 7.1.6); a sequence declaration's body is a sequence, not a
// parenthesized property (IEEE 1850 6.1.1.1).
func TestPSLSequenceContext(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity e is end;
architecture a of e is
  signal clk, x, y : bit;
  default clock is clk'event;
  sequence s1 is {x; y};
  sequence s2 is ({x; y}); --E
  property p1 is always x;
begin
  c1: cover s1;
  c2: cover p1; --E
  c3: cover {s1; p1}; --E
  a1: assert p1;
  a2: assert always s1;
  a3: assert {x} |=> p1;
end;
`}})
}

// TestPSLOperatorForms: goto and non-consecutive repetitions repeat a
// Boolean, a suffix implication starts with a sequence, never takes a
// Boolean or a sequence (simple subset), range bounds are ordered, a
// clock is a Boolean and union joins values of one type (IEEE 1850
// 4.4.4, 5.1.3, 6.1.1, 6.2.1.6.1).
func TestPSLOperatorForms(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity e is end;
architecture a of e is
  signal clk, x, y : bit;
  signal i : integer;
  signal c : character;
  default clock is clk'event;
begin
  a1: assert never {x} |=> {y; x}; --E
  a2: assert {x[*]}[=2]; --E
  a3: assert {x}[->1]; --E
  a4: assert always x[->1 to 3];
  a5: assert always {x[=2]; y};
  a6: assert (x -> next y) |=> {y}; --E
  a7: assert always {x} |=> {y};
  a8: cover {x}[*3 to 2]; --E
  a9: cover {x}[*2 to 3];
  b1: assert (next_e[5 to 3] (y)); --E
  b2: assert (x -> next y) @(1); --E
  b3: assert (x -> next y) @(clk'event);
  b4: assert never (x) -> eventually! (y); --E
  b5: assert never {x; y};
  b6: assert never x;
  b7: cover {(x union c) = '1'}; --E
  b8: cover {(x union y) = '1'};
end;
`}})
}

// TestVunitBindingArchitecture: a verification unit bound to e(arch)
// names an existing architecture of e (IEEE 1850 7.2).
func TestVunitBindingArchitecture(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity mwe is
end entity mwe;
architecture behavioral of mwe is
begin
end architecture behavioral;
vunit v1(mwe(abc)) --E
{
}
vunit v2(mwe(behavioral))
{
}
`}})
}

// vunitDesign binds PSL verification units to a design entity in
// another file (#110). a_props.vhd sorts before dut.vhd, so the vunits
// see the bound units only because they depend on them. Lines marked
// --E have an error.
var vunitDesign = map[string]string{
	"a_props.vhd": `vunit v_arch(dut(rtl)) {
  default clock is rising_edge(clk);
  assert always (a -> b);
  assert always (q -> width = 4);
}
vunit v_entity(work.dut) {
  default clock is rising_edge(clk);
  assert always (a -> b);
  assert always (q -> a); --E
}
vunit v_nope(dut(nope)) { --E
  default clock is rising_edge(clk);
  assert always (a -> b);
}
vunit v_typo(dut(rtl)) {
  default clock is rising_edge(clk);
  assert always (a -> bb); --E
}
vunit a(dut(rtl)) {
  default clock is rising_edge(clk);
  assert always (a -> b);
}
vunit width(dut(rtl)) {
  default clock is rising_edge(clk);
  assert always (q -> width = 4);
}
vunit q(dut) {
  default clock is rising_edge(clk);
  assert always (q -> a); --E
}
`,
	"dut.vhd": `library ieee;
use ieee.std_logic_1164.all;
entity dut is
  generic (width : natural := 4);
  port (clk : in std_logic; a, b : in std_logic);
end entity;
architecture rtl of dut is
  signal q : std_logic;
begin
  q <= a and b;
end architecture;
`,
}

// vunitManyDesign has eight vunits of one file bound to one
// architecture, so they share a level and the real job pool analyzes
// them together (TestParallelJobs). Their directives read the file's
// PSL map while analyzing it: an @ clock, a range count and a union
// (pslClock, pslCount, the union check).
var vunitManyDesign = map[string]string{
	"dut.vhd": vunitDesign["dut.vhd"],
	"props.vhd": func() string {
		var b strings.Builder
		for i := range 8 {
			fmt.Fprintf(&b, "vunit v%d(dut(rtl)) {\n", i)
			for range 10 {
				b.WriteString("  assert always (a -> b) " +
					"@ rising_edge(clk);\n" +
					"  cover {a[*2 to 3]; b} @ rising_edge(clk);\n" +
					"  cover {(a union b) = '1'};\n")
			}
			b.WriteString("}\n")
		}
		return b.String()
	}(),
}

// TestVunitBoundNames: inside a verification unit bound to e(arch),
// the declarations of e and arch and e's context clause are visible;
// bound to e alone, only e's (IEEE 1850 7.2, #110).
func TestVunitBoundNames(t *testing.T) {
	r := analyze(t, vunitDesign, nil)
	type line struct {
		path string
		n    int
	}
	var want, got []line
	for _, p := range slices.Sorted(mapsKeys(vunitDesign)) {
		for i, l := range strings.Split(vunitDesign[p], "\n") {
			if strings.Contains(l, "--E") {
				want = append(want, line{p, i + 1})
			}
		}
	}
	for _, d := range r.Diagnostics {
		if d.Severity.String() == "error" {
			got = append(got, line{d.Path, d.Line})
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("error lines %v, want %v\n%s", got, want, diagText(r))
	}
	src := vunitDesign["a_props.vhd"]
	for _, c := range []struct {
		at   int // occurrence index of name in a_props.vhd
		name string
		kind DeclKind
		line int // of the declaration in dut.vhd
	}{
		{0, "clk", DeclPort, 5}, {0, "a -> b", DeclPort, 5},
		{0, "q ->", DeclSignal, 8}, {0, "width", DeclGeneric, 4},
		{1, "clk", DeclPort, 5}, {3, "clk", DeclPort, 5},
		// A vunit's own name does not hide a declaration of the
		// bound unit (vunit a, port a).
		{4, "a -> b", DeclPort, 5}, {2, "q ->", DeclSignal, 8},
		{2, "width", DeclGeneric, 4},
	} {
		off := nthOffset(src, c.name, c.at)
		ref, ok := r.RefAt("a_props.vhd", off)
		if !ok || ref.Decl == nil {
			t.Errorf("%s #%d: no reference", c.name, c.at)
			continue
		}
		d := ref.Decl
		if l, _ := d.File.Lines.LineCol(d.Span.Start); d.Kind != c.kind ||
			d.File.Path != "dut.vhd" || l != c.line {
			t.Errorf("%s #%d: %s at %s:%d, want %s at dut.vhd:%d",
				c.name, c.at, d.Kind, d.File.Path, l, c.kind, c.line)
		}
	}
}

// nthOffset returns the offset of the n-th (from 0) occurrence of sub
// in s, -1 when there is none.
func nthOffset(s, sub string, n int) int {
	off := 0
	for {
		i := strings.Index(s[off:], sub)
		if i < 0 {
			return -1
		}
		if n == 0 {
			return off + i
		}
		n--
		off += i + len(sub)
	}
}

// TestPSLInstanceForms (#66): an instance of a property or sequence
// has one actual per formal parameter (IEEE 1850 6.2.4), and the count
// of next_event is at least 1 (IEEE 1850 6.2.1.6).
func TestPSLInstanceForms(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity e is end;
architecture a of e is
  signal clk, x, y : bit;
  default clock is clk'event;
  property p (boolean l, r) is always (l -> next r);
  sequence s (boolean l) is {l; l};
  property p0 is always x;
begin
  a1: assert p(x, y);
  a2: assert p(x); --E
  a3: assert p0;
  a4: assert p0(x); --E
  a5: cover {s(x)};
  a6: cover {s(x, y)}; --E
  a7: assert p; --E
  a8: assert always (x -> next_event(y)[0](x)); --E
  a9: assert always (x -> next_event_a(y)[1 to 2](x));
  b1: assert always (x -> next_event(y)(x));
end;
`}})
}

// TestPSLForall (#66): the parameter of forall is declared for its
// property only (IEEE 1850 6.2.1.7).
func TestPSLForall(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity e is end;
architecture a of e is
  signal clk, x : bit;
  signal v : bit_vector(0 to 1);
  default clock is clk'event;
begin
  a1: assert forall i in {0 to 1} : always (x -> v(i) = '1');
  a2: assert always (x -> v(i) = '1'); --E
end;
`}})
}

// TestPSLInstanceInParens (#66): a property or sequence instance in
// parentheses is the instance (FL_Property ::= ( FL_Property )), not a
// value, in every directive form, also as a whole assert, and its name
// refers to the declaration.
func TestPSLInstanceInParens(t *testing.T) {
	src := `
library ieee;
use ieee.std_logic_1164.all;
entity e is end;
architecture a of e is
  signal clk, x, y : std_logic;
  default clock is rising_edge(clk);
  property p0 is (x = '1' -> next y = '1');
  property p1 (boolean l) is always (l -> next y = '1');
  property pf (property q) is always (x = '1' -> (q));
  sequence s0 is {x = '1'; y = '0'};
begin
  a1: assert always (p0);
  a2: assert always {s0} |=> (p0);
  a3: cover {(s0)};
  a4: assert (p1(x = '1'));
  a5: assert ((p1(x = '1')));
  a6: assert pf(next y = '1');
end;
`
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: src}})
	r := analyze(t, map[string]string{"e.vhd": src}, func(c *config.Config) {
		c.Standard = "2008"
	})
	// Each prefix is followed by an instance's name.
	for _, prefix := range []string{"a1: assert always (",
		"a2: assert always {s0} |=> (", "a3: cover {(", "a4: assert (",
		"a5: assert (("} {
		off := nthOffset(src, prefix, 0) + len(prefix)
		ref, ok := r.RefAt("e.vhd", off)
		if !ok || ref.Decl == nil || ref.Decl.Kind != DeclPSL {
			t.Errorf("%s: %+v", prefix, ref.Decl)
		}
	}
}

// TestPSLRecursiveDecl (#66): a property or sequence declaration that
// instantiates itself is an error (IEEE 1850 does not allow recursion);
// one that instantiates an earlier declaration twice is not.
func TestPSLRecursiveDecl(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity e is end;
architecture a of e is
  signal clk, x, y : bit;
  default clock is clk'event;
  property p is always (x -> next p); --E
  property q is (always q) and (always x); --E
  sequence s is {x; s}; --E
  property r0 is always (x -> next y);
  property r1 is (always r0) and (always r0);
begin
  a1: assert r1;
end;
`}})
}
