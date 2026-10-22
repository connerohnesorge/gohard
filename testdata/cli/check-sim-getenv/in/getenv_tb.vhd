use std.env.all;
use std.textio.all;

entity getenv_sub is
  generic (mode : string := GETENV("MODE"));
end entity;

architecture sim of getenv_sub is
begin
  process
  begin
    report "MODE=" & mode;
    wait;
  end process;
end architecture;

use std.env.all;
use std.textio.all;

entity getenv_tb is
end entity;

architecture sim of getenv_tb is
  constant BAR : string := GETENV("BAR");
  signal foo : string(1 to 3) := GETENV("FOO");
begin
  u : entity work.getenv_sub;

  process
    variable l : line := GETENV("HOME");
  begin
    report "FOO=" & foo & " BAR=" & BAR & " HOME=" & GETENV("HOME") &
      " UNSET=[" & GETENV("GOHARD_UNSET") & "]";
    if l = null then
      report "HOME line is null";
    else
      report "HOME line is " & l.all;
    end if;
    wait;
  end process;
end architecture;
