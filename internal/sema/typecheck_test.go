package sema

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

// markedErrors analyzes src under std and checks that the lines with
// an error are exactly the lines marked "-- error": every rule fires
// where it must and nowhere else in the same text.
func markedErrors(t *testing.T, std, src string) {
	t.Helper()
	r := analyze(t, map[string]string{"t.vhd": src},
		func(c *config.Config) { c.Standard = std })
	var want, got []int
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "-- error") {
			want = append(want, i+1)
		}
	}
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error && !slices.Contains(got, d.Line) {
			got = append(got, d.Line)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("error lines %v, want %v\n%s", got, want, diagText(r))
	}
}

// TestStringLiteralContext: a string or bit-string literal is a value
// only of a one-dimensional character array type holding its
// characters (LRM 9.3.2), whatever the context: a target, a
// condition, a time, a range bound.
func TestStringLiteralContext(t *testing.T) {
	markedErrors(t, "1993", `entity e is end;
architecture a of e is
  signal foo : bit;
  signal v : bit_vector(0 to 1);
  signal c : boolean;
  type page is array (0 to X"FFF") of bit; -- error
begin
  foo <= "0" when c else '1'; -- error
  v <= "01" when c else "10";
  process begin
    wait for x"FFFFFFFE"; -- error
    for i in 1 to 2 loop
      next when "tt"; -- error
    end loop;
    v <= "0Z"; -- error
    wait;
  end process;
end;
`)
}

// TestAggregateContext: an aggregate is a value of a composite type
// only (LRM 9.3.3.1).
func TestAggregateContext(t *testing.T) {
	markedErrors(t, "2008", `entity e is end;
architecture a of e is
  signal s : bit;
  signal v : bit_vector(0 to 1);
  component comp port (a1 : in bit); end component;
begin
  s <= (others => '0'); -- error
  v <= (others => '0');
  u: comp port map (a1 => (others => '1')); -- error
end;
`)
}

// TestAggregateTargetValue: the type of an aggregate target comes from
// the value alone (LRM 10.6.2.1), so the value is neither an aggregate
// nor a string literal unless another alternative decides the type.
func TestAggregateTargetValue(t *testing.T) {
	markedErrors(t, "2008", `entity e is end;
architecture a of e is
  signal s, t : bit;
  signal sel : integer;
begin
  with sel select (s, t) <= bit_vector'("01") when 2, "10" when others;
  with sel select (s, t) <= "01" when 2, "10" when others; -- error
  process
    variable x, y : bit;
    variable v : bit_vector(1 to 2);
  begin
    (x, y) := "10"; -- error
    (s, t) <= ('1', '0') after 10 ns; -- error
    (x, y) := v;
    (x, y) := bit_vector'("10");
    wait;
  end process;
end;
`)
}

// TestOthersObjectTarget: others in an aggregate needs a constrained
// context; an object whose declared subtype is unconstrained (a port
// or parameter) gets its index range only at elaboration, so
// others assigned to it is an error (LRM 9.3.3.3 c, as GHDL and NVC
// say). Lenient analysis takes the object's range (GHDL -frelaxed, NVC
// --relaxed); a call argument has no range even then.
func TestOthersObjectTarget(t *testing.T) {
	src := `entity e is
  port (o : out bit_vector);
end;
architecture a of e is
  procedure set (v : out string) is
  begin
    v := (others => ' '); --S
  end set;
  procedure use_it (v : in bit_vector) is
  begin
  end use_it;
begin
  o <= (others => '0'); --S
  process
    variable v : string (1 to 4);
  begin
    v := (others => 'x');
    set(v);
    use_it((others => '0')); --S --E
    wait;
  end process;
end;
`
	checkMarked(t, []markedCase{
		{name: "strict", std: "2008",
			src: strings.ReplaceAll(src, "--S", "--E")},
		{name: "lenient", std: "2008", lenient: true, src: src},
	})
}

