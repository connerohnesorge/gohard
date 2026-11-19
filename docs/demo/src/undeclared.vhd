library ieee;
use ieee.std_logic_1164.all;

entity undeclared is
  port (y : out std_logic);
end entity undeclared;

architecture rtl of undeclared is
begin
  y <= missing;
end architecture rtl;
