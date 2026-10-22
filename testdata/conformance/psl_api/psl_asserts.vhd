-- A PSL assertion is an assertion like a VHDL one (NVC 1.23): a
-- severity disabled with SetVhdlAssertEnable drops its violation (no
-- message, no count); once enabled again it is reported.
library ieee;
use ieee.std_logic_1164.all;
use std.env.all;

entity psl_asserts_tb is
end entity psl_asserts_tb;

architecture sim of psl_asserts_tb is
  signal clk : std_logic := '0';
  signal x   : std_logic := '0';
  default clock is rising_edge(clk);
begin
  clk <= not clk after 5 ns when now < 50 ns;

  x_low : assert always (x = '0') report "x is high";

  check : process
  begin
    SetVhdlAssertEnable(error, false);
    x <= '1' after 12 ns, '0' after 22 ns;
    wait for 32 ns;
    report "disabled: count " & integer'image(GetVhdlAssertCount) &
      ", failed " & boolean'image(IsVhdlAssertFailed(error));
    SetVhdlAssertEnable(error, true);
    report "enabled " & boolean'image(GetVhdlAssertEnable(error));
    x <= '1', '0' after 10 ns;
    wait;
  end process check;
end architecture sim;
