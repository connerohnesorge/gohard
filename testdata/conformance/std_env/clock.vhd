-- nvc-differs: NVC reads the wall clock and the host time zone
-- The clock is simulation.env.epoch (default 2000-01-01T00:00:00Z) plus
-- the simulated time, in UTC: LOCALTIME equals GMTIME (#18).
use std.env.all;

entity clock_tb is
end entity clock_tb;

architecture sim of clock_tb is
begin
  check : process
  begin
    report "GMTIME = " & TO_STRING(GMTIME) & ", LOCALTIME = " &
      TO_STRING(LOCALTIME) & ", EPOCH = " & TO_STRING(EPOCH, 1);
    wait for 500 us;
    report "GMTIME = " & TO_STRING(GMTIME, 6) & ", EPOCH = " &
      TO_STRING(EPOCH, 4);
    report "LOCALTIME(1.0e9) = " & TO_STRING(LOCALTIME(1.0e9)) &
      ", LOCALTIME(GMTIME(1.0e9)) = " &
      TO_STRING(LOCALTIME(GMTIME(1.0e9))) & ", GMTIME(GMTIME(1.0e9)) = " &
      TO_STRING(GMTIME(GMTIME(1.0e9)));
    assert LOCALTIME = GMTIME report "LOCALTIME" severity failure;
    wait;
  end process check;
end architecture sim;
