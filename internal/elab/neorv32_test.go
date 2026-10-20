package elab

import "testing"

// NEORV32's testbench (#19, #25) writes conditions in parentheses: a
// parenthesized expression is the expression it encloses (LRM 9.1),
// in a concurrent or sequential conditional assignment alike.
func TestParenthesizedCondition(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  signal d : std_ulogic := '1';
  signal b : boolean := false;
  signal y, w, v : std_logic;
begin
  w <= '1' when (b) else '0';
  v <= '0' when ((d = '0')) else 'Z';
  p : process
    variable l : line;
  begin
    y <= '0' when (d = '0') else 'Z';
    wait for 1 ns;
    write(l, std_logic'image(y) & std_logic'image(w) &
      std_logic'image(v));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
	r.wantOut(t, "'Z''0''Z'")
}

// A port associated subelement by subelement (LRM 6.5.7.1), as
// NEORV32's slink FIFO is, shares each actual's net elements; a type
// conversion actual that changes only the subtype (std_logic of a
// std_ulogic) shares its operand's.
func TestPartialPortAssociation(t *testing.T) {
	src := prelude + `
entity sub is
  port (a : in std_logic_vector(5 downto 0); c : in std_logic;
        y : out std_ulogic_vector(5 downto 0));
end entity;
architecture r of sub is
begin
  y <= std_ulogic_vector(a) when c = '1' else (others => '0');
end architecture;

library ieee;
use ieee.std_logic_1164.all;
use std.textio.all;
entity tb is end entity;
architecture a of tb is
  type rec is record
    d : std_ulogic_vector(3 downto 0);
    l, m : std_ulogic;
  end record;
  signal r_in, r_out : rec := ((others => '0'), '0', '0');
  signal clk : std_ulogic := '0';
begin
  u : entity work.sub port map (
    a(3 downto 0) => r_in.d, a(4) => r_in.l, a(5) => r_in.m,
    c => std_logic(clk),
    y(5) => r_out.l, y(4) => r_out.m, y(3 downto 0) => r_out.d);
  p : process
    variable l : line;
  begin
    r_in <= ("1010", '1', '0');
    wait for 1 ns;
    write(l, to_string(r_out.d) & std_ulogic'image(r_out.l) &
      std_ulogic'image(r_out.m));
    writeline(output, l);
    clk <= '1';
    wait for 1 ns;
    write(l, to_string(r_out.d) & std_ulogic'image(r_out.l) &
      std_ulogic'image(r_out.m));
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
	r.wantOut(t, "0000'0''0'", "1010'0''1'")
}

// The logical name of a file declaration is any string expression
// (LRM 6.4.2.5), such as NEORV32's sim_uart_rx NAME & ".log".
func TestFileNameExpression(t *testing.T) {
	src := prelude + `
entity tb is generic (NAME : string := "uart0"); end entity;
architecture a of tb is
begin
  p : process
    file f : text open write_mode is NAME & ".log";
    variable l : line;
  begin
    write(l, string'("hi"));
    writeline(f, l);
    file_close(f);
    file_open(f, "uart0.log", read_mode);
    readline(f, l);
    writeline(output, l);
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
	r.wantOut(t, "hi")
}
