library ieee;
use ieee.std_logic_1164.all;

entity alias_sens is
  port (a, b : in std_logic; y : out std_logic);
end entity;

architecture rtl of alias_sens is
begin
  p : process (a)
    alias lb is b;
  begin
    y <= a and lb;
  end process;
end architecture;
