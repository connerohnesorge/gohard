-- budget_hang_tb: never terminates on purpose (#8, #14, #35).
-- The clock toggles every 5 ns forever and nothing calls std.env.stop or
-- finish, so the run only ends when simulation.time_budget (default
-- 1 ms of simulated time) is exhausted: sim/budget-exceeded, exit 1.
-- Each time step has a bounded number of deltas, so neither
-- simulation.delta_budget nor simulation.step_budget is reached first.
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity budget_hang_tb is
end entity budget_hang_tb;

architecture sim of budget_hang_tb is
  signal clk   : std_logic := '0';
  signal rst   : std_logic := '1';
  signal count : unsigned(7 downto 0);
begin
  dut : entity work.budget_hang
    port map (clk => clk, rst => rst, count => count);

  clk <= not clk after 5 ns;
  rst <= '0' after 20 ns;
end architecture sim;
