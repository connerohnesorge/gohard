use std.textio.all;

-- Neither file is ever closed: both are closed implicitly at the end
-- of the run (LRM 5.5.2) and land in .gohard/run/<hash>/.
entity tb is
end entity;

architecture sim of tb is
begin
  process
    file d : text open write_mode is "out/declared.txt";
    file o : text;
    variable l : line;
  begin
    file_open(o, "out/opened.txt", write_mode);
    write(l, string'("declared"));
    writeline(d, l);
    write(l, string'("opened"));
    writeline(o, l);
    wait for 1 ns;
    write(l, string'("later"));
    writeline(d, l);
    wait;
  end process;
end architecture;
