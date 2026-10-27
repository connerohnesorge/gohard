-- psl_comments_tb: drives the clock and stops with std.env.finish at
-- 100 ns. The outcome is decided by the `-- psl` directives in
-- psl_comments.
library ieee;
use ieee.std_logic_1164.all;
use std.env.all;

entity psl_comments_tb is
end entity psl_comments_tb;

architecture sim of psl_comments_tb is
  signal clk : std_logic := '0';
  signal a   : std_logic;
  signal b   : std_logic;
begin
  clk <= not clk after 5 ns;

  dut : entity work.psl_comments
    port map (clk => clk, a => a, b => b);

  stopper : process
  begin
    wait for 100 ns;
    finish;
  end process stopper;
end architecture sim;
