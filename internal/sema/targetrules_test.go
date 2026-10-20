package sema

import (
	"strings"
	"testing"
)

// TestTargetRules: an attribute or a function call is no assignment
// target (LRM 10.5.2.1, 10.6.2.1); a subprogram declared outside a
// process drives only its own signal parameters (LRM 10.5.2.2,
// 4.2.2.2); 'STABLE and the other implicit signals of a signal
// parameter cannot be read in a subprogram (LRM 4.2.2.2).
func TestTargetRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "targets", std: "2008", src: `
package pack is
  procedure do_something;
  signal s : bit;
end package;
package body pack is
  procedure drive_out (signal p : out bit) is
  begin
    p <= '1';
  end procedure;
  procedure outer (signal q : inout bit) is
    procedure inner is
    begin
      q <= '0';
    end procedure;
  begin
    drive_out(q);
    if q = '1' and not q'stable then --E
      q <= '1';
    end if;
  end procedure;
  procedure do_something is
  begin
    s <= '0'; --E
    s <= force '1';
    s <= release;
    drive_out(s); --E
  end procedure;
end package body;
use work.pack.all;
entity e is end;
architecture a of e is
  signal s1, s2 : bit;
  procedure drive2 (signal p : out bit) is
  begin
    p <= '1';
  end procedure;
  function check (x : integer) return integer is
  begin
    return 10 * x;
  end;
begin
  process
    variable k : integer;
    procedure local is
    begin
      s1 <= '1';
      drive2(s2);
    end procedure;
  begin
    s1'stable <= s2; --E
    check(k) := check(k) + 24; --E
    s1 <= s2;
    wait;
  end process;
end;
`}})
}

// TestBlockMapActuals: the actuals of a block header's maps are names
// of the enclosing region; the block's own generics and ports are not
// visible there (LRM 12.2), so (c => c) needs an outer c and
// (clk => clk) maps the outer signal.
func TestBlockMapActuals(t *testing.T) {
	checkMarked(t, []markedCase{{name: "blocks", std: "1993", src: `
entity tb is end;
architecture behav of tb is
  signal clk : bit;
  constant w : natural := 4;
begin
  b : block
    generic (c : natural);
    generic map (c => c); --E
  begin
  end block b;
  b2 : block
    generic (w : natural);
    generic map (w => w);
    port (clk : in bit; d : in bit_vector(w - 1 downto 0));
    port map (clk => clk, d => (others => '0'));
  begin
    assert clk = '0';
  end block b2;
end behav;
`}})
}

// TestAmbiguousDerefTarget: f.all as a target, with f two
// parameterless functions returning access values, is ambiguous: the
// target gives no type to choose by (LRM 12.5).
func TestAmbiguousDerefTarget(t *testing.T) {
	checkMarked(t, []markedCase{{name: "deref", std: "2008", src: `
entity e is end;
architecture a of e is
  type str_acc is access string;
  type bv_acc is access bit_vector;
  function f return str_acc is begin return new string'("abc"); end f;
  function f return bv_acc is begin return new bit_vector'("001"); end f;
  function g return bv_acc is begin return new bit_vector'("001"); end g;
begin
  process
  begin
    f.all := "010"; --E
    g.all := "010";
    wait;
  end process;
end;
`}})
}

// TestAggregateTargetSlices: before VHDL-2008 the elements of an
// aggregate target are of the element type, so a slice is no element
// (LRM-2002 8.5); VHDL-2008 allows it.
func TestAggregateTargetSlices(t *testing.T) {
	src := `
entity e is end;
architecture a of e is
  subtype bvi is bit_vector(0 to 7);
  signal s : bvi;
  subtype b2 is bit_vector(1 to 2);
  type bv2d is array (1 to 2) of b2;
  signal bv : bit_vector(1 to 3);
  signal bv2 : b2;
begin
  process
  begin
    (s(0 to 5), s(6), s(7 to 7)) <= bvi'(others => '0'); --S
    (s(0), s(1)) <= bit_vector'("01");
    (bv(1 to 2), bv2) <= bv2d'("10", "01");
    wait;
  end process;
end;
`
	checkMarked(t, []markedCase{
		{name: "1993", std: "1993",
			src: strings.ReplaceAll(src, "--S", "--E")},
		{name: "2008", std: "2008", src: src},
	})
}
