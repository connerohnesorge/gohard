library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    d   : in  std_ulogic;
    q   : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  ddr : process (clk)
  begin
    if rising_edge(clk) then
      q <= d;
    end if;
    if falling_edge(clk) then
      q <= not d;
    end if;
  end process;
end architecture;
