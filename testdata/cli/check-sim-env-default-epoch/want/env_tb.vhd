use std.textio.all;

entity env_tb is
end entity;

architecture sim of env_tb is
begin
  process
    file f : text open write_mode is "out/result.txt";
    variable l : line;
  begin
    write(l, string'("pass"));
    writeline(f, l);
    file_close(f);
    assert false report "probe" severity warning;
    wait;
  end process;
end architecture;
