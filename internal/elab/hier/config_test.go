package hier

import (
	"fmt"
	"strings"
	"testing"
)

// configDesign binds component instances every way LRM 7.3 allows: a
// configuration declaration with component configurations (by label,
// others, all, nested generate block configurations with index
// specifications, incremental binding, open), a configuration
// specification in an architecture, `use configuration`, and the
// default binding, whose component generic defaults reach the entity.
const configDesign = `
entity leaf is
  generic (W : positive := 1; TAG : string := "e");
end;
architecture a1 of leaf is begin end;
architecture a2 of leaf is begin end;

entity other is
  generic (W : positive := 9);
end;
architecture rtl of other is begin end;

entity mid is end;
architecture rtl of mid is
  component leaf is
    generic (W : positive := 5);
  end component;
  for s1 : leaf use entity work.leaf(a1) generic map (W => 20);
begin
  s1: leaf generic map (W => 2);
  s2: leaf;
end;

configuration mid_cfg of mid is
  for rtl
  end for;
end configuration;

entity top is end;
architecture rtl of top is
  component leaf is
    generic (W : positive := 3);
  end component;
  component mid is end component;
begin
  u0: leaf generic map (W => 7);
  u1: leaf;
  u2: leaf;
  u3: leaf;
  g: for i in 0 to 2 generate
    gl: leaf generic map (W => i + 1);
  end generate;
  m: mid;
  direct: entity work.leaf(a2) generic map (TAG => "d");
  viacfg: configuration work.mid_cfg;
end;

configuration top_cfg of top is
  for rtl
    for u0 : leaf use entity work.leaf(a2);
    end for;
    for u1 : leaf use entity work.other;
    end for;
    for u2 : leaf use open;
    end for;
    for others : leaf generic map (W => 40);
    end for;
    for g(1)
      for gl : leaf use entity work.leaf(a2);
      end for;
    end for;
    for g(2 to 2)
      for all : leaf use entity work.other generic map (W => 22);
      end for;
    end for;
    for m : mid use configuration work.mid_cfg;
    end for;
  end for;
end configuration;
`

// TestConfigurations elaborates top through its configuration and
// directly, checking every binding rule.
func TestConfigurations(t *testing.T) {
	r := analyze(t, map[string]string{"c.vhd": configDesign})
	h := Build(r, Options{Tops: []string{"top_cfg"}})
	want := `top entity work.top(rtl) config work.top_cfg
  top.u0 entity work.leaf(a2) w=7 tag="e"
  top.u1 entity work.other(rtl) w=3
  top.u2 unbound component leaf
  top.u3 entity work.leaf(a2) w=40 tag="e"
  top.g(0) for-generate
    top.g(0).gl entity work.leaf(a2) w=1 tag="e"
  top.g(1) for-generate
    top.g(1).gl entity work.leaf(a2) w=2 tag="e"
  top.g(2) for-generate
    top.g(2).gl entity work.other(rtl) w=22
  top.m entity work.mid(rtl) config work.mid_cfg
    top.m.s1 entity work.leaf(a1) w=20 tag="e"
    top.m.s2 entity work.leaf(a2) w=5 tag="e"
  top.direct entity work.leaf(a2) w=1 tag="d"
  top.viacfg entity work.mid(rtl) config work.mid_cfg
    top.viacfg.s1 entity work.leaf(a1) w=20 tag="e"
    top.viacfg.s2 entity work.leaf(a2) w=5 tag="e"
`
	if got := h.Dump(); got != want {
		t.Errorf("configured:\n%s\nwant:\n%s", got, want)
	}
	if len(h.Diagnostics) != 0 {
		t.Errorf("diagnostics: %v", h.Diagnostics)
	}
	// Without the configuration every component instance takes the
	// default binding: the entity of the same name, its last
	// architecture, the component's generic values.
	h = Build(r, Options{Tops: []string{"top"}})
	for _, w := range []string{
		"top.u0 entity work.leaf(a2) w=7",
		"top.u1 entity work.leaf(a2) w=3",
		"top.g(2).gl entity work.leaf(a2) w=3",
		"top.m entity work.mid(rtl)\n",
	} {
		if !strings.Contains(h.Dump(), w) {
			t.Errorf("default binding: missing %q in\n%s", w, h.Dump())
		}
	}
}

