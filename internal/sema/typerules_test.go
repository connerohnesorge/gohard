package sema

import "testing"

// TestTypeRules: protected types are for variables only, no default
// for a parameter of one, and before VHDL-2019 no function returns one
// (LRM 4.2.1, 6.4.2, 6.5.2); an access type designates no file type
// and no constrained incomplete type (LRM 5.4.1, 5.4.2); a resolution
// function takes an array of the resolved type and returns it (LRM
// 4.6); an interface type is a generic only (LRM 6.5.1).
func TestTypeRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2002", std: "2002", src: `
package p is
  type sc is protected
    procedure inc;
  end protected;
  constant c : sc; --E
  function make return sc; --E
  procedure proc (variable s : inout sc);
  procedure proc2 (variable s : inout sc := make); --E
  type ft is file of integer;
  type a1 is access ft; --E
  type mvl is ('0', '1', 'Z');
  type mvl_vec is array (positive range <>) of mvl;
  function tri (x : mvl_vec) return mvl;
  subtype rmvl is tri mvl;
  type t1 is access mvl;
  subtype st1 is tri t1; --E
  type inc;
  type t4 is access inc;
  type t5 is access inc(1 to 2); --E
  type inc is array (natural range <>) of bit;
end package;
`}, {name: "2008", std: "2008", src: `
entity e is end;
architecture a of e is
  function test1 generic (type t) (x : t) return bit;
  function test2 (type t) return bit; --E
begin
end;
`}, {name: "2019", std: "2019", src: `
entity f is
  generic (type t7 is access type is private;
           type t8 is file of type is private);
end;
architecture a of f is
  signal s7 : t7; --E
  signal s8 : t8; --E
begin
end;
`}})
}

// TestMissingGenericActuals: a package, subprogram or component
// instance gives an actual for every generic without a default: a
// constant without a default expression, a type, a subprogram without
// is <> or is name, a package (LRM 6.5.6.2).
func TestMissingGenericActuals(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
package gp is
  generic (g : integer; h : integer := 2);
end package;
package gt is
  generic (type t; function plus (a : t) return t is <>;
           function minus (a : t) return t);
end package;
entity e is end;
architecture a of e is
  function plus (a : integer) return integer is begin return a; end;
  function adder generic (type t; n : t) (x : t) return t is
  begin
    return x;
  end function;
  package p1 is new work.gp generic map (g => 1);
  package p2 is new work.gp; --E
  package p3 is new work.gp generic map (h => 3); --E
  package p4 is new work.gt generic map (t => integer, minus => plus);
  package p5 is new work.gt generic map (t => integer); --E
  function add1 is new adder generic map (t => integer, n => 1);
  function add2 is new adder generic map (t => integer); --E
  component c is generic (type t); end component;
begin
  u1: c generic map (t => bit);
  u2: c; --E
end;
`}})
}

// TestRecordChoices: a record aggregate's choices are element simple
// names or others (LRM 9.3.3.2).
func TestRecordChoices(t *testing.T) {
	checkMarked(t, []markedCase{{name: "records", std: "1993", src: `
entity e is end;
architecture a of e is
  type r is record
    ele_1, ele_2 : integer;
  end record;
begin
  process
    variable v : r;
  begin
    v := (1 + 1 => 20, ele_2 => 0); --E
    v := (ele_1 => 20, ele_2 => 0);
    v := (ele_1 | ele_2 => 0);
    v := (others => 0);
    wait;
  end process;
end;
`}})
}

// TestMissingProtectedBody: a protected type declared in an
// architecture (or in a package that has a body) has its body there
// (LRM 5.6.1, 5.6.3); a package without a body leaves it to
// elaboration.
func TestMissingProtectedBody(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2002", std: "2002", src: `
package nobody is
  type pt is protected
    procedure inc;
  end protected;
end package;
entity e is end;
architecture a of e is
  type pt is protected --E
    function get return integer;
  end protected;
  type qt is protected
    function get return integer;
  end protected;
  type qt is protected body
    function get return integer is begin return 1; end;
  end protected body;
begin
end;
`}})
}

// TestGenericTypeActuals: in an instance a generic type denotes its
// actual, so a later generic constant or a port of that type takes
// actuals of the actual type (LRM 6.5.7.2).
func TestGenericTypeActuals(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
entity sub is
  generic (type data_type);
  port (a : in data_type);
end;
architecture x of sub is begin end;
entity top is end;
architecture y of top is
  function adder generic (type t; n : t) (x : t) return t is
  begin
    return x;
  end function;
  function add1 is new adder generic map (t => integer, n => 1);
  function add2 is new adder generic map (t => string, n => 1); --E
  component sub is
    generic (type data_type);
    port (a : in data_type);
  end component;
  signal b1 : bit;
  signal i1 : integer;
begin
  u1: sub generic map (data_type => integer)
    port map (
      a => b1 --E
    );
  u2: sub generic map (data_type => integer) port map (a => i1);
  u3: entity work.sub generic map (data_type => bit) port map (a => b1);
end;
`}})
}

