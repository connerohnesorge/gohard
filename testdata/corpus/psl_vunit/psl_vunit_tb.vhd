-- psl_vunit_tb: drives the clock and finishes at 100 ns. The vunit's
-- assert fails at 65 ns; gohard.yaml sets simulation.stop_level:
-- failure, so the run goes on to the finish.
library ieee;
use ieee.std_logic_1164.all;
use std.env.all;

entity psl_vunit_tb is
end entity psl_vunit_tb;

architecture sim of psl_vunit_tb is
  signal clk : std_logic := '0';
  signal req : std_logic;
  signal ack : std_logic;
begin
  clk <= not clk after 5 ns;

  dut : entity work.psl_vunit
    port map (clk => clk, req => req, ack => ack);

  stopper : process
  begin
    wait for 100 ns;
    finish;
  end process stopper;
end architecture sim;
