use std.env.all;
use std.textio.all;

-- DIR_CREATEDIR and textio writes after DIR_WORKINGDIR land in the
-- run's .gohard/run/<hash>/ folder, never in the project; a path
-- outside the project is denied.
entity tb is
end entity;

architecture sim of tb is
begin
  process
    file f : text;
    variable l : line;
  begin
    report dir_create_status'image(dir_createdir("out/logs", true));
    report dir_create_status'image(dir_createdir("../escape"));
    report dir_open_status'image(dir_workingdir("out"));
    file_open(f, "logs/run.txt", write_mode);
    write(l, string'("written below .gohard/run"));
    writeline(f, l);
    file_close(f);
    wait;
  end process;
end architecture;
