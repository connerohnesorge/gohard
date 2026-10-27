library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    q   : out std_ulogic
  );
end entity;

architecture rtl of top is
  signal stamp : time;
begin
  reg : process (clk)
  begin
    if rising_edge(clk) then
      stamp <= now;
    end if;
  end process;

  q <= '1' when stamp > 100 ns else '0';
end architecture;
