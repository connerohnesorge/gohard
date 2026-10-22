-- nvc-differs: NVC's GetVhdlAssertFormat default
-- SetVhdlAssertFormat changes the diagnostic text only (the trace
-- keeps the raw message); an invalid format sets Valid false.
use std.env.all;

entity assert_format_tb is
end entity assert_format_tb;

architecture sim of assert_format_tb is
begin
  check : process
    variable ok : boolean;
  begin
    report "read severity " & severity_level'image(GetVhdlReadSeverity) &
      ", format """ & GetVhdlAssertFormat(warning) & """";
    SetVhdlAssertFormat(warning, "[{S}] {r}", ok);
    report "valid " & boolean'image(ok) & ", format """ &
      GetVhdlAssertFormat(warning) & """";
    assert false report "formatted" severity warning;
    SetVhdlAssertFormat(warning, "{q}", ok);
    report "valid " & boolean'image(ok) & ", format """ &
      GetVhdlAssertFormat(warning) & """";
    SetVhdlAssertFormat(warning, "");
    assert false report "plain" severity warning;
    wait;
  end process check;
end architecture sim;
