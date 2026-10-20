package sema

import "testing"

// TestUseLibraryAll: use work.all makes every primary unit of work
// potentially visible (LRM 12.4), so a unit named by a simple name
// through it is analyzed first, whatever the file order; two units
// naming each other through it make no import cycle.
func TestUseLibraryAll(t *testing.T) {
	top := `library ieee;
use ieee.std_logic_1164.all;
library work;
use work.all;
use work.pkg.all;
entity top is
  port (clk : in std_logic);
end entity;
architecture behv of top is
  signal r : rec;
begin
end architecture;
`
	tb := `library ieee;
use ieee.std_logic_1164.all;
library work;
use work.all;
entity tb is
end entity tb;
architecture behv of tb is
  signal clk : std_logic := '0';
begin
  top_i : entity top port map (clk => clk);
end architecture behv;
`
	pkg := `package pkg is
  type rec is record a : integer; end record;
end package;
`
	for _, names := range [][3]string{{"a.vhd", "b.vhd", "c.vhd"},
		{"c.vhd", "b.vhd", "a.vhd"}, {"b.vhd", "a.vhd", "c.vhd"}} {
		noErrors(t, analyze(t, map[string]string{names[0]: tb,
			names[1]: top, names[2]: pkg}, nil))
	}
	noErrors(t, analyze(t, map[string]string{"p.vhd": `use work.all;
package p is
  constant c : integer := 1;
end package;
use work.all;
package q is
  constant d : integer := p.c;
end package;
use work.all;
package body p is
  constant e : integer := q.d;
end package body;
`}, nil))
}