// TestInstanceChecks: per-instance errors in declarations, generic
// values and port map actuals name the instance and its generics, and
// errors that do not depend on the instance are left to sema (one
// report per unit, not one per instance).
func TestInstanceChecks(t *testing.T) {
	r := analyze(t, map[string]string{"k.vhd": `
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
entity leaf is
  generic (W : natural := 4);
  port (d : in std_logic_vector(W - 1 downto 0) := (others => '0'));
end;
architecture rtl of leaf is
  constant HALF : natural := W / 2 - 1;
  signal s : unsigned(W - 1 downto 0) := to_unsigned(1, 4);
begin
end;
library ieee;
use ieee.std_logic_1164.all;
entity top is end;
architecture rtl of top is
  signal bus8 : std_logic_vector(7 downto 0);
begin
  ok: entity work.leaf generic map (W => 4) port map (d => bus8(3 downto 0));
  g: for i in 0 to 2 generate
    constant HI : natural := 4 * i + 3;
  begin
    u: entity work.leaf generic map (W => 4)
      port map (d => bus8(HI downto 4 * i));
  end generate;
  small: entity work.leaf generic map (W => 1);
  wide: entity work.leaf generic map (W => 6)
    port map (d => bus8(5 downto 0));
end;
`})
	h := Build(r, Options{Tops: []string{"top"}})
	var got []string
	for _, d := range h.Diagnostics {
		got = append(got, fmt.Sprintf("%d:%d %s %s", d.Line, d.Col,
			d.Code, d.Message))
	}
	want := []string{
		"10:30 sema/static-error constant HALF: value -1 is out of " +
			"range 0 to 2147483647 in top.small (W=1)",
		"11:42 sema/static-error signal s: length mismatch: expected " +
			"1 elements, got 4 in top.small (W=1)",
		"11:42 sema/static-error signal s: length mismatch: expected " +
			"6 elements, got 4 in top.wide (W=6)",
		"25:22 sema/static-error slice 11 downto 8 is out of range " +
			"7 downto 0 in top.g(2).u (W=4)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
}

// TestFunctionGenerics: generic values computed by user and ieee
// functions are known per instance, so generate ranges and widths that
// depend on them are checked.
func TestFunctionGenerics(t *testing.T) {
	r := analyze(t, map[string]string{"f.vhd": `
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
package util is
  function clog2 (n : positive) return natural;
end;
package body util is
  function clog2 (n : positive) return natural is
    variable r : natural := 0;
  begin
    while 2 ** r < n loop
      r := r + 1;
    end loop;
    return r;
  end;
end;
use work.util.all;
library ieee;
use ieee.numeric_std.all;
entity leaf is
  generic (DEPTH : positive := 2; AW : natural := clog2(2));
  port (a : in bit_vector(AW - 1 downto 0));
end;
architecture rtl of leaf is begin end;
use work.util.all;
library ieee;
use ieee.numeric_std.all;
entity top is end;
architecture rtl of top is
  constant SEL : unsigned(3 downto 0) := to_unsigned(9, 4);
  signal a5 : bit_vector(4 downto 0);
begin
  g: for i in 1 to to_integer(SEL(1 downto 0)) generate
    u: entity work.leaf generic map (DEPTH => 2 ** (i + 3),
      AW => clog2(2 ** (i + 3))) port map (a => a5);
  end generate;
end;
`})
	h := Build(r, Options{Tops: []string{"top"}})
	want := `top entity work.top(rtl)
  top.g(1) for-generate
    top.g(1).u entity work.leaf(rtl) depth=16 aw=4
`
	if got := h.Dump(); got != want {
		t.Errorf("dump:\n%s\nwant:\n%s", got, want)
	}
	if len(h.Diagnostics) != 1 || !strings.Contains(
		h.Diagnostics[0].Message, "width mismatch: port a is 4 wide, "+
			"its actual is 5 in top.g(1).u (DEPTH=16, AW=4)") {
		t.Errorf("diagnostics: %v", h.Diagnostics)
	}
}
