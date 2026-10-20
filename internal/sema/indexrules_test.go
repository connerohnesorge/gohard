package sema

import "testing"

// TestIndexPrefixes: only an array value, or a function call, is
// indexed or sliced: not a label, a design unit, a package or a type
// conversion, and a user-defined attribute is indexed only when its
// value is an array (LRM 8.1, 8.4, 8.5, 9.3.6).
func TestIndexPrefixes(t *testing.T) {
	checkMarked(t, []markedCase{{name: "prefixes", std: "1993", src: `
entity e is end;
architecture rtl of e is
  type t_my_bv is array (natural range <>) of bit;
begin
  ex: block
    constant c0 : bit := e(0); --E
    constant c1 : bit := rtl(0); --E
    constant c3 : bit := std.standard(0); --E
    constant c4 : bit_vector(1 downto 0) := e(1 downto 0); --E
  begin
  end block;
  process
    variable i : integer;
    variable x : bit_vector(1 to 4);
    variable y : t_my_bv(1 to 4);
    attribute a : bit_vector;
    attribute a of i : variable is "101";
    attribute b : integer;
    attribute b of i : variable is 4;
  begin
    lp: for k in x'range loop
      y(1) := lp(k); --E
      y(k) := x(k);
    end loop;
    y(1) := t_my_bv(x)(1); --E
    y := t_my_bv(x)(1 to 4); --E
    y := t_my_bv(x(1 to 4));
    assert i'a(1) = '0';
    assert i'b(1) = 1; --E
    wait;
  end process;
end;
`}})
}

// TestInstantiatedUnitName: the instantiated unit is a simple or
// expanded name of one component, entity or configuration; a
// component redeclaring a port of the entity does not hide the port
// (one declarative region, LRM 12.1), so the instance names the port
// (LRM 11.7.1, 12.3).
func TestInstantiatedUnitName(t *testing.T) {
	checkMarked(t, []markedCase{{name: "units", std: "1993", src: `
entity foo is port (x : in bit); end;
architecture a of foo is begin end;
entity e is port (write : in bit); end;
architecture a of e is
  signal v : bit_vector(1 to 2);
  signal x : bit;
  component write is port (x : in bit); end component; --E
  component foo is port (x : in bit); end component;
begin
  k: v(1) port map (x); --E
  k2: (x, x)
    port map (x); --E
  dut: component write port map (x); --E
  ok: foo port map (x);
  ok2: entity work.foo port map (x);
end;
`}})
}

// TestSliceDirectionBySubtype: a non-null slice runs in the direction
// of its prefix's index range, also when an index constraint names a
// subtype (LRM 8.5).
func TestSliceDirectionBySubtype(t *testing.T) {
	checkMarked(t, []markedCase{{name: "slices", std: "1993", src: `
entity e is end;
architecture a of e is
  type idx is range 1 to 10;
  type aray1 is array (idx range <>) of bit;
begin
  process
    variable v1, v2 : aray1(idx);
    variable v3 : aray1(1 to 5);
  begin
    v2 := v1(10 downto 1); --E
    v2 := v1(1 to 10);
    v3 := v1(1 to 5) & v1(5 downto 6);
    wait;
  end process;
end;
`}})
}

// TestAttributeDimension: the dimension parameter of an array
// attribute is a locally static expression from 1 to the array's
// dimensionality, folded (LRM 16.2.3).
func TestAttributeDimension(t *testing.T) {
	checkMarked(t, []markedCase{{name: "dims", std: "1993", src: `
entity e is end;
architecture a of e is
  type int10_vec is array (1 to 10) of integer;
begin
  process
    variable x : integer := 1;
    variable v : bit_vector(1 to 5);
  begin
    assert v'range(2+0)'left = 5; --E
    assert v'range(1+0)'left = 1;
    assert int10_vec'left(x) = 2; --E
    assert int10_vec'left(1) = 1;
    wait;
  end process;
end;
`}})
}

// TestRangeOperand: a range attribute is no operand (LRM 9.1).
func TestRangeOperand(t *testing.T) {
	for _, std := range []string{"1993", "2008", "2019"} {
		checkMarked(t, []markedCase{{name: std, std: std, src: `
entity e is end;
architecture a of e is
begin
  process
    variable v : bit_vector(1 to 3);
  begin
    assert v'reverse_range = 4; --E
    assert v'length = 3;
    wait;
  end process;
end;
`}})
	}
}

// TestAmbiguousSliceCall: f(1 to 2) slicing the result of a
// parameterless call is ambiguous when two overloads of f can be
// called without actuals (LRM 8.5, 12.5).
func TestAmbiguousSliceCall(t *testing.T) {
	checkMarked(t, []markedCase{{name: "1993", std: "1993", src: `
entity e is end entity;
architecture a of e is
  function f (b : integer := 0) return string is begin return "abc"; end;
  function f return string is begin return "def"; end;
  function g return string is begin return "def"; end;
begin
  assert false report "x: " & f(1 to 2) severity note; --E
  assert false report "x: " & g(1 to 2) severity note;
  assert false report "x: " & f(1) severity note;
end architecture;
`}})
}