// TestRangeIndexType: a discrete range in an index constraint, a slice
// or a constraint by a type mark is of the index type (LRM 5.3.2.2,
// 8.5).
func TestRangeIndexType(t *testing.T) {
	markedErrors(t, "1993", `entity e is end;
architecture a of e is
  type five1 is range 1 to 5;
  type five2 is range 1 to 5;
  type arr is array (five1 range <>) of boolean;
  subtype a3 is arr(five1);
  type index is range 3 downto 0;
  subtype bvi is bit_vector(index); -- error
  subtype bvn is bit_vector(natural range 0 to 3);
begin
  process
    variable v : a3;
    constant f2 : five2 := 2;
    constant f4 : five2 := 4;
    constant f14 : five1 := 4;
  begin
    v(2 to 4) := v(f2 to f4); -- error
    v(2 to 4) := v(2 to f14);
    wait;
  end process;
end;
`)
}

// TestCaseSelectorType: a case expression is discrete or a
// one-dimensional character array; a matching case's is BIT,
// STD_ULOGIC or an array of them (LRM 10.9).
func TestCaseSelectorType(t *testing.T) {
	src := `library ieee; use ieee.std_logic_1164.all;
entity e is end;
architecture a of e is
  type iarr is array (natural range <>) of integer;
  signal r : real;
  signal o : integer;
begin
  with r select o <= 1 when 1.0, 0 when others; -- error
  process
    variable ia : iarr(0 to 1);
    variable s : string(1 to 2);
    variable u : std_logic_vector(0 to 1);
    variable i : integer;
  begin
    case r is -- error
      when others => null;
    end case;
    case ia is -- error
      when others => null;
    end case;
    case s is
      when others => null;
    end case;
    case? u is
      when others => null;
    end case?;
    case? i is -- error
      when others => null;
    end case?;
    wait;
  end process;
end;
`
	markedErrors(t, "2008", src)
}

// TestAliasSubtype: an object alias's subtype indication has the base
// type of the aliased object (LRM 6.6.2).
func TestAliasSubtype(t *testing.T) {
	markedErrors(t, "1993", `entity e is end;
architecture a of e is
  type int_array is array (natural range <>) of integer;
  signal data : integer;
  signal x : int_array(1 to 3);
  alias sign : bit is data; -- error
  alias whole : integer is data;
  alias xx : integer is x(1 to 2); -- error
  alias xs : int_array is x(1 to 2);
  alias x1 : natural is x(1);
begin
end;
`)
}

