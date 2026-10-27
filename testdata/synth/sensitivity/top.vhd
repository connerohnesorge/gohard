library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    a : in  std_ulogic;
    b : in  std_ulogic;
    y : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  -- b is read but missing from the sensitivity list.
  comb : process (a)
  begin
    y <= a and b;
  end process;
end architecture;
