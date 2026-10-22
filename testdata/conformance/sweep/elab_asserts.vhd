-- Assertion and report statements of the functions elaboration runs
-- (a constant and a signal's initial value, LRM 14.4.2) are reported
-- before any process runs, and counted (GetVhdlAssertCount), as NVC's
-- (init) process does.
use std.env.all;

entity elab_asserts_tb is
end entity elab_asserts_tb;

architecture sim of elab_asserts_tb is
  function f (x : integer) return integer is
  begin
    report "in f " & integer'image(x);
    assert x > 10 report "x too small " & integer'image(x)
      severity warning;
    return x + 1;
  end function f;

  constant c : integer := f(5);
  signal s : integer := f(6);
begin
  process
  begin
    report "c=" & integer'image(c) & " s=" & integer'image(s) &
      " asserts=" & integer'image(GetVhdlAssertCount);
    assert GetVhdlAssertCount = 2 severity failure;
    wait;
  end process;
end architecture sim;
