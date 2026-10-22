-- GetVhdlAssertCount, IsVhdlAssertFailed, ClearVhdlAssert,
-- Set/GetVhdlAssertEnable and Set/GetVhdlReadSeverity: only enabled
-- assertion violations count; the total is of warning and above.
use std.env.all;

entity asserts_tb is
end entity asserts_tb;

architecture sim of asserts_tb is
begin
  check : process
  begin
    report "count " & integer'image(GetVhdlAssertCount) & ", failed " &
      boolean'image(IsVhdlAssertFailed);
    assert false report "first warning" severity warning;
    assert false report "second warning" severity warning;
    report "count " & integer'image(GetVhdlAssertCount) & ", warnings " &
      integer'image(GetVhdlAssertCount(warning)) & ", errors " &
      integer'image(GetVhdlAssertCount(error)) & ", failed " &
      boolean'image(IsVhdlAssertFailed) & ", warning failed " &
      boolean'image(IsVhdlAssertFailed(warning)) & ", error failed " &
      boolean'image(IsVhdlAssertFailed(error));
    SetVhdlAssertEnable(warning, false);
    report "warning enabled " & boolean'image(GetVhdlAssertEnable(warning)) &
      ", error enabled " & boolean'image(GetVhdlAssertEnable(error));
    assert false report "disabled warning" severity warning;
    report "warnings " & integer'image(GetVhdlAssertCount(warning));
    SetVhdlAssertEnable(true);
    assert false report "third warning" severity warning;
    report "warnings " & integer'image(GetVhdlAssertCount(warning));
    ClearVhdlAssert;
    report "after clear " & integer'image(GetVhdlAssertCount);
    SetVhdlReadSeverity(warning);
    report "read severity " & severity_level'image(GetVhdlReadSeverity);
    wait;
  end process check;
end architecture sim;
