-- assert_fail_tb: fails on purpose (#8, #20, #35).
-- Emits exactly one message per severity, at fixed times:
--   10 ns  report, severity note    -> hint
--   20 ns  assert, severity warning -> warning
--   30 ns  assert, severity error   -> error; the run halts here under
--          the default simulation.stop_level (error), exit 1
-- Nothing is scheduled after 30 ns, so tools that do not halt on error
-- reach the end of events at 30 ns as well. The failure severity is
-- never used, so every tool ends at the same time with the same output.
-- The warning and error texts embed the observed sum, so the recorded
-- messages pin the value the DUT computed, not just the check firing.
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity assert_fail_tb is
end entity assert_fail_tb;

architecture sim of assert_fail_tb is
  signal a : unsigned(3 downto 0) := (others => '0');
  signal b : unsigned(3 downto 0) := (others => '0');
  signal s : unsigned(3 downto 0);
begin
  dut : entity work.assert_fail
    port map (a => a, b => b, s => s);

  check : process
  begin
    a <= to_unsigned(3, 4);
    b <= to_unsigned(4, 4);
    wait for 10 ns;
    report "assert_fail: starting checks";
    a <= to_unsigned(9, 4);
    b <= to_unsigned(9, 4);
    wait for 10 ns;
    -- 9 + 9 wraps to 2 in 4 bits, so s > a is false.
    assert s > a
      report "assert_fail: sum wrapped to " & integer'image(to_integer(s))
      severity warning;
    a <= to_unsigned(5, 4);
    b <= to_unsigned(6, 4);
    wait for 10 ns;
    assert s = to_unsigned(12, 4)
      report "assert_fail: expected 12, got " & integer'image(to_integer(s))
      severity error;
    wait;
  end process check;
end architecture sim;
