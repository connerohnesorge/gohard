-- nvc-differs: NVC 1.23 counts report statements in GetVhdlAssertCount
-- Report statements are not assertion violations: GetVhdlAssertCount
-- counts only failed asserts (#18 Semantics decided for v1.1), at
-- every severity, note included.
use std.env.all;

entity report_counts_tb is
end entity report_counts_tb;

architecture sim of report_counts_tb is
begin
  check : process
  begin
    report "first report";
    report "second report" severity warning;
    report "notes " & integer'image(GetVhdlAssertCount(note)) &
      ", warnings " & integer'image(GetVhdlAssertCount(warning)) &
      ", all " & integer'image(GetVhdlAssertCount);
    assert false report "a failed note assert" severity note;
    assert false report "a failed warning assert" severity warning;
    report "notes " & integer'image(GetVhdlAssertCount(note)) &
      ", warnings " & integer'image(GetVhdlAssertCount(warning)) &
      ", all " & integer'image(GetVhdlAssertCount);
    wait;
  end process check;
end architecture sim;
