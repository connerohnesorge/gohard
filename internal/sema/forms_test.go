package sema

import (
	"strings"
	"testing"
)

// TestStatementForms: the forms the parser accepts that the grammar
// excludes: a case without alternatives, an unlabeled block or
// generate, a generic clause after the port clause, an entity
// statement that is not passive in form, a return that does not fit
// its subprogram, an expression where a discrete range belongs, an
// integer literal with a negative exponent, a unary operand of ** and
// a sign after an adding operator, and inertial outside a port map
// (LRM 3.2.1, 3.2.4, 5.3.2.1, 6.5.7.1, 9.1, 10.9, 10.13, 11.2, 11.8,
// 15.5).
func TestStatementForms(t *testing.T) {
	checkMarked(t, []markedCase{{name: "1993", std: "1993", src: `
entity e is
  generic (g : integer := 1);
  port (p : in bit := '0');
  signal err : boolean := false;
begin
  err <= true; --E
  assert true;
end;
architecture a of e is
  component c
    port (p : boolean := true);
    generic (g : integer := 1); --E
  end component;
  type er1 is array(5, 2) of integer; --E
  type ok1 is array (0 to 1) of integer;
  subtype ok2 is bit_vector(ok1'range);
  procedure pr is
  begin
    return 1; --E
  end;
  function f return integer is
  begin
    if g = 1 then
      return; --E
    end if;
    return 2;
  end;
begin
  block begin end block; --E
  lb: block begin end block;
  for i in 1 to 3 generate end generate; --E
  lg: for i in 1 to 3 generate end generate;
  process
    variable x : integer;
  begin
    case x is --E
    end case;
    case x is when others => null; end case;
    for i in 0 loop end loop; --E
    for i in ok1'range loop end loop;
    for i in integer range 0 to 1 loop end loop;
    x := 123e-3; --E
    x := 123e3;
    x := 16#F#e1;
    x := 2 ** abs x; --E
    x := 2 ** (abs x);
    x := x - (-x);
    x := -x + x;
    x := abs x ** 2;
    wait;
  end process;
end;
`}, {name: "2008", std: "2008", src: `
entity f is
  generic (g : integer := 1);
  port (p : in bit := '0');
end;
architecture a of f is
  signal s : bit;
  procedure pr (x : integer) is begin end;
begin
  u1: entity work.f generic map (g => inertial 5) port map (p => s); --E
  u2: entity work.f generic map (g => 5) port map (p => inertial s);
  pr(inertial 1); --E
  process
    variable x : bit;
  begin
    case? x is when others => null; end case; --E
    case? x is when others => null; end case ?;
    case x is when others => null; end case ?; --E
    wait;
  end process;
end;
`}})
}

// TestNamedReturn: return id of T needs VHDL-2019 and is not for an
// interface function (LRM-2019 4.2.1).
func TestNamedReturn(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2008", std: "2008", src: `
package p is
  generic (function func (a : integer) return r of integer); --E
  function g (a : integer) return r of integer; --E
end p;
`}, {name: "2019", std: "2019", src: `
package p is
  generic (function func (a : integer) return r of integer); --E
end p;
`}})
}

// TestPackageBodyAttributes: before VHDL-2008 a package body holds no
// attribute declaration or specification (LRM-2002 2.6).
func TestPackageBodyAttributes(t *testing.T) {
	src := `
package p is
  function foo return integer;
end;
package body p is
  attribute a : integer; --S
  attribute a of foo : function is 1; --S
  function foo return integer is begin return 1; end;
end;
`
	checkMarked(t, []markedCase{
		{name: "1993", std: "1993",
			src: strings.ReplaceAll(src, "--S", "--E")},
		{name: "2008", std: "2008", src: src},
	})
}

// TestDeferredConstantUse: in its package declaration a deferred
// constant is read only by interface default expressions (LRM
// 6.4.2.2).
func TestDeferredConstantUse(t *testing.T) {
	checkMarked(t, []markedCase{{name: "2002", std: "2002", src: `
package defer1 is
  constant c2 : integer;
  constant c3 : integer := c2 + 1; --E
  constant c4 : integer := 4;
  constant c5 : integer := c4 + 1;
  function func2 (x : integer := c2) return integer;
end package;
package body defer1 is
  constant c2 : integer := 2;
  constant c6 : integer := c2 + 1;
  function func2 (x : integer := c2) return integer is
  begin
    return x;
  end;
end package body;
`}})
}

// TestRangeConstraintForm: a range constraint holds a range or a range
// attribute name, not a parenthesized range (LRM 5.2.1).
func TestRangeConstraintForm(t *testing.T) {
	checkMarked(t, []markedCase{{name: "1993", std: "1993", src: `
package p is
  subtype c1 is character range (NUL to DEL); --E
  subtype c2 is character range NUL to DEL;
  subtype i1 is integer range c2'pos(NUL) to 3;
  type a is array (1 to 3) of bit;
  subtype i2 is integer range a'range;
end;
`}})
}

// TestNestedBlockSpecification: the block configuration inside a
// component configuration names the architecture the entity aspect
// binds (LRM 3.4.3).
func TestNestedBlockSpecification(t *testing.T) {
	checkMarked(t, []markedCase{{name: "1993", std: "1993", src: `
entity full_adder is end;
architecture structural of full_adder is begin end;
entity e is end;
architecture arch of e is
  component adder end component;
begin
  a1 : adder;
  a2 : adder;
end;
configuration cfg of e is
  for arch
    for a1 : adder use entity work.full_adder(structural);
      for bad_block_spec --E
      end for;
    end for;
    for a2 : adder use entity work.full_adder(structural);
      for structural
      end for;
    end for;
  end for;
end cfg;
`}})
}

// TestConfigSpecEndFor: end for of a configuration specification is
// VHDL-2008 (LRM-2008 7.3.1).
func TestConfigSpecEndFor(t *testing.T) {
	src := `
entity t is port (i : in bit); end;
architecture a of t is begin end;
entity e is end;
architecture arch of e is
  signal s : bit;
  component t is port (i : in bit); end component;
  for u : t
    use entity work.t;
  end for; --S
begin
  u : t port map (s);
end;
`
	checkMarked(t, []markedCase{
		{name: "1993", std: "1993",
			src: strings.ReplaceAll(src, "--S", "--E")},
		{name: "2008", std: "2008", src: src},
	})
}
