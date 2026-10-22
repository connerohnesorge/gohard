-- 64-bit INTEGER in VHDL-2019: textio READ and WRITE of a value that
-- needs 41 bits.
use std.textio.all;

entity textio_tb is
end entity textio_tb;

architecture sim of textio_tb is
begin
  p : process
    variable l : line;
    variable v : integer;
    variable ok : boolean;
  begin
    l := new string'("1099511627776 -1099511627777");
    read(l, v, ok);
    assert ok report "read failed" severity error;
    report "read " & integer'image(v);
    assert v = 2 ** 40 report "v" severity error;
    read(l, v);
    report "read " & integer'image(v);
    deallocate(l);
    write(l, v * 2);
    report "write " & l.all;
    assert l.all = "-2199023255554" report "write" severity error;
    wait;
  end process p;
end architecture sim;
