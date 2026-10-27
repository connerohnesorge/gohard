library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    x   : in  real;
    y   : out real
  );
end entity;

architecture rtl of top is
begin
  reg : process (clk)
  begin
    if rising_edge(clk) then
      y <= x * 0.5;
    end if;
  end process;
end architecture;
