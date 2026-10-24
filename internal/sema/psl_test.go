package sema

import "testing"

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

// TestVunitBindingScope: an inline vunit bound to e(arch) sees the
// bound architecture and entity declarations.
func TestVunitBindingScope(t *testing.T) {
	checkMarked(t, []markedCase{{name: "psl", std: "2008", src: `
entity dut is
  port (clk : in bit; a, b : in bit);
end entity;
architecture rtl of dut is
  signal s : bit := '0';
begin
end architecture;
vunit v_dut(dut(rtl)) {
  default clock is clk'event and clk = '1';
  assert always (a -> b);
  assert always (s = '0');
  assert always (nope = '0'); --E
}
`}})
}
