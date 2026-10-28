package sema

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

// nameErrLines analyzes src (one file, under std, lenient as given) and
// checks the invariant of the name rules: an error is reported on
// exactly the lines marked "-- E", and on no other line. want, when
// not empty, must occur in the diagnostics.
func nameErrLines(t *testing.T, std string, lenient bool, src string,
	want ...string,
) {
	t.Helper()
	r := analyze(t, map[string]string{"t.vhd": src},
		func(c *config.Config) {
			c.Standard = std
			c.Lenient = lenient
		})
	var marked, got []int
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "-- E") {
			marked = append(marked, i+1)
		}
	}
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error && !slices.Contains(got, d.Line) {
			got = append(got, d.Line)
		}
	}
	slices.Sort(got)
	text := diagText(r)
	if !slices.Equal(got, marked) {
		t.Errorf("error lines %v, want %v:\n%s", got, marked, text)
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q in:\n%s", w, text)
		}
	}
}

// TestLibraryAndTypePrefixes: a library name is neither indexed nor
// sliced, L.all only ends a use clause, a type mark is no prefix of a
// selected name (LRM 8.1, 8.3, 12.4).
func TestLibraryAndTypePrefixes(t *testing.T) {
	nameErrLines(t, "2008", false, `entity e is end;
architecture a of e is
  type rec is record fld : integer; end record;
  constant c0 : bit := std(0); -- E
  constant c1 : bit_vector(1 downto 0) := work(1 downto 0); -- E
  constant c2 : integer := rec.fld; -- E
  constant c3 : boolean := boolean.foo; -- E
  constant c4 : integer := std.standard.integer'(1);
  signal r : rec;
  constant c5 : integer := r.fld;
begin
end;
`, "library std cannot be indexed or sliced",
		"type rec cannot be the prefix of a selected name")
	nameErrLines(t, "2008", false, `library ieee;
use ieee.all.common.x; -- E
entity e is end;
`, "cannot be selected by all")
}

// TestPhysicalUnitNames: the unit of a physical literal names a unit
// (LRM 9.3.2), not a port or a component.
func TestPhysicalUnitNames(t *testing.T) {
	nameErrLines(t, "1993", false, `entity e is port (fs : in bit); end;
architecture a of e is
  component comp is end component;
  constant d1 : time := 1 comp; -- E
  constant d2 : time := 5 ns;
begin
  process begin wait for 5 fs; end process; -- E
end;
`, "comp is not a physical unit")
}

// TestSelfReference: a declaration hides an outer homograph from its
// start, so its own name in it is an error (LRM 12.3), for an alias
// too; lenient analysis sees the outer one instead.
func TestSelfReference(t *testing.T) {
	src := `entity e is generic (g : natural := 1); end;
architecture a of e is
  constant c1 : integer := 1;
  alias a1 is c1;
begin
  process is
    variable g : natural := g; -- E
    alias a1 is a1; -- E
  begin wait; end process;
end;
`
	nameErrLines(t, "1993", false, src, "a1 is used in its own")
	lenient := strings.ReplaceAll(src, "-- E", "")
	nameErrLines(t, "1993", true, lenient)
}

// TestLabelRedeclaredLater: of a signal and a later statement label of
// one name, the label is reported (LRM 12.3: labels are declared
// before the declarations that precede them in the text).
func TestLabelRedeclaredLater(t *testing.T) {
	nameErrLines(t, "2008", false, `entity sub is end;
architecture a of sub is begin end;
entity e is end;
architecture a of e is
  signal u : bit;
begin
  u : entity work.sub; -- E
end;
`, "u is already declared in this region (line 5)")
}

