-- The implicit TO_STRING of the enumeration types std.env declares
-- (DAYOFWEEK, DIR_OPEN_STATUS, DIR_CREATE_STATUS, DIR_DELETE_STATUS,
-- FILE_DELETE_STATUS) is that of any enumeration type (LRM 5.7).
use std.env.all;

entity enum_to_string_tb is
end entity enum_to_string_tb;

architecture sim of enum_to_string_tb is
begin
  process
    variable d  : dayofweek := thursday;
    variable o  : dir_open_status := status_not_found;
    variable c  : dir_create_status;
    variable dl : dir_delete_status := status_access_denied;
    variable f  : file_delete_status := status_ok;
  begin
    report "day " & to_string(d) & " " & to_string(dayofweek'succ(d));
    report "open " & to_string(o) & " create " & to_string(c);
    report "delete " & to_string(dl) & " file " & to_string(f);
    assert to_string(d) = "thursday" severity failure;
    assert to_string(o) = dir_open_status'image(o) severity failure;
    wait;
  end process;
end architecture sim;
