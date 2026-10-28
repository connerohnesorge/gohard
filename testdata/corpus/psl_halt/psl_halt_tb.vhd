-- psl_halt_tb: drives the clock. The PSL assert in psl_halt fails at
-- 65 ns and halts the run, so the report at 80 ns and the finish at
-- 100 ns are never reached.
library ieee;
use ieee.std_logic_1164.all;
use std.env.all;

entity psl_halt_tb is
end entity psl_halt_tb;

architecture sim of psl_halt_tb is
  signal clk : std_logic := '0';
  signal req : std_logic;
  signal ack : std_logic;
begin
  clk <= not clk after 5 ns;

  dut : entity work.psl_halt
    port map (clk => clk, req => req, ack => ack);

  late : process
  begin
    wait for 80 ns;
    report "psl_halt: still running after the failed assert"
      severity error;
    wait for 20 ns;
    finish;
  end process late;
end architecture sim;
