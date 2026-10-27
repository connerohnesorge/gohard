library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in std_ulogic;
    d   : in bit
  );
end entity;

architecture rtl of top is
  type bit_file is file of bit;
begin
  logger : process (clk)
    file log_f : bit_file open write_mode is "log.bin";
  begin
    if rising_edge(clk) then
      write(log_f, d);
    end if;
  end process;
end architecture;