// TestAttributeSpecifications: the named entities of an attribute
// specification are declared and of its entity class, a named entity
// gets one value of an attribute, and an attribute is not of an
// access, file or protected type (LRM 6.7, 7.2).
func TestAttributeSpecifications(t *testing.T) {
	nameErrLines(t, "2008", false, `entity e is end;
architecture a of e is
  type acc is access integer;
  type ft is file of integer;
  type rec is record p : acc; end record;
  attribute att : integer;
  attribute bad1 : acc; -- E
  attribute bad2 : ft; -- E
  attribute bad3 : rec; -- E
  signal s : integer;
  type t is range 1 to 2;
  subtype st is t;
  attribute att of s : signal is 1;
  attribute att of s : signal is 2; -- E
  attribute att of q : signal is 3; -- E
  attribute att of t : subtype is 4; -- E
  attribute att of st : type is 5; -- E
  attribute att of st : subtype is 6;
  attribute att of lbl : label is 7;
  procedure p (x : integer) is begin end;
  procedure p (x : bit) is begin end;
  attribute att of p : procedure is 8;
begin
  process is
    variable v : integer;
    attribute att of v : signal is 9; -- E
  begin wait; end process;
end;
`, "q is not declared", "variable v is not of class 'signal'",
		"s already has a value of attribute att",
		"type t is not of class 'subtype'")
}

// TestPredefinedAttributePrefixes: the kinds of prefix each predefined
// attribute takes (LRM 16.2).
func TestPredefinedAttributePrefixes(t *testing.T) {
	nameErrLines(t, "2008", false, `entity e is port (i : in integer;
  o : out integer); end;
architecture a of e is
  type ev is (v1, v2);
  type ia is array (natural range <>) of integer;
  subtype ca is ia(1 to 2);
  type rec is record f : integer; end record;
  type ia2 is array (1 to 2) of integer;
  attribute s : string;
  attribute u : integer;
  signal sig : integer;
  attribute u of sig : signal is 1;
begin
  p : process is
    variable x : integer;
    variable y : rec;
    variable z : ia2;
  begin
    assert ev'length = 2; -- E
    assert ia'length = 2; -- E
    assert ca'length = 2 and ca'left = 1 and z'length = 2;
    assert ev'left = v1 and integer'high > 0;
    for k in x'range loop end loop; -- E
    assert y'length = 1; -- E
    assert e'length = 1; -- E
    assert ia2'image(z) = ""; -- E
    assert x'base'left = 1; -- E
    assert integer'val(1.5) = 1; -- E
    assert integer'val(1) = 1;
    assert s(1) = 'c'; -- E
    assert p'event; -- E
    assert sig'event and o'driving;
    assert i'driving; -- E
    assert z(1)'u = 1; -- E
    assert sig'u = 1;
    assert ia'subtype'simple_name = ""; -- E
    assert sig'simple_name = "sig";
    wait;
  end process;
end;
`, "the prefix of 'length must be a constrained array type, not ia",
		"the parameter of 'VAL must be of an integer type",
		"attribute s cannot be indexed",
		"the prefix of 'event must be a signal, not p",
		"must not be i, of mode in")
}

// TestAttributesByStandard: 'ELEMENT and 'SUBTYPE are predefined from
// VHDL-2008 and 'INDEX from VHDL-2019; before, a user-defined attribute
// of the name is meant. T'SUBTYPE(X) is a type conversion.
func TestAttributesByStandard(t *testing.T) {
	nameErrLines(t, "1993", false, `entity e is end;
architecture a of e is
  attribute index : integer;
  attribute element : integer;
  signal s : bit;
  attribute index of s : signal is 4;
  attribute element of s : signal is 5;
begin
  assert s'index = 4 and s'element = 5;
end;
`)
	nameErrLines(t, "2008", false, `entity e is end;
architecture a of e is
  signal s : bit_vector(1 to 2);
  constant c : bit_vector(1 to 2) := s'subtype(s);
  constant d : bit_vector(1 to 2) := s'subtype(1); -- E
begin
end;
`, "cannot convert")
}

// TestUnspecifiedUserAttribute: the attribute name P'A of an object of
// another unit that no specification gives A is an error (LRM 7.2).
func TestUnspecifiedUserAttribute(t *testing.T) {
	r := analyze(t, map[string]string{"t.vhd": `package pk is
  attribute period : time;
end;
use work.pk.period;
entity inner is
  port (clk, clk2 : in bit);
  attribute period of clk2 : signal is 1 ns;
end;
architecture a of inner is
  constant p1 : time := clk'period;
  constant p2 : time := clk2'period;
begin
end;
`}, nil)
	got := diagText(r)
	if strings.Count(got, "error") != 1 || !strings.Contains(got,
		"t.vhd:10:25: sema/error: clk has no value of attribute period") {
		t.Fatalf("got:\n%s", got)
	}
}

