library ieee;
use ieee.std_logic_1164.all;
use std.textio.all;

entity top is
  port (
    clk : in std_ulogic;
    d   : in std_ulogic
  );
end entity;

architecture rtl of top is
begin
  logger : process (clk)
    file log_f : text open write_mode is "log.txt";
    variable l : line;
  begin
    if rising_edge(clk) then
      write(l, d);
      writeline(log_f, l);
    end if;
  end process;
end architecture;
