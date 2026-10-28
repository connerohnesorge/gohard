library ieee;
use ieee.std_logic_1164.all;
entity dut is
  port (clk : in std_logic; a, b : in std_logic);
end entity;
architecture rtl of dut is
  property follows(boolean x, y) is always (x -> next y);
  sequence pair(boolean x, y) is {x; y};
begin
end architecture;
