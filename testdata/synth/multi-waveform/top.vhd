library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    p   : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  pulse : process (clk)
  begin
    if rising_edge(clk) then
      p <= '1', '0' after 5 ns;
    end if;
  end process;
end architecture;
