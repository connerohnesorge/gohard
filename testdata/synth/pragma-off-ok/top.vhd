library ieee;
use ieee.std_logic_1164.all;
-- pragma translate_off
use std.textio.all;
-- pragma translate_on

entity top is
  port (
    clk : in  std_ulogic;
    d   : in  std_ulogic;
    q   : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  reg : process (clk)
  begin
    if rising_edge(clk) then
      q <= d;
    end if;
  end process;

  -- pragma translate_off
  logger : process (clk)
    file log_f : text open write_mode is "log.txt";
    variable l : line;
  begin
    if rising_edge(clk) then
      write(l, d);
      writeline(log_f, l);
    end if;
  end process;
  -- pragma translate_on
end architecture;
