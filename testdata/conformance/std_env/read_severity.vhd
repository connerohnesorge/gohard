-- A textio READ without GOOD that fails is an assertion violation of
-- the GetVhdlReadSeverity level (error by default, as in NVC), and the
-- line is left as it was (LRM 16.4, 16.5.4). The value of a scalar
-- after a failed read is not checked: NVC's READ body zeroes it.
use std.textio.all;
use std.env.all;

entity read_severity_tb is
end entity read_severity_tb;

architecture sim of read_severity_tb is
begin
  process
    variable l : line;
    variable i : integer := 7;
    variable b : bit_vector(3 downto 0) := "0110";
  begin
    report "default " & severity_level'image(GetVhdlReadSeverity);
    SetVhdlReadSeverity(warning);
    l := new string'("abc 12");
    read(l, i);
    report "line """ & l.all & """";
    read(l, b);
    report "b " & to_string(b) & " line """ & l.all & """";
    SetVhdlReadSeverity(note);
    l := new string'("x");
    read(l, i);
    report "warnings " & integer'image(GetVhdlAssertCount(warning));
    wait;
  end process;
end architecture sim;
