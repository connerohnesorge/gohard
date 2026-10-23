library ieee;
use ieee.std_logic_1164.all;

entity edges is
  port (clk, a : in std_logic; q1, q2 : out std_logic);
end entity;

architecture rtl of edges is
begin
  p1 : process (clk)
  begin
    if rising_edge(clk) then
      q1 <= a;
    end if;
  end process;

  p2 : process (clk)
  begin
    if clk'event and clk = '1' then
      q2 <= a;
    end if;
  end process;
end architecture;
