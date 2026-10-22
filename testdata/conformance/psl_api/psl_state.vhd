-- The std.env PSL API (LRM 16.5): PslAssertFailed, PslIsCovered,
-- PslIsAssertCovered, Set/GetPslCoverAssert and ClearPslState. NVC
-- 1.23 does not implement them (each reports "not implemented"), so
-- there is no NVC recording. An assert is covered once the left side
-- of its implication held; a cover once its sequence matched;
-- SetPslCoverAssert(TRUE) makes PslIsCovered also require every
-- assert covered. ClearPslState starts every directive over (not the
-- VHDL assert counters), so the end-of-run cover report only sees the
-- matches after it. SetVhdlAssertFormat rewrites a PSL violation's
-- message, as in NVC.
library ieee;
use ieee.std_logic_1164.all;
use std.env.all;

entity psl_state_tb is
end entity psl_state_tb;

architecture sim of psl_state_tb is
  signal clk : std_logic := '0';
  signal x   : std_logic := '0';
  signal y   : std_logic := '0';
  default clock is rising_edge(clk);
begin
  clk <= not clk after 5 ns when now < 100 ns;

  x_then_y : assert always (x = '1' -> next y = '1')
    report "y did not follow x" severity warning;
  saw_x : cover {x = '1'};
  saw_xy : cover {x = '1'; y = '1'};

  check : process
    procedure show(what : string) is
    begin
      report what & ": failed " & boolean'image(PslAssertFailed) &
        ", covered " & boolean'image(PslIsCovered) &
        ", asserts covered " & boolean'image(PslIsAssertCovered) &
        ", cover asserts " & boolean'image(GetPslCoverAssert) &
        ", warnings " & integer'image(GetVhdlAssertCount(warning));
    end procedure show;
  begin
    show("start");
    -- x at the 15 ns edge, y at the 25 ns edge: everything covered.
    x <= '1' after 12 ns, '0' after 22 ns;
    y <= '1' after 22 ns, '0' after 32 ns;
    wait for 32 ns;
    show("covered");
    SetPslCoverAssert;
    show("cover asserts");
    ClearPslState;
    show("cleared");
    -- x at the 35 ns edge, y low at 45 ns: x_then_y fails.
    SetVhdlAssertFormat(warning, "[{S}] {r}");
    x <= '1', '0' after 10 ns;
    wait for 20 ns;
    show("failed");
    ClearPslState;
    SetPslCoverAssert(false);
    show("cleared again");
    wait for 20 ns;
    finish;
  end process check;
end architecture sim;
