-- DIR_SEPARATOR, a deferred constant of std.env without a package
-- body, is "/" on every host; paths built with it work in DIR_*.
use std.env.all;

entity dir_separator_tb is
end entity dir_separator_tb;

architecture sim of dir_separator_tb is
begin
  check : process
  begin
    report "DIR_SEPARATOR = """ & DIR_SEPARATOR & """, length " &
      integer'image(DIR_SEPARATOR'length);
    assert dir_createdir("sep_a") = status_ok;
    assert dir_createdir("sep_a" & DIR_SEPARATOR & "b") = status_ok;
    report "sep_a/b is a directory: " &
      boolean'image(dir_itemisdir("sep_a/b"));
    wait;
  end process check;
end architecture sim;
