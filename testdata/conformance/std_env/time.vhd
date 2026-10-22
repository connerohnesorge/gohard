-- The pure TIME_RECORD conversions and operators, TO_STRING and the
-- TIME/REAL conversions, none of which depends on the time zone.
use std.env.all;

entity time_tb is
end entity time_tb;

architecture sim of time_tb is
begin
  check : process
    variable tr : time_record;
    variable r  : real;
  begin
    tr := GMTIME(0.0);
    report "GMTIME(0.0) = " & TO_STRING(tr);
    tr := GMTIME(951782400.25);
    report "leap day = " & TO_STRING(tr) & " " & TO_STRING(tr, 2) & " " &
      TO_STRING(tr, 6);
    report "weekday " & DAYOFWEEK'image(tr.weekday) & ", day of year " &
      integer'image(tr.dayofyear) & ", month " & integer'image(tr.month) &
      ", microsecond " & integer'image(tr.microsecond);
    assert EPOCH(tr) = 951782400.25 report "EPOCH(TREC)" severity failure;
    tr := tr + 86400.5;
    report "plus = " & TO_STRING(tr, 6);
    tr := 3600.0 + tr;
    report "plus = " & TO_STRING(tr, 6);
    tr := tr - 7200.25;
    report "minus = " & TO_STRING(tr, 6);
    r := tr - GMTIME(951782400.0);
    report "difference = " & TO_STRING(r, 2);
    report "TIME_TO_SECONDS(1500 ms) = " &
      TO_STRING(TIME_TO_SECONDS(1500 ms), 1);
    report "SECONDS_TO_TIME(2.5) = " & time'image(SECONDS_TO_TIME(2.5));
    wait;
  end process check;
end architecture sim;
