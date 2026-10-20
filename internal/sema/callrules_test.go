package sema

import "testing"

// TestCallRules: an open actual needs a formal of mode in with a
// default, a formal is named once, a variable formal takes no file
// (LRM 4.2.2, 6.5.7.1), and a real literal divided by a literal zero
// has no REAL value (LRM 9.4.1); an integer division by zero stays a
// run-time error.
func TestCallRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "calls", std: "1993", src: `
entity e is end;
architecture a of e is
  type ft is file of integer;
  file f : ft;
  signal s : integer;
  function g(x : integer; y : integer := 0) return integer is
  begin
    return x + y;
  end;
  procedure ps(signal a : in integer) is begin end;
  procedure pv(variable v : inout ft) is begin end;  --E
begin
  process
    variable r : real;
    variable i : integer;
  begin
    i := g(1, y => open);
    i := g(x => open);                --E
    i := g(x => 1, x => 2);           --E
    ps(a => open);                    --E
    ps(a => s);
    pv(f);                            --E
    r := 1.0 / 0.0;                   --E
    r := 1.0 / 2.0;
    i := 1 / 0;
    wait;
  end process;
end;
`}})
}

// TestMapRules: formal designators are names (LRM 6.5.7.1); an
// indexed formal's literal index lies in its range, a sliced formal
// and a whole constrained formal have their actual's length, a literal
// scalar actual lies in its formal's range, no subelement is open
// (unless lenient) and a binding's generic map gives every generic
// without default (LRM 6.5.6.2, 6.5.6.3, 7.3.2.2). Block maps follow
// the same rules.
func TestMapRules(t *testing.T) {
	src := `
entity sub is
  generic (g : integer range 0 to 3 := 0);
  port (x : in bit_vector(3 downto 0) := "0000";
    y : out bit_vector(1 downto 0));
end;
architecture a of sub is begin end;
entity top is end;
architecture a of top is
  signal a4 : bit_vector(3 downto 0);
  signal a3 : bit_vector(2 downto 0);
  signal b2 : bit_vector(1 downto 0);
begin
  u1: entity work.sub generic map (g => 7)          --E
    port map (x => a4, y => b2);
  u2: entity work.sub port map (x => a3, y => b2);  --E
  u3: entity work.sub port map (x(-1) => a4(0),     --E
    x(3 downto 1) => a3, y => b2);
  u4: entity work.sub port map (x(0) => a4(0),
    x(3 downto 1) => a4, y => b2);                   --E
  u5: entity work.sub port map (x(0) => open,       %s
    x(3 downto 1) => a3, y => b2);
  u6: entity work.sub port map (6 => a4, y => b2);  --E
  b: block is
    port (p : in bit_vector(1 to 3));
    port map (p => a4);                              --E
  begin
  end block;
end;
`
	checkMarked(t, []markedCase{
		{name: "strict", src: sprintf2(src, "--E", "")},
		{name: "lenient", lenient: true, src: sprintf2(src, "", "")},
	})
	checkMarked(t, []markedCase{{name: "binding", src: `
entity leaf is
  generic (n : integer; m : integer := 1);
end;
architecture a of leaf is begin end;
entity top is end;
architecture a of top is
  component leaf is
    generic (n : integer := 2);
  end component;
  for u1 : leaf use entity work.leaf;
  for u2 : leaf use entity work.leaf   --E
    generic map (m => 3);
  for u3 : leaf use entity work.leaf generic map (n => 3);
begin
  u1: leaf;
  u2: leaf;
  u3: leaf;
end;
`}})
}

// TestValueRules: static values the tools check at analysis: a case
// choice outside the selector's subtype, natural's bounds included
// (LRM 10.9), string choices of one length, a physical literal beyond
// TIME and waveform times in order (LRM 10.5.2.1), a loop bound that
// overflows INTEGER (LRM 9.4.1), a unit name is not locally static,
// targets name objects and aggregate target elements are signals for a
// signal assignment (LRM 10.5, 10.6), and port actuals are static
// names (LRM 6.5.6.3).
func TestValueRules(t *testing.T) {
	checkMarked(t, []markedCase{{name: "values", src: `
entity sub is
  port (i : in bit);
end;
architecture a of sub is begin end;
entity e is end;
architecture a of e is
  signal s, t : bit;
  signal v : bit_vector(0 to 3);
  type t3 is range 1 to (ms / ns);              --E
begin
  s <= '0', '1' after 9223372036854775802 ns;   --E
  t <= '0', '1' after 2 ns, '0' after 1 ns;     --E
  g: for k in 0 to 3 generate
    u: entity work.sub port map (i => v(k));
  end generate;
  process
    variable n : natural;
    variable x : bit;
    variable str : string(1 to 3);
  begin
    case n is
      when -1 to 5 => null;                     --E
      when others => null;
    end case;
    case str is
      when "abc" => null;
      when "ab" => null;                        --E
      when others => null;
    end case;
    for i in integer'high to integer'high + 1 loop   --E
    end loop;
    for i in integer'high - 1 to integer'high loop
    end loop;
    sec := 1;                                    --E
    (x, s) <= bit_vector'("01");                 --E
    for j in 0 to 3 loop
      x := v(j);
    end loop;
    wait;
  end process;
end;
`}})
}

// TestPureExternalName: a pure function names no external signal or
// variable; an impure one may (LRM 4.1).
func TestPureExternalName(t *testing.T) {
	checkMarked(t, []markedCase{{name: "ext", src: `
entity e is end;
architecture a of e is
  signal cnt : integer;
  function f return integer is
  begin
    return <<signal .e.cnt : integer>>;    --E
  end;
  impure function g return integer is
  begin
    return <<signal .e.cnt : integer>>;
  end;
begin
end;
`}})
}

// TestUnknownFormalAt: a named actual for no parameter of the only
// subprogram called is reported at the formal (LRM 6.5.7.1).
func TestUnknownFormalAt(t *testing.T) {
	checkMarked(t, []markedCase{{name: "formals", std: "1993", src: `
package p is end p;
package body p is
  procedure mask (variable corrupt : out integer) is
  begin
  end;
  procedure lookup (variable dc : out integer; constant ei : in integer) is
  begin
    mask ( --E
      corrupt => dc,
      ei => ei --E
    );
    mask(corrupt => dc);
  end;
end package body p;
`}})
}