// TestConversionOperand: the operand of a type conversion is a
// complete context (LRM 9.3.6): no string literal, aggregate or
// literal overloaded on its type; before VHDL-2008 the index types of
// closely related arrays are closely related too.
func TestConversionOperand(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  type grapes is (sweet, sour);
  type oranges is (sweet, bitter);
  type century is array (1 to 2) of real;
  type millenia is array (bit'('0') to bit'('1')) of real;
begin
  process
    variable g : grapes;
    variable s : string(1 to 4);
    variable b : bit_vector(0 to 1);
    variable h : century;
    variable k : millenia;
  begin
    s := string("CLSI"); -- error
    b := bit_vector(('0', '1')); -- error
    g := grapes(sweet); -- error
    g := grapes(sour);
    b := bit_vector(b);
    k := millenia(h); -- error93
    wait;
  end process;
end;
`
	markedErrors(t, "2008", strings.ReplaceAll(src, "-- error93", ""))
	markedErrors(t, "1993", strings.ReplaceAll(src, "-- error93",
		"-- error"))
}

// TestAllocatorConstraint: an allocator without an initial value
// allocates a constrained subtype (LRM 9.3.7).
func TestAllocatorConstraint(t *testing.T) {
	markedErrors(t, "1993", `entity e is end;
architecture a of e is
begin
  process
    type ptr is access bit_vector;
    variable p : ptr := new bit_vector; -- error
    variable q : ptr := new bit_vector(0 to 3);
    variable r : ptr := new bit_vector'("01");
  begin
    wait;
  end process;
end;
`)
}

// TestPhysicalDefinition: a physical type's range is integer and its
// secondary units are integer multiples (LRM 5.2.4.1).
func TestPhysicalDefinition(t *testing.T) {
	markedErrors(t, "1993", `package p is
  type t1 is range 0.0 to 100.0 -- error
    units a; b = 10 a; end units;
  type t2 is range 0 to 100
    units u; v = 10.1 u; -- error
    w = 10 v; end units;
end;
`)
}

// TestAssociationOrder: positional actuals come before named ones and
// positional aggregate elements before others (LRM 6.5.7.1, 9.3.3.1).
func TestAssociationOrder(t *testing.T) {
	markedErrors(t, "1993", `entity e is end;
architecture a of e is
  function f (x, y, z : integer) return integer is
  begin
    return x;
  end;
  signal i : integer;
  signal b : bit_vector(0 to 3);
begin
  i <= f(1, z => 2, y => 3);
  i <= f(z => 2, y => 3, 1); -- error
  b <= ('1', others => '0');
  b <= (others => '0', '1'); -- error
end;
`)
}

// TestRecordOthers: others in a record aggregate stands for at least
// one element, all of one type (LRM 9.3.3.2).
func TestRecordOthers(t *testing.T) {
	markedErrors(t, "1993", `package p is
  type rec is record a, b : integer; c : real; end record;
  type r2 is record a : integer; b : integer; end record;
  constant k1 : rec := (a => 1, others => 2); -- error
  constant k2 : rec := (1, 2, 3.0, others => 3); -- error
  constant k3 : rec := (c => 1.0, others => 2);
  constant k4 : r2 := (others => 0);
end;
`)
}

// TestConstraintApplies: a range constraint applies to a scalar type,
// an index constraint to an array type (or an access to one) with one
// range per index, and neither to an interface type; a constrained
// array is not constrained again (LRM 5.3.2.2, 6.3).
func TestConstraintApplies(t *testing.T) {
	markedErrors(t, "2008", `package p is
  type a is array (natural range <>) of integer;
  type z is array (natural range <>, natural range <>) of integer;
  type w2 is array (1 to 10) of integer;
  type pa is access a;
  type pw is access w2;
  subtype a1 is a(1 to 8);
  subtype a3 is a(1 to 8, 1 to 8); -- error
  subtype z1 is z(1 to 3); -- error
  subtype z2 is z(1 to 3, 1 to 3);
  subtype pa1 is pa(1 to 2);
  subtype pw1 is pw(1 to 2); -- error
  subtype s1 is integer range 0 to 10;
  subtype s2 is integer(0 to 10); -- error
  subtype s3 is a range 0 to 1; -- error
end;
package g is
  generic (type t);
  subtype mysub is t range 1 to 2; -- error
end;
`)
}

// TestUnconstrainedElement: before VHDL-2008 array and record elements
// are of constrained subtypes (LRM-93 3.2.1); VHDL-2008 lifts it.
func TestUnconstrainedElement(t *testing.T) {
	src := `package p is
  type a is array (natural range <>) of integer;
  type aa is array (natural range <>) of a; -- error93
  type ab is array (natural range <>) of a(0 to 1);
  type r is record x : a; end record; -- error93
end;
`
	markedErrors(t, "2008", strings.ReplaceAll(src, "-- error93", ""))
	markedErrors(t, "1993", strings.ReplaceAll(src, "-- error93",
		"-- error"))
}

// TestProtectedRules: one body per protected type (LRM 5.6.3); a
// shared variable follows its type's body (LRM 14.4.2.5); before
// VHDL-2019 no access type designates a protected type and no method
// takes or returns an access or file (LRM-2008 5.4.1, 5.6.2).
func TestProtectedRules(t *testing.T) {
	src := `package p is
  type cnt is protected
    procedure inc;
  end protected;
  shared variable early : cnt; -- error
  type ptr is access cnt; -- error08 (dropped for 2019)
  type ip is access integer;
  type m is protected
    procedure set (x : inout ip); -- error08
    impure function get return ip; -- error08
    impure function val return integer;
  end protected;
end;
package body p is
  shared variable before : cnt; -- error
  type cnt is protected body
    procedure inc is begin end;
  end protected body;
  type cnt is protected body -- error
  end protected body;
  shared variable later : cnt;
  type m is protected body
    procedure set (x : inout ip) is begin end;
    impure function get return ip is begin return null; end;
    impure function val return integer is begin return 0; end;
  end protected body;
end;
`
	markedErrors(t, "2008", strings.ReplaceAll(src, "-- error08",
		"-- error"))
	// VHDL-2019 allows both; access to protected is not implemented
	// yet (unsupported/construct), so that line is left out.
	src19 := strings.ReplaceAll(src,
		"  type ptr is access cnt; -- error08 (dropped for 2019)\n", "")
	markedErrors(t, "2019", strings.ReplaceAll(src19, "-- error08", ""))
}

// TestUseVisibleHomographs: before VHDL-2008 an explicit operator and
// the implicit homograph of another package, both made visible by use
// clauses, hide each other (LRM-93 10.4), so the call is ambiguous;
// VHDL-2008 lets the explicit one win (LRM 12.4 b). A declaration in
// an enclosing region hides the use-visible one in both.
func TestUseVisibleHomographs(t *testing.T) {
	src := `package p1 is
  type iv is array (integer range <>) of integer;
end package;
use work.p1.all;
package p2 is
  function "=" (x, y : iv) return boolean;
end package;
use work.p1.all;
use work.p2.all;
entity e is end;
architecture a of e is
  signal x, y : iv(3 downto 0);
begin
  assert x = y; -- error93
  b: block
    function "=" (x, y : iv) return boolean is
    begin
      return true;
    end;
  begin
    assert x = y;
  end block;
end;
`
	markedErrors(t, "2008", strings.ReplaceAll(src, "-- error93", ""))
	markedErrors(t, "1993", strings.ReplaceAll(src, "-- error93",
		"-- error"))
}

// TestStandardOpsView: the operations of a std.standard type of a later
// standard are hidden with it: a VHDL-1993 design sees no "&" building
// INTEGER_VECTOR, so its own array of integers is the only reading.
func TestStandardOpsView(t *testing.T) {
	src := `entity e is end;
architecture a of e is
  type iv is array (natural range <>) of integer;
  signal i : integer;
begin
  assert (i & i) = (i & i); -- error08
end;
`
	markedErrors(t, "1993", strings.ReplaceAll(src, "-- error08", ""))
	markedErrors(t, "2008", strings.ReplaceAll(src, "-- error08",
		"-- error"))
}

// TestAmbiguousValue: a parameterless function declared in two
// packages, both use-visible, is ambiguous where both fit the context
// (LRM 12.5); an inner enumeration literal of another type does not
// hide them and is ruled out by the context.
func TestAmbiguousValue(t *testing.T) {
	markedErrors(t, "2008", `package pkg1 is
  function cond return boolean;
  function one return integer;
  function u return real;
end;
package pkg2 is
  function cond return boolean;
  function one return real;
  function u (a, b : real) return real;
end;
use work.pkg1.all;
use work.pkg2.all;
entity e is end;
architecture a of e is
  type enum_t is (cond);
  signal i : integer;
  signal r : real;
begin
  process
  begin
    r <= u;
    if cond then -- error
    end if;
    i <= one;
    wait;
  end process;
end;
`)
}

// TestNotAValue: a type mark is no conversion operand and a range
// attribute no value (LRM 9.1, 16.2); a subtype indication starts with
// a type mark (LRM 6.3); a type mark choice is of the index type.
func TestNotAValue(t *testing.T) {
	markedErrors(t, "1993", `entity e is end;
architecture a of e is
  subtype small is integer range 0 to 7;
  signal s : bit_vector(0 to 3);
  signal i : integer;
  constant c : integer := s'range; -- error
begin
  i <= small(small); -- error
  i <= small(i);
  s <= (bit => '0'); -- error
  s <= (natural range 0 to 3 => '0');
  process
  begin
    for k in 1 range 1 to 2 loop -- error
    end loop;
    for k in s'range loop
    end loop;
    wait;
  end process;
end;
`)
}

// TestAggregateSliceLength: in VHDL-2008 a positional slice gives as
// many elements as its value has, and the aggregate matches the
// length of a constrained context (LRM 9.3.3.3).
func TestAggregateSliceLength(t *testing.T) {
	markedErrors(t, "2008", `entity e is
  port (o : out bit_vector(4 downto 0));
end;
architecture a of e is
  constant c6 : integer_vector(1 to 3) := (1, 2, 3);
  constant c7 : integer_vector(1 to 4) := (c6, 4);
  constant c9 : integer_vector(1 to 5) := (c6, 4); -- error
  constant c13 : integer_vector(1 to 4) :=
    (integer_vector'(1, 2), integer_vector'(3, 4));
begin
  o <= ("01", '0'); -- error
  o <= ("0101", '0');
end;
`)
}
