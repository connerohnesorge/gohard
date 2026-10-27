package sema

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestPSLNamesAndBuiltins: a named sequence or property is a PSL
// operand by its name (IEEE 1850 6.1.1.1, 6.2.4), not a value error;
// prev, rose, fell, stable, onehot and onehot0 are built-in functions
// inside PSL code, typed by their argument (IEEE 1850 5.2.3): onehot
// of a scalar, or rose of an integer, is an error, and outside PSL the
// names are ordinary identifiers.
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
  b1: process
    variable prev : integer := 0; -- an identifier outside PSL
  begin
    prev := prev + 1;
    wait;
  end process;
end;
`}})
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
