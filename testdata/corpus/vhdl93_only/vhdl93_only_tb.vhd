-- vhdl93_only_tb: self-checking VHDL-1993 testbench (#8).
-- std.env does not exist in VHDL-1993, so the run stops at the end of
-- events: the stimulus process ends in a bare wait. vhdl93_only_count
-- counts the rising edges of force in a non-protected shared variable
-- (#63).
library ieee;
use ieee.std_logic_1164.all;

entity vhdl93_only_tb is
end vhdl93_only_tb;

architecture sim of vhdl93_only_tb is
  signal force   : std_logic := '0';
  signal release : std_logic := '0';
  signal context : std_logic;
  signal count   : natural;
begin
  dut : entity work.vhdl93_only
    port map (force => force, release => release, context => context);

  cnt : entity work.vhdl93_only_count
    port map (tick => force, count => count);

  check : process
  begin
    wait for 10 ns;
    assert context = '0' report "latch not clear" severity failure;
    force <= '1';
    wait for 10 ns;
    force <= '0';
    wait for 10 ns;
    assert context = '1' report "latch did not set" severity failure;
    release <= '1';
    force <= '1';
    wait for 10 ns;
    assert context = '0' report "release did not win" severity failure;
    release <= '0';
    force <= '0';
    wait for 10 ns;
    assert context = '0' report "latch did not hold" severity failure;
    assert count = 2 report "shared variable count" severity failure;
    wait;
  end process check;
end sim;
