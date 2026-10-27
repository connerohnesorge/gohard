library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    clk : in  std_ulogic;
    d   : in  std_ulogic;
    q   : out std_ulogic;
    y   : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  reg : process (clk)
  begin
    if rising_edge(clk) then
      q <= d after 2 ns;
    end if;
  end process;

  y <= transport not d after 5 ns;
end architecture;