// TestGenericPackageTypeActuals: a port of type p.t, with p a generic
// package, takes the type its actual package instance gives t (LRM
// 6.5.7.2).
func TestGenericPackageTypeActuals(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
package adder is
  generic (type t);
end package;
entity top is end;
architecture y of top is
  component sub is
    generic (package p is new work.adder generic map (<>));
    port (x : in p.t; z : out p.t);
  end component;
  signal xr, zr : real;
  signal xi : integer;
  package real_adder is new work.adder generic map (real);
begin
  u1: sub generic map (real_adder) port map (xr, zr);
  u2: sub generic map (real_adder) port map (xi, open); --E
end;
`}})
}

// TestFormalConversions: T(x) => actual with T a visible type mark and
// x a formal converts the formal, so the actual is of type T (LRM
// 6.5.7.1); a conversion actual of an unconstrained port has a
// constrained type mark (LRM 6.5.6.3).
func TestFormalConversions(t *testing.T) {
	checkMarked(t, []markedCase{{name: "1993", std: "1993", src: `
entity x is
  port (t : out bit_vector(0 to 0); z : out bit_vector(0 to 0));
end entity;
architecture a of x is begin end architecture;
entity y is
  port (foo : in bit_vector);
end entity;
architecture a of y is begin end architecture;
entity w is
  port (r : out bit_vector(0 to 1); q : inout bit_vector(0 to 1));
end entity;
architecture a of w is begin end architecture;
entity e is end entity;
architecture a of e is
  subtype t is bit_vector(0 to 0);
  signal s1, s2 : bit_vector(0 to 0);
  subtype b8 is bit_vector(7 downto 0);
  signal actual_for_t : bit;
  signal actual_for_z : t;
  signal bar : bit_vector(7 downto 0);
  signal i : integer;
begin
  i1: entity work.x port map (
    t(z) => actual_for_t, --E
    t => actual_for_z);
  i5: entity work.x port map (
    t(z) => actual_for_z,
    t(z) => actual_for_z, --E
    t => actual_for_z);
  i6: entity work.w port map (
    bit_vector(r(0 to 0)) => s1,
    bit_vector(r(1 to 1)) => s2,
    q(0) => s1(0),
    q(1) => s2(0));
  b1: block is
    port (p : inout real);
    port map (integer(p) => real(i));
  begin
  end block;
  i2: entity work.y port map (foo => bit_vector(bar)); --E
  i3: entity work.y port map (foo => b8(bar));
  i4: entity work.y port map (foo => bar);
end architecture;
`}})
}

// TestInstanceElementConstraint: an array or record type of a package
// instance whose element is a generic type with an unconstrained
// actual (string) makes an unconstrained object subtype (LRM 6.4.2.3).
func TestInstanceElementConstraint(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
package pkg_arr is
  generic (type g_type);
  type t_arr is array (natural range <>) of g_type;
  type t_rec is record
    el : g_type;
  end record;
end;
entity tst is end;
architecture sim of tst is
  package sp is new work.pkg_arr generic map (g_type => string);
  package ip is new work.pkg_arr generic map (g_type => integer);
  signal s1 : sp.t_arr(1 to 2); --E
  signal s2 : sp.t_rec; --E
  signal s3 : ip.t_arr(1 to 2);
  signal s4 : ip.t_rec;
begin
end architecture sim;
`}})
}

// TestGenericTypeEquality: the actual of a generic type has = and /=,
// so a file or protected type is no actual (LRM 6.5.3).
func TestGenericTypeEquality(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
package generic_pkg is
  generic (type g_type);
end package;
use std.textio.all;
entity e is end entity;
architecture arch of e is
  package p1 is new work.generic_pkg generic map (g_type => text); --E
  package p2 is new work.generic_pkg generic map (g_type => line);
begin
end architecture;
`}})
}

// TestInterfacePackageDefault: generic map (default) of an interface
// package needs a default for each generic (LRM 6.5.5).
func TestInterfacePackageDefault(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
package pack1 is
  generic (x : integer := 5; y : integer := 2);
end package;
package pack2 is
  generic (x : integer; y : integer := 2);
end package;
package pack3 is
  component comp3 is
    generic (package p is new work.pack1 generic map (default));
  end component;
  component comp4 is
    generic (package p is new work.pack2 generic map (default)); --E
  end component;
end package;
`}})
}

// TestAggregateElementLengths: the elements of an array aggregate
// whose element subtype is unconstrained have one length (LRM
// 9.3.3.3).
func TestAggregateElementLengths(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
package p is
  type my_vector is array (natural range <>) of integer;
  type my_vv is array (natural range <>) of my_vector;
  constant c_1 : my_vector(1 downto 0) := (others => 1);
  constant c_2 : my_vector(2 downto 0) := (others => 2);
  constant c_3 : my_vector(3 downto 2) := (others => 3);
  constant c_foo : my_vv(1 downto 0) := (
    0 => c_1,
    1 => c_2 --E
  );
  constant c_ok : my_vv(1 downto 0) := (0 => c_1, 1 => c_3);
end;
`}})
}
