package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// TestStructuralChecks: one error each, at the offending part, for
// rules GHDL and NVC both enforce (the semantic-error oracle, #34):
// signal generics, interface constants of another mode, constants
// without a value outside a package declaration, wait statements in a
// function or a sensitized process, next and exit outside a loop or
// naming another statement, others that is not alone in the last
// alternative, and (from VHDL-2002 on) shared variables that are not
// of a protected type. The legal forms next to them stay clean.
func TestStructuralChecks(t *testing.T) {
	src := `package p is
  constant deferred : integer;
end;
package body p is
  constant deferred : integer := 1;
end;
entity e is
  generic (signal g : integer; constant c : out integer; ok : integer);
end;
architecture a of e is
  constant k : integer;
  shared variable sv : integer;
  function f return integer is
  begin
    wait for 1 ns;
    return 1;
  end;
  procedure pr is
  begin
    wait for 1 ns;
  end;
begin
  process (ok)
    variable v : integer;
  begin
    wait;
    next;
    l: for i in 0 to 1 loop
      exit l;
      next l when i = 0;
      m: while true loop
        exit l;
      end loop;
    end loop;
    case v is
      when others => null;
      when 1 => null;
    end case;
    case v is
      when 1 | others => null;
    end case;
    v := 0;
  end process;
  process
  begin
    exit;
    wait;
  end process;
end;
`
	want := []string{
		"e.vhd:8:12: sema/error: a signal interface is not allowed",
		"e.vhd:8:45: sema/error: the mode of an interface constant",
		"e.vhd:11:12: sema/error: a constant outside a package",
		"e.vhd:12:24: sema/error: the type of a shared variable",
		"e.vhd:15:5: sema/error: function f cannot contain a wait",
		"e.vhd:26:5: sema/error: a process with a sensitivity list",
		"e.vhd:27:5: sema/error: next must be inside a loop",
		"e.vhd:36:12: sema/error: others must be the choice of the last",
		"e.vhd:40:16: sema/error: others must be the only choice",
		"e.vhd:46:5: sema/error: exit must be inside a loop",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
	// Shared variables of any type are VHDL-1993.
	r = analyze(t, map[string]string{"e.vhd": src},
		func(c *config.Config) { c.Standard = "1993" })
	if strings.Contains(diagText(r), "shared variable") {
		t.Errorf("1993: %s", diagText(r))
	}
}

// TestValueChecks: a type mark where a value is required, and an
// indexed name with the wrong number of indices, are one error each;
// X(S) with S a subtype is a slice, and generic type actuals are type
// marks.
func TestValueChecks(t *testing.T) {
	src := `entity g is generic (type T); end;
architecture a of g is begin end;
entity e is end;
architecture a of e is
  subtype idx is integer range 0 to 1;
  type mat is array (0 to 1, 0 to 1) of integer;
  signal s : integer;
  signal m : mat;
  signal v : bit_vector(0 to 3);
begin
  u: entity work.g generic map (T => integer);
  process begin
    s <= idx;
    s <= m(1);
    s <= m(0, 1, 1);
    s <= m(idx, 0);
    v(idx) <= "00";
    s <= m(0, 1);
    wait;
  end process;
end;
`
	want := []string{
		"e.vhd:13:10: sema/error: idx is a type, not a value",
		"e.vhd:14:11: sema/error: m has 2 dimensions, not 1 indices",
		"e.vhd:15:11: sema/error: m has 2 dimensions, not 3 indices",
		"e.vhd:16:12: sema/error: idx is a type, not a value",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.TrimSpace(diagText(r))
	if got != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
}

// TestRangeBounds: the bounds of a range have one type: overloaded
// literals that leave two types possible are ambiguous, bounds of
// different types a mismatch; an integer or floating type definition
// may mix types of one class but not integer with floating point.
func TestRangeBounds(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  type i1 is range 1 to 10;
  type i2 is range 11 to 20;
  constant v1 : i1 := 1;
  constant v2 : i2 := 20;
  type i5 is range v1 to v2;
  type bad is range 1 to 9.0;
  type arr is array (0 to 1.5) of bit;
  constant pi : real := 3.14;
begin
  process begin
    for x in '0' to '1' loop end loop;
    for c in 'a' to 'z' loop end loop;
    for i in 0 to pi loop end loop;
    wait;
  end process;
end;
`
	want := []string{
		"e.vhd:8:21: sema/error: the bounds of a type definition",
		"e.vhd:9:22: sema/type-mismatch",
		"e.vhd:13:14: sema/error: the type of the range is ambiguous",
		"e.vhd:15:14: sema/type-mismatch",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
}

// TestConstraintChecks: others in an aggregate whose context gives no
// index range, a second index constraint on a constrained array, the
// value attributes of a scalar object, and a slice of a scalar are one
// error each. Record constraints, element constraints, allocated
// objects and obj'SUBTYPE of an unconstrained parameter do give the
// range.
func TestConstraintChecks(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  type rec is record f : bit_vector; n : integer; end record;
  type arr is array (natural range <>) of bit_vector;
  type ptr is access bit_vector;
  subtype w4 is bit_vector(0 to 3);
  subtype w2 is w4(0 to 1);
  signal r : rec(f(0 to 3));
  signal m : arr(0 to 1)(0 to 7);
  constant k : rec(f(0 to 1)) := (f => (others => '0'), n => 1);
  procedure p (x : out bit_vector) is
    variable tmp : x'subtype := (others => '0');
  begin
    x := (others => '0');
  end;
begin
  process
    variable i : integer;
    variable q : ptr;
  begin
    r.f <= (others => '1');
    m(0) <= (others => '1');
    q := new bit_vector(0 to 3);
    q.all := (others => '0');
    i := i'high + i'length;
    i := i(0 to 1);
    wait;
  end process;
end;
`
	want := []string{
		"e.vhd:7:19: sema/error: w4 is already constrained",
		"e.vhd:14:11: sema/error: others needs an index range",
		"e.vhd:25:10: sema/error: the prefix of 'high must be an array",
		"e.vhd:25:19: sema/error: the prefix of 'length must be an array",
		"e.vhd:26:10: sema/error: i is INTEGER, which cannot be sliced",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
}

// TestSignalAttributeStatic: the time parameter of 'STABLE, 'QUIET
// and 'DELAYED is a static expression: constants (when their values
// are), generics and literals are; signals, variables, parameters and
// constants computed from them are not.
func TestSignalAttributeStatic(t *testing.T) {
	src := `entity e is generic (G : time := 1 ns); end;
architecture a of e is
  signal s : bit;
  signal ts : time;
  constant k : time := 2 ns;
  procedure p (constant d : time) is
    constant c : time := d;
    constant c2 : time := 3 ns;
  begin
    assert s'stable(c) and s'stable(c2) and s'stable(d);
  end;
begin
  process
    variable v : integer := 1;
  begin
    assert s'stable(k + G) and s'quiet(v * ns) and s'stable(ts);
    wait;
  end process;
  s <= s'delayed(5 ns);
end;
`
	want := []string{
		"e.vhd:10:21: sema/error: the parameter of 'stable must be static",
		"e.vhd:10:54: sema/error: the parameter of 'stable must be static",
		"e.vhd:16:40: sema/error: the parameter of 'quiet must be static",
		"e.vhd:16:61: sema/error: the parameter of 'stable must be static",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.TrimSpace(diagText(r))
	if got != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
}

// TestConformance: a subprogram body repeats its declaration's
// specification lexically (LRM 4.10): other parameter names, an added
// or dropped mode, or a class dropped next to a mode do not conform;
// letter case, and the class constant of a parameter without a mode,
// do not matter.
func TestConformance(t *testing.T) {
	src := `package p is
  function f1 (a : bit) return bit;
  function f2 (a : bit) return bit;
  function f3 (a : bit) return bit;
  procedure p1 (constant x : in integer);
  procedure p2 (constant x : integer);
  function f4 (A : Bit; n : integer := 16#1_0#) return bit;
end;
package body p is
  function f1 (b : bit) return bit is begin return b; end;
  function f2 (a : in bit) return bit is begin return a; end;
  function f3 (constant a : bit) return bit is begin return a; end;
  procedure p1 (x : in integer) is begin end;
  procedure p2 (constant x : in integer) is begin end;
  function f4 (a : bit; N : INTEGER := 16#10#) return BIT is
  begin return a; end;
end;
`
	want := []string{
		"p.vhd:10:12: sema/error: the body of f1 does not conform to " +
			"its declaration at line 2",
		"p.vhd:11:12: sema/error: the body of f2 does not conform",
		"p.vhd:13:13: sema/error: the body of p1 does not conform",
		"p.vhd:14:13: sema/error: the body of p2 does not conform",
	}
	r := analyze(t, map[string]string{"p.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
}

// TestTypeMarkOperands: a type mark is no operand of an operator, an
// argument, a range bound, a selector or an assignment target.
func TestTypeMarkOperands(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  subtype small is integer range 0 to 3;
  signal s : integer;
  function f (x : integer) return integer is begin return x; end;
begin
  process
    variable v : integer;
  begin
    if small > s then null; end if;
    v := f(small);
    for i in small to 3 loop end loop;
    case small is when others => null; end case;
    small := v;
    wait;
  end process;
end;
`
	want := []string{
		"e.vhd:10:8: sema/error: small is a type, not a value",
		"e.vhd:11:12: sema/error: small is a type, not a value",
		"e.vhd:12:14: sema/error: small is a type, not a value",
		"e.vhd:13:10: sema/error: small is a type, not a value",
		"e.vhd:14:5: sema/error: small is a type, not a value",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) < len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for _, w := range want {
		if !strings.Contains(strings.Join(got, "\n"), w) {
			t.Errorf("missing %s in\n%s", w, strings.Join(got, "\n"))
		}
	}
}

// TestStaticnessChecks: locally static is required for the bounds of
// a type definition (which must also be numeric), case choices, and a
// choice of an aggregate with more than one choice; a case generate's
// choices need only be globally static; VHDL-2008 counts calls of the
// ieee packages' functions with static arguments, and array attributes
// of objects of locally static subtypes. A slice runs the way its
// prefix's index range does, unless it is a null slice.
func TestStaticnessChecks(t *testing.T) {
	src := `library ieee;
use ieee.numeric_std.all;
entity e is generic (G : integer := 3); end;
architecture a of e is
  type t1 is range 0 to G;
  type t2 is range false to true;
  constant K : unsigned(4 downto 0) := to_unsigned(17, 5);
  signal v : bit_vector(7 downto 0);
  signal u : unsigned(4 downto 0);
begin
  process
    variable i : integer;
  begin
    case i is when G => null; when others => null; end case;
    case u is when K => null; when others => null; end case;
    v <= (G => '1', others => '0');
    v <= (v'high => '1', others => '0');
    v <= (G => '1');
    v(0 to 3) <= "0000";
    v(3 downto 4) <= "";
    v(0 to -1) <= "";
    wait;
  end process;
  gen: case G generate
    when G => begin end;
  end generate;
end;
`
	want := []string{
		"e.vhd:5:25: sema/error: the bounds of a type definition must be " +
			"locally static",
		"e.vhd:6:20: sema/error: the bounds of a type definition must be " +
			"integer or floating point, not false",
		"e.vhd:14:20: sema/error: the choice G must be locally static",
		"e.vhd:16:11: sema/error: the choice G is not locally static",
		"e.vhd:19:7: sema/error: the slice runs to but the index range",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
}

// TestObjectChecks: names declared again in an architecture that its
// entity declares, empty interface lists, file types in composites and
// file types of unsupported values, objects of unconstrained composite
// subtypes, generics without default or actual, assignments to objects
// of another class or of mode in, reads of out ports before VHDL-2008,
// and actuals of the wrong class are one error each.
func TestObjectChecks(t *testing.T) {
	src := `entity sub is generic (W : integer; D : integer := 1); end;
architecture a of sub is begin end;
entity e is port (clk, din : in bit; o : out bit); end;
architecture a of e is
  signal clk : bit;
  type ft is file of integer;
  type ptr is access integer;
  type arr is array (0 to 1) of ft;
  type fp is file of ptr;
  signal u : bit_vector;
  signal ok : bit_vector(0 to 1);
  procedure pv (variable x : inout integer) is begin end;
  procedure ps (signal x : in bit) is begin end;
  procedure pin (x : in integer) is begin x := 1; end;
begin
  i1: entity work.sub;
  i2: entity work.sub generic map (W => 1);
  process
    variable v : integer;
    constant k : integer := 0;
  begin
    k := 1;
    din <= '1';
    v <= 1;
    ok(0) <= o;
    pv(k);
    ps('1');
    pv(v);
    ps(clk);
    wait;
  end process;
end;
`
	want := []string{
		"e.vhd:5:10: sema/error: clk is already declared",
		"e.vhd:8:33: sema/error: an element of a composite type cannot",
		"e.vhd:9:22: sema/error: ptr cannot be the type of a file's",
		"e.vhd:10:14: sema/error: a signal cannot be of the unconstrained",
		"e.vhd:14:43: sema/error: parameter x is not a variable",
		"e.vhd:16:3: sema/error: generic W of sub has no default and no",
		"e.vhd:22:5: sema/error: constant k is not a variable",
		"e.vhd:23:5: sema/error: port din has mode in and cannot be",
		"e.vhd:24:5: sema/error: variable v is not a signal",
		"e.vhd:26:8: sema/error: the actual for variable parameter x " +
			"must be a variable, not constant k",
		"e.vhd:27:8: sema/error: the actual for signal parameter x must " +
			"be a signal name",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
	// Reading an out port is an error before VHDL-2008.
	r = analyze(t, map[string]string{"e.vhd": src},
		func(c *config.Config) { c.Standard = "1993" })
	if !strings.Contains(diagText(r), "e.vhd:25:14: sema/error: port o "+
		"has mode out and cannot be read before VHDL-2008") {
		t.Errorf("1993:\n%s", diagText(r))
	}
	empty := "entity x is port ( ); end;\n"
	r = analyze(t, map[string]string{"x.vhd": empty}, nil)
	if !strings.Contains(diagText(r), "x.vhd:1:18: sema/error: an "+
		"interface list cannot be empty") {
		t.Errorf("empty list:\n%s", diagText(r))
	}
}

// TestLiteralChecks: a waveform element after the first has an after
// clause with a later time; a slice has one discrete range of a
// one-dimensional array; a string or bit-string literal has the length
// of a subtype with literal bounds (not when it is a slice of a
// partial formal or a VHDL-2008 aggregate slice); an aggregate's type
// comes only from its context, so an operator between two aggregates
// is ambiguous; a name is not visible in its own declaration while
// an outer one of the same name is hidden.
func TestLiteralChecks(t *testing.T) {
	src := `entity sub is port (i : in bit_vector(7 downto 0)); end;
architecture a of sub is begin end;
entity e is end;
architecture a of e is
  type mat is array (0 to 1, 0 to 1) of bit;
  constant c1 : integer := 1;
  signal s : integer;
  signal m : mat;
  signal v : bit_vector(1 downto 0);
  signal w : bit_vector(7 downto 0);
  signal b : boolean;
begin
  s <= 0, 1;
  s <= 0 after 2 ns, 1 after 1 ns;
  s <= 0 after 1 ns, 1 after 2 ns;
  m(0 to 1, 0 to 0) <= m(0 to 1, 0 to 0);
  v <= "101";
  w <= (x"F", x"0");
  u: entity work.sub port map (i(1 downto 0) => "00",
    i(7 downto 2) => "000000");
  b <= ('0', '1') = ('0', '1');
  p: process
    constant c1 : integer := c1;
  begin
    wait;
  end process;
end;
`
	want := []string{
		"e.vhd:13:11: sema/error: a waveform element after the first",
		"e.vhd:14:30: sema/error: a waveform element's time must be",
		"e.vhd:16:13: sema/error: a slice has one discrete range",
		"e.vhd:16:34: sema/error: a slice has one discrete range",
		"e.vhd:17:8: sema/error: the literal has 3 elements, but",
		"e.vhd:21:19: sema/ambiguous-overload",
		"e.vhd:23:30: sema/error: c1 is used in its own declaration",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
}

// TestCaseCoverage: without others, the literal choices of a case
// statement or selected assignment cover each value of the selector's
// subtype (an object's subtype, a qualified expression's type mark, an
// expression's base type) once, inside it. Also: return outside a
// subprogram, an attribute dimension the array lacks, a non-discrete
// loop range, and an array aggregate mixing positional and named
// associations.
func TestCaseCoverage(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  type color is (red, green, blue);
  subtype quad is integer range 0 to 3;
  signal c : color;
  signal q : quad;
  signal v : bit_vector(0 to 3);
  signal r : integer;
begin
  process
    variable i : integer;
  begin
    case c is when red | green => null; end case;
    case c is when red => null; when green | blue => null; end case;
    case q is when 0 to 2 => null; when 2 => null; when 3 => null;
      end case;
    case q is when 0 to 4 => null; end case;
    case quad'(i mod 4) is when 0 | 1 | 2 | 3 => null; end case;
    case i is when 0 => null; end case;
    for k in 0.0 to 1.0 loop end loop;
    r <= v'length(2);
    v <= ('0', 1 => '1', others => '0');
    return;
  end process;
  with c select r <= 1 when red, 2 when green;
end;
`
	want := []string{
		"e.vhd:13:5: sema/error: the choices do not cover blue",
		"e.vhd:15:41: sema/error: the choice 2 repeats a value",
		"e.vhd:17:20: sema/error: the choice 0 to 4 is outside the range",
		"e.vhd:19:5: sema/error: the choices do not cover " +
			"-2147483648 to -1",
		"e.vhd:20:14: sema/error: the range of a loop or generate",
		"e.vhd:21:19: sema/error: dimension 2 of 'length is not one",
		"e.vhd:22:10: sema/error: an array aggregate's associations",
		"e.vhd:23:5: sema/error: a return statement must be inside",
		"e.vhd:25:10: sema/error: the choices do not cover blue",
	}
	r := analyze(t, map[string]string{"e.vhd": src}, nil)
	got := strings.Split(strings.TrimSpace(diagText(r)), "\n")
	if len(got) != len(want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("got  %s\nwant %s...", got[i], want[i])
		}
	}
}
