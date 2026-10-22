use std.textio.all;

-- The run reads a stimulus file; the repro file records its hash, so
-- a replay after the file changed is refused like a changed source.
entity rd is
end entity;

architecture sim of rd is
begin
  process
    file f : text;
    variable l : line;
  begin
    file_open(f, "data/in.txt", read_mode);
    readline(f, l);
    report l.all;
    wait;
  end process;
end architecture;
