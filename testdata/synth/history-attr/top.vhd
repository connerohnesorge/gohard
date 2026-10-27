library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (
    a : in  std_ulogic;
    q : out std_ulogic
  );
end entity;

architecture rtl of top is
begin
  q <= '1' when a'last_value = '0' and a'stable(5 ns) else '0';
end architecture;
