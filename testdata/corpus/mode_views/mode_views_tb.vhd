-- mode_views_tb: self-checking; stops with std.env.finish.
library ieee;
use ieee.std_logic_1164.all;
use std.env.all;
use work.mode_views_pkg.all;

entity mode_views_tb is
end entity mode_views_tb;

architecture sim of mode_views_tb is
  signal clk   : std_logic := '0';
  signal sum   : natural;
  signal count : natural;
  signal done  : boolean := false;
begin
  clk <= not clk after 5 ns when not done;

  dut : entity work.mode_views
    port map (clk => clk, sum => sum, count => count);

  check : process
  begin
    assert C_STD_TAG = "2019"
      report "built-in branch not taken" severity failure;
    assert C_XOR_KEY = x"A5"
      report "MV_PROFILE branch not taken" severity failure;
    wait until count = 4 for 1 us;
    assert count = 4 report "handshake did not complete" severity failure;
    assert sum = 10 report "wrong sum through view port" severity failure;
    done <= true;
    finish;
  end process;
end architecture sim;
