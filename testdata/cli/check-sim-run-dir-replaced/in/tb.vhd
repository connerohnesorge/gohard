use std.textio.all;

-- Every run replaces its run directory: what an earlier run with the
-- same identity left in .gohard/run/<hash>/ is gone afterwards.
entity tb is
end entity;

architecture sim of tb is
begin
  process
    file f : text;
    variable l : line;
  begin
    file_open(f, "out/r.txt", write_mode);
    write(l, string'("fresh"));
    writeline(f, l);
    wait;
  end process;
end architecture;