// TestNameRuleScopes: a duplicate subprogram body, an expanded name
// outside its statement, an alias signature or a default of an
// interface subprogram that matches nothing, a generic subprogram
// actual that is no subprogram, and an entity instantiated as a
// component (LRM 4.3, 8.3, 6.6.3, 6.5.6.2, 11.7.1).
func TestNameRuleScopes(t *testing.T) {
	nameErrLines(t, "2008", false, `entity sub is end;
architecture a of sub is begin end;
package pk is
  function f (a : integer) return integer;
  procedure identifier;
  alias al1 is identifier[return integer]; -- E
  alias al2 is identifier[];
end;
package body pk is
  function f (a : integer) return integer is begin return 1; end;
  function f (a : integer) return integer is begin return 2; end; -- E
  procedure identifier is begin end;
end;
package gp is
  generic (function plus (x : integer) return integer);
end;
entity e is
  generic (
    procedure dr1 (signal s : out bit) is nothing_here; -- E
    function g1 (x : integer) return integer is work.pk.f;
    function g2 (x, y : integer) return integer is work.pk.f); -- E
end;
architecture a of e is
  constant plus : natural := 5;
  package i1 is new work.gp generic map (plus => plus); -- E
  package i2 is new work.gp generic map (plus => work.pk.f);
begin
  u : work.sub; -- E
  p : process is
    variable j : integer;
  begin
    l1 : for i in 1 to 2 loop
      j := l1.i;
    end loop;
    j := l1.i; -- E
    wait;
  end process;
  h : if true generate
    signal g : integer;
  begin
    g <= h.g;
  end generate;
end;
`, "the body of f is already defined (line 10)",
		"matches the signature [return integer]",
		"the expanded name l1.i is only allowed within l1",
		"no visible subprogram work.pk.f matches the profile of g2",
		"plus is not a subprogram")
	// Lenient analysis accepts the entity as a component.
	nameErrLines(t, "2008", true, `entity sub is end;
architecture a of sub is begin end;
entity e is end;
architecture a of e is
begin
  u : work.sub;
end;
`)
}

// TestStringCharactersVisible: the character literals of a string
// literal are visible where it is written (LRM 9.3.2).
func TestStringCharactersVisible(t *testing.T) {
	nameErrLines(t, "2008", false, `library ieee;
use ieee.numeric_std.all;
entity e is end;
architecture a of e is
  constant c : unsigned(0 downto 0) := "1"; -- E
  constant d : unsigned(0 downto 0) := unsigned'(0 => '1'); -- E
begin
end;
`, "character '1' of type STD_ULOGIC is not visible")
	nameErrLines(t, "2008", false, `library ieee;
use ieee.std_logic_1164.all, ieee.numeric_std.all;
entity e is end;
architecture a of e is
  constant c : unsigned(0 downto 0) := "1";
begin
end;
`)
}

// TestLenientConformance: the class constant of a mode-less parameter
// may differ between declaration and body only under lenient analysis
// (LRM 4.10 allows no such variation).
func TestLenientConformance(t *testing.T) {
	src := `package p is
  function f1 (constant a : bit) return bit;
end;
package body p is
  function f1 (a : bit) return bit is begin return a; end; -- E
end;
`
	nameErrLines(t, "1993", false, src, "does not conform")
	nameErrLines(t, "1993", true, strings.ReplaceAll(src, "-- E", ""))
}

// TestNameRulesOncePerLine: a rule reports each offending name once,
// however often its construct repeats (a property over n).
func TestNameRulesOncePerLine(t *testing.T) {
	for n := 1; n <= 5; n++ {
		var b strings.Builder
		b.WriteString("entity e is end;\narchitecture a of e is\n")
		for i := range n {
			b.WriteString("  constant c" + strconv.Itoa(i) +
				" : bit := std(" + strconv.Itoa(i) + "); -- E\n")
		}
		b.WriteString("begin\nend;\n")
		r := analyze(t, map[string]string{"t.vhd": b.String()}, nil)
		if c := strings.Count(diagText(r), "cannot be indexed"); c != n {
			t.Fatalf("n=%d: %d reports:\n%s", n, c, diagText(r))
		}
	}
}
