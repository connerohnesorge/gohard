-- FILE_NAME and FILE_LINE in every return form, in a process and in
-- a procedure. FILE_PATH is the directory of the file (GoHard: project
-- relative, NVC: absolute), so only its use is checked here.
use std.env.all;
use std.textio.all;

entity file_tb is
end entity file_tb;

architecture sim of file_tb is
  procedure where is
    constant here : positive := FILE_LINE;
  begin
    report "procedure: " & FILE_NAME & " line " & FILE_LINE &
      " declared at " & integer'image(here);
  end procedure where;
begin
  check : process
    variable l : line;
    variable n : positive;
  begin
    where;
    l := FILE_NAME;
    report "FILE_NAME line: " & l.all;
    n := FILE_LINE;
    report "FILE_LINE positive: " & integer'image(n);
    l := FILE_PATH;
    assert l.all'length > 0 and l.all = string'(FILE_PATH)
      report "FILE_PATH" severity failure;
    wait;
  end process check;
end architecture sim;
